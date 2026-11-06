package ted

import (
	"fmt"
	"strconv"
	"strings"
)

// A procurement is not one notice but a sequence of them: a prior information
// notice, the contract notice, perhaps a change notice moving the deadline, and
// finally the award notice saying who won and for how much. eForms ties them
// together with the procedure identifier (ContractFolderID), and the award
// notice carries a results section that the other parsers here ignore.
//
// Procedure reads what research teams ask of that sequence: the outcome of each
// lot (winner, number of bids, awarded value), how long the contract runs and
// whether it can be renewed — which is what says when it will be tendered
// again — and, for a change notice, what changed.

// Lot outcomes, as coded by eForms (winner-selection-status).
const (
	OutcomeWinner   = "selec-w" // a winner was chosen
	OutcomeOpen     = "open-nw" // the competition is still open
	OutcomeNoWinner = "clos-nw" // closed without a winner
)

// Procedure is the procedure-level reading of one eForms notice.
type Procedure struct {
	ProcedureID  string         `json:"procedure_id,omitempty" jsonschema:"the eForms procedure identifier shared by every notice of this procurement"`
	NoticeID     string         `json:"notice_id,omitempty"`
	Version      string         `json:"version,omitempty"`
	NoticeType   string         `json:"notice_type,omitempty"`
	Language     string         `json:"language,omitempty"`
	IssueDate    string         `json:"issue_date,omitempty"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description,omitempty"`
	Buyer        *Organization  `json:"buyer,omitempty"`
	TotalAwarded string         `json:"total_awarded,omitempty"`
	Currency     string         `json:"currency,omitempty"`
	Lots         []ProcedureLot `json:"lots,omitempty"`
	Changes      *NoticeChanges `json:"changes,omitempty" jsonschema:"present on a change notice: what it corrects and why"`
}

// ProcedureLot is one lot with its contract terms and, on an award notice, its
// outcome.
type ProcedureLot struct {
	ID                 string      `json:"id,omitempty"`
	Title              string      `json:"title,omitempty"`
	CPV                []string    `json:"cpv,omitempty"`
	EstimatedValue     string      `json:"estimated_value,omitempty"`
	Currency           string      `json:"currency,omitempty"`
	Duration           Duration    `json:"duration,omitzero"`
	RenewalDescription string      `json:"renewal,omitempty" jsonschema:"how the buyer describes the renewal option, verbatim"`
	MaxRenewals        int         `json:"max_renewals,omitempty"`
	OptionsDescription string      `json:"options,omitempty"`
	FrameworkAgreement string      `json:"framework_agreement,omitempty" jsonschema:"eForms framework-agreement code when the lot is one (fa-wo-rc, fa-w-rc, fa-mix)"`
	Outcome            *LotOutcome `json:"outcome,omitempty"`
}

// LotOutcome is what an award notice says happened to a lot.
type LotOutcome struct {
	Status         string   `json:"status,omitempty" jsonschema:"selec-w (winner chosen), open-nw (still open), clos-nw (closed without a winner)"`
	TendersCount   int      `json:"tenders_received" jsonschema:"number of tenders received; -1 when the notice does not say"`
	Winners        []Winner `json:"winners,omitempty"`
	AwardedValue   string   `json:"awarded_value,omitempty"`
	Currency       string   `json:"currency,omitempty"`
	AwardDate      string   `json:"award_date,omitempty" jsonschema:"when the winner was chosen"`
	ContractDate   string   `json:"contract_date,omitempty" jsonschema:"when the contract was signed"`
	DecisionReason string   `json:"decision_reason,omitempty" jsonschema:"why no winner was chosen, when that is the outcome"`
}

// Winner is an economic operator awarded a lot.
type Winner struct {
	Name      string `json:"name"`
	CompanyID string `json:"company_id,omitempty" jsonschema:"national registration number (in Italy the partita IVA / codice fiscale)"`
	Size      string `json:"size,omitempty" jsonschema:"micro, small, medium or large, as declared in the notice"`
	City      string `json:"city,omitempty"`
	Country   string `json:"country,omitempty"`
}

// NoticeChanges is the change section of a notice that corrects an earlier one.
type NoticeChanges struct {
	ChangedNotice string   `json:"changed_notice,omitempty" jsonschema:"identifier of the notice being changed"`
	ReasonCode    string   `json:"reason_code,omitempty"`
	Reason        string   `json:"reason,omitempty"`
	Descriptions  []string `json:"descriptions,omitempty"`
	Sections      []string `json:"sections,omitempty" jsonschema:"the lots or parts of the notice the change touches"`
}

