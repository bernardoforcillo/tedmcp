package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bernardoforcillo/tedmcp/internal/match"
	"github.com/bernardoforcillo/tedmcp/internal/store"
	"github.com/bernardoforcillo/tedmcp/internal/ted"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The team tools turn tedmcp from something each analyst queries into
// something a team works in: searches saved once and run every morning for
// what is new, one shortlist with an owner and a status per tender, deadlines
// on everyone's calendar, and term packs that make a careful cross-border scan
// repeatable by someone who did not write it.

func registerTeamTools(s *mcp.Server, tc *ted.Client, st *store.Store) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "save_search",
		Description: "Save a search_tenders query under a name so anyone on the team can run it again. Saving an existing name " +
			"replaces its filters but keeps its memory of what it has already shown. Use relative date filters sparingly: " +
			"a saved search is usually best left without published_from, since run_saved_search already reports only what is new.",
	}, handleSaveSearch(st))
	mcp.AddTool(s, &mcp.Tool{
		Name: "run_saved_search",
		Description: "Run a saved search and report only the notices it has not shown before — the daily 'what is new' for a " +
			"market. Marks the new notices as seen (set mark_seen=false to peek without doing so). Use name='*' to run every " +
			"saved search at once for a morning digest.",
	}, handleRunSavedSearch(tc, st))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_saved_searches",
		Description: "List the team's saved searches with their filters, who created them and when each last ran.",
	}, handleListSavedSearches(st))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_saved_search",
		Description: "Delete a saved search by name.",
	}, handleDeleteSavedSearch(st))

	mcp.AddTool(s, &mcp.Tool{
		Name: "shortlist_add",
		Description: "Put a tender on the team shortlist with a status (" + strings.Join(store.Statuses, ", ") + "), an owner and " +
			"a note. Reads the notice once to record its title, buyer, value, submission deadline with time of day and the " +
			"deadline for questions, so the shortlist and calendar stay useful without re-reading TED. Adding a tender already " +
			"on the list updates it.",
	}, handleShortlistAdd(tc, st))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "shortlist_update",
		Description: "Change a shortlisted tender's status or owner, or add a note. Notes accumulate with their date and author.",
	}, handleShortlistUpdate(st))
	mcp.AddTool(s, &mcp.Tool{
		Name: "shortlist_list",
		Description: "The team shortlist, soonest deadline first, filterable by status and owner. Flags tenders whose deadline " +
			"has passed and those closing within a week. Set format=csv to export it for a bid tracker or CRM.",
	}, handleShortlistList(st))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "shortlist_remove",
		Description: "Remove a tender from the shortlist. Prefer setting status=dropped, which keeps the history.",
	}, handleShortlistRemove(st))
	mcp.AddTool(s, &mcp.Tool{
		Name: "export_calendar",
		Description: "Export submission deadlines and question deadlines as an iCalendar (.ics) file, with reminders, for the " +
			"shortlist (active statuses by default) or for given publication numbers. Times keep the offset the notice states, " +
			"so an 08:00 deadline in Rome lands at 08:00 Rome time in every calendar.",
	}, handleExportCalendar(tc, st))

	mcp.AddTool(s, &mcp.Tool{
		Name: "save_term_pack",
		Description: "Save a named term pack — per-language terms and an optional near pair — for scan_tenders, " +
			"scan_tender_documents and extract_requirements' custom checks to use by name (term_pack). This is how a search " +
			"one analyst refined across languages becomes something the whole team runs the same way.",
	}, handleSaveTermPack(st))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_term_packs",
		Description: "List the team's term packs with the languages each covers.",
	}, handleListTermPacks(st))
	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_term_pack",
		Description: "Delete a term pack by name.",
	}, handleDeleteTermPack(st))

	registerTeamResources(s, st)
}

// --- saved searches ---

type SaveSearchInput struct {
	Name        string      `json:"name" jsonschema:"a name the team will recognise, e.g. Mense scolastiche Lombardia"`
	Description string      `json:"description,omitempty"`
	Filters     SearchInput `json:"filters" jsonschema:"the search_tenders inputs to save"`
}

type SavedSearchView struct {
	Key         string      `json:"key"`
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Filters     SearchInput `json:"filters"`
	CreatedBy   string      `json:"created_by,omitempty"`
	CreatedAt   string      `json:"created_at,omitempty"`
	LastRun     string      `json:"last_run,omitempty"`
	LastRunBy   string      `json:"last_run_by,omitempty"`
	SeenCount   int         `json:"seen"`
}

func viewSearch(key string, s *store.SavedSearch) SavedSearchView {
	v := SavedSearchView{Key: key, Name: s.Name, Description: s.Description, CreatedBy: s.CreatedBy,
		CreatedAt: s.CreatedAt, LastRun: s.LastRun, LastRunBy: s.LastRunBy, SeenCount: len(s.Seen)}
	_ = json.Unmarshal(s.Filters, &v.Filters)
	return v
}

