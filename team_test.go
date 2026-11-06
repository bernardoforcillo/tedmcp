package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/bernardoforcillo/tedmcp/internal/match"
	"github.com/bernardoforcillo/tedmcp/internal/store"
	"github.com/bernardoforcillo/tedmcp/internal/ted"
	"github.com/bernardoforcillo/tedmcp/internal/webdoc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeTED serves the two halves of TED this server talks to: the search API,
// answering the handful of query shapes the tools send, and the notice XML.
type fakeTED struct {
	*httptest.Server
	mu      sync.Mutex
	notices []fakeNotice
}

type fakeNotice struct {
	pn, title, buyer, noticeType, published, procedureID string
	xml                                                  string
}

func newFakeTED(t *testing.T, notices ...fakeNotice) *fakeTED {
	t.Helper()
	f := &fakeTED{notices: notices}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v3/notices/search", f.search)
	mux.HandleFunc("GET /en/notice/{pn}/xml", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, n := range f.notices {
			if n.pn == r.PathValue("pn") {
				w.Header().Set("Content-Type", "application/xml")
				_, _ = w.Write([]byte(n.xml))
				return
			}
		}
		http.NotFound(w, r)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeTED) add(n fakeNotice) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notices = append(f.notices, n)
}

var (
	pnQuery   = regexp.MustCompile(`publication-number=([\d-]+)`)
	procQuery = regexp.MustCompile(`procedure-identifier="([^"]+)"`)
)

