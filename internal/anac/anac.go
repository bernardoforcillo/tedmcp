// Package anac reads the Italian public-contracts open data published by ANAC
// (Autorità Nazionale Anticorruzione) and joins it to TED notices.
//
// TED says what is being bought; ANAC says which CIG it was given, what it was
// worth, and how it ended. Neither carries the tender documents themselves.
// The join key is the buyer's codice fiscale, which every Italian eForms
// notice carries and which ANAC repeats as cf_amministrazione_appaltante.
//
// The data is distributed only as monthly bulk archives from
// dati.anticorruzione.it, and that site's firewall refuses clients that do not
// present themselves as a web browser. This package therefore reads a snapshot
// the user has downloaded, rather than pretending to be a browser in order to
// fetch it. Download the JSON archive of the "cig" dataset from
// https://dati.anticorruzione.it/opendata/dataset/cig and pass its path.
package anac

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// Record is the part of an ANAC contract record worth reporting back.
type Record struct {
	CIG               string `json:"cig"`
	NumeroGara        string `json:"numero_gara,omitempty"`
	OggettoGara       string `json:"oggetto_gara,omitempty"`
	OggettoLotto      string `json:"oggetto_lotto,omitempty"`
	ImportoLotto      Number `json:"importo_lotto,omitempty"`
	ImportoGara       Number `json:"importo_complessivo_gara,omitempty"`
	CPV               string `json:"cod_cpv,omitempty"`
	CPVDescription    string `json:"descrizione_cpv,omitempty"`
	DataPubblicazione string `json:"data_pubblicazione,omitempty"`
	DataScadenza      string `json:"data_scadenza_offerta,omitempty"`
	Buyer             string `json:"denominazione_amministrazione_appaltante,omitempty"`
	BuyerCF           string `json:"cf_amministrazione_appaltante,omitempty"`
	Stato             string `json:"stato,omitempty"`
	Esito             string `json:"ESITO,omitempty"`
	TipoScelta        string `json:"tipo_scelta_contraente,omitempty"`
	NLotti            Number `json:"n_lotti_componenti,omitempty"`
	Provincia         string `json:"provincia,omitempty"`
}

// Number is a numeric field of the ANAC export. The same column arrives as a
// JSON number in one row and as a quoted string in the next, so decoding it
// strictly would throw away real records.
type Number float64

func (n *Number) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("not a number: %s", b)
	}
	*n = Number(f)
	return nil
}

func (n Number) Float() float64 { return float64(n) }

// Query selects records from a snapshot. An empty BuyerCF matches nothing:
// scanning the whole archive without a buyer filter would return millions of
// rows and is never what a caller wants.
type Query struct {
	BuyerCF     []string // codice fiscale of the contracting authority
	CPVPrefixes []string // keep only these CPV families, e.g. "555"
	Value       string   // match this contract value, as printed by TED
	Limit       int
}

// valueTolerance absorbs the rounding differences between how TED and ANAC
// print the same amount.
const valueTolerance = 0.01

// Snapshot is a downloaded ANAC bulk archive.
type Snapshot struct {
	Path string
}

// Open checks that the archive is readable and returns a handle to it.
func Open(path string) (*Snapshot, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open ANAC snapshot %s: %w", path, err)
	}
	defer r.Close()

	if len(r.File) == 0 {
		return nil, fmt.Errorf("ANAC snapshot %s is empty", path)
	}
	return &Snapshot{Path: path}, nil
}

// maxLineBytes bounds one JSON record; ANAC rows run to a couple of kilobytes.
const maxLineBytes = 4 << 20

// LookupResult is what a scan found, and what it could not read. Malformed is
// reported rather than swallowed: a schema change upstream would otherwise
// look exactly like a buyer with no contracts.
type LookupResult struct {
	Records   []Record `json:"records,omitempty"`
	Scanned   int      `json:"scanned" jsonschema:"records read from the snapshot"`
	Candidate int      `json:"candidate" jsonschema:"records naming one of the requested buyers"`
	Malformed int      `json:"malformed,omitempty" jsonschema:"candidate records that could not be decoded"`
	Sample    string   `json:"malformed_sample,omitempty" jsonschema:"the first decoding error, when any record was malformed"`
}

// Lookup scans the snapshot and returns the matching records.
//
// The archive holds hundreds of thousands of records, so each line is first
// tested for the buyer's codice fiscale as raw bytes and only parsed as JSON
// when that cheap test passes.
func (s *Snapshot) Lookup(q Query) (*LookupResult, error) {
	if len(q.BuyerCF) == 0 {
		return nil, fmt.Errorf("a buyer codice fiscale is required: scanning the whole archive is not supported")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}

	needles := make([][]byte, 0, len(q.BuyerCF))
	wanted := make(map[string]bool, len(q.BuyerCF))
	for _, cf := range q.BuyerCF {
		if cf = strings.TrimSpace(cf); cf != "" {
			needles = append(needles, []byte(cf))
			wanted[cf] = true
		}
	}

	var want float64
	haveWant := false
	if v := strings.TrimSpace(q.Value); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid value %q: %w", q.Value, err)
		}
		want, haveWant = f, true
	}

	zr, err := zip.OpenReader(s.Path)
	if err != nil {
		return nil, fmt.Errorf("open ANAC snapshot: %w", err)
	}
	defer zr.Close()

	out := &LookupResult{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f.Name, err)
		}
		err = scanRecords(rc, needles, out, func(rec Record) bool {
			if !wanted[rec.BuyerCF] {
				return true
			}
			if !matchesCPV(rec.CPV, q.CPVPrefixes) {
				return true
			}
			if haveWant && !matchesValue(rec, want) {
				return true
			}
			out.Records = append(out.Records, rec)
			return len(out.Records) < limit
		})
		rc.Close()
		if err != nil {
			return nil, err
		}
		if len(out.Records) >= limit {
			break
		}
	}
	return out, nil
}

// scanRecords walks the newline-delimited JSON, calling visit for every record
// whose raw line contains one of the needles. visit returns false to stop.
func scanRecords(r io.Reader, needles [][]byte, stats *LookupResult, visit func(Record) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)

	for sc.Scan() {
		stats.Scanned++
		line := sc.Bytes()
		if len(line) == 0 || !containsAny(line, needles) {
			continue
		}
		stats.Candidate++

		var rec Record
		if err := json.Unmarshal(line, &rec); err != nil {
			// Keep scanning, but keep the count: rows silently dropped here
			// would masquerade as a buyer having no contracts at all.
			stats.Malformed++
			if stats.Sample == "" {
				stats.Sample = err.Error()
			}
			continue
		}
		if !visit(rec) {
			return nil
		}
	}
	return sc.Err()
}

func containsAny(line []byte, needles [][]byte) bool {
	for _, n := range needles {
		if bytes.Contains(line, n) {
			return true
		}
	}
	return false
}

func matchesCPV(cpv string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(cpv, p) {
			return true
		}
	}
	return false
}

// matchesValue accepts a record whose lot or overall amount equals the wanted
// figure: TED may publish either, depending on how the notice was filled in.
func matchesValue(rec Record, want float64) bool {
	return math.Abs(rec.ImportoLotto.Float()-want) < valueTolerance ||
		math.Abs(rec.ImportoGara.Float()-want) < valueTolerance
}