func handleSaveSearch(st *store.Store) func(context.Context, *mcp.CallToolRequest, SaveSearchInput) (*mcp.CallToolResult, SavedSearchView, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in SaveSearchInput) (*mcp.CallToolResult, SavedSearchView, error) {
		key := store.Key(in.Name)
		if key == "" {
			return errorResult(fmt.Errorf("name is required")), SavedSearchView{}, nil
		}
		// Refuse a search that would not run, now rather than tomorrow morning.
		f := in.Filters
		if _, err := ted.BuildQuery(ted.SearchFilters{Query: f.Query, Keywords: f.Keywords, CPV: f.Cpv, Country: f.Country,
			NoticeType: f.NoticeType, PublishedFrom: f.PublishedFrom, PublishedTo: f.PublishedTo,
			DeadlineFrom: f.DeadlineFrom, DeadlineTo: f.DeadlineTo, SortBy: f.SortBy, SortOrder: f.SortOrder}); err != nil {
			return errorResult(err), SavedSearchView{}, nil
		}
		raw, err := json.Marshal(in.Filters)
		if err != nil {
			return errorResult(err), SavedSearchView{}, nil
		}

		var view SavedSearchView
		err = st.Update(func(d *store.Data) error {
			s := d.Searches[key]
			if s == nil {
				s = &store.SavedSearch{CreatedBy: caller(req), CreatedAt: st.Now()}
				d.Searches[key] = s
			}
			s.Name = strings.TrimSpace(in.Name)
			s.Description = strings.TrimSpace(in.Description)
			s.Filters = raw
			view = viewSearch(key, s)
			return nil
		})
		if err != nil {
			return errorResult(err), SavedSearchView{}, nil
		}
		return textResult(fmt.Sprintf("Saved search %q (key %s). Run it with run_saved_search.", view.Name, key)), view, nil
	}
}

type RunSavedInput struct {
	Name     string `json:"name" jsonschema:"the saved search to run, or * for all of them"`
	OnlyNew  *bool  `json:"only_new,omitempty" jsonschema:"report only notices this search has not shown before (default true)"`
	MarkSeen *bool  `json:"mark_seen,omitempty" jsonschema:"remember the notices reported now as seen (default true)"`
	Limit    int    `json:"limit,omitempty" jsonschema:"newest notices to look at per search, 1-250 (default 50)"`
}

type RunSavedResult struct {
	Name        string              `json:"name"`
	Query       string              `json:"query,omitempty"`
	Total       int                 `json:"total"`
	Looked      int                 `json:"looked_at"`
	AlreadySeen int                 `json:"already_seen"`
	New         []ted.NoticeSummary `json:"new,omitempty"`
	Error       string              `json:"error,omitempty"`
}

type RunSavedOutput struct {
	Results []RunSavedResult `json:"results"`
	Note    string           `json:"note,omitempty"`
}

func handleRunSavedSearch(tc *ted.Client, st *store.Store) func(context.Context, *mcp.CallToolRequest, RunSavedInput) (*mcp.CallToolResult, RunSavedOutput, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in RunSavedInput) (*mcp.CallToolResult, RunSavedOutput, error) {
		onlyNew := in.OnlyNew == nil || *in.OnlyNew
		markSeen := in.MarkSeen == nil || *in.MarkSeen
		limit := clamp(in.Limit, 50, 1, 250)

		type job struct {
			key    string
			name   string
			filter SearchInput
			seen   map[string]bool
		}
		var jobs []job
		st.View(func(d *store.Data) {
			for key, s := range d.Searches {
				if strings.TrimSpace(in.Name) != "*" && key != store.Key(in.Name) {
					continue
				}
				j := job{key: key, name: s.Name, seen: map[string]bool{}}
				_ = json.Unmarshal(s.Filters, &j.filter)
				for _, n := range s.Seen {
					j.seen[n] = true
				}
				jobs = append(jobs, j)
			}
		})
		if len(jobs) == 0 {
			return errorResult(fmt.Errorf("no saved search named %q — list_saved_searches shows them", in.Name)), RunSavedOutput{}, nil
		}
		sort.Slice(jobs, func(i, k int) bool { return jobs[i].name < jobs[k].name })

		var out RunSavedOutput
		for _, j := range jobs {
			f := j.filter
			f.Page, f.Limit, f.Format = 1, limit, ""
			// Newest first, whatever the saved order: "new" means recently published.
			f.SortBy, f.SortOrder = "publication-date", "DESC"
			res := RunSavedResult{Name: j.name}
			found, err := runSearch(ctx, tc, f)
			if err != nil {
				res.Error = err.Error()
				out.Results = append(out.Results, res)
				continue
			}
			res.Query, res.Total, res.Looked = found.Query, found.Total, found.Count
			var shown []string
			for _, n := range found.Notices {
				if j.seen[n.PublicationNumber] {
					res.AlreadySeen++
					if onlyNew {
						continue
					}
				}
				res.New = append(res.New, n)
				shown = append(shown, n.PublicationNumber)
			}
			if markSeen {
				who := caller(req)
				_ = st.Update(func(d *store.Data) error {
					if s := d.Searches[j.key]; s != nil {
						s.MarkSeen(shown)
						s.LastRun, s.LastRunBy = st.Now(), who
					}
					return nil
				})
			}
			out.Results = append(out.Results, res)
		}
		for _, r := range out.Results {
			if r.Looked == limit && r.AlreadySeen == 0 && r.Total > limit {
				out.Note = fmt.Sprintf("At least one search had more than %d new notices; raise limit to see the rest, "+
					"or narrow its filters.", limit)
			}
		}
		return textResult(formatRunSaved(out)), out, nil
	}
}

