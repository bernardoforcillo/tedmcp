package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStorePersistsAcrossOpens(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Update(func(d *Data) error {
		d.Shortlist["442511-2026"] = &Entry{PublicationNumber: "442511-2026", Status: "qualifying", Owner: "giulia"}
		d.Searches["mense"] = &SavedSearch{Name: "Mense", Filters: json.RawMessage(`{"cpv":["555*"]}`)}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	again.View(func(d *Data) {
		if e := d.Shortlist["442511-2026"]; e == nil || e.Owner != "giulia" {
			t.Errorf("shortlist did not persist: %+v", e)
		}
		if s := d.Searches["mense"]; s == nil || compact(s.Filters) != `{"cpv":["555*"]}` {
			t.Errorf("search did not persist: %+v", s)
		}
	})
}

// A corrupt file must stop the server rather than be silently replaced by an
// empty one on the next write.
func TestOpenRefusesCorruptData(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("expected an error for corrupt team data")
	}
}

func TestMarkSeenIsBoundedAndDeduplicated(t *testing.T) {
	s := &SavedSearch{}
	s.MarkSeen([]string{"1-2026", "2-2026", "1-2026"})
	if len(s.Seen) != 2 || !s.HasSeen("2-2026") {
		t.Fatalf("seen = %v", s.Seen)
	}
	many := make([]string, MaxSeen+10)
	for i := range many {
		many[i] = string(rune('a'+i%26)) + "-" + itoa(i)
	}
	s.MarkSeen(many)
	if len(s.Seen) != MaxSeen {
		t.Fatalf("seen grew to %d, bound is %d", len(s.Seen), MaxSeen)
	}
	if s.HasSeen("1-2026") {
		t.Error("the oldest entries should be the ones forgotten")
	}
}

func TestKeyAndSorting(t *testing.T) {
	if Key("  Mense Scolastiche / ITA ") != "mense-scolastiche-ita" {
		t.Errorf("Key = %q", Key("  Mense Scolastiche / ITA "))
	}
	d := Data{}
	d.init()
	d.Shortlist["a"] = &Entry{PublicationNumber: "a"}
	d.Shortlist["b"] = &Entry{PublicationNumber: "b", DeadlineDate: "2026-09-01"}
	d.Shortlist["c"] = &Entry{PublicationNumber: "c", DeadlineDate: "2026-08-01"}
	got := d.SortedEntries()
	if got[0].PublicationNumber != "c" || got[1].PublicationNumber != "b" || got[2].PublicationNumber != "a" {
		t.Errorf("order: %s %s %s", got[0].PublicationNumber, got[1].PublicationNumber, got[2].PublicationNumber)
	}
}

func TestDisabledStoreWritesNothing(t *testing.T) {
	s, err := Open(Disabled)
	if err != nil || s.Path() != "" {
		t.Fatalf("Open(off) = %v, path %q", err, s.Path())
	}
	if err := s.Update(func(d *Data) error { d.Shortlist["x"] = &Entry{}; return nil }); err != nil {
		t.Fatal(err)
	}
}

func compact(raw json.RawMessage) string {
	var v any
	_ = json.Unmarshal(raw, &v)
	b, _ := json.Marshal(v)
	return string(b)
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}