// ParseProcedure reads the procedure, lot terms, results and changes of an
// eForms notice. It never fails on a notice without results: a contract notice
// simply has no outcomes.
func ParseProcedure(data []byte) (*Procedure, error) {
	var root xmlProcRoot
	if err := unmarshalLoose(data, &root); err != nil {
		return nil, fmt.Errorf("parse eForms procedure: %w", err)
	}

	p := &Procedure{
		ProcedureID: clean(root.ContractFolderID),
		NoticeID:    clean(root.ID),
		Version:     clean(root.VersionID),
		NoticeType:  clean(root.NoticeTypeCode),
		Language:    normalizeLang(clean(root.NoticeLanguageCode)),
		IssueDate:   realDate(root.IssueDate),
		Title:       clean(root.Project.Name),
		Description: clean(root.Project.Description),
	}

	ext := root.Extension
	orgs := map[string]xmlOrganization{}
	for _, o := range ext.Organizations {
		orgs[clean(o.Company.ID)] = o
	}
	if o, ok := orgs[clean(root.ContractingParty.PartyID)]; ok {
		p.Buyer = &Organization{
			ID:        clean(o.Company.ID),
			Name:      clean(o.Company.Name),
			CompanyID: clean(o.Company.CompanyID),
			City:      clean(o.Company.City),
			Country:   clean(o.Company.Country),
			Roles:     []string{"buyer"},
		}
	}

	res := ext.NoticeResult
	p.TotalAwarded = clean(res.TotalAmount.Value)
	p.Currency = clean(res.TotalAmount.Currency)

	tenders := map[string]xmlLotTender{}
	for _, t := range res.LotTenders {
		tenders[clean(t.ID)] = t
	}
	contracts := map[string]xmlSettledContract{}
	for _, c := range res.Contracts {
		contracts[clean(c.ID)] = c
	}
	parties := map[string]xmlTenderingParty{}
	for _, tp := range res.Parties {
		parties[clean(tp.ID)] = tp
	}
	outcomes := map[string]*LotOutcome{}
	for _, lr := range res.LotResults {
		outcomes[clean(lr.LotID)] = lr.outcome(tenders, contracts, parties, orgs)
	}

	for _, l := range root.Lots {
		lot := ProcedureLot{
			ID:                 clean(l.ID),
			Title:              clean(l.Project.Name),
			CPV:                l.Project.cpv(),
			EstimatedValue:     clean(l.Project.Total.Amount.Value),
			Currency:           clean(l.Project.Total.Amount.Currency),
			Duration:           l.Project.Period.duration(),
			RenewalDescription: clean(l.Project.Extension.Renewal),
			OptionsDescription: clean(l.Project.Extension.Options),
			Outcome:            outcomes[clean(l.ID)],
		}
		if n, err := strconv.Atoi(clean(l.Project.Extension.MaxRenewals)); err == nil {
			lot.MaxRenewals = n
		}
		for _, cs := range l.Process.ContractingSystems {
			if cs.Code.ListName == "framework-agreement" && clean(cs.Code.Value) != "none" {
				lot.FrameworkAgreement = clean(cs.Code.Value)
			}
		}
		// Many buyers leave the planned period empty and write the duration
		// into the title instead: "PERIODO dal 01/01/2027 al 31/12/2028".
		if lot.Duration.Empty() {
			lot.Duration = InferDuration(lot.Title, clean(l.Project.Description), p.Title, p.Description)
		}
		lot.Duration.RenewalMonths = MonthsIn(lot.RenewalDescription)
		p.Lots = append(p.Lots, lot)
	}

	// A results section can name lots the notice does not otherwise describe.
	for id, o := range outcomes {
		found := false
		for _, l := range p.Lots {
			if l.ID == id {
				found = true
				break
			}
		}
		if !found {
			p.Lots = append(p.Lots, ProcedureLot{ID: id, Outcome: o})
		}
	}

	if ch := ext.Changes; ch.present() {
		c := &NoticeChanges{
			ChangedNotice: clean(ch.ChangedNotice),
			ReasonCode:    clean(ch.Reason.Code),
			Reason:        clean(ch.Reason.Description),
		}
		for _, change := range ch.Changes {
			if d := clean(change.Description); d != "" {
				c.Descriptions = appendUnique(c.Descriptions, d)
			}
			for _, s := range change.Sections {
				if s = clean(s); s != "" {
					c.Sections = appendUnique(c.Sections, s)
				}
			}
		}
		p.Changes = c
	}
	return p, nil
}

