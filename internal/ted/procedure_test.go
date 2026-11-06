package ted

import "testing"

// sampleAward is a trimmed copy of TED notice 770297-2026: one lot, five
// tenders, won by a cooperative named both on the tendering party and as an
// organisation, with a renewal option written as prose and the contract period
// written only into the title.
const sampleAward = `<?xml version="1.0" encoding="UTF-8"?>
<ContractAwardNotice xmlns:cac="urn:cac" xmlns:cbc="urn:cbc" xmlns:ext="urn:ext" xmlns:efac="urn:efac" xmlns:efbc="urn:efbc" xmlns:efext="urn:efext">
  <ext:UBLExtensions><ext:UBLExtension><ext:ExtensionContent><efext:EformsExtension>
    <efac:NoticeResult>
      <cbc:TotalAmount currencyID="EUR">1880126.62</cbc:TotalAmount>
      <efac:LotResult>
        <cbc:ID schemeName="result">RES-0001</cbc:ID>
        <cbc:TenderResultCode listName="winner-selection-status">selec-w</cbc:TenderResultCode>
        <efac:LotTender><cbc:ID schemeName="tender">TEN-0001</cbc:ID></efac:LotTender>
        <efac:ReceivedSubmissionsStatistics>
          <efbc:StatisticsCode listName="received-submission-type">tenders</efbc:StatisticsCode>
          <efbc:StatisticsNumeric>5</efbc:StatisticsNumeric>
        </efac:ReceivedSubmissionsStatistics>
        <efac:SettledContract><cbc:ID schemeName="contract">CON-0001</cbc:ID></efac:SettledContract>
        <efac:TenderLot><cbc:ID schemeName="Lot">LOT-0001</cbc:ID></efac:TenderLot>
      </efac:LotResult>
      <efac:LotTender>
        <cbc:ID schemeName="tender">TEN-0001</cbc:ID>
        <cac:LegalMonetaryTotal><cbc:PayableAmount currencyID="EUR">1880126.62</cbc:PayableAmount></cac:LegalMonetaryTotal>
        <efac:TenderingParty><cbc:ID schemeName="tendering-party">TPA-0001</cbc:ID></efac:TenderingParty>
        <efac:TenderLot><cbc:ID schemeName="Lot">LOT-0001</cbc:ID></efac:TenderLot>
      </efac:LotTender>
      <efac:SettledContract>
        <cbc:ID schemeName="contract">CON-0001</cbc:ID>
        <cbc:AwardDate>2025-12-01+01:00</cbc:AwardDate>
        <cbc:IssueDate>2026-09-01+02:00</cbc:IssueDate>
        <efac:LotTender><cbc:ID schemeName="tender">TEN-0001</cbc:ID></efac:LotTender>
      </efac:SettledContract>
      <efac:TenderingParty>
        <cbc:ID schemeName="tendering-party">TPA-0001</cbc:ID>
        <cbc:Name>CIRFOOD S.C.</cbc:Name>
        <efac:Tenderer><cbc:ID schemeName="organization">ORG-0003</cbc:ID></efac:Tenderer>
      </efac:TenderingParty>
    </efac:NoticeResult>
    <efac:Organizations>
      <efac:Organization><efac:Company>
        <cac:PartyIdentification><cbc:ID schemeName="organization">ORG-0001</cbc:ID></cac:PartyIdentification>
        <cac:PartyName><cbc:Name languageID="ITA">Stazione Appaltante Provinciale di Como</cbc:Name></cac:PartyName>
        <cac:PartyLegalEntity><cbc:CompanyID>80004650133</cbc:CompanyID></cac:PartyLegalEntity>
      </efac:Company></efac:Organization>
      <efac:Organization><efac:Company>
        <efbc:CompanySizeCode listName="economic-operator-size">large</efbc:CompanySizeCode>
        <cac:PartyIdentification><cbc:ID schemeName="organization">ORG-0003</cbc:ID></cac:PartyIdentification>
        <cac:PartyName><cbc:Name languageID="ITA">CIRFOOD S.C.</cbc:Name></cac:PartyName>
        <cac:PostalAddress><cbc:CityName>REGGIO EMILIA (RE)</cbc:CityName>
          <cac:Country><cbc:IdentificationCode listName="country">ITA</cbc:IdentificationCode></cac:Country></cac:PostalAddress>
        <cac:PartyLegalEntity><cbc:CompanyID>00464110352</cbc:CompanyID></cac:PartyLegalEntity>
      </efac:Company></efac:Organization>
    </efac:Organizations>
  </efext:EformsExtension></ext:ExtensionContent></ext:UBLExtension></ext:UBLExtensions>
  <cbc:ID schemeName="notice-id">f84d47d3-c4bb-4d73-ab18-cba7ad388001</cbc:ID>
  <cbc:ContractFolderID>d30b0285-76b9-49d5-88da-1ea635452153</cbc:ContractFolderID>
  <cbc:IssueDate>2026-11-04+01:00</cbc:IssueDate>
  <cbc:VersionID>01</cbc:VersionID>
  <cbc:NoticeTypeCode listName="result">can-standard</cbc:NoticeTypeCode>
  <cbc:NoticeLanguageCode>ITA</cbc:NoticeLanguageCode>
  <cac:ContractingParty><cac:Party><cac:PartyIdentification><cbc:ID schemeName="organization">ORG-0001</cbc:ID></cac:PartyIdentification></cac:Party></cac:ContractingParty>
  <cac:ProcurementProject>
    <cbc:Name languageID="ITA">SERVIZIO DI RISTORAZIONE SCOLASTICA. GENNAIO 2026-AGOSTO 2029 CON EVENTUALE PROROGA 4 ANNI</cbc:Name>
  </cac:ProcurementProject>
  <cac:ProcurementProjectLot>
    <cbc:ID schemeName="Lot">LOT-0001</cbc:ID>
    <cac:ProcurementProject>
      <cbc:Name languageID="ITA">SERVIZIO DI RISTORAZIONE SCOLASTICA. GENNAIO 2026-AGOSTO 2029 CON EVENTUALE PROROGA 4 ANNI</cbc:Name>
      <cac:RequestedTenderTotal><cbc:EstimatedOverallContractAmount currencyID="EUR">4617023.12</cbc:EstimatedOverallContractAmount></cac:RequestedTenderTotal>
      <cac:MainCommodityClassification><cbc:ItemClassificationCode listName="cpv">55523100</cbc:ItemClassificationCode></cac:MainCommodityClassification>
      <cac:ContractExtension>
        <cbc:MaximumNumberNumeric>1</cbc:MaximumNumberNumeric>
        <cac:Renewal><cac:Period><cbc:Description languageID="ITA">Eventuale rinnovo ulteriori 4 anni scolastici</cbc:Description></cac:Period></cac:Renewal>
      </cac:ContractExtension>
    </cac:ProcurementProject>
  </cac:ProcurementProjectLot>
  <cac:TenderResult><cbc:AwardDate>2000-01-01+01:00</cbc:AwardDate></cac:TenderResult>
</ContractAwardNotice>`

