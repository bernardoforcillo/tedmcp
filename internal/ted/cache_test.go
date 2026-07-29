package ted

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestCache(t *testing.T) *Cache {
	t.Helper()
	t.Setenv("TEDMCP_CACHE_DIR", t.TempDir())
	c, err := NewCache("")
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatal("cache unexpectedly disabled")
	}
	return c
}

func TestCacheRoundTrip(t *testing.T) {
	c := newTestCache(t)

	if _, ok := c.Get("517698-2026.xml"); ok {
		t.Fatal("empty cache reported a hit")
	}
	if err := c.Put("517698-2026.xml", []byte("<ContractNotice/>")); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Get("517698-2026.xml")
	if !ok || string(got) != "<ContractNotice/>" {
		t.Fatalf("got %q, ok=%v", got, ok)
	}

	s := c.Stats()
	if s.Hits != 1 || s.Misses != 1 {
		t.Errorf("stats = %+v, want 1 hit and 1 miss", s)
	}
}

func TestCacheNeverServesAnEmptyEntry(t *testing.T) {
	c := newTestCache(t)

	// A zero-length file is what a crashed or truncated write leaves behind.
	// Serving it would look like a notice with no content at all.
	if err := os.WriteFile(filepath.Join(c.Dir, "truncated.xml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("truncated.xml"); ok {
		t.Fatal("a zero-length entry was served as a document")
	}
}

func TestCachePutIsAtomic(t *testing.T) {
	c := newTestCache(t)
	payload := strings.Repeat("<lot/>", 20000)

	// Concurrent writers must never leave a reader with a half-written file.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Put("busy.xml", []byte(payload)); err != nil {
				t.Error(err)
			}
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, ok := c.Get("busy.xml"); ok && string(got) != payload {
				t.Errorf("read a partial document: %d of %d bytes", len(got), len(payload))
			}
		}()
	}
	wg.Wait()

	// No temporary files may survive.
	entries, err := os.ReadDir(c.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestCacheSweepDropsOldestFirst(t *testing.T) {
	c := newTestCache(t)
	c.MaxBytes = 300

	for _, name := range []string{"old.xml", "mid.xml", "new.xml"} {
		if err := c.Put(name, []byte(strings.Repeat("x", 200))); err != nil {
			t.Fatal(err)
		}
		// Distinct modification times decide the eviction order.
		time.Sleep(10 * time.Millisecond)
	}

	c.sweep()

	if _, err := os.Stat(filepath.Join(c.Dir, "new.xml")); err != nil {
		t.Errorf("the newest entry was evicted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(c.Dir, "old.xml")); err == nil {
		t.Error("the oldest entry survived a sweep that had to free space")
	}
}

func TestCacheDirectoryPrecedence(t *testing.T) {
	env := t.TempDir()
	arg := t.TempDir()
	t.Setenv("TEDMCP_CACHE_DIR", env)

	// An explicit directory wins over the environment.
	c, err := NewCache(arg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(c.Dir, filepath.Base(arg)) {
		t.Errorf("dir = %q, want the explicitly passed %q", c.Dir, arg)
	}

	// With no argument, the environment decides.
	c, err = NewCache("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(c.Dir, filepath.Base(env)) {
		t.Errorf("dir = %q, want the environment's %q", c.Dir, env)
	}

	// With neither, the default sits beside the work.
	t.Setenv("TEDMCP_CACHE_DIR", "")
	t.Chdir(t.TempDir())
	c, err = NewCache("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(filepath.ToSlash(c.Dir), DefaultCacheDir) {
		t.Errorf("dir = %q, want it to end in %q", c.Dir, DefaultCacheDir)
	}
}

func TestCacheDisabled(t *testing.T) {
	t.Setenv("TEDMCP_CACHE_DIR", "off")
	c, err := NewCache("")
	if err != nil {
		t.Fatal(err)
	}
	if c != nil {
		t.Fatal("TEDMCP_CACHE_DIR=off must disable the cache")
	}
	if c, err := NewCache("off"); err != nil || c != nil {
		t.Fatalf("an explicit \"off\" must disable the cache, got %v, %v", c, err)
	}
	// A nil cache stays usable, so callers need no nil checks.
	if _, ok := c.Get("x.xml"); ok {
		t.Error("nil cache reported a hit")
	}
	if err := c.Put("x.xml", []byte("data")); err != nil {
		t.Errorf("nil cache Put returned %v", err)
	}
}

func TestCacheKey(t *testing.T) {
	tests := []struct {
		url      string
		want     string
		wantOK   bool
		wantName bool // the readable publication-number form
	}{
		{"https://ted.europa.eu/en/notice/517698-2026/xml", "517698-2026.xml", true, true},
		{"https://ted.europa.eu/en/notice/2584-2024/xml", "2584-2024.xml", true, true},
		// Only notice XML is immutable enough to keep; a rendered page is not.
		{"https://ted.europa.eu/en/notice/517698-2026/pdf", "", false, false},
		{"https://ted.europa.eu/en/notice/-/detail/517698-2026", "", false, false},
		// An unexpected shape must not become a path or escape the directory.
		{"https://example.org/../../etc/passwd/xml", "", true, false},
	}

	for _, tt := range tests {
		got, ok := CacheKey(tt.url)
		if ok != tt.wantOK {
			t.Errorf("CacheKey(%q) ok = %v, want %v", tt.url, ok, tt.wantOK)
			continue
		}
		if tt.wantName && got != tt.want {
			t.Errorf("CacheKey(%q) = %q, want %q", tt.url, got, tt.want)
		}
		if ok && (strings.ContainsAny(got, `/\`) || strings.Contains(got, "..")) {
			t.Errorf("CacheKey(%q) = %q, which is not a safe filename", tt.url, got)
		}
	}
}
