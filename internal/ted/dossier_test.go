package ted

import "testing"

// sampleDossier carries two organisations on purpose: with a single one, a
// role assigned through a stale slice pointer still lands, so multi-party
// notices are the only ones that catch that class of bug.
const sampleDossier = `<?xml version="1.0" encoding="UTF-8"?>
<ContractNotice xmlns:cac="urn:cac" xmlns:cbc="urn:cbc" xmlns:ext="urn:ext" xmlns:efac="urn:efac" xmlns:efbc="urn:efbc">
  <ext:UBLExtensions><ext:UBLExtension><ext:ExtensionContent><efext:EformsExtension xmlns:efext="urn:efext">
    <efac:Organizations>
      <efac:Organization>
        <efac:Company>
          <cbc:WebsiteURI>https://www.comune.esempio.it/</cbc:WebsiteURI>
          <cac:PartyIdentification><cbc:ID schemeName="organization">ORG-0001</cbc:ID></cac:PartyIdentification>
          <cac:PartyName><cbc:Name languageID="ITA">COMUNE DI ESEMPIO</cbc:Name></cac:PartyName>
          <cac:PostalAddress><cbc:CityName>ESEMPIO</cbc:CityName>
            <cac:Country><cbc:IdentificationCode listName="country">ITA</cbc:IdentificationCode></cac:Country>
          </cac:PostalAddress>
          <cac:PartyLegalEntity><cbc:CompanyID>01472860012</cbc:CompanyID></cac:PartyLegalEntity>
          <cac:Contact>
            <cbc:Telephone>+39 0110000000</cbc:Telephone>
            <cbc:ElectronicMail>esempio@cert.example.it</cbc:ElectronicMail>
          </cac:Contact>
        </efac:Company>
      </efac:Organization>
      <efac:Organization>
        <efac:Company>
          <cbc:WebsiteURI>https://www.giustizia-amministrativa.it/</cbc:WebsiteURI>
          <cac:PartyIdentification><cbc:ID schemeName="organization">ORG-0002</cbc:ID></cac:PartyIdentification>
          <cac:PartyName><cbc:Name languageID="ITA">TAR ESEMPIO</cbc:Name></cac:PartyName>
        </efac:Company>
      </efac:Organization>
    </efac:Organizations>
  </efext:EformsExtension></ext:ExtensionContent></ext:UBLExtension></ext:UBLExtensions>
  <cbc:ContractFolderID>G00749</cbc:ContractFolderID>
  <cac:ContractingParty>
    <cbc:BuyerProfileURI>https://appalti.comune.esempio.it/PortaleAppalti/</cbc:BuyerProfileURI>
    <cac:Party><cac:PartyIdentification><cbc:ID schemeName="organization">ORG-0001</cbc:ID></cac:PartyIdentification></cac:Party>
  </cac:ContractingParty>
  <cac:TenderingProcess>
    <cbc:ProcedureCode listName="procurement-procedure-type">open</cbc:ProcedureCode>
    <cac:TenderSubmissionDeadlinePeriod>
      <cbc:EndDate>2026-08-24+02:00</cbc:EndDate>
      <cbc:EndTime>08:00:00+02:00</cbc:EndTime>
    </cac:TenderSubmissionDeadlinePeriod>
  </cac:TenderingProcess>
  <cac:TenderingTerms>
    <cac:AppealTerms>
      <cac:AppealReceiverParty><cac:PartyIdentification><cbc:ID schemeName="organization">ORG-0002</cbc:ID></cac:PartyIdentification></cac:AppealReceiverParty>
      <cac:AppealInformationParty><cac:PartyIdentification><cbc:ID schemeName="organization">ORG-0001</cbc:ID></cac:PartyIdentification></cac:AppealInformationParty>
    </cac:AppealTerms>
  </cac:TenderingTerms>
  <cac:ProcurementProjectLot>
    <cbc:ID schemeName="Lot">LOT-0001</cbc:ID>
    <cac:ProcurementProject>
      <cbc:Name languageID="ITA">Ristorazione scolastica</cbc:Name>
      <cac:RequestedTenderTotal><cbc:EstimatedOverallContractAmount currencyID="EUR">4450600</cbc:EstimatedOverallContractAmount></cac:RequestedTenderTotal>
      <cac:MainCommodityClassification><cbc:ItemClassificationCode listName="cpv">55524000</cbc:ItemClassificationCode></cac:MainCommodityClassification>
      <cac:AdditionalCommodityClassification><cbc:ItemClassificationCode listName="cpv">55523100</cbc:ItemClassificationCode></cac:AdditionalCommodityClassification>
    </cac:ProcurementProject>
    <cac:TenderingTerms>
      <cac:CallForTendersDocumentReference>
        <cbc:ID>G00749</cbc:ID>
        <cac:Attachment><cac:ExternalReference><cbc:URI>https://appalti.comune.esempio.it/PortaleAppalti/it/procedure/codice/G00749</cbc:URI></cac:ExternalReference></cac:Attachment>
      </cac:CallForTendersDocumentReference>
      <cac:TenderRecipientParty><cbc:EndpointID>https://appalti.comune.esempio.it/PortaleAppalti/it/procedure/codice/G00749</cbc:EndpointID></cac:TenderRecipientParty>
      <cac:AwardingTerms><cac:AwardingCriterion>
        <cac:SubordinateAwardingCriterion>
          <ext:UBLExtensions><ext:UBLExtension><ext:ExtensionContent><efext:EformsExtension xmlns:efext="urn:efext">
            <efac:AwardCriterionParameter>
              <efbc:ParameterCode listName="number-weight">per-exa</efbc:ParameterCode>
              <efbc:ParameterNumeric>5</efbc:ParameterNumeric>
            </efac:AwardCriterionParameter>
          </efext:EformsExtension></ext:ExtensionContent></ext:UBLExtension></ext:UBLExtensions>
          <cbc:AwardingCriterionTypeCode>quality</cbc:AwardingCriterionTypeCode>
          <cbc:Description languageID="ITA">GESTIONE DELLE ECCEDENZE ALIMENTARI</cbc:Description>
        </cac:SubordinateAwardingCriterion>
      </cac:AwardingCriterion></cac:TenderingTerms>
  </cac:ProcurementProjectLot>
</ContractNotice>`