func (f *fakeTED) search(w http.ResponseWriter, r *http.Request) {
	var req ted.SearchRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []ted.Notice
	for _, n := range f.notices {
		switch {
		case pnQuery.MatchString(req.Query):
			if pnQuery.FindStringSubmatch(req.Query)[1] != n.pn {
				continue
			}
		case procQuery.MatchString(req.Query):
			if procQuery.FindStringSubmatch(req.Query)[1] != n.procedureID {
				continue
			}
		case strings.Contains(req.Query, "can-standard") && !strings.HasPrefix(n.noticeType, "can"):
			continue
		}
		out = append(out, ted.Notice{
			"publication-number": n.pn,
			"notice-title":       map[string]any{"ENG": n.title},
			"notice-type":        n.noticeType,
			"buyer-name":         map[string]any{"ITA": []any{n.buyer}},
			"buyer-country":      []any{"ITA"},
			"publication-date":   n.published + "+01:00",
			"links":              map[string]any{"xml": map[string]any{"MUL": f.URL + "/en/notice/" + n.pn + "/xml"}},
		})
	}
	// Newest first, as TED sorts by default; the history asks for ASC.
	if !strings.Contains(req.Query, "ASC") {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	_ = json.NewEncoder(w).Encode(ted.SearchResponse{Notices: out, TotalNoticeCount: len(out)})
}

func (f *fakeTED) client() *ted.Client {
	c := ted.NewClient()
	c.BaseURL = f.URL
	return c
}

// awardXML builds an award notice for one lot.
func awardXML(procedureID, title, buyer, buyerCF, winner, winnerID, estimated, awarded string, tenders int, awardDate string) string {
	return fmt.Sprintf(`<ContractAwardNotice xmlns:cac="c" xmlns:cbc="b" xmlns:ext="e" xmlns:efac="a" xmlns:efbc="f" xmlns:efext="x">
<ext:UBLExtensions><ext:UBLExtension><ext:ExtensionContent><efext:EformsExtension>
 <efac:NoticeResult>
  <efac:LotResult><cbc:TenderResultCode>selec-w</cbc:TenderResultCode>
   <efac:LotTender><cbc:ID>TEN-1</cbc:ID></efac:LotTender>
   <efac:ReceivedSubmissionsStatistics><efbc:StatisticsCode>tenders</efbc:StatisticsCode><efbc:StatisticsNumeric>%d</efbc:StatisticsNumeric></efac:ReceivedSubmissionsStatistics>
   <efac:SettledContract><cbc:ID>CON-1</cbc:ID></efac:SettledContract>
   <efac:TenderLot><cbc:ID>LOT-0001</cbc:ID></efac:TenderLot>
  </efac:LotResult>
  <efac:LotTender><cbc:ID>TEN-1</cbc:ID><cac:LegalMonetaryTotal><cbc:PayableAmount currencyID="EUR">%s</cbc:PayableAmount></cac:LegalMonetaryTotal>
   <efac:TenderingParty><cbc:ID>TPA-1</cbc:ID></efac:TenderingParty></efac:LotTender>
  <efac:SettledContract><cbc:ID>CON-1</cbc:ID><cbc:AwardDate>%s+01:00</cbc:AwardDate><efac:LotTender><cbc:ID>TEN-1</cbc:ID></efac:LotTender></efac:SettledContract>
  <efac:TenderingParty><cbc:ID>TPA-1</cbc:ID><efac:Tenderer><cbc:ID>ORG-2</cbc:ID></efac:Tenderer></efac:TenderingParty>
 </efac:NoticeResult>
 <efac:Organizations>
  <efac:Organization><efac:Company><cac:PartyIdentification><cbc:ID>ORG-1</cbc:ID></cac:PartyIdentification>
   <cac:PartyName><cbc:Name>%s</cbc:Name></cac:PartyName><cac:PartyLegalEntity><cbc:CompanyID>%s</cbc:CompanyID></cac:PartyLegalEntity></efac:Company></efac:Organization>
  <efac:Organization><efac:Company><cac:PartyIdentification><cbc:ID>ORG-2</cbc:ID></cac:PartyIdentification>
   <cac:PartyName><cbc:Name>%s</cbc:Name></cac:PartyName><cac:PartyLegalEntity><cbc:CompanyID>%s</cbc:CompanyID></cac:PartyLegalEntity></efac:Company></efac:Organization>
 </efac:Organizations>
</efext:EformsExtension></ext:ExtensionContent></ext:UBLExtension></ext:UBLExtensions>
<cbc:ContractFolderID>%s</cbc:ContractFolderID>
<cbc:NoticeTypeCode>can-standard</cbc:NoticeTypeCode><cbc:NoticeLanguageCode>ITA</cbc:NoticeLanguageCode>
<cac:ContractingParty><cac:Party><cac:PartyIdentification><cbc:ID>ORG-1</cbc:ID></cac:PartyIdentification></cac:Party></cac:ContractingParty>
<cac:ProcurementProjectLot><cbc:ID>LOT-0001</cbc:ID><cac:ProcurementProject><cbc:Name>%s</cbc:Name>
 <cac:RequestedTenderTotal><cbc:EstimatedOverallContractAmount currencyID="EUR">%s</cbc:EstimatedOverallContractAmount></cac:RequestedTenderTotal>
 <cac:MainCommodityClassification><cbc:ItemClassificationCode>55524000</cbc:ItemClassificationCode></cac:MainCommodityClassification>
 <cac:ContractExtension><cac:Renewal><cac:Period><cbc:Description>Eventuale rinnovo di 24 mesi</cbc:Description></cac:Period></cac:Renewal></cac:ContractExtension>
</cac:ProcurementProject></cac:ProcurementProjectLot>
</ContractAwardNotice>`, tenders, awarded, awardDate, buyer, buyerCF, winner, winnerID, procedureID, title, estimated)
}

// contractXML builds a contract notice with a deadline, an optional change
// section and a link to the tender documents.
func contractXML(procedureID, deadline, clock, docsURL, change string) string {
	changes := ""
	if change != "" {
		changes = `<efac:Changes><efac:ChangeReason><cbc:ReasonCode>cor-buy</cbc:ReasonCode></efac:ChangeReason>
		  <efac:Change><efbc:ChangeDescription>` + change + `</efbc:ChangeDescription></efac:Change></efac:Changes>`
	}
	return `<ContractNotice xmlns:cac="c" xmlns:cbc="b" xmlns:ext="e" xmlns:efac="a" xmlns:efbc="f" xmlns:efext="x">
<ext:UBLExtensions><ext:UBLExtension><ext:ExtensionContent><efext:EformsExtension>` + changes + `
 <efac:Organizations><efac:Organization><efac:Company><cac:PartyIdentification><cbc:ID>ORG-1</cbc:ID></cac:PartyIdentification>
  <cac:PartyName><cbc:Name>COMUNE DI ESEMPIO</cbc:Name></cac:PartyName></efac:Company></efac:Organization></efac:Organizations>
</efext:EformsExtension></ext:ExtensionContent></ext:UBLExtension></ext:UBLExtensions>
<cbc:ContractFolderID>` + procedureID + `</cbc:ContractFolderID>
<cbc:NoticeTypeCode>cn-standard</cbc:NoticeTypeCode><cbc:NoticeLanguageCode>ITA</cbc:NoticeLanguageCode>
<cac:ContractingParty><cac:Party><cac:PartyIdentification><cbc:ID>ORG-1</cbc:ID></cac:PartyIdentification></cac:Party></cac:ContractingParty>
<cac:ProcurementProjectLot><cbc:ID>LOT-0001</cbc:ID>
 <cac:TenderingTerms><cac:CallForTendersDocumentReference><cbc:ID>G00749</cbc:ID>
  <cac:Attachment><cac:ExternalReference><cbc:URI>` + docsURL + `</cbc:URI></cac:ExternalReference></cac:Attachment>
 </cac:CallForTendersDocumentReference></cac:TenderingTerms>
 <cac:TenderingProcess>
  <cac:TenderSubmissionDeadlinePeriod><cbc:EndDate>` + deadline + `+02:00</cbc:EndDate><cbc:EndTime>` + clock + `:00+02:00</cbc:EndTime></cac:TenderSubmissionDeadlinePeriod>
  <cac:AdditionalInformationRequestPeriod><cbc:EndDate>2026-08-14+02:00</cbc:EndDate><cbc:EndTime>12:00:00+02:00</cbc:EndTime></cac:AdditionalInformationRequestPeriod>
 </cac:TenderingProcess>
 <cac:ProcurementProject><cbc:Name>Ristorazione scolastica</cbc:Name>
  <cac:PlannedPeriod><cbc:DurationMeasure unitCode="MONTH">36</cbc:DurationMeasure></cac:PlannedPeriod></cac:ProcurementProject>
</cac:ProcurementProjectLot>
</ContractNotice>`
}

func callTool[In, Out any](t *testing.T, h func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error), in In) (*mcp.CallToolResult, Out) {
	t.Helper()
	res, out, err := h(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %s", resultText(res))
	}
	return res, out
}

