package ted

import "testing"

func TestCPVClause(t *testing.T) {
	tests := []struct {
		name    string
		in      []string
		want    string
		wantErr bool
	}{
		{"single exact", []string{"55500000"}, "classification-cpv=55500000", false},
		{"multi exact", []string{"55500000", "55524000"}, "classification-cpv IN (55500000 55524000)", false},
		{"check digit stripped", []string{"55524000-5"}, "classification-cpv=55524000", false},
		{"star wildcard", []string{"555*"}, "classification-cpv=555*", false},
		{"X placeholders", []string{"555XXXXX"}, "classification-cpv=555*", false},
		{"partial digits become prefix", []string{"5552"}, "classification-cpv=5552*", false},
		{"mixed exact and wildcard", []string{"55500000", "55524000", "555XXXXX"},
			"(classification-cpv IN (55500000 55524000) OR classification-cpv=555*)", false},
		{"comma-joined string", []string{"55500000, 55524000"}, "classification-cpv IN (55500000 55524000)", false},
		{"dedupe", []string{"555*", "555XXXXX"}, "classification-cpv=555*", false},
		{"non-trailing wildcard rejected", []string{"5X5*"}, "", true},
		{"too short wildcard rejected", []string{"5*"}, "", true},
		{"too many digits rejected", []string{"555000001"}, "", true},
		{"non-numeric rejected", []string{"abc"}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cpvClause(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("cpvClause(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestBuildQueryWithWildcardCPV(t *testing.T) {
	got, err := BuildQuery(SearchFilters{
		CPV:     []string{"55500000", "55524000", "555XXXXX"},
		Country: []string{"deu"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "(classification-cpv IN (55500000 55524000) OR classification-cpv=555*) AND buyer-country=DEU SORT BY publication-date DESC"
	if got != want {
		t.Fatalf("BuildQuery = %q, want %q", got, want)
	}
}