func TestParseDossier(t *testing.T) {
	d, err := ParseDossier([]byte(sampleDossier))
	if err != nil {
		t.Fatalf("ParseDossier: %v", err)
	}

	if d.InternalID != "G00749" {
		t.Errorf("internal id = %q, want G00749", d.InternalID)
	}
	if d.ProcedureType != "open" {
		t.Errorf("procedure type = %q, want open", d.ProcedureType)
	}
	// The time of day is the part a reader most easily misses.
	if d.DeadlineDate != "2026-08-24" || d.DeadlineTime != "08:00" {
		t.Errorf("deadline = %q %q, want 2026-08-24 08:00", d.DeadlineDate, d.DeadlineTime)
	}

	if d.Buyer == nil {
		t.Fatal("buyer not resolved")
	}
	if d.Buyer.Name != "COMUNE DI ESEMPIO" || d.Buyer.CompanyID != "01472860012" {
		t.Errorf("buyer = %+v", *d.Buyer)
	}
	if d.Buyer.Email != "esempio@cert.example.it" || d.Buyer.Phone == "" {
		t.Errorf("buyer contacts missing: %+v", *d.Buyer)
	}

	// Both organisations must keep their roles, not just the last one appended.
	if len(d.Organizations) != 2 {
		t.Fatalf("got %d organisations, want 2", len(d.Organizations))
	}
	if !hasString(d.Organizations[0].Roles, "buyer") {
		t.Errorf("first organisation lost its role: %v", d.Organizations[0].Roles)
	}
	if !hasString(d.Organizations[1].Roles, LinkAppealBody) {
		t.Errorf("second organisation lost its role: %v", d.Organizations[1].Roles)
	}

	if len(d.Lots) != 1 {
		t.Fatalf("got %d lots, want 1", len(d.Lots))
	}
	lot := d.Lots[0]
	if lot.Value != "4450600" || lot.Currency != "EUR" {
		t.Errorf("lot value = %q %q", lot.Value, lot.Currency)
	}
	if len(lot.CPV) != 2 {
		t.Errorf("lot cpv = %v, want both main and additional", lot.CPV)
	}
	if len(lot.Criteria) != 1 || lot.Criteria[0].Weight != "5" {
		t.Errorf("lot criteria = %+v", lot.Criteria)
	}
}

func TestDossierLinkRoles(t *testing.T) {
	d, err := ParseDossier([]byte(sampleDossier))
	if err != nil {
		t.Fatalf("ParseDossier: %v", err)
	}

	roles := map[string]string{}
	for _, l := range d.Links {
		roles[l.Role] = l.URL
	}

	want := map[string]string{
		LinkTenderDocuments: "https://appalti.comune.esempio.it/PortaleAppalti/it/procedure/codice/G00749",
		LinkBuyerProfile:    "https://appalti.comune.esempio.it/PortaleAppalti/",
		LinkBuyerWebsite:    "https://www.comune.esempio.it/",
		// The appeals court is a link in the notice but never a document source.
		LinkAppealBody: "https://www.giustizia-amministrativa.it/",
	}
	for role, url := range want {
		if roles[role] != url {
			t.Errorf("link role %s = %q, want %q", role, roles[role], url)
		}
	}

	if got := d.DocumentsURL(); got != want[LinkTenderDocuments] {
		t.Errorf("DocumentsURL() = %q, want the tender-documents link", got)
	}
}