// Awarded reports whether any lot of the procedure has a winner.
func (p *Procedure) Awarded() bool {
	for _, l := range p.Lots {
		if l.Outcome != nil && l.Outcome.Status == OutcomeWinner {
			return true
		}
	}
	return false
}

// --- XML mapping ---

type xmlProcRoot struct {
	ID                 string `xml:"ID"`
	ContractFolderID   string `xml:"ContractFolderID"`
	VersionID          string `xml:"VersionID"`
	IssueDate          string `xml:"IssueDate"`
	NoticeTypeCode     string `xml:"NoticeTypeCode"`
	NoticeLanguageCode string `xml:"NoticeLanguageCode"`
	Extension          struct {
		NoticeResult  xmlNoticeResult   `xml:"NoticeResult"`
		Organizations []xmlOrganization `xml:"Organizations>Organization"`
		Changes       xmlChanges        `xml:"Changes"`
	} `xml:"UBLExtensions>UBLExtension>ExtensionContent>EformsExtension"`
	ContractingParty struct {
		PartyID string `xml:"Party>PartyIdentification>ID"`
	} `xml:"ContractingParty"`
	Project xmlProcProject `xml:"ProcurementProject"`
	Lots    []xmlProcLot   `xml:"ProcurementProjectLot"`
}

type xmlProcProject struct {
	Name        string              `xml:"Name"`
	Description string              `xml:"Description"`
	Total       xmlRequestedTotal   `xml:"RequestedTenderTotal"`
	Main        xmlClassification   `xml:"MainCommodityClassification"`
	Additional  []xmlClassification `xml:"AdditionalCommodityClassification"`
	Period      xmlPlannedPeriod    `xml:"PlannedPeriod"`
	Extension   struct {
		Options     string `xml:"OptionsDescription"`
		MaxRenewals string `xml:"MaximumNumberNumeric"`
		Renewal     string `xml:"Renewal>Period>Description"`
	} `xml:"ContractExtension"`
}

func (p xmlProcProject) cpv() []string {
	return xmlProject{Main: p.Main, Additional: p.Additional}.cpv()
}

type xmlRequestedTotal struct {
	Amount struct {
		Value    string `xml:",chardata"`
		Currency string `xml:"currencyID,attr"`
	} `xml:"EstimatedOverallContractAmount"`
}

type xmlPlannedPeriod struct {
	StartDate string `xml:"StartDate"`
	EndDate   string `xml:"EndDate"`
	Measure   struct {
		Value string `xml:",chardata"`
		Unit  string `xml:"unitCode,attr"`
	} `xml:"DurationMeasure"`
	DescriptionCode string `xml:"DescriptionCode"`
}

func (pp xmlPlannedPeriod) duration() Duration {
	d := Duration{Start: realDate(pp.StartDate), End: realDate(pp.EndDate)}
	if v, err := strconv.ParseFloat(clean(pp.Measure.Value), 64); err == nil && v > 0 {
		switch strings.ToUpper(clean(pp.Measure.Unit)) {
		case "DAY":
			d.Months = v / 30.4375
		case "WEEK":
			d.Months = v * 7 / 30.4375
		case "YEAR":
			d.Months = v * 12
		default: // MONTH, and the unit eForms uses when it says nothing
			d.Months = v
		}
	}
	if !d.Empty() {
		d.Source = DurationFromNotice
	}
	if code := strings.ToUpper(clean(pp.DescriptionCode)); code == "UNLIMITED" {
		d.Text = "unlimited duration"
	}
	return d
}

type xmlProcLot struct {
	ID      string         `xml:"ID"`
	Project xmlProcProject `xml:"ProcurementProject"`
	Process struct {
		ContractingSystems []struct {
			Code struct {
				Value    string `xml:",chardata"`
				ListName string `xml:"listName,attr"`
			} `xml:"ContractingSystemTypeCode"`
		} `xml:"ContractingSystem"`
	} `xml:"TenderingProcess"`
}

type xmlNoticeResult struct {
	TotalAmount struct {
		Value    string `xml:",chardata"`
		Currency string `xml:"currencyID,attr"`
	} `xml:"TotalAmount"`
	LotResults []xmlLotResult       `xml:"LotResult"`
	LotTenders []xmlLotTender       `xml:"LotTender"`
	Contracts  []xmlSettledContract `xml:"SettledContract"`
	Parties    []xmlTenderingParty  `xml:"TenderingParty"`
}

