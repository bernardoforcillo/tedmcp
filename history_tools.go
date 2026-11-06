package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/bernardoforcillo/tedmcp/internal/ted"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A notice is a snapshot. The deadline it states may have been extended by a
// change notice published last week, a lot may since have been cancelled, and
// the procedure may already be awarded. eForms gives every notice of one
// procurement the same procedure identifier, and TED can search by it, so the
// whole sequence can be read in one call — which is what anyone about to spend
// a week on a bid should do first.

func registerHistoryTools(s *mcp.Server, tc *ted.Client) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "get_procedure_history",
		Description: "Every notice of one procurement, in order — prior information, contract notice, change notices, award — " +
			"linked by the eForms procedure identifier. Reports what changed between them: deadlines moved, change notices " +
			"with their stated reasons, lots closed without a winner, and the outcome once awarded. Call this before investing " +
			"in a bid, and whenever a tender has been on the shortlist for a while: a notice found by search may already be " +
			"superseded. Give a publication_number (any notice of the procedure) or a procedure_id.",
	}, handleProcedureHistory(tc))
}

type HistoryInput struct {
	PublicationNumber string `json:"publication_number,omitempty" jsonschema:"any notice of the procurement, e.g. 561293-2024"`
	ProcedureID       string `json:"procedure_id,omitempty" jsonschema:"the eForms procedure identifier, when already known"`
}

// HistoryEntry is one notice of the procedure.
type HistoryEntry struct {
	PublicationNumber string             `json:"publication_number"`
	NoticeType        string             `json:"notice_type,omitempty"`
	Published         string             `json:"published,omitempty"`
	Version           string             `json:"version,omitempty"`
	Title             string             `json:"title,omitempty"`
	Deadline          string             `json:"deadline,omitempty" jsonschema:"submission deadline this notice states, with its time of day"`
	QuestionsDeadline string             `json:"questions_deadline,omitempty"`
	Changes           *ted.NoticeChanges `json:"changes,omitempty"`
	Outcomes          []string           `json:"outcomes,omitempty" jsonschema:"one line per lot: status, winner, bids, value"`
	URL               string             `json:"url,omitempty"`
	Error             string             `json:"error,omitempty"`
}

type HistoryOutput struct {
	ProcedureID string         `json:"procedure_id"`
	Notices     []HistoryEntry `json:"notices"`
	Alerts      []string       `json:"alerts,omitempty" jsonschema:"what changed across the sequence, most important first"`
	Status      string         `json:"status" jsonschema:"open, awarded, partly-awarded, closed-without-award, or unknown"`
	Note        string         `json:"note,omitempty"`
}

func handleProcedureHistory(tc *ted.Client) func(context.Context, *mcp.CallToolRequest, HistoryInput) (*mcp.CallToolResult, HistoryOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in HistoryInput) (*mcp.CallToolResult, HistoryOutput, error) {
		pid := strings.TrimSpace(in.ProcedureID)
		pn := strings.TrimSpace(in.PublicationNumber)
		if pid == "" && pn == "" {
			return errorResult(fmt.Errorf("give publication_number or procedure_id")), HistoryOutput{}, nil
		}
		if pid == "" {
			r := readNotice(ctx, tc, pn)
			if r.Err != nil {
				return errorResult(r.Err), HistoryOutput{}, nil
			}
			pid = r.Procedure.ProcedureID
			if pid == "" {
				return errorResult(fmt.Errorf("notice %s carries no eForms procedure identifier — it predates eForms "+
					"(November 2023) or uses the old TED schema, so TED cannot link it to the rest of its procedure", pn)), HistoryOutput{}, nil
			}
		}

		query := `procedure-identifier="` + strings.ReplaceAll(pid, `"`, ``) + `" SORT BY publication-date ASC`
		notices, _, err := searchNotices(ctx, tc, query, 1, 100)
		if err != nil {
			return errorResult(err), HistoryOutput{}, nil
		}
		read := readProcedures(ctx, tc, notices, 4)

		out := HistoryOutput{ProcedureID: pid}
		for _, r := range read {
			out.Notices = append(out.Notices, historyEntry(r))
		}
		out.Alerts, out.Status = historyAlerts(read, pn)
		if len(out.Notices) == 0 {
			out.Note = "TED returned no notices for this procedure identifier."
		}
		return textResult(formatHistory(out)), out, nil
	}
}

func historyEntry(r procNotice) HistoryEntry {
	e := HistoryEntry{
		PublicationNumber: r.Summary.PublicationNumber,
		NoticeType:        r.Summary.NoticeType,
		Published:         r.Summary.PublicationDate,
		Title:             r.Summary.Title,
		URL:               r.Summary.TEDURL,
	}
	if r.Err != nil {
		e.Error = r.Err.Error()
		return e
	}
	e.Version = r.Procedure.Version
	e.Changes = r.Procedure.Changes
	if d := r.Dossier; d != nil {
		e.Deadline = joinNonEmpty(" ", d.DeadlineDate, d.DeadlineTime)
		e.QuestionsDeadline = joinNonEmpty(" ", d.QuestionsDate, d.QuestionsTime)
	}
	for _, l := range r.Procedure.Lots {
		if l.Outcome == nil {
			continue
		}
		e.Outcomes = append(e.Outcomes, outcomeLine(l))
	}
	return e
}