func formatRunSaved(out RunSavedOutput) string {
	var b strings.Builder
	for i, r := range out.Results {
		if i > 0 {
			b.WriteString("\n")
		}
		if r.Error != "" {
			fmt.Fprintf(&b, "■ %s — could not run: %s\n", r.Name, r.Error)
			continue
		}
		fmt.Fprintf(&b, "■ %s — %d new (looked at the %d newest of %d; %d already seen)\n", r.Name, len(r.New), r.Looked, r.Total, r.AlreadySeen)
		for _, n := range r.New {
			fmt.Fprintf(&b, "  - %s  %s  %s — %s", n.PublicationNumber, n.PublicationDate, firstNonEmpty(n.BuyerName, ""), truncate(firstNonEmpty(n.Title, "(untitled)"), 110))
			if n.Deadline != "" {
				fmt.Fprintf(&b, "  [deadline %s]", trimDateString(n.Deadline))
			}
			b.WriteString("\n")
		}
	}
	if out.Note != "" {
		fmt.Fprintf(&b, "\n%s", out.Note)
	}
	return b.String()
}

type NameInput struct {
	Name string `json:"name"`
}

type ListSearchesOutput struct {
	Searches []SavedSearchView `json:"searches"`
}

func handleListSavedSearches(st *store.Store) func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, ListSearchesOutput, error) {
	return func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, ListSearchesOutput, error) {
		var out ListSearchesOutput
		st.View(func(d *store.Data) {
			for key, s := range d.Searches {
				out.Searches = append(out.Searches, viewSearch(key, s))
			}
		})
		sort.Slice(out.Searches, func(i, j int) bool { return out.Searches[i].Name < out.Searches[j].Name })
		if len(out.Searches) == 0 {
			return textResult("No saved searches yet. Create one with save_search."), out, nil
		}
		var b strings.Builder
		for _, s := range out.Searches {
			raw, _ := json.Marshal(s.Filters)
			fmt.Fprintf(&b, "■ %s (key %s)\n  filters: %s\n", s.Name, s.Key, raw)
			if s.Description != "" {
				fmt.Fprintf(&b, "  %s\n", s.Description)
			}
			fmt.Fprintf(&b, "  created %s %s · last run %s %s · %d notice(s) seen\n",
				s.CreatedAt, byWhom(s.CreatedBy), firstNonEmpty(s.LastRun, "never"), byWhom(s.LastRunBy), s.SeenCount)
		}
		return textResult(b.String()), out, nil
	}
}

func handleDeleteSavedSearch(st *store.Store) func(context.Context, *mcp.CallToolRequest, NameInput) (*mcp.CallToolResult, struct{}, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, in NameInput) (*mcp.CallToolResult, struct{}, error) {
		key := store.Key(in.Name)
		found := false
		err := st.Update(func(d *store.Data) error {
			if _, found = d.Searches[key]; found {
				delete(d.Searches, key)
			}
			return nil
		})
		if err != nil {
			return errorResult(err), struct{}{}, nil
		}
		if !found {
			return errorResult(fmt.Errorf("no saved search named %q", in.Name)), struct{}{}, nil
		}
		return textResult(fmt.Sprintf("Deleted saved search %q.", in.Name)), struct{}{}, nil
	}
}

// --- shortlist ---

type ShortlistAddInput struct {
	PublicationNumber string `json:"publication_number"`
	Status            string `json:"status,omitempty" jsonschema:"watching (default), qualifying, bidding, submitted, won, lost or dropped"`
	Owner             string `json:"owner,omitempty" jsonschema:"who on the team is responsible"`
	Note              string `json:"note,omitempty"`
}

type ShortlistUpdateInput struct {
	PublicationNumber string `json:"publication_number"`
	Status            string `json:"status,omitempty"`
	Owner             string `json:"owner,omitempty"`
	Note              string `json:"note,omitempty"`
}

type EntryOutput struct {
	Entry store.Entry `json:"entry"`
}

