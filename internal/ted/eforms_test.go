package ted

import "testing"

// sampleNotice mirrors the shape of a real eForms notice: namespaced elements,
// a scoring grid repeated per lot, numbers that are weights and numbers that
// are thresholds, and text that TED escapes twice.
const sampleNotice = `<?xml version="1.0" encoding="UTF-8"?>
<ContractNotice xmlns:cac="urn:cac" xmlns:cbc="urn:cbc" xmlns:ext="urn:ext" xmlns:efac="urn:efac" xmlns:efbc="urn:efbc">
  <cac:ProcurementProject>
    <cbc:Description languageID="ITA">Servizio di refezione scolastica per le scuole dell&amp;#8217;infanzia</cbc:Description>
    <cac:ProcurementAdditionalType>
      <cbc:ProcurementTypeCode listName="strategic-procurement">env-imp</cbc:ProcurementTypeCode>
    </cac:ProcurementAdditionalType>
    <cbc:ProcurementTypeCode listName="contract-nature">services</cbc:ProcurementTypeCode>
  </cac:ProcurementProject>
  <cac:AwardingTerms>
    <cac:AwardingCriterion>
      <cac:SubordinateAwardingCriterion>
        <ext:UBLExtensions><ext:UBLExtension><ext:ExtensionContent>
          <efac:AwardCriterionParameter>
            <efbc:ParameterCode listName="number-threshold">min-score</efbc:ParameterCode>
            <efbc:ParameterNumeric>3</efbc:ParameterNumeric>
          </efac:AwardCriterionParameter>
          <efac:AwardCriterionParameter>
            <efbc:ParameterCode listName="number-weight">per-exa</efbc:ParameterCode>
            <efbc:ParameterNumeric>5</efbc:ParameterNumeric>
          </efac:AwardCriterionParameter>
        </ext:ExtensionContent></ext:UBLExtension></ext:UBLExtensions>
        <cbc:AwardingCriterionTypeCode listName="award-criterion-type">quality</cbc:AwardingCriterionTypeCode>
        <cbc:Description languageID="ITA">GESTIONE DELLE ECCEDENZE ALIMENTARI
        progetto di recupero degli avanzi alimentari</cbc:Description>
      </cac:SubordinateAwardingCriterion>
      <cac:SubordinateAwardingCriterion>
        <cbc:AwardingCriterionTypeCode listName="award-criterion-type">price</cbc:AwardingCriterionTypeCode>
        <cbc:Description languageID="ITA">Offerta economica</cbc:Description>
      </cac:SubordinateAwardingCriterion>
    </cac:AwardingCriterion>
  </cac:AwardingTerms>
  <cac:AwardingTerms>
    <cac:AwardingCriterion>
      <cac:SubordinateAwardingCriterion>
        <ext:UBLExtensions><ext:UBLExtension><ext:ExtensionContent>
          <efac:AwardCriterionParameter>
            <efbc:ParameterCode listName="number-weight">per-exa</efbc:ParameterCode>
            <efbc:ParameterNumeric>5</efbc:ParameterNumeric>
          </efac:AwardCriterionParameter>
        </ext:ExtensionContent></ext:UBLExtension></ext:UBLExtensions>
        <cbc:AwardingCriterionTypeCode listName="award-criterion-type">quality</cbc:AwardingCriterionTypeCode>
        <cbc:Description languageID="ITA">GESTIONE DELLE ECCEDENZE ALIMENTARI
        progetto di recupero degli avanzi alimentari</cbc:Description>
      </cac:SubordinateAwardingCriterion>
    </cac:AwardingCriterion>
  </cac:AwardingTerms>
  <cac:CallForTendersDocumentReference>
    <cac:Attachment><cac:ExternalReference>
      <cbc:URI>https://appalti.example.it/PortaleAppalti/it/procedure/codice/G00749</cbc:URI>
    </cac:ExternalReference></cac:Attachment>
  </cac:CallForTendersDocumentReference>
</ContractNotice>`

func TestParseEForms(t *testing.T) {
	got, err := ParseEForms([]byte(sampleNotice))
	if err != nil {
		t.Fatalf("ParseEForms: %v", err)
	}

	// The grid is restated for every lot; identical criteria collapse to one.
	if len(got.AwardCriteria) != 2 {
		t.Fatalf("got %d criteria, want 2: %+v", len(got.AwardCriteria), got.AwardCriteria)
	}

	first := got.AwardCriteria[0]
	if first.Type != "quality" {
		t.Errorf("criterion type = %q, want quality", first.Type)
	}
	// 3 is the minimum score to stay in the running; 5 is what the criterion is worth.
	if first.Weight != "5" {
		t.Errorf("criterion weight = %q, want 5 (the weight, not the threshold)", first.Weight)
	}
	want := "GESTIONE DELLE ECCEDENZE ALIMENTARI progetto di recupero degli avanzi alimentari"
	if first.Description != want {
		t.Errorf("criterion description = %q, want %q", first.Description, want)
	}
	if got.AwardCriteria[1].Weight != "" {
		t.Errorf("criterion without a published weight = %q, want empty", got.AwardCriteria[1].Weight)
	}

	// TED escapes entities twice, so a decoded node still holds "&#8217;".
	wantDesc := "Servizio di refezione scolastica per le scuole dell’infanzia"
	if len(got.Descriptions) != 1 || got.Descriptions[0] != wantDesc {
		t.Errorf("descriptions = %q, want [%q]", got.Descriptions, wantDesc)
	}

	if len(got.Strategic) != 1 || got.Strategic[0] != "env-imp" {
		t.Errorf("strategic = %q, want [env-imp] (contract-nature must not leak in)", got.Strategic)
	}
	if len(got.DocumentURLs) != 1 {
		t.Fatalf("document urls = %q, want 1", got.DocumentURLs)
	}
}

func TestParseEFormsEmptyDocument(t *testing.T) {
	got, err := ParseEForms([]byte(`<ContractNotice></ContractNotice>`))
	if err != nil {
		t.Fatalf("ParseEForms: %v", err)
	}
	if len(got.AwardCriteria) != 0 || len(got.Descriptions) != 0 {
		t.Fatalf("expected empty content, got %+v", got)
	}
}
