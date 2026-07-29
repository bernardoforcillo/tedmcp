package ted

import (
	"encoding/xml"
	"fmt"
	"strings"
)

// Roles a link can play in a notice. A notice lists several URLs and they are
// not interchangeable: the appeal body's site is not somewhere to find the
// capitolato, and the buyer's homepage is not where offers are submitted.
const (
	LinkTenderDocuments = "tender-documents"    // where the capitolato and disciplinare are published
	LinkSubmission      = "submission-endpoint" // where offers must be filed
	LinkBuyerProfile    = "buyer-profile"       // the buyer's procurement portal root
	LinkBuyerWebsite    = "buyer-website"       // the buyer's institutional site
	LinkAppealBody      = "appeal-body"         // the court that hears appeals — not a document source
	LinkOther           = "other"
)

// Organization is a party named in the notice.
type Organization struct {
	ID        string   `json:"id,omitempty"`
	Name      string   `json:"name,omitempty"`
	CompanyID string   `json:"company_id,omitempty" jsonschema:"national registration number; in Italy the codice fiscale / partita IVA, which joins to ANAC open data"`
	Roles     []string `json:"roles,omitempty" jsonschema:"buyer, appeal-body, appeal-information, mediation"`
	Email     string   `json:"email,omitempty"`
	Phone     string   `json:"phone,omitempty"`
	Website   string   `json:"website,omitempty"`
	City      string   `json:"city,omitempty"`
	Country   string   `json:"country,omitempty"`
}

// Lot is one awardable part of a procurement.
type Lot struct {
	ID            string           `json:"id,omitempty"`
	Title         string           `json:"title,omitempty"`
	Value         string           `json:"value,omitempty"`
	Currency      string           `json:"currency,omitempty"`
	CPV           []string         `json:"cpv,omitempty"`
	DeadlineDate  string           `json:"deadline_date,omitempty"`
	DeadlineTime  string           `json:"deadline_time,omitempty" jsonschema:"time of day the submission deadline falls, which is often early morning and easy to miss"`
	DocumentsURL  string           `json:"documents_url,omitempty"`
	SubmissionURL string           `json:"submission_url,omitempty"`
	Criteria      []AwardCriterion `json:"criteria,omitempty"`
}

// DocumentLink is a URL from the notice together with what it actually is.
type DocumentLink struct {
	URL  string `json:"url"`
	Role string `json:"role"`
	Note string `json:"note,omitempty"`
}

// Dossier is everything the notice itself tells us about a procurement: who is
// buying, how to reach them, exactly when offers close, what the lots are, and
// which link leads where. All of it comes from the same XML already fetched to
// read the award criteria, so assembling it costs no extra request.
type Dossier struct {
	InternalID    string         `json:"internal_id,omitempty" jsonschema:"the buyer's own reference for the procedure, e.g. G00749"`
	ProcedureType string         `json:"procedure_type,omitempty" jsonschema:"open, restricted, negotiated…"`
	DeadlineDate  string         `json:"deadline_date,omitempty"`
	DeadlineTime  string         `json:"deadline_time,omitempty"`
	Buyer         *Organization  `json:"buyer,omitempty"`
	Organizations []Organization `json:"organizations,omitempty"`
	Lots          []Lot          `json:"lots,omitempty"`
	Links         []DocumentLink `json:"links,omitempty"`
}