func handleShortlistAdd(tc *ted.Client, st *store.Store) func(context.Context, *mcp.CallToolRequest, ShortlistAddInput) (*mcp.CallToolResult, EntryOutput, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, in ShortlistAddInput) (*mcp.CallToolResult, EntryOutput, error) {
		pn := strings.TrimSpace(in.PublicationNumber)
		if pn == "" {
			return errorResult(fmt.Errorf("publication_number is required")), EntryOutput{}, nil
		}
		status := strings.ToLower(strings.TrimSpace(in.Status))
		if status == "" {
			status = "watching"
		}
		if !store.ValidStatus(status) {
			return errorResult(fmt.Errorf("unknown status %q: use one of %s", in.Status, strings.Join(store.Statuses, ", "))), EntryOutput{}, nil
		}

		r := readNotice(ctx, tc, pn)
		if r.Err != nil {
			return errorResult(fmt.Errorf("could not read notice %s: %w", pn, r.Err)), EntryOutput{}, nil
		}
		who := caller(req)
		var saved store.Entry
		err := st.Update(func(d *store.Data) error {
			e := d.Shortlist[pn]
			if e == nil {
				e = &store.Entry{PublicationNumber: pn, AddedBy: who, AddedAt: st.Now()}
				d.Shortlist[pn] = e
			}
			fillEntry(e, r)
			e.Status = status
			if o := strings.TrimSpace(in.Owner); o != "" {
				e.Owner = o
			}
			if n := strings.TrimSpace(in.Note); n != "" {
				e.Notes = append(e.Notes, store.Note{At: st.Now(), By: who, Text: n})
			}
			e.UpdatedAt = st.Now()
			saved = *e
			return nil
		})
		if err != nil {
			return errorResult(err), EntryOutput{}, nil
		}
		return textResult("On the shortlist:\n" + formatEntry(saved, today())), EntryOutput{Entry: saved}, nil
	}
}

func fillEntry(e *store.Entry, r procNotice) {
	s := r.Summary
	e.Title, e.Buyer, e.Country, e.Value, e.URL = s.Title, s.BuyerName, s.BuyerCountry, s.TotalValue, s.TEDURL
	if d := r.Dossier; d != nil {
		e.DeadlineDate, e.DeadlineTime, e.DeadlineZone = d.DeadlineDate, d.DeadlineTime, d.DeadlineZone
		e.QuestionsDate, e.QuestionsTime = d.QuestionsDate, d.QuestionsTime
		e.ProcedureID = d.ProcedureID
		e.DocumentsURL = d.DocumentsURL()
	}
	if e.DeadlineDate == "" {
		e.DeadlineDate = trimDateString(s.Deadline)
	}
}

func handleShortlistUpdate(st *store.Store) func(context.Context, *mcp.CallToolRequest, ShortlistUpdateInput) (*mcp.CallToolResult, EntryOutput, error) {
	return func(_ context.Context, req *mcp.CallToolRequest, in ShortlistUpdateInput) (*mcp.CallToolResult, EntryOutput, error) {
		pn := strings.TrimSpace(in.PublicationNumber)
		status := strings.ToLower(strings.TrimSpace(in.Status))
		if status != "" && !store.ValidStatus(status) {
			return errorResult(fmt.Errorf("unknown status %q: use one of %s", in.Status, strings.Join(store.Statuses, ", "))), EntryOutput{}, nil
		}
		who := caller(req)
		var saved store.Entry
		err := st.Update(func(d *store.Data) error {
			e := d.Shortlist[pn]
			if e == nil {
				return fmt.Errorf("%s is not on the shortlist — add it with shortlist_add", pn)
			}
			if status != "" && status != e.Status {
				e.Notes = append(e.Notes, store.Note{At: st.Now(), By: who, Text: "status " + e.Status + " → " + status})
				e.Status = status
			}
			if o := strings.TrimSpace(in.Owner); o != "" {
				e.Owner = o
			}
			if n := strings.TrimSpace(in.Note); n != "" {
				e.Notes = append(e.Notes, store.Note{At: st.Now(), By: who, Text: n})
			}
			e.UpdatedAt = st.Now()
			saved = *e
			return nil
		})
		if err != nil {
			return errorResult(err), EntryOutput{}, nil
		}
		return textResult("Updated:\n" + formatEntry(saved, today())), EntryOutput{Entry: saved}, nil
	}
}

type ShortlistListInput struct {
	Status []string `json:"status,omitempty" jsonschema:"keep only these statuses (default: all)"`
	Owner  string   `json:"owner,omitempty"`
	Format string   `json:"format,omitempty" jsonschema:"text (default) or csv"`
}

type ShortlistListOutput struct {
	Entries []store.Entry `json:"entries"`
}

