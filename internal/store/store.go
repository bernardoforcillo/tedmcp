// Package store keeps what a tender research team builds up between sessions:
// the searches it runs every morning and what each one has already shown, the
// shortlist of tenders being qualified with who owns each and where it stands,
// and the term packs — curated per-language vocabularies — that make a scan
// repeatable by someone who did not write it.
//
// Without it every analyst starts from zero in every conversation. With it a
// saved search answers "what is new since yesterday" and the shortlist is one
// shared answer to "what are we bidding on".
//
// Storage is a single JSON document, written atomically on every change. That
// is deliberately modest: a team's shortlist is hundreds of entries, not
// millions, and a file that a person can open, read, back up and diff is worth
// more here than a database. Writes are serialised by a mutex, so one process
// is the unit of consistency — run one replica.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultDir is where the team data lives when nothing else is configured.
const DefaultDir = ".tenders/data"

// Disabled keeps the data in memory only.
const Disabled = "off"

const fileName = "team.json"

// MaxSeen bounds how many publication numbers a saved search remembers. A
// search that has shown more than this is a firehose, and forgetting the oldest
// only risks re-showing a notice from long ago.
const MaxSeen = 5000

// Shortlist statuses, in the order a tender moves through them.
var Statuses = []string{"watching", "qualifying", "bidding", "submitted", "won", "lost", "dropped"}

// ValidStatus reports whether s is a known shortlist status.
func ValidStatus(s string) bool {
	for _, v := range Statuses {
		if v == s {
			return true
		}
	}
	return false
}

// Data is everything the store holds.
type Data struct {
	Version   int                     `json:"version"`
	Searches  map[string]*SavedSearch `json:"searches"`
	Shortlist map[string]*Entry       `json:"shortlist"`
	TermPacks map[string]*TermPack    `json:"term_packs"`
}

// SavedSearch is a named search with a memory of what it has already shown.
type SavedSearch struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Filters     json.RawMessage `json:"filters" jsonschema:"the search_tenders inputs this search runs with"`
	CreatedBy   string          `json:"created_by,omitempty"`
	CreatedAt   string          `json:"created_at,omitempty"`
	LastRun     string          `json:"last_run,omitempty"`
	LastRunBy   string          `json:"last_run_by,omitempty"`
	Seen        []string        `json:"seen,omitempty"`
}

// MarkSeen records publication numbers as shown, newest last, bounded.
func (s *SavedSearch) MarkSeen(numbers []string) {
	have := make(map[string]bool, len(s.Seen))
	for _, n := range s.Seen {
		have[n] = true
	}
	for _, n := range numbers {
		if n != "" && !have[n] {
			have[n] = true
			s.Seen = append(s.Seen, n)
		}
	}
	if over := len(s.Seen) - MaxSeen; over > 0 {
		s.Seen = append([]string(nil), s.Seen[over:]...)
	}
}

// HasSeen reports whether a publication number was already shown.
func (s *SavedSearch) HasSeen(n string) bool {
	for _, v := range s.Seen {
		if v == n {
			return true
		}
	}
	return false
}

// Entry is one tender on the shortlist.
type Entry struct {
	PublicationNumber string `json:"publication_number"`
	Title             string `json:"title,omitempty"`
	Buyer             string `json:"buyer,omitempty"`
	Country           string `json:"country,omitempty"`
	Value             string `json:"value,omitempty"`
	DeadlineDate      string `json:"deadline_date,omitempty"`
	DeadlineTime      string `json:"deadline_time,omitempty"`
	DeadlineZone      string `json:"deadline_zone,omitempty"`
	QuestionsDate     string `json:"questions_deadline_date,omitempty"`
	QuestionsTime     string `json:"questions_deadline_time,omitempty"`
	ProcedureID       string `json:"procedure_id,omitempty"`
	URL               string `json:"url,omitempty"`
	DocumentsURL      string `json:"documents_url,omitempty"`
	Status            string `json:"status"`
	Owner             string `json:"owner,omitempty"`
	Notes             []Note `json:"notes,omitempty"`
	AddedBy           string `json:"added_by,omitempty"`
	AddedAt           string `json:"added_at,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
}

// Note is a dated remark on a shortlist entry.
type Note struct {
	At   string `json:"at"`
	By   string `json:"by,omitempty"`
	Text string `json:"text"`
}

// TermPack is a named, curated set of per-language search terms.
type TermPack struct {
	Name            string              `json:"name"`
	Description     string              `json:"description,omitempty"`
	TermsByLanguage map[string][]string `json:"terms_by_language,omitempty"`
	Near            json.RawMessage     `json:"near,omitempty"`
	UpdatedBy       string              `json:"updated_by,omitempty"`
	UpdatedAt       string              `json:"updated_at,omitempty"`
}

// Store is the team data, held in memory and mirrored to disk.
type Store struct {
	path string // "" when disabled
	mu   sync.Mutex
	data Data
	now  func() time.Time
}

// Open loads the store from a directory, chosen by the argument, then the
// TEDMCP_DATA_DIR environment variable, then DefaultDir. "off" keeps the data
// in memory, which is what tests and throwaway sessions want.
func Open(dir string) (*Store, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = strings.TrimSpace(os.Getenv("TEDMCP_DATA_DIR"))
	}
	if dir == "" {
		dir = DefaultDir
	}
	s := &Store{now: time.Now}
	s.data.init()
	if strings.EqualFold(dir, Disabled) {
		return s, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create data directory %s: %w", dir, err)
	}
	s.path = filepath.Join(dir, fileName)
	raw, err := os.ReadFile(s.path)
	switch {
	case os.IsNotExist(err):
		return s, nil
	case err != nil:
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		// Refuse rather than start empty: the next write would replace the
		// team's shortlist with nothing.
		return nil, fmt.Errorf("%s is not valid team data (%v); fix or move it before starting", s.path, err)
	}
	s.data.init()
	return s, nil
}

// Path is the file the store writes, or "" when it is in memory only.
func (s *Store) Path() string { return s.path }

// Now returns the current time as stored timestamps write it.
func (s *Store) Now() string { return s.now().UTC().Format(time.RFC3339) }

// View runs fn with read access to the data. fn must not keep references.
func (s *Store) View(fn func(*Data)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.data)
}

// Update runs fn with write access and saves the result. If fn returns an
// error nothing is saved — but changes it made in memory stay, so fn should
// validate before it mutates.
func (s *Store) Update(fn func(*Data) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn(&s.data); err != nil {
		return err
	}
	return s.save()
}

func (s *Store) save() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(&s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".team-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op once renamed
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}

func (d *Data) init() {
	d.Version = 1
	if d.Searches == nil {
		d.Searches = map[string]*SavedSearch{}
	}
	if d.Shortlist == nil {
		d.Shortlist = map[string]*Entry{}
	}
	if d.TermPacks == nil {
		d.TermPacks = map[string]*TermPack{}
	}
}

// Key normalises a name used to index searches and term packs, so "Mense
// scolastiche" and "mense-scolastiche" are the same entry.
func Key(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r > 127:
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// SortedEntries returns shortlist entries ordered by deadline, soonest first,
// with undated entries last.
func (d *Data) SortedEntries() []*Entry {
	out := make([]*Entry, 0, len(d.Shortlist))
	for _, e := range d.Shortlist {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].DeadlineDate, out[j].DeadlineDate
		switch {
		case a == "" && b != "":
			return false
		case a != "" && b == "":
			return true
		case a != b:
			return a < b
		}
		return out[i].PublicationNumber < out[j].PublicationNumber
	})
	return out
}
