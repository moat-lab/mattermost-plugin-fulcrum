package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// tasksListFilter mirrors the `filter` block of `fulcrum tasks list --json`
// (cli/JSON_SCHEMA.md §tasks.list). The renderer trusts this echo for filter
// semantics — never re-parses slash argv — so the Title chip can't lie about
// what was actually queried (spike §B.2.2 hard rule).
type tasksListFilter struct {
	Status     string  `json:"status"`
	Priority   *string `json:"priority"`
	ProjectID  *string `json:"project_id"`
	Tag        *string `json:"tag"`
	Page       int     `json:"page"`
	PageSize   int     `json:"page_size"`
	TotalPages int     `json:"total_pages"`
}

// tasksListPayload mirrors the data payload of `fulcrum tasks list --json`.
type tasksListPayload struct {
	Filter tasksListFilter `json:"filter"`
	Total  int             `json:"total"`
	Tasks  []taskSummary   `json:"tasks"`
}

// tasksListRowCap caps the rendered table at the spike §B.2.3 ceiling. The CLI
// page_size is the canonical authority for paging; this cap protects the table
// from a future page_size change that would overflow a single Mattermost post.
const tasksListRowCap = 20

// tasksListTitleCap is the §B.2.3 task-title truncation budget.
const tasksListTitleCap = 60

// renderTasksList produces the today-tasks-panel SlackAttachment per spike
// §B.2. The five state branches (empty / single-page / paginated /
// filtered-empty / error) collapse into three rendering paths here: error is
// reached via renderBusinessError before this renderer; empty and
// filtered-empty share the no-rows form but differ in text + button set; and
// single-page / paginated share the table form but differ on Pretext + Prev /
// Next buttons. `now` is injected so tests pin the Due column.
func renderTasksList(raw json.RawMessage, now time.Time) (*model.SlackAttachment, error) {
	var p tasksListPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("tasks.list payload: %w", err)
	}
	filtered := tasksListHasFilter(p.Filter)
	summary := tasksListFilterSummary(p.Filter)
	att := &model.SlackAttachment{
		Color:   colorAccent,
		Title:   fmt.Sprintf("Tasks · %s (%d)", summary, p.Total),
		Footer:  tasksListFooter(p.Filter, p.pageSize()),
		Actions: tasksListActions(p.Filter, p.Total, filtered),
	}
	if p.Filter.TotalPages > 1 {
		page := p.Filter.Page
		if page < 1 {
			page = 1
		}
		att.Pretext = fmt.Sprintf("page %d/%d", page, p.Filter.TotalPages)
	}

	if p.Total == 0 {
		if filtered {
			att.Text = fmt.Sprintf("_No tasks match filter `%s`. Clear filter:_", summary)
		} else {
			att.Text = "_No active tasks. Use `/f tasks create --title=\"...\"` to add one._"
		}
		return att, nil
	}

	att.Text = tasksListTableText(p.Tasks, now)
	return att, nil
}

// pageSize returns the CLI-reported page_size with a sane fallback so the
// footer can't divide by zero against an older CLI that omits the field on
// the empty branch.
func (p tasksListPayload) pageSize() int {
	if p.Filter.PageSize > 0 {
		return p.Filter.PageSize
	}
	return tasksListRowCap
}

// tasksListHasFilter returns true when the request carried any non-default
// filter (anything but bare `status=active`). The result decides between the
// empty and filtered-empty branches when Total == 0.
func tasksListHasFilter(f tasksListFilter) bool {
	if tasksListStatusAlias(f.Status) != "active" {
		return true
	}
	if f.Priority != nil && *f.Priority != "" {
		return true
	}
	if f.ProjectID != nil && *f.ProjectID != "" {
		return true
	}
	if f.Tag != nil && *f.Tag != "" {
		return true
	}
	return false
}

// tasksListStatusAlias collapses both the enum (`TO_DO`) and the CLI input
// alias (`todo`) onto the CLI input alias. The plugin needs the alias form for
// argv reconstruction (CLI `tasks list --status=<alias>` accepts the alias
// form per cli/JSON_SCHEMA.md §tasks.list).
func tasksListStatusAlias(status string) string {
	switch status {
	case "", "active":
		return "active"
	case "TO_DO", "todo":
		return "todo"
	case "IN_PROGRESS", "doing", "progress", "wip":
		return "doing"
	case "IN_REVIEW", "review":
		return "review"
	case "DONE", "done":
		return "done"
	case "CANCELED", "cancelled", "canceled":
		return "canceled"
	}
	return strings.ToLower(status)
}

// tasksListFilterSummary composes the human-readable filter chip per spike
// §B.2.3, in the order status → priority → project → tag. The page segment is
// reserved for Pretext / Footer and is intentionally excluded from the Title
// chip so the chip text stays stable across paging clicks.
func tasksListFilterSummary(f tasksListFilter) string {
	parts := []string{tasksListStatusLabel(f.Status)}
	if f.Priority != nil && *f.Priority != "" {
		parts = append(parts, *f.Priority+" pri")
	}
	if f.ProjectID != nil && *f.ProjectID != "" {
		parts = append(parts, "#"+*f.ProjectID)
	}
	if f.Tag != nil && *f.Tag != "" {
		parts = append(parts, ":label: "+*f.Tag)
	}
	return strings.Join(parts, " · ")
}

// tasksListStatusLabel returns the chip text for a status filter. Defaults to
// "today" for the bare active filter per spike §B.2.3.
func tasksListStatusLabel(status string) string {
	switch tasksListStatusAlias(status) {
	case "active":
		return "today"
	case "todo":
		return "to-do"
	case "doing":
		return "in progress"
	case "review":
		return "in review"
	case "done":
		return "done"
	case "canceled":
		return "canceled"
	}
	return status
}

