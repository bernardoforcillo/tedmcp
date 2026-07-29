package ted

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// DefaultCacheBytes bounds the cache so a long-running server cannot fill the
// user's disk. A notice document runs 20–120 KB, so this holds several
// thousand of them.
const DefaultCacheBytes int64 = 512 << 20

// sweepInterval is how many writes pass between size checks. Walking the
// directory on every write would cost more than the cache saves.
const sweepInterval = 128

// Cache stores notice documents on disk.
//
// TED publication numbers are stable and a corrected notice is republished
// under a new number, so a cached document never goes stale and needs no
// expiry. The point is iteration: refining a search pattern means matching the
// same few hundred documents over and over, and without a cache each pass
// re-downloads all of them from a host that rate-limits.
type Cache struct {
	Dir      string
	MaxBytes int64

	hits   atomic.Int64
	misses atomic.Int64
	writes atomic.Int64

	mu sync.Mutex // serialises sweeps
}

// CacheStats reports what a run got out of the cache.
type CacheStats struct {
	Hits   int64  `json:"hits"`
	Misses int64  `json:"misses"`
	Dir    string `json:"dir,omitempty"`
}

// DefaultCacheDir is where notice documents are kept unless the caller says
// otherwise: a directory beside the work, so a project's cache travels with it
// and is easy to inspect or delete.
const DefaultCacheDir = ".tenders/cache"

// CacheDisabled is the directory value that turns caching off.
const CacheDisabled = "off"

// NewCache prepares a cache directory, returning a nil cache (and no error)
// when caching is switched off.
//
// The directory is chosen by, in order: the argument, the TEDMCP_CACHE_DIR
// environment variable, then DefaultCacheDir. Passing "off" — as the argument
// or the variable — disables caching.
func NewCache(dir string) (*Cache, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = strings.TrimSpace(os.Getenv("TEDMCP_CACHE_DIR"))
	}
	if dir == "" {
		dir = DefaultCacheDir
	}
	if strings.EqualFold(dir, CacheDisabled) || strings.EqualFold(dir, "none") {
		return nil, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache directory %s: %w", dir, err)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	return &Cache{Dir: abs, MaxBytes: DefaultCacheBytes}, nil
}

// Stats returns the hit and miss counts accumulated so far.
func (c *Cache) Stats() CacheStats {
	if c == nil {
		return CacheStats{}
	}
	return CacheStats{Hits: c.hits.Load(), Misses: c.misses.Load(), Dir: c.Dir}
}

// Get returns a cached document. A key that names no readable, non-empty file
// is a miss: a truncated or half-written entry must never be served as if it
// were the document.
func (c *Cache) Get(key string) ([]byte, bool) {
	if c == nil || key == "" {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(c.Dir, key))
	if err != nil || len(data) == 0 {
		c.misses.Add(1)
		return nil, false
	}
	c.hits.Add(1)
	return data, true
}

// Put stores a document. Writing goes through a temporary file and a rename so
// a reader in another goroutine never observes a partial document.
func (c *Cache) Put(key string, data []byte) error {
	if c == nil || key == "" || len(data) == 0 {
		return nil
	}

	tmp, err := os.CreateTemp(c.Dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, filepath.Join(c.Dir, key)); err != nil {
		return err
	}

	if c.writes.Add(1)%sweepInterval == 0 {
		c.sweep()
	}
	return nil
}

// sweep drops the least recently modified entries when the cache outgrows its
// bound, down to 80% so the next writes do not immediately trigger another.
func (c *Cache) sweep() {
	c.mu.Lock()
	defer c.mu.Unlock()

	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		return
	}

	type item struct {
		name  string
		size  int64
		mtime int64
	}
	var files []item
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.IsDir() {
			continue
		}
		files = append(files, item{e.Name(), info.Size(), info.ModTime().UnixNano()})
		total += info.Size()
	}
	if total <= c.MaxBytes {
		return
	}

	sort.Slice(files, func(i, j int) bool { return files[i].mtime < files[j].mtime })
	target := c.MaxBytes * 4 / 5
	for _, f := range files {
		if total <= target {
			break
		}
		if os.Remove(filepath.Join(c.Dir, f.name)) == nil {
			total -= f.size
		}
	}
}

// CacheKey derives a stable filename for a document URL.
//
// Only notice XML is cached: its content is fixed once published. A TED URL
// like /en/notice/517698-2026/xml yields "517698-2026.xml", which keeps the
// directory readable; anything else falls back to a digest.
func CacheKey(url string) (string, bool) {
	if !strings.HasSuffix(url, "/xml") {
		return "", false
	}
	trimmed := strings.TrimSuffix(url, "/xml")
	if i := strings.LastIndexByte(trimmed, '/'); i >= 0 {
		if id := trimmed[i+1:]; isPublicationNumber(id) {
			return id + ".xml", true
		}
	}
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:16]) + ".xml", true
}

// isPublicationNumber reports whether s looks like "517698-2026" — the only
// shape allowed to become a filename directly.
func isPublicationNumber(s string) bool {
	if len(s) < 3 || len(s) > 32 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return strings.Count(s, "-") == 1
}