func handleShortlistList(st *store.Store) func(context.Context, *mcp.CallToolRequest, ShortlistListInput) (*mcp.CallToolResult, ShortlistListOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, in ShortlistListInput) (*mcp.CallToolResult, ShortlistListOutput, error) {
		out := ShortlistListOutput{Entries: shortlistEntries(st, in.Status, in.Owner)}
		now := today()
		var b strings.Builder
		if len(out.Entries) == 0 {
			b.WriteString("The shortlist is empty for these filters.")
		}
		for i, e := range out.Entries {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(formatEntry(e, now))
		}
		if wantsCSV(in.Format) {
			return csvResult(b.String(), "shortlist", shortlistCSV(out.Entries)), out, nil
		}
		return textResult(b.String()), out, nil
	}
}

func shortlistEntries(st *store.Store, statuses []string, owner string) []store.Entry {
	want := map[string]bool{}
	for _, s := range statuses {
		want[strings.ToLower(strings.TrimSpace(s))] = true
	}
	owner = strings.ToLower(strings.TrimSpace(owner))
	var out []store.Entry
	st.View(func(d *store.Data) {
		for _, e := range d.SortedEntries() {
			if len(want) > 0 && !want[e.Status] {
				continue
			}
			if owner != "" && strings.ToLower(e.Owner) != owner {
				continue
			}
			c := *e
			c.Notes = append([]store.Note(nil), e.Notes...)
			out = append(out, c)
		}
	})
	return out
}

func formatEntry(e store.Entry, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "■ %s [%s]", e.PublicationNumber, e.Status)
	if e.Owner != "" {
		fmt.Fprintf(&b, " owner: %s", e.Owner)
	}
	fmt.Fprintf(&b, "\n  %s", truncate(firstNonEmpty(e.Title, "(untitled)"), 140))
	if e.Buyer != "" {
		fmt.Fprintf(&b, "\n  buyer: %s", joinNonEmpty(" / ", e.Buyer, e.Country))
	}
	if e.Value != "" {
		fmt.Fprintf(&b, "  value: %s", e.Value)
	}
	if e.DeadlineDate != "" {
		fmt.Fprintf(&b, "\n  deadline: %s%s", joinNonEmpty(" ", e.DeadlineDate, e.DeadlineTime, e.DeadlineZone), urgency(e.DeadlineDate, now))
	}
	if e.QuestionsDate != "" {
		fmt.Fprintf(&b, "\n  questions by: %s%s", joinNonEmpty(" ", e.QuestionsDate, e.QuestionsTime), urgency(e.QuestionsDate, now))
	}
	if e.DocumentsURL != "" {
		fmt.Fprintf(&b, "\n  documents: %s", e.DocumentsURL)
	}
	for _, n := range lastNotes(e.Notes, 3) {
		fmt.Fprintf(&b, "\n  note %s %s: %s", trimDateString(n.At), byWhom(n.By), n.Text)
	}
	return b.String()
}

func urgency(date string, now time.Time) string {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return ""
	}
	days := int(t.Sub(now).Hours() / 24)
	switch {
	case days < 0:
		return "  (passed)"
	case days == 0:
		return "  (TODAY)"
	case days <= 7:
		return fmt.Sprintf("  (in %d day(s))", days)
	}
	return ""
}

func lastNotes(notes []store.Note, n int) []store.Note {
	if len(notes) > n {
		return notes[len(notes)-n:]
	}
	return notes
}

func shortlistCSV(entries []store.Entry) [][]string {
	rows := [][]string{{"publication_number", "status", "owner", "title", "buyer", "country", "value", "deadline_date",
		"deadline_time", "questions_deadline_date", "questions_deadline_time", "procedure_id", "documents_url", "url", "last_note", "updated_at"}}
	for _, e := range entries {
		last := ""
		if len(e.Notes) > 0 {
			last = e.Notes[len(e.Notes)-1].Text
		}
		rows = append(rows, []string{e.PublicationNumber, e.Status, e.Owner, e.Title, e.Buyer, e.Country, e.Value,
			e.DeadlineDate, e.DeadlineTime, e.QuestionsDate, e.QuestionsTime, e.ProcedureID, e.DocumentsURL, e.URL, last, e.UpdatedAt})
	}
	return rows
}

type PublicationInput struct {
	PublicationNumber string `json:"publication_number"`
}

func handleShortlistRemove(st *store.Store) func(context.Context, *mcp.CallToolRequest, PublicationInput) (*mcp.CallToolResult, struct{}, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, in PublicationInput) (*mcp.CallToolResult, struct{}, error) {
		pn := strings.TrimSpace(in.PublicationNumber)
		found := false
		if err := st.Update(func(d *store.Data) error {
			if _, found = d.Shortlist[pn]; found {
				delete(d.Shortlist, pn)
			}
			return nil
		}); err != nil {
			return errorResult(err), struct{}{}, nil
		}
		if !found {
			return errorResult(fmt.Errorf("%s is not on the shortlist", pn)), struct{}{}, nil
		}
		return textResult(fmt.Sprintf("Removed %s from the shortlist.", pn)), struct{}{}, nil
	}
}