func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		switch c := c.(type) {
		case *mcp.TextContent:
			b.WriteString(c.Text)
		case *mcp.EmbeddedResource:
			b.WriteString(c.Resource.Text)
		}
	}
	return b.String()
}

func memStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(store.Disabled)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// --- award_history / forecast_retenders ---

func marketTED(t *testing.T) *fakeTED {
	return newFakeTED(t,
		fakeNotice{pn: "100-2026", noticeType: "can-standard", published: "2026-03-01", buyer: "COMUNE DI A", procedureID: "p1",
			xml: awardXML("p1", "Mensa scolastica PERIODO dal 01/09/2026 al 31/08/2029", "COMUNE DI A", "111", "CIRFOOD S.C.", "00464110352", "1000000", "900000", 5, "2026-02-01")},
		fakeNotice{pn: "200-2026", noticeType: "can-standard", published: "2026-04-01", buyer: "COMUNE DI B", procedureID: "p2",
			xml: awardXML("p2", "Refezione scolastica", "COMUNE DI B", "222", "CIRFOOD SOC. COOP.", "IT00464110352", "500000", "450000", 1, "2026-03-15")},
		fakeNotice{pn: "300-2026", noticeType: "can-standard", published: "2026-05-01", buyer: "COMUNE DI A", procedureID: "p3",
			xml: awardXML("p3", "Pasti a domicilio dal 01/01/2027 al 31/12/2027", "COMUNE DI A", "111", "EURORISTORAZIONE SRL", "01998810244", "200000", "190000", 3, "2026-04-20")},
	)
}

func TestAwardHistoryRanksCompetitorsAcrossSpellings(t *testing.T) {
	f := marketTED(t)
	res, out := callTool(t, handleAwardHistory(f.client()), AwardHistoryInput{
		MarketFilters: MarketFilters{Cpv: []string{"555*"}, Country: []string{"ITA"}},
		Format:        "csv",
	})
	if out.Read != 3 || len(out.Awards) != 3 {
		t.Fatalf("read %d, awards %d", out.Read, len(out.Awards))
	}
	// Two spellings, one VAT number: one competitor.
	top := out.Competitors[0]
	if top.Wins != 2 || top.TotalAwarded != "1350000.00" || top.SingleBidWins != 1 {
		t.Errorf("top competitor: %+v", top)
	}
	if out.Market.Awarded != 3 || out.Market.SingleBidShare == nil || *out.Market.SingleBidShare != 33.3 {
		t.Errorf("market: %+v", out.Market)
	}
	if out.Market.MedianDiscount == nil || *out.Market.MedianDiscount != 10 {
		t.Errorf("median discount: %v", out.Market.MedianDiscount)
	}
	if b := out.Buyers[0]; b.Name != "COMUNE DI A" || b.Awards != 2 || b.DistinctWinners != 2 {
		t.Errorf("buyer stats: %+v", b)
	}
	if len(res.Content) != 2 || !strings.Contains(resultText(res), "publication_number,published,buyer") {
		t.Errorf("csv export missing: %s", resultText(res))
	}

	_, only := callTool(t, handleAwardHistory(f.client()), AwardHistoryInput{
		MarketFilters: MarketFilters{Country: []string{"ITA"}}, Winner: "euroristorazione",
	})
	if len(only.Awards) != 1 || only.Awards[0].PublicationNumber != "300-2026" {
		t.Errorf("winner filter: %+v", only.Awards)
	}
}