func TestParseProcedureReadsAwardOutcome(t *testing.T) {
	p, err := ParseProcedure([]byte(sampleAward))
	if err != nil {
		t.Fatal(err)
	}
	if p.ProcedureID != "d30b0285-76b9-49d5-88da-1ea635452153" || p.NoticeType != "can-standard" || p.Language != "ITA" {
		t.Fatalf("header: %+v", p)
	}
	if p.Buyer == nil || p.Buyer.CompanyID != "80004650133" {
		t.Fatalf("buyer: %+v", p.Buyer)
	}
	if len(p.Lots) != 1 {
		t.Fatalf("lots: %+v", p.Lots)
	}
	lot := p.Lots[0]
	o := lot.Outcome
	if o == nil || o.Status != OutcomeWinner || o.TendersCount != 5 {
		t.Fatalf("outcome: %+v", o)
	}
	if o.AwardedValue != "1880126.62" || o.AwardDate != "2025-12-01" || o.ContractDate != "2026-09-01" {
		t.Errorf("value/dates: %+v", o)
	}
	if len(o.Winners) != 1 || o.Winners[0].Name != "CIRFOOD S.C." || o.Winners[0].CompanyID != "00464110352" || o.Winners[0].Size != "large" {
		t.Errorf("winner: %+v", o.Winners)
	}
	if lot.EstimatedValue != "4617023.12" || lot.MaxRenewals != 1 {
		t.Errorf("lot terms: %+v", lot)
	}
	if lot.Duration.Start != "2026-01-01" || lot.Duration.End != "2029-08-31" || lot.Duration.Source != DurationFromText {
		t.Errorf("duration from title: %+v", lot.Duration)
	}
	if lot.Duration.RenewalMonths != 48 {
		t.Errorf("renewal months = %d, want 48", lot.Duration.RenewalMonths)
	}
	if !p.Awarded() {
		t.Error("Awarded() = false")
	}
}

const sampleChange = `<ContractNotice xmlns:cac="urn:cac" xmlns:cbc="urn:cbc" xmlns:ext="urn:ext" xmlns:efac="urn:efac" xmlns:efbc="urn:efbc" xmlns:efext="urn:efext">
  <ext:UBLExtensions><ext:UBLExtension><ext:ExtensionContent><efext:EformsExtension>
    <efac:Changes>
      <efbc:ChangedNoticeIdentifier>6d4eff0f-ee5c-43b4-a449-43d085530b22-01</efbc:ChangedNoticeIdentifier>
      <efac:ChangeReason><cbc:ReasonCode listName="change-corrig-justification">cor-buy</cbc:ReasonCode>
        <efbc:ReasonDescription languageID="ITA">Proroga dei termini</efbc:ReasonDescription></efac:ChangeReason>
      <efac:Change>
        <efbc:ChangeDescription languageID="ITA">Il termine di presentazione delle offerte è prorogato al 04/11/2024</efbc:ChangeDescription>
        <efac:ChangedSection><efbc:ChangedSectionIdentifier>LOT-0001</efbc:ChangedSectionIdentifier></efac:ChangedSection>
      </efac:Change>
    </efac:Changes>
  </efext:EformsExtension></ext:ExtensionContent></ext:UBLExtension></ext:UBLExtensions>
  <cbc:ContractFolderID>ccc10029</cbc:ContractFolderID>
  <cbc:VersionID>02</cbc:VersionID>
  <cbc:NoticeTypeCode listName="competition">cn-standard</cbc:NoticeTypeCode>
  <cac:ProcurementProjectLot><cbc:ID schemeName="Lot">LOT-0001</cbc:ID>
    <cac:ProcurementProject><cbc:Name>Ristorazione</cbc:Name>
      <cac:PlannedPeriod><cbc:DurationMeasure unitCode="YEAR">3</cbc:DurationMeasure></cac:PlannedPeriod>
    </cac:ProcurementProject>
  </cac:ProcurementProjectLot>
</ContractNotice>`