// --- calendar ---

type CalendarInput struct {
	PublicationNumbers []string `json:"publication_numbers,omitempty" jsonschema:"export these tenders (read from TED if not shortlisted); default: the shortlist"`
	Status             []string `json:"status,omitempty" jsonschema:"shortlist statuses to export (default watching, qualifying, bidding)"`
	ReminderDays       int      `json:"reminder_days,omitempty" jsonschema:"days before a submission deadline to remind (default 3)"`
}

type CalendarOutput struct {
	Events int    `json:"events"`
	ICS    string `json:"ics"`
}

func handleExportCalendar(tc *ted.Client, st *store.Store) func(context.Context, *mcp.CallToolRequest, CalendarInput) (*mcp.CallToolResult, CalendarOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in CalendarInput) (*mcp.CallToolResult, CalendarOutput, error) {
		var entries []store.Entry
		if len(in.PublicationNumbers) > 0 {
			listed := map[string]store.Entry{}
			for _, e := range shortlistEntries(st, nil, "") {
				listed[e.PublicationNumber] = e
			}
			for _, pn := range in.PublicationNumbers {
				pn = strings.TrimSpace(pn)
				if e, ok := listed[pn]; ok {
					entries = append(entries, e)
					continue
				}
				r := readNotice(ctx, tc, pn)
				if r.Err != nil {
					return errorResult(fmt.Errorf("could not read %s: %w", pn, r.Err)), CalendarOutput{}, nil
				}
				e := store.Entry{PublicationNumber: pn}
				fillEntry(&e, r)
				entries = append(entries, e)
			}
		} else {
			statuses := in.Status
			if len(statuses) == 0 {
				statuses = []string{"watching", "qualifying", "bidding"}
			}
			entries = shortlistEntries(st, statuses, "")
		}

		ics, events := buildICS(entries, clamp(in.ReminderDays, 3, 1, 30), time.Now().UTC())
		out := CalendarOutput{Events: events, ICS: ics}
		summary := fmt.Sprintf("%d calendar event(s) for %d tender(s). Import the attached tedmcp-deadlines.ics into any calendar.", events, len(entries))
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: summary},
			&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{
				URI: "tedmcp://export/tedmcp-deadlines.ics", MIMEType: "text/calendar", Text: ics,
			}},
		}}, out, nil
	}
}

// buildICS renders deadlines as an iCalendar file. A deadline with a time and
// an offset becomes an exact instant in UTC; one without a time becomes an
// all-day event, since inventing a time would be worse than saying none.
func buildICS(entries []store.Entry, reminderDays int, now time.Time) (string, int) {
	var b strings.Builder
	line := func(s string) { b.WriteString(foldICS(s)); b.WriteString("\r\n") }
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//tedmcp//tender deadlines//EN")
	line("CALSCALE:GREGORIAN")
	line("X-WR-CALNAME:Tender deadlines")

	events := 0
	stamp := now.Format("20060102T150405Z")
	add := func(e store.Entry, kind, date, clock, zone, label string, remind int) {
		if date == "" {
			return
		}
		events++
		line("BEGIN:VEVENT")
		line("UID:" + e.PublicationNumber + "-" + kind + "@tedmcp")
		line("DTSTAMP:" + stamp)
		if start, ok := icsInstant(date, clock, zone); ok {
			line("DTSTART:" + start)
			line("DTEND:" + start)
		} else {
			day := strings.ReplaceAll(date, "-", "")
			line("DTSTART;VALUE=DATE:" + day)
			if next, err := time.Parse("2006-01-02", date); err == nil {
				line("DTEND;VALUE=DATE:" + next.AddDate(0, 0, 1).Format("20060102"))
			}
		}
		line("SUMMARY:" + icsEscape(label+": "+truncate(firstNonEmpty(e.Title, e.PublicationNumber), 120)))
		desc := []string{"TED " + e.PublicationNumber}
		if e.Buyer != "" {
			desc = append(desc, "Buyer: "+e.Buyer)
		}
		if e.Status != "" {
			desc = append(desc, "Status: "+e.Status+joinNonEmpty("", ", owner: "+e.Owner))
		}
		if e.DocumentsURL != "" {
			desc = append(desc, "Documents: "+e.DocumentsURL)
		}
		line("DESCRIPTION:" + icsEscape(strings.Join(desc, "\n")))
		if e.URL != "" {
			line("URL:" + e.URL)
		}
		line("BEGIN:VALARM")
		line("ACTION:DISPLAY")
		line(fmt.Sprintf("TRIGGER:-P%dD", remind))
		line("DESCRIPTION:" + icsEscape(label))
		line("END:VALARM")
		line("END:VEVENT")
	}
	for _, e := range entries {
		add(e, "submission", e.DeadlineDate, e.DeadlineTime, e.DeadlineZone, "Tender deadline", reminderDays)
		// The questions period closes in the same time zone as the deadline.
		add(e, "questions", e.QuestionsDate, e.QuestionsTime, e.DeadlineZone, "Questions deadline", 1)
	}
	line("END:VCALENDAR")
	return b.String(), events
}