func TestForecastPlacesContractsInTheWindow(t *testing.T) {
	f := marketTED(t)
	_, out := callTool(t, handleForecast(f.client()), ForecastInput{
		MarketFilters: MarketFilters{Country: []string{"ITA"}, PublishedFrom: "2020-01-01"},
		EndsFrom:      "2027-06-01", EndsTo: "2029-12-31",
	})
	if len(out.Contracts) != 2 {
		t.Fatalf("contracts: %+v", out.Contracts)
	}
	first, second := out.Contracts[0], out.Contracts[1]
	// Ends 2027-12-31, inside the window on its own.
	if first.PublicationNumber != "300-2026" || first.End != "2027-12-31" || first.InWindow != "base" || first.EndSource != ted.DurationFromText {
		t.Errorf("first: %+v", first)
	}
	if first.EndWithRenewals != "2029-12-31" || first.WatchFrom != "2027-03-31" {
		t.Errorf("renewal/watch: %+v", first)
	}
	if second.PublicationNumber != "100-2026" || second.End != "2029-08-31" || len(second.Incumbent) != 1 {
		t.Errorf("second: %+v", second)
	}
	// 200-2026 states no period at all: counted, not listed, not lost.
	if out.Unknown != 1 {
		t.Errorf("unknown = %d, want 1", out.Unknown)
	}

	// A window reached only through the renewal option.
	_, later := callTool(t, handleForecast(f.client()), ForecastInput{
		MarketFilters: MarketFilters{Country: []string{"ITA"}, PublishedFrom: "2020-01-01"},
		EndsFrom:      "2031-01-01", EndsTo: "2031-12-31",
	})
	if len(later.Contracts) != 1 || later.Contracts[0].InWindow != "renewal" || later.Contracts[0].EndWithRenewals != "2031-08-31" {
		t.Errorf("renewal window: %+v", later.Contracts)
	}
}

// --- get_procedure_history ---

func TestProcedureHistoryReportsWhatChanged(t *testing.T) {
	f := newFakeTED(t,
		fakeNotice{pn: "561293-2024", noticeType: "cn-standard", published: "2024-09-18", procedureID: "ccc",
			xml: contractXML("ccc", "2024-10-21", "12:00", "https://example.invalid/", "")},
		fakeNotice{pn: "600000-2024", noticeType: "cn-standard", published: "2024-10-10", procedureID: "ccc",
			xml: contractXML("ccc", "2024-11-04", "12:00", "https://example.invalid/", "Proroga del termine")},
		fakeNotice{pn: "772763-2026", noticeType: "can-standard", published: "2026-11-06", procedureID: "ccc",
			xml: awardXML("ccc", "Ristorazione", "COMUNE DI VIGEVANO", "85001870188", "SODEXO ITALIA S.P.A.", "00805980158", "12545717.58", "12048073", 6, "2024-12-13")},
	)
	_, out := callTool(t, handleProcedureHistory(f.client()), HistoryInput{PublicationNumber: "561293-2024"})
	if out.ProcedureID != "ccc" || len(out.Notices) != 3 || out.Status != "awarded" {
		t.Fatalf("history: %+v", out)
	}
	alerts := strings.Join(out.Alerts, "\n")
	for _, want := range []string{"561293-2024 is not the latest", "Deadline moved from 2024-10-21 12:00", "to 2024-11-04 12:00", "Proroga del termine"} {
		if !strings.Contains(alerts, want) {
			t.Errorf("alerts missing %q:\n%s", want, alerts)
		}
	}
	if o := out.Notices[2].Outcomes; len(o) != 1 || !strings.Contains(o[0], "SODEXO") || !strings.Contains(o[0], "6 bid(s)") {
		t.Errorf("outcome: %v", o)
	}
}