// ParseDossier reads the notice-level detail out of an eForms document.
func ParseDossier(data []byte) (*Dossier, error) {
	var root xmlRoot
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("parse eForms dossier: %w", err)
	}

	d := &Dossier{
		ProcedureType: clean(root.Process.ProcedureCode),
		DeadlineDate:  trimDate(clean(root.Process.Deadline.EndDate)),
		DeadlineTime:  trimTime(clean(root.Process.Deadline.EndTime)),
	}

	// Organisations are declared once and referred to by ID from the places
	// that give them a role, so collect them first and label them after.
	// Index by position, not by pointer: appending reallocates the slice and
	// would strand any pointer taken before the growth.
	indexByID := map[string]int{}
	for _, o := range root.Organizations {
		org := Organization{
			ID:        clean(o.Company.ID),
			Name:      clean(o.Company.Name),
			CompanyID: clean(o.Company.CompanyID),
			Email:     clean(o.Company.Email),
			Phone:     clean(o.Company.Phone),
			Website:   clean(o.Company.WebsiteURI),
			City:      clean(o.Company.City),
			Country:   clean(o.Company.Country),
		}
		if org.ID == "" && org.Name == "" {
			continue
		}
		d.Organizations = append(d.Organizations, org)
		indexByID[org.ID] = len(d.Organizations) - 1
	}

	addRole := func(id, role string) {
		if i, ok := indexByID[clean(id)]; ok {
			d.Organizations[i].Roles = appendUnique(d.Organizations[i].Roles, role)
		}
	}
	addRole(root.ContractingParty.PartyID, "buyer")
	for _, terms := range root.allTerms() {
		addRole(terms.Appeal.Receiver.ID, LinkAppealBody)
		addRole(terms.Appeal.Information.ID, "appeal-information")
		addRole(terms.Appeal.Mediation.ID, "mediation")
	}
	for i := range d.Organizations {
		if hasString(d.Organizations[i].Roles, "buyer") {
			d.Buyer = &d.Organizations[i]
			break
		}
	}

	for _, l := range root.Lots {
		lot := Lot{
			ID:            clean(l.ID),
			Title:         clean(l.Project.Name),
			Value:         clean(l.Project.Total.Amount.Value),
			Currency:      clean(l.Project.Total.Amount.Currency),
			CPV:           l.Project.cpv(),
			DeadlineDate:  trimDate(clean(l.Process.Deadline.EndDate)),
			DeadlineTime:  trimTime(clean(l.Process.Deadline.EndTime)),
			DocumentsURL:  clean(l.Terms.Documents.URI),
			SubmissionURL: clean(l.Terms.Recipient.EndpointID),
			Criteria:      l.Terms.Awarding.Criterion.criteria(),
		}
		d.Lots = append(d.Lots, lot)
		if d.DeadlineDate == "" {
			d.DeadlineDate, d.DeadlineTime = lot.DeadlineDate, lot.DeadlineTime
		}
		// The reference attached to the tender documents is the one printed on
		// the buyer's portal (G00749); the folder ID is often a bare UUID that
		// means nothing to anyone looking the procedure up.
		if ref := clean(l.Terms.Documents.ID); isHumanReference(ref) {
			d.InternalID = ref
		}
	}
	if d.InternalID == "" {
		d.InternalID = clean(root.ContractFolderID)
	}

	d.Links = d.buildLinks(root)
	return d, nil
}

// buildLinks classifies every URL in the notice by what it leads to, so the
// one that actually reaches the tender documents is not lost among the rest.
func (d *Dossier) buildLinks(root xmlRoot) []DocumentLink {
	var links []DocumentLink
	seen := map[string]bool{}

	add := func(url, role, note string) {
		url = clean(url)
		if url == "" || !strings.HasPrefix(url, "http") || seen[url] {
			return
		}
		seen[url] = true
		links = append(links, DocumentLink{URL: url, Role: role, Note: note})
	}

	for _, terms := range root.allTerms() {
		add(terms.Documents.URI, LinkTenderDocuments, "procedure page named by the notice as the source of the tender documents")
		add(terms.Recipient.EndpointID, LinkSubmission, "where offers are submitted")
	}
	add(root.ContractingParty.BuyerProfileURI, LinkBuyerProfile, "buyer's procurement portal")

	// A buyer that also names itself as the body hearing appeals is still, for
	// our purposes, the buyer — test that role first.
	for _, o := range d.Organizations {
		switch {
		case hasString(o.Roles, "buyer"):
			add(o.Website, LinkBuyerWebsite, o.Name)
		case hasString(o.Roles, LinkAppealBody):
			add(o.Website, LinkAppealBody, "review body ("+o.Name+") — appeals, not tender documents")
		default:
			add(o.Website, LinkOther, o.Name)
		}
	}
	return links
}

// DocumentsURL returns the best link to the tender documents, or "" if the
// notice names none.
func (d *Dossier) DocumentsURL() string {
	for _, role := range []string{LinkTenderDocuments, LinkSubmission, LinkBuyerProfile} {
		for _, l := range d.Links {
			if l.Role == role {
				return l.URL
			}
		}
	}
	return ""
}

// --- XML mapping ---
//
// Field tags name only the local element, so the eForms namespace prefixes
// (cac:, cbc:, efac:, efbc:) do not have to be spelled out.

type xmlRoot struct {
	ContractFolderID string            `xml:"ContractFolderID"`
	Organizations    []xmlOrganization `xml:"UBLExtensions>UBLExtension>ExtensionContent>EformsExtension>Organizations>Organization"`
	ContractingParty struct {
		BuyerProfileURI string `xml:"BuyerProfileURI"`
		PartyID         string `xml:"Party>PartyIdentification>ID"`
	} `xml:"ContractingParty"`
	Process xmlProcess `xml:"TenderingProcess"`
	Terms   xmlTerms   `xml:"TenderingTerms"`
	Lots    []xmlLot   `xml:"ProcurementProjectLot"`
}

// allTerms returns the notice-level terms plus every lot's, since single-lot
// notices put everything on the lot and multi-lot ones split it.
func (r xmlRoot) allTerms() []xmlTerms {
	terms := []xmlTerms{r.Terms}
	for _, l := range r.Lots {
		terms = append(terms, l.Terms)
	}
	return terms
}