func icsInstant(date, clock, zone string) (string, bool) {
	if clock == "" || zone == "" {
		return "", false
	}
	if zone == "Z" {
		zone = "+00:00"
	}
	t, err := time.Parse("2006-01-02 15:04 -07:00", date+" "+clock+" "+zone)
	if err != nil {
		return "", false
	}
	return t.UTC().Format("20060102T150405Z"), true
}

func icsEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", `\n`, "\r", "")
	return r.Replace(s)
}

// foldICS breaks lines longer than 75 octets, as RFC 5545 requires, without
// splitting a UTF-8 character.
func foldICS(s string) string {
	if len(s) <= 75 {
		return s
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		size := len(string(r))
		if n+size > 75 {
			b.WriteString("\r\n ")
			n = 1
		}
		b.WriteRune(r)
		n += size
	}
	return b.String()
}

// --- term packs ---

type SaveTermPackInput struct {
	Name            string              `json:"name" jsonschema:"e.g. food-waste"`
	Description     string              `json:"description,omitempty"`
	TermsByLanguage map[string][]string `json:"terms_by_language" jsonschema:"terms keyed by language (ISO 639-2/T; * for any), as in scan_tenders"`
	Near            *match.Near         `json:"near,omitempty"`
}

type TermPackView struct {
	Key             string              `json:"key"`
	Name            string              `json:"name"`
	Description     string              `json:"description,omitempty"`
	Languages       []string            `json:"languages"`
	TermsByLanguage map[string][]string `json:"terms_by_language,omitempty"`
	Near            *match.Near         `json:"near,omitempty"`
	UpdatedBy       string              `json:"updated_by,omitempty"`
	UpdatedAt       string              `json:"updated_at,omitempty"`
}

func viewPack(key string, p *store.TermPack) TermPackView {
	v := TermPackView{Key: key, Name: p.Name, Description: p.Description, TermsByLanguage: p.TermsByLanguage,
		UpdatedBy: p.UpdatedBy, UpdatedAt: p.UpdatedAt}
	if len(p.Near) > 0 {
		var n match.Near
		if json.Unmarshal(p.Near, &n) == nil {
			v.Near = &n
		}
	}
	spec := match.Spec{Terms: p.TermsByLanguage, Near: v.Near}
	v.Languages = spec.Normalize().Languages()
	return v
}

func handleSaveTermPack(st *store.Store) func(context.Context, *mcp.CallToolRequest, SaveTermPackInput) (*mcp.CallToolResult, TermPackView, error) {
	return func(_ context.Context, req *mcp.CallToolRequest, in SaveTermPackInput) (*mcp.CallToolResult, TermPackView, error) {
		key := store.Key(in.Name)
		if key == "" {
			return errorResult(fmt.Errorf("name is required")), TermPackView{}, nil
		}
		spec := match.Spec{Terms: in.TermsByLanguage, Near: in.Near}.Normalize()
		if spec.Empty() {
			return errorResult(fmt.Errorf("a term pack needs terms_by_language or near")), TermPackView{}, nil
		}
		var near json.RawMessage
		if in.Near != nil {
			near, _ = json.Marshal(in.Near)
		}
		var view TermPackView
		err := st.Update(func(d *store.Data) error {
			p := &store.TermPack{Name: strings.TrimSpace(in.Name), Description: strings.TrimSpace(in.Description),
				TermsByLanguage: in.TermsByLanguage, Near: near, UpdatedBy: caller(req), UpdatedAt: st.Now()}
			d.TermPacks[key] = p
			view = viewPack(key, p)
			return nil
		})
		if err != nil {
			return errorResult(err), TermPackView{}, nil
		}
		return textResult(fmt.Sprintf("Saved term pack %q covering %s. Use it with term_pack=%q in scan_tenders or scan_tender_documents.",
			view.Name, strings.Join(view.Languages, ", "), key)), view, nil
	}
}

type ListPacksOutput struct {
	Packs []TermPackView `json:"packs"`
}

func handleListTermPacks(st *store.Store) func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, ListPacksOutput, error) {
	return func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, ListPacksOutput, error) {
		out := ListPacksOutput{Packs: termPacks(st)}
		if len(out.Packs) == 0 {
			return textResult("No term packs yet. Create one with save_term_pack."), out, nil
		}
		var b strings.Builder
		for _, p := range out.Packs {
			fmt.Fprintf(&b, "■ %s (key %s) — %s\n", p.Name, p.Key, strings.Join(p.Languages, ", "))
			if p.Description != "" {
				fmt.Fprintf(&b, "  %s\n", p.Description)
			}
			for _, lang := range sortedKeysOf(p.TermsByLanguage) {
				fmt.Fprintf(&b, "  %s: %s\n", lang, strings.Join(p.TermsByLanguage[lang], ", "))
			}
			if p.Near != nil {
				fmt.Fprintf(&b, "  near (window %d)\n", p.Near.Window)
			}
		}
		return textResult(b.String()), out, nil
	}
}