// --- extract_requirements ---

func TestExtractRequirementsReadsTheDisciplinare(t *testing.T) {
	portal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/files/disciplinare.txt" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "Art. 6 Il concorrente deve possedere un fatturato globale minimo annuo non inferiore a € 1.500.000,00.\n"+
			"Art. 10 Il sopralluogo è obbligatorio.\nArt. 9 La garanzia provvisoria è pari al 2% dell'importo.")
	}))
	defer portal.Close()

	f := newFakeTED(t, fakeNotice{pn: "442511-2026", noticeType: "cn-standard", published: "2026-07-01", procedureID: "q",
		xml: contractXML("q", "2026-08-24", "08:00", portal.URL+"/files/disciplinare.txt", "")})

	_, out := callTool(t, handleExtractRequirements(f.client(), webdoc.NewClient("tedmcp-test/0.1")), RequirementsInput{PublicationNumber: "442511-2026"})
	if out.Language != "ITA" || len(out.DocumentsRead) != 1 {
		t.Fatalf("documents: %+v", out)
	}
	got := map[string]string{}
	for _, r := range out.Results {
		if r.Found {
			got[r.ID] = strings.Join(append(r.Values, r.Flags...), " ")
		}
	}
	if !strings.Contains(got["turnover"], "1.500.000,00") || !strings.Contains(got["site_visit"], "mandatory") || !strings.Contains(got["bid_guarantee"], "2%") {
		t.Errorf("results: %v", got)
	}
	if n := out.Notice; n == nil || n.Deadline != "2026-08-24 08:00" || n.QuestionsDeadline != "2026-08-14 12:00" || len(n.Duration) != 1 {
		t.Errorf("notice facts: %+v", out.Notice)
	}

	if _, err := buildChecks([]string{"turnover", "nope"}, nil); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("unknown check ids must be named: %v", err)
	}
}

// --- team tools ---

func TestShortlistAndCalendar(t *testing.T) {
	f := newFakeTED(t, fakeNotice{pn: "442511-2026", title: "Ristorazione scolastica", buyer: "COMUNE DI ESEMPIO",
		noticeType: "cn-standard", published: "2026-07-01", procedureID: "q",
		xml: contractXML("q", "2026-08-24", "08:00", "https://appalti.example.it/G00749", "")})
	st := memStore(t)

	_, added := callTool(t, handleShortlistAdd(f.client(), st), ShortlistAddInput{PublicationNumber: "442511-2026", Owner: "giulia", Note: "fits our references"})
	e := added.Entry
	if e.Status != "watching" || e.DeadlineTime != "08:00" || e.DeadlineZone != "+02:00" || e.QuestionsDate != "2026-08-14" || e.ProcedureID != "q" {
		t.Fatalf("entry: %+v", e)
	}

	_, updated := callTool(t, handleShortlistUpdate(st), ShortlistUpdateInput{PublicationNumber: "442511-2026", Status: "bidding"})
	if updated.Entry.Status != "bidding" || len(updated.Entry.Notes) != 2 {
		t.Errorf("update: %+v", updated.Entry)
	}
	res, _, _ := handleShortlistUpdate(st)(context.Background(), nil, ShortlistUpdateInput{PublicationNumber: "442511-2026", Status: "maybe"})
	if !res.IsError {
		t.Error("an unknown status must be refused")
	}

	res, list := callTool(t, handleShortlistList(st), ShortlistListInput{Status: []string{"bidding"}, Format: "csv"})
	if len(list.Entries) != 1 || !strings.Contains(resultText(res), "442511-2026,bidding,giulia") {
		t.Errorf("list/csv: %s", resultText(res))
	}

	_, cal := callTool(t, handleExportCalendar(f.client(), st), CalendarInput{})
	if cal.Events != 2 {
		t.Fatalf("events = %d\n%s", cal.Events, cal.ICS)
	}
	// 08:00 at +02:00 is 06:00 UTC — the instant, not the wall-clock digits.
	for _, want := range []string{"DTSTART:20260824T060000Z", "DTSTART:20260814T100000Z", "TRIGGER:-P3D", "BEGIN:VCALENDAR"} {
		if !strings.Contains(cal.ICS, want) {
			t.Errorf("ics missing %s:\n%s", want, cal.ICS)
		}
	}
	for _, line := range strings.Split(cal.ICS, "\r\n") {
		if len(line) > 75 {
			t.Errorf("unfolded ics line (%d octets): %s", len(line), line)
		}
	}
}