func TestParseProcedureReadsChangesAndPlannedPeriod(t *testing.T) {
	p, err := ParseProcedure([]byte(sampleChange))
	if err != nil {
		t.Fatal(err)
	}
	c := p.Changes
	if c == nil || c.ReasonCode != "cor-buy" || len(c.Descriptions) != 1 || len(c.Sections) != 1 || c.Sections[0] != "LOT-0001" {
		t.Fatalf("changes: %+v", c)
	}
	if p.Awarded() || p.Lots[0].Outcome != nil {
		t.Error("a contract notice has no outcome")
	}
	d := p.Lots[0].Duration
	if d.Months != 36 || d.Source != DurationFromNotice {
		t.Fatalf("planned period: %+v", d)
	}
	if end, how := d.EndDate("2025-01-15"); end != "2028-01-15" || how != DurationEstimated {
		t.Errorf("EndDate = %s (%s)", end, how)
	}
}

func TestInferDuration(t *testing.T) {
	cases := []struct {
		text       string
		start, end string
		months     float64
	}{
		{"FORNITURA PASTI PERIODO dal 01/01/2027 al 31/12/2028", "2027-01-01", "2028-12-31", 0},
		{"Servizio mensa scolastica — durata dell'appalto: 36 mesi", "", "", 36},
		{"Catering services, contract duration of two years", "", "", 24},
		{"Ristorazione scolastica comune di vigevano", "", "", 0},
		// A lone deadline is not a period.
		{"offerte entro il 21/10/2024", "", "", 0},
		// 31 February is not a date, and must not become one.
		{"dal 31/02/2027 al 30/06/2027 e oltre", "", "", 0},
	}
	for _, c := range cases {
		d := InferDuration(c.text)
		if d.Start != c.start || d.End != c.end || d.Months != c.months {
			t.Errorf("%q: got %+v", c.text, d)
		}
	}
}

func TestMonthsIn(t *testing.T) {
	cases := map[string]int{
		"Eventuale rinnovo di 12 mesi":                  12,
		"Eventuale rinnovo ulteriori 4 anni scolastici": 48,
		"renewal for one year":                          12,
		"rinnovo alle medesime condizioni":              0,
	}
	for text, want := range cases {
		if got := MonthsIn(text); got != want {
			t.Errorf("MonthsIn(%q) = %d, want %d", text, got, want)
		}
	}
}

func TestDossierReadsQuestionsDeadlineAndZone(t *testing.T) {
	const doc = `<ContractNotice xmlns:cac="urn:cac" xmlns:cbc="urn:cbc">
  <cbc:ContractFolderID>abc</cbc:ContractFolderID>
  <cac:ProcurementProjectLot><cbc:ID>LOT-0001</cbc:ID>
    <cac:TenderingProcess>
      <cac:TenderSubmissionDeadlinePeriod><cbc:EndDate>2026-08-24+02:00</cbc:EndDate><cbc:EndTime>08:00:00+02:00</cbc:EndTime></cac:TenderSubmissionDeadlinePeriod>
      <cac:AdditionalInformationRequestPeriod><cbc:EndDate>2026-08-14+02:00</cbc:EndDate><cbc:EndTime>12:00:00+02:00</cbc:EndTime></cac:AdditionalInformationRequestPeriod>
      <cac:OpenTenderEvent><cbc:OccurrenceDate>2026-08-25+02:00</cbc:OccurrenceDate><cbc:OccurrenceTime>10:00:00+02:00</cbc:OccurrenceTime></cac:OpenTenderEvent>
    </cac:TenderingProcess>
  </cac:ProcurementProjectLot>
</ContractNotice>`
	d, err := ParseDossier([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if d.DeadlineDate != "2026-08-24" || d.DeadlineTime != "08:00" || d.DeadlineZone != "+02:00" {
		t.Errorf("deadline: %s %s %s", d.DeadlineDate, d.DeadlineTime, d.DeadlineZone)
	}
	if d.QuestionsDate != "2026-08-14" || d.QuestionsTime != "12:00" {
		t.Errorf("questions: %s %s", d.QuestionsDate, d.QuestionsTime)
	}
	if d.OpeningDate != "2026-08-25" || d.OpeningTime != "10:00" || d.ProcedureID != "abc" {
		t.Errorf("opening/procedure: %+v", d)
	}
}