func termPacks(st *store.Store) []TermPackView {
	var out []TermPackView
	st.View(func(d *store.Data) {
		for key, p := range d.TermPacks {
			out = append(out, viewPack(key, p))
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func handleDeleteTermPack(st *store.Store) func(context.Context, *mcp.CallToolRequest, NameInput) (*mcp.CallToolResult, struct{}, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, in NameInput) (*mcp.CallToolResult, struct{}, error) {
		key := store.Key(in.Name)
		found := false
		if err := st.Update(func(d *store.Data) error {
			if _, found = d.TermPacks[key]; found {
				delete(d.TermPacks, key)
			}
			return nil
		}); err != nil {
			return errorResult(err), struct{}{}, nil
		}
		if !found {
			return errorResult(fmt.Errorf("no term pack named %q", in.Name)), struct{}{}, nil
		}
		return textResult(fmt.Sprintf("Deleted term pack %q.", in.Name)), struct{}{}, nil
	}
}

// applyTermPack merges a named pack into a scan's terms. The caller's own terms
// are kept alongside, so a pack can be extended for one search.
func applyTermPack(st *store.Store, name string, terms *map[string][]string, near **match.Near) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	if st == nil {
		return fmt.Errorf("term packs are not available: the server has no team store")
	}
	key := store.Key(name)
	var pack *TermPackView
	st.View(func(d *store.Data) {
		if p, ok := d.TermPacks[key]; ok {
			v := viewPack(key, p)
			pack = &v
		}
	})
	if pack == nil {
		return fmt.Errorf("no term pack named %q — list_term_packs shows them", name)
	}
	if *terms == nil {
		*terms = map[string][]string{}
	}
	for lang, list := range pack.TermsByLanguage {
		(*terms)[lang] = append((*terms)[lang], list...)
	}
	if *near == nil && pack.Near != nil {
		*near = pack.Near
	}
	return nil
}

// --- resources ---

func registerTeamResources(s *mcp.Server, st *store.Store) {
	jsonResource := func(uri string, v any) (*mcp.ReadResourceResult, error) {
		raw, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(raw)}}}, nil
	}

	s.AddResource(&mcp.Resource{
		URI: "tedmcp://shortlist", Name: "shortlist", Title: "Team shortlist", MIMEType: "application/json",
		Description: "Every tender on the team shortlist with status, owner, deadlines and notes.",
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return jsonResource(req.Params.URI, shortlistEntries(st, nil, ""))
	})
	s.AddResource(&mcp.Resource{
		URI: "tedmcp://shortlist.ics", Name: "shortlist-calendar", Title: "Shortlist deadlines (iCalendar)", MIMEType: "text/calendar",
		Description: "Submission and question deadlines of active shortlisted tenders.",
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		ics, _ := buildICS(shortlistEntries(st, []string{"watching", "qualifying", "bidding"}, ""), 3, time.Now().UTC())
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "text/calendar", Text: ics}}}, nil
	})
	s.AddResource(&mcp.Resource{
		URI: "tedmcp://saved-searches", Name: "saved-searches", Title: "Saved searches", MIMEType: "application/json",
		Description: "The team's saved searches and their filters.",
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		var views []SavedSearchView
		st.View(func(d *store.Data) {
			for key, s := range d.Searches {
				views = append(views, viewSearch(key, s))
			}
		})
		sort.Slice(views, func(i, j int) bool { return views[i].Key < views[j].Key })
		return jsonResource(req.Params.URI, views)
	})
	s.AddResource(&mcp.Resource{
		URI: "tedmcp://term-packs", Name: "term-packs", Title: "Term packs", MIMEType: "application/json",
		Description: "Curated per-language search vocabularies, usable by name in the scans.",
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return jsonResource(req.Params.URI, termPacks(st))
	})
	s.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "tedmcp://term-packs/{name}", Name: "term-pack", Title: "Term pack", MIMEType: "application/json",
		Description: "One term pack by key.",
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		key := store.Key(strings.TrimPrefix(req.Params.URI, "tedmcp://term-packs/"))
		var view *TermPackView
		st.View(func(d *store.Data) {
			if p, ok := d.TermPacks[key]; ok {
				v := viewPack(key, p)
				view = &v
			}
		})
		if view == nil {
			return nil, mcp.ResourceNotFoundError(req.Params.URI)
		}
		return jsonResource(req.Params.URI, view)
	})
}

// --- helpers ---

func byWhom(who string) string {
	if who == "" {
		return ""
	}
	return "by " + who
}

func trimDateString(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

func sortedKeysOf(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