func TestSavedSearchReportsOnlyWhatIsNew(t *testing.T) {
	f := newFakeTED(t,
		fakeNotice{pn: "1-2026", noticeType: "cn-standard", published: "2026-10-01"},
		fakeNotice{pn: "2-2026", noticeType: "cn-standard", published: "2026-10-02"},
	)
	st := memStore(t)
	callTool(t, handleSaveSearch(st), SaveSearchInput{Name: "Mense Lombardia", Filters: SearchInput{Cpv: []string{"555*"}}})

	run := func() RunSavedOutput {
		_, out := callTool(t, handleRunSavedSearch(f.client(), st), RunSavedInput{Name: "mense lombardia"})
		return out
	}
	if got := run(); len(got.Results[0].New) != 2 {
		t.Fatalf("first run: %+v", got)
	}
	if got := run(); len(got.Results[0].New) != 0 || got.Results[0].AlreadySeen != 2 {
		t.Fatalf("second run must find nothing new: %+v", got)
	}
	f.add(fakeNotice{pn: "3-2026", noticeType: "cn-standard", published: "2026-10-03"})
	if got := run(); len(got.Results[0].New) != 1 || got.Results[0].New[0].PublicationNumber != "3-2026" {
		t.Fatalf("third run: %+v", got)
	}

	res, _, _ := handleSaveSearch(st)(context.Background(), nil, SaveSearchInput{Name: "broken", Filters: SearchInput{Cpv: []string{"5X5*"}}})
	if !res.IsError {
		t.Error("a search that cannot run must not be saved")
	}
}

func TestTermPackFeedsTheScans(t *testing.T) {
	st := memStore(t)
	callTool(t, handleSaveTermPack(st), SaveTermPackInput{Name: "Food waste", TermsByLanguage: map[string][]string{
		"ITA": {"eccedenz"}, "DEU": {"lebensmittelverschwendung"},
	}})
	terms := map[string][]string{"ITA": {"spreco"}}
	var near *match.Near
	if err := applyTermPack(st, "food-waste", &terms, &near); err != nil {
		t.Fatal(err)
	}
	if len(terms["ITA"]) != 2 || len(terms["DEU"]) != 1 {
		t.Errorf("merged terms: %v", terms)
	}
	if err := applyTermPack(st, "missing", &terms, &near); err == nil {
		t.Error("an unknown pack must be an error, not an empty search")
	}
}

// --- auth ---

func TestHTTPRequiresTokenWhenConfigured(t *testing.T) {
	t.Setenv("TEDMCP_TOKENS", "giulia:0123456789abcdef0123,marco:fedcba9876543210fedc")
	tokens, err := loadTokens("")
	if err != nil || len(tokens) != 2 {
		t.Fatalf("tokens: %v %d", err, len(tokens))
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "tedmcp", Version: "test"}, nil)
	h := mcpHandler(server, tokens)

	if rec := postMCP(t, h, initializeRequest, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: status %d", rec.Code)
	}
	if rec := postMCP(t, h, initializeRequest, map[string]string{"Authorization": "Bearer wrong-token-wrong-token"}); rec.Code != http.StatusUnauthorized {
		t.Errorf("bad token: status %d", rec.Code)
	}
	if rec := postMCP(t, h, initializeRequest, map[string]string{"Authorization": "Bearer 0123456789abcdef0123"}); rec.Code != http.StatusOK {
		t.Errorf("good token: status %d %s", rec.Code, rec.Body.String())
	}
	// The probe and the AGPL source offer stay open.
	for _, path := range []string{"/healthz", "/"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s needs no token, got %d", path, rec.Code)
		}
	}

	t.Setenv("TEDMCP_TOKENS", "short:abc")
	if _, err := loadTokens(""); err == nil {
		t.Error("a short token must be refused")
	}
}

// --- prompts ---

func TestPromptsNameTheirArguments(t *testing.T) {
	for _, p := range prompts {
		args := map[string]string{}
		for _, a := range p.args {
			args[a.Name] = "ARG-" + a.Name
		}
		text := p.render(args)
		for _, a := range p.args {
			if a.Required && !strings.Contains(text, "ARG-"+a.Name) {
				t.Errorf("prompt %s does not use its %s argument", p.name, a.Name)
			}
		}
	}
}