// tasksListFooter formats the footer line per spike §B.2.3. The CLI's
// total_pages is the authority on paging; an absent total_pages (older CLI)
// falls back to "1" so the chip is never blank.
func tasksListFooter(f tasksListFilter, pageSize int) string {
	page := f.Page
	if page < 1 {
		page = 1
	}
	totalPages := f.TotalPages
	if totalPages < 1 {
		totalPages = 1
	}
	return fmt.Sprintf("fulcrum/tasks.list · page=%d/%d · page_size=%d", page, totalPages, pageSize)
}

// tasksListTableText composes the markdown table body. Rows beyond
// tasksListRowCap are dropped from the table; the user paginates via the
// Prev / Next buttons so the cap doesn't silently hide tasks.
func tasksListTableText(tasks []taskSummary, now time.Time) string {
	headers := []string{"Pri", "Status", "Title", "ID", "Due"}
	limit := len(tasks)
	if limit > tasksListRowCap {
		limit = tasksListRowCap
	}
	rows := make([][]string, 0, limit)
	for i := 0; i < limit; i++ {
		t := tasks[i]
		rows = append(rows, []string{
			priorityLetter(t.Priority),
			statusChip(t.Status),
			tasksListTitleCell(t.Title),
			"`" + t.ID + "`",
			tasksListDueCell(t.DueDate, now),
		})
	}
	return renderMarkdownTable(headers, rows)
}

// tasksListTitleCell prepares the Title cell for the markdown table: rune-aware
// truncation per spike §B.2.3 plus pipe escaping so a title containing `|`
// (rare but legal) doesn't break the table layout.
func tasksListTitleCell(title string) string {
	t := truncateMD(title, tasksListTitleCap)
	if strings.Contains(t, "|") {
		t = strings.ReplaceAll(t, "|", "&#124;")
	}
	return t
}

// tasksListDueCell renders the Due column. Date-only ISO from
// taskSummary.dueDate goes through formatRelTime which already handles the
// `now`-relative phrasing. Empty / nil inputs collapse to "—" so the column
// width stays stable.
func tasksListDueCell(due *string, now time.Time) string {
	if due == nil || *due == "" {
		return "—"
	}
	return formatRelTime(*due, now)
}

// tasksListActions emits the button row per spike §B.2.4. Order: Refresh
// always; Prev / Next on the paginated branch (conditioned on page > 1 / page
// < total_pages); Clear filter on filtered-empty; Create task on
// empty / filtered-empty (primary).
func tasksListActions(f tasksListFilter, total int, filtered bool) []*model.PostAction {
	actions := []*model.PostAction{
		makeAction("tasks_list_refresh", "Refresh", postActionStyleDefault, tasksListArgv(f, currentPage(f))),
	}
	if total > 0 {
		if f.Page > 1 {
			actions = append(actions, makeAction("tasks_list_prev", "Prev", postActionStyleDefault, tasksListArgv(f, f.Page-1)))
		}
		if f.TotalPages > 0 && f.Page < f.TotalPages {
			actions = append(actions, makeAction("tasks_list_next", "Next", postActionStyleDefault, tasksListArgv(f, f.Page+1)))
		}
	}
	if total == 0 && filtered {
		actions = append(actions, makeAction("tasks_list_clear_filter", "Clear filter", postActionStyleDefault, []string{"tasks", "list", "--status=active"}))
	}
	if total == 0 {
		// Per spike §B.2.4: Create task is a primary button on both empty
		// branches (it cannot create directly because slash `tasks create`
		// requires --title — the button just routes users to the help card
		// that documents the slash form).
		actions = append(actions, makeAction("tasks_list_create", "Create task", postActionStylePrimary, []string{"help"}))
	}
	return actions
}

// currentPage normalizes the echoed page to at least 1 so a missing field on
// an older CLI doesn't produce `--page=0` in the Refresh argv.
func currentPage(f tasksListFilter) int {
	if f.Page < 1 {
		return 1
	}
	return f.Page
}

// tasksListArgv reconstructs the CLI argv from the echoed filter + target
// page. `status` is always emitted explicitly (per spike §B.1.4 dashboard
// example) so the button click stays deterministic against future changes to
// the CLI's default. `page=1` is the CLI default and is omitted from the argv
// to keep button payloads compact (and to keep the Refresh button on a
// first-page card argv-identical to the dashboard's "View today's tasks"
// button).
func tasksListArgv(f tasksListFilter, targetPage int) []string {
	argv := []string{"tasks", "list", "--status=" + tasksListStatusAlias(f.Status)}
	if f.Priority != nil && *f.Priority != "" {
		argv = append(argv, "--priority="+*f.Priority)
	}
	if f.ProjectID != nil && *f.ProjectID != "" {
		argv = append(argv, "--project="+*f.ProjectID)
	}
	if f.Tag != nil && *f.Tag != "" {
		argv = append(argv, "--tag="+*f.Tag)
	}
	if targetPage > 1 {
		argv = append(argv, fmt.Sprintf("--page=%d", targetPage))
	}
	return argv
}

// priorityLetter returns the compact one-letter token used in the
// today-tasks-panel table per spike §B.2.3. Distinct from priorityChip so the
// table column stays dense; the chip-with-emoji form is used by the
// task-detail-view title.
func priorityLetter(p *string) string {
	if p == nil {
		return "—"
	}
	switch *p {
	case "high":
		return "H"
	case "medium":
		return "M"
	case "low":
		return "L"
	}
	return "—"
}