type xmlLotResult struct {
	ID         string   `xml:"ID"`
	Status     string   `xml:"TenderResultCode"`
	TenderIDs  []string `xml:"LotTender>ID"`
	Statistics []struct {
		Code    string `xml:"StatisticsCode"`
		Numeric string `xml:"StatisticsNumeric"`
	} `xml:"ReceivedSubmissionsStatistics"`
	ContractIDs []string `xml:"SettledContract>ID"`
	LotID       string   `xml:"TenderLot>ID"`
	Decision    string   `xml:"DecisionReason>DecisionReasonCode"`
}

type xmlLotTender struct {
	ID      string `xml:"ID"`
	Rank    string `xml:"RankCode"`
	Payable struct {
		Value    string `xml:",chardata"`
		Currency string `xml:"currencyID,attr"`
	} `xml:"LegalMonetaryTotal>PayableAmount"`
	PartyID string `xml:"TenderingParty>ID"`
}

type xmlSettledContract struct {
	ID        string   `xml:"ID"`
	AwardDate string   `xml:"AwardDate"`
	IssueDate string   `xml:"IssueDate"`
	TenderIDs []string `xml:"LotTender>ID"`
}

type xmlTenderingParty struct {
	ID          string   `xml:"ID"`
	Name        string   `xml:"Name"`
	TendererIDs []string `xml:"Tenderer>ID"`
}

type xmlChanges struct {
	ChangedNotice string `xml:"ChangedNoticeIdentifier"`
	Reason        struct {
		Code        string `xml:"ReasonCode"`
		Description string `xml:"ReasonDescription"`
	} `xml:"ChangeReason"`
	Changes []struct {
		Description string   `xml:"ChangeDescription"`
		Sections    []string `xml:"ChangedSection>ChangedSectionIdentifier"`
	} `xml:"Change"`
}

func (c xmlChanges) present() bool {
	return clean(c.ChangedNotice) != "" || clean(c.Reason.Code) != "" || len(c.Changes) > 0
}

func (lr xmlLotResult) outcome(tenders map[string]xmlLotTender, contracts map[string]xmlSettledContract,
	parties map[string]xmlTenderingParty, orgs map[string]xmlOrganization) *LotOutcome {

	o := &LotOutcome{
		Status:         clean(lr.Status),
		TendersCount:   -1,
		DecisionReason: clean(lr.Decision),
	}
	for _, s := range lr.Statistics {
		if clean(s.Code) == "tenders" {
			if n, err := strconv.Atoi(clean(s.Numeric)); err == nil {
				o.TendersCount = n
			}
		}
	}

	// The winning tenders are the ones a signed contract points to; a lot
	// result also lists tenders that merely ranked.
	var winning []string
	for _, cid := range lr.ContractIDs {
		c, ok := contracts[clean(cid)]
		if !ok {
			continue
		}
		if o.AwardDate == "" {
			o.AwardDate = realDate(c.AwardDate)
		}
		if o.ContractDate == "" {
			o.ContractDate = realDate(c.IssueDate)
		}
		for _, tid := range c.TenderIDs {
			winning = appendUnique(winning, clean(tid))
		}
	}
	if len(winning) == 0 && o.Status == OutcomeWinner {
		for _, tid := range lr.TenderIDs {
			winning = appendUnique(winning, clean(tid))
		}
	}

	var total float64
	var haveTotal bool
	for _, tid := range winning {
		t, ok := tenders[tid]
		if !ok {
			continue
		}
		if v, err := strconv.ParseFloat(clean(t.Payable.Value), 64); err == nil {
			total += v
			haveTotal = true
			o.Currency = clean(t.Payable.Currency)
		}
		party, ok := parties[clean(t.PartyID)]
		if !ok {
			continue
		}
		before := len(o.Winners)
		for _, oid := range party.TendererIDs {
			org, ok := orgs[clean(oid)]
			if !ok {
				continue
			}
			o.Winners = append(o.Winners, Winner{
				Name:      clean(org.Company.Name),
				CompanyID: clean(org.Company.CompanyID),
				Size:      clean(org.Company.Size),
				City:      clean(org.Company.City),
				Country:   clean(org.Company.Country),
			})
		}
		// A consortium is named on the tendering party; its members may not
		// be listed as organisations at all.
		if len(o.Winners) == before && clean(party.Name) != "" {
			o.Winners = append(o.Winners, Winner{Name: clean(party.Name)})
		}
	}
	if haveTotal {
		o.AwardedValue = strconv.FormatFloat(total, 'f', 2, 64)
	}
	return o
}

// realDate trims an eForms date and drops the placeholders some notices carry
// ("2000-01-01"), which would otherwise read as an award twenty years ago.
func realDate(s string) string {
	d := trimDate(clean(s))
	if len(d) < 4 || d[:4] < "2005" {
		return ""
	}
	return d
}