type xmlOrganization struct {
	Company struct {
		WebsiteURI string `xml:"WebsiteURI"`
		ID         string `xml:"PartyIdentification>ID"`
		Name       string `xml:"PartyName>Name"`
		City       string `xml:"PostalAddress>CityName"`
		Country    string `xml:"PostalAddress>Country>IdentificationCode"`
		CompanyID  string `xml:"PartyLegalEntity>CompanyID"`
		Phone      string `xml:"Contact>Telephone"`
		Email      string `xml:"Contact>ElectronicMail"`
	} `xml:"Company"`
}

type xmlProcess struct {
	ProcedureCode string `xml:"ProcedureCode"`
	Deadline      struct {
		EndDate string `xml:"EndDate"`
		EndTime string `xml:"EndTime"`
	} `xml:"TenderSubmissionDeadlinePeriod"`
}

type xmlTerms struct {
	Documents struct {
		ID  string `xml:"ID"`
		URI string `xml:"Attachment>ExternalReference>URI"`
	} `xml:"CallForTendersDocumentReference"`
	Recipient struct {
		EndpointID string `xml:"EndpointID"`
	} `xml:"TenderRecipientParty"`
	Appeal struct {
		Receiver    xmlPartyRef `xml:"AppealReceiverParty>PartyIdentification"`
		Information xmlPartyRef `xml:"AppealInformationParty>PartyIdentification"`
		Mediation   xmlPartyRef `xml:"MediationParty>PartyIdentification"`
	} `xml:"AppealTerms"`
	Awarding struct {
		Criterion xmlAwardingCriterion `xml:"AwardingCriterion"`
	} `xml:"AwardingTerms"`
}

type xmlPartyRef struct {
	ID string `xml:"ID"`
}

type xmlAwardingCriterion struct {
	Subordinate []xmlSubCriterion `xml:"SubordinateAwardingCriterion"`
}

func (c xmlAwardingCriterion) criteria() []AwardCriterion {
	var out []AwardCriterion
	for _, s := range c.Subordinate {
		crit := AwardCriterion{
			Type:        clean(s.TypeCode),
			Name:        clean(s.Name),
			Description: clean(s.Description),
			Weight:      s.weight(),
		}
		if crit.Text() != "" {
			out = append(out, crit)
		}
	}
	return dedupeCriteria(out)
}

type xmlSubCriterion struct {
	TypeCode    string         `xml:"AwardingCriterionTypeCode"`
	Name        string         `xml:"Name"`
	Description string         `xml:"Description"`
	Parameters  []xmlCritParam `xml:"UBLExtensions>UBLExtension>ExtensionContent>EformsExtension>AwardCriterionParameter"`
}

// weight picks the number that is the criterion's score. The same block also
// carries minimum scores and fixed values, which are not weights.
func (s xmlSubCriterion) weight() string {
	for _, p := range s.Parameters {
		if p.Code.ListName == "number-weight" {
			return clean(p.Numeric)
		}
	}
	return ""
}

type xmlCritParam struct {
	Code struct {
		ListName string `xml:"listName,attr"`
	} `xml:"ParameterCode"`
	Numeric string `xml:"ParameterNumeric"`
}

type xmlLot struct {
	ID      string     `xml:"ID"`
	Project xmlProject `xml:"ProcurementProject"`
	Process xmlProcess `xml:"TenderingProcess"`
	Terms   xmlTerms   `xml:"TenderingTerms"`
}

type xmlProject struct {
	Name  string `xml:"Name"`
	Total struct {
		Amount struct {
			Value    string `xml:",chardata"`
			Currency string `xml:"currencyID,attr"`
		} `xml:"EstimatedOverallContractAmount"`
	} `xml:"RequestedTenderTotal"`
	Main       xmlClassification   `xml:"MainCommodityClassification"`
	Additional []xmlClassification `xml:"AdditionalCommodityClassification"`
}

func (p xmlProject) cpv() []string {
	out := []string{clean(p.Main.Code)}
	for _, a := range p.Additional {
		out = append(out, clean(a.Code))
	}
	return dedupe(nonEmpty(out))
}

type xmlClassification struct {
	Code string `xml:"ItemClassificationCode"`
}

// --- helpers ---

func clean(s string) string {
	return cleanText(s)
}

// isHumanReference reports whether an identifier is one a person could quote
// to the buyer, as opposed to a machine-generated UUID.
func isHumanReference(s string) bool {
	if s == "" || len(s) > 40 {
		return false
	}
	dashes := strings.Count(s, "-")
	return !(len(s) == 36 && dashes == 4)
}

// trimTime keeps the wall-clock part of an eForms time like "08:00:00+02:00".
func trimTime(s string) string {
	if len(s) >= 5 {
		return s[:5]
	}
	return s
}

func appendUnique(list []string, v string) []string {
	if hasString(list, v) {
		return list
	}
	return append(list, v)
}

func hasString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func unmarshalLoose(data []byte, v any) error {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	return dec.Decode(v)
}