func outcomeLine(l ted.ProcedureLot) string {
	o := l.Outcome
	parts := []string{firstNonEmpty(l.ID, "lot")}
	switch o.Status {
	case ted.OutcomeWinner:
		var names []string
		for _, w := range o.Winners {
			names = append(names, w.Name)
		}
		parts = append(parts, "awarded to "+firstNonEmpty(strings.Join(names, " + "), "(winner not named)"))
	case ted.OutcomeNoWinner:
		parts = append(parts, "closed without a winner")
		if o.DecisionReason != "" {
			parts = append(parts, "reason: "+o.DecisionReason)
		}
	case ted.OutcomeOpen:
		parts = append(parts, "still open")
	default:
		parts = append(parts, firstNonEmpty(o.Status, "outcome not stated"))
	}
	if o.TendersCount >= 0 {
		parts = append(parts, fmt.Sprintf("%d bid(s)", o.TendersCount))
	}
	if o.AwardedValue != "" {
		parts = append(parts, joinNonEmpty(" ", o.AwardedValue, o.Currency))
	}
	if o.AwardDate != "" {
		parts = append(parts, "awarded "+o.AwardDate)
	}
	return strings.Join(parts, " — ")
}

// historyAlerts reads the sequence for what a bid team must not miss.
func historyAlerts(read []procNotice, asked string) ([]string, string) {
	var alerts []string
	lastDeadline, lastFrom := "", ""
	awarded, noWinner, outcomes := 0, 0, 0
	askedIndex := -1

	for i, r := range read {
		if r.Summary.PublicationNumber == asked {
			askedIndex = i
		}
		if r.Err != nil {
			alerts = append(alerts, fmt.Sprintf("%s could not be read (%v): the history may be incomplete", r.Summary.PublicationNumber, r.Err))
			continue
		}
		if r.Dossier != nil && r.Dossier.DeadlineDate != "" {
			dl := joinNonEmpty(" ", r.Dossier.DeadlineDate, r.Dossier.DeadlineTime)
			if lastDeadline != "" && dl != lastDeadline {
				alerts = append(alerts, fmt.Sprintf("Deadline moved from %s (%s) to %s (%s)", lastDeadline, lastFrom, dl, r.Summary.PublicationNumber))
			}
			lastDeadline, lastFrom = dl, r.Summary.PublicationNumber
		}
		if c := r.Procedure.Changes; c != nil {
			msg := fmt.Sprintf("Change notice %s", r.Summary.PublicationNumber)
			if c.Reason != "" || c.ReasonCode != "" {
				msg += ": " + joinNonEmpty(" — ", c.ReasonCode, c.Reason)
			}
			if len(c.Descriptions) > 0 {
				msg += " — " + truncate(strings.Join(c.Descriptions, " / "), 300)
			}
			alerts = append(alerts, msg)
		}
		for _, l := range r.Procedure.Lots {
			if l.Outcome == nil {
				continue
			}
			outcomes++
			switch l.Outcome.Status {
			case ted.OutcomeWinner:
				awarded++
			case ted.OutcomeNoWinner:
				noWinner++
				alerts = append(alerts, fmt.Sprintf("Lot %s closed without a winner (%s)", l.ID, r.Summary.PublicationNumber))
			}
		}
	}
	if askedIndex >= 0 && askedIndex < len(read)-1 {
		alerts = append([]string{fmt.Sprintf("%s is not the latest notice of this procedure: %d later notice(s) follow it",
			asked, len(read)-1-askedIndex)}, alerts...)
	}

	status := "unknown"
	switch {
	case outcomes == 0 && len(read) > 0:
		status = "open"
	case awarded > 0 && noWinner == 0:
		status = "awarded"
	case awarded > 0:
		status = "partly-awarded"
	case noWinner > 0:
		status = "closed-without-award"
	}
	return alerts, status
}

func formatHistory(out HistoryOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Procedure %s — %d notice(s), status: %s\n", out.ProcedureID, len(out.Notices), out.Status)
	if len(out.Alerts) > 0 {
		b.WriteString("\nWhat changed:\n")
		for _, a := range out.Alerts {
			fmt.Fprintf(&b, "  ! %s\n", a)
		}
	}
	for i, n := range out.Notices {
		fmt.Fprintf(&b, "\n%d. %s  %s  published %s", i+1, n.PublicationNumber, n.NoticeType, n.Published)
		if n.Version != "" {
			fmt.Fprintf(&b, "  (version %s)", n.Version)
		}
		if n.Error != "" {
			fmt.Fprintf(&b, "\n   could not be read: %s", n.Error)
			continue
		}
		writeIndented(&b, "deadline", n.Deadline)
		writeIndented(&b, "questions by", n.QuestionsDeadline)
		for _, o := range n.Outcomes {
			writeIndented(&b, "outcome", o)
		}
		if n.URL != "" {
			writeIndented(&b, "link", n.URL)
		}
	}
	if out.Note != "" {
		fmt.Fprintf(&b, "\n\n%s", out.Note)
	}
	return b.String()
}

func writeIndented(b *strings.Builder, label, value string) {
	if value != "" {
		fmt.Fprintf(b, "\n   %s: %s", label, value)
	}
}
