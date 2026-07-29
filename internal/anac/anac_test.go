package anac

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// writeSnapshot builds a snapshot archive holding the given NDJSON lines.
func writeSnapshot(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cig.zip")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	w, err := zw.Create("cig.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if _, err := w.Write([]byte(l + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// ANAC types the same column inconsistently: n_lotti_componenti and the
// amounts arrive as bare numbers in some rows and as quoted strings in others.
const (
	rowNumeric = `{"cig":"BC31BD63BC","cf_amministrazione_appaltante":"01199250158","denominazione_amministrazione_appaltante":"COMUNE DI MILANO","cod_cpv":"55330000-2","importo_lotto":7249359.11,"n_lotti_componenti":1,"oggetto_lotto":"CAFFE LETTERARIO"}`
	rowStrings = `{"cig":"BC26D38A78","cf_amministrazione_appaltante":"01199250158","denominazione_amministrazione_appaltante":"COMUNE DI MILANO","cod_cpv":"55310000-6","importo_lotto":"84361696.00","n_lotti_componenti":"1","oggetto_lotto":"MUDEC"}`
	rowOther   = `{"cig":"AAAAAAAAAA","cf_amministrazione_appaltante":"99999999999","cod_cpv":"45000000-7","importo_lotto":100.0,"n_lotti_componenti":1}`
	rowBroken  = `{"cig":"BROKEN","cf_amministrazione_appaltante":"01199250158","importo_lotto":{"unexpected":"shape"}}`
)

func TestLookupDecodesMixedTypes(t *testing.T) {
	s, err := Open(writeSnapshot(t, rowNumeric, rowStrings, rowOther))
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.Lookup(Query{BuyerCF: []string{"01199250158"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 2 {
		t.Fatalf("got %d records, want 2 — a quoted number must not drop a row", len(got.Records))
	}
	if got.Malformed != 0 {
		t.Errorf("malformed = %d, want 0", got.Malformed)
	}
	if v := got.Records[1].ImportoLotto.Float(); v != 84361696.00 {
		t.Errorf("string-typed amount decoded as %v", v)
	}
	if n := got.Records[1].NLotti.Float(); n != 1 {
		t.Errorf("string-typed lot count decoded as %v", n)
	}
}

func TestLookupJoinsByValue(t *testing.T) {
	s, err := Open(writeSnapshot(t, rowNumeric, rowStrings, rowOther))
	if err != nil {
		t.Fatal(err)
	}

	// The amount as TED prints it is enough to pick one gara out of a buyer's many.
	got, err := s.Lookup(Query{BuyerCF: []string{"01199250158"}, Value: "7249359.11"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 1 || got.Records[0].CIG != "BC31BD63BC" {
		t.Fatalf("value join returned %+v, want just CIG BC31BD63BC", got.Records)
	}
}

func TestLookupFiltersCPVAndBuyer(t *testing.T) {
	s, err := Open(writeSnapshot(t, rowNumeric, rowStrings, rowOther))
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.Lookup(Query{BuyerCF: []string{"01199250158"}, CPVPrefixes: []string{"5533"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 1 || got.Records[0].CIG != "BC31BD63BC" {
		t.Fatalf("cpv filter returned %+v", got.Records)
	}
	if got.Candidate != 2 {
		t.Errorf("candidate = %d, want the 2 rows naming this buyer", got.Candidate)
	}
}

func TestLookupReportsMalformedRows(t *testing.T) {
	s, err := Open(writeSnapshot(t, rowBroken, rowNumeric))
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.Lookup(Query{BuyerCF: []string{"01199250158"}})
	if err != nil {
		t.Fatal(err)
	}
	// The point of counting: a row we cannot read must not look like a row
	// that does not exist.
	if got.Malformed != 1 || got.Sample == "" {
		t.Fatalf("malformed=%d sample=%q, want the undecodable row reported", got.Malformed, got.Sample)
	}
	if len(got.Records) != 1 {
		t.Errorf("the readable row should still come back, got %d", len(got.Records))
	}
}

func TestLookupRequiresBuyer(t *testing.T) {
	s, err := Open(writeSnapshot(t, rowNumeric))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(Query{}); err == nil {
		t.Fatal("a lookup with no buyer must be refused, not answered with a full scan")
	}
}
