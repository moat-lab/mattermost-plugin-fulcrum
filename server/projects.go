package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// projectSummary mirrors the canonical ProjectSummary shape in
// cli/JSON_SCHEMA.md §projects. description is *string because the CLI marks
// it nullable; defaultAgent is *string for the same reason. taskCounts is a
// nested object the renderer reads to compose the "Tasks (active / total)"
// column directly off the envelope rather than round-tripping `tasks list`.
type projectSummary struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Description  *string            `json:"description"`
	Status       string             `json:"status"`
	DefaultAgent *string            `json:"defaultAgent"`
	TaskCounts   projectTaskCounts  `json:"taskCounts"`
}

// projectTaskCounts mirrors ProjectSummary.taskCounts. Both fields are
// non-nullable in the CLI schema today; the renderer doesn't defensively
// guard against missing values because the schema gate (schema_version=1)
// already validates the envelope shape.
type projectTaskCounts struct {
	Total  int `json:"total"`
	Active int `json:"active"`
}

// projectsPayload mirrors the `data` payload of `fulcrum projects --json`
// (cli/JSON_SCHEMA.md §projects). The CLI does not echo a query argument
// (projects has no flags today), so the payload only carries `total` and the
// per-project rows.
type projectsPayload struct {
	Total    int              `json:"total"`
	Projects []projectSummary `json:"projects"`
}

// projectsRowCap caps the rendered markdown table at the spike §B.12.3
// ceiling (50 rows). Beyond the cap the renderer drops trailing rows and
// appends a footer truncation note so the user knows the count came from
// `total`, not from what they see in the table.
const projectsRowCap = 50

// projectsDescriptionWidth caps the Description column at the §B.12.3 width.
// Longer descriptions are tail-truncated with `…` so the table layout stays
// stable across rows; the user's full description still lives in the CLI and
// in `/f tasks list --project=<slug>` follow-ups.
const projectsDescriptionWidth = 60

// projectsBranch is the §B.12.3 three-way card classification: empty
// (total=0) / mixed (any archived or non-active row) / all-active. The error
// branch is routed via the renderBusinessError verb switch before this enum
// is reached.
type projectsBranch int

const (
	projectsBranchEmpty projectsBranch = iota
	projectsBranchMixed
	projectsBranchAllActive
)

// renderProjects produces the projects-panel SlackAttachment per spike
// §B.12. The envelope-error branch is reached via renderBusinessError's
// projects switch arm before this renderer is called; the remaining three
// branches share the same Title / Footer shape and differ on Color +
// Pretext + Text.
func renderProjects(raw json.RawMessage) (*model.SlackAttachment, error) {
	var p projectsPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("projects payload: %w", err)
	}
	counts := projectsCounts(p.Projects)
	branch := projectsBranchFor(p, counts)
	att := &model.SlackAttachment{
		Color:   projectsColor(branch),
		Title:   fmt.Sprintf("Projects (%d)", p.Total),
		Pretext: projectsPretext(counts),
		Text:    projectsText(p, branch),
		Footer:  projectsFooter(p.Total, len(p.Projects)),
		Actions: projectsActions(),
	}
	return att, nil
}

// projectsStatusCounts bundles the per-status counts the §B.12 color and
// pretext branches need. `unknown` collects every status outside the
// canonical two so a future CLI status addition (e.g. `paused`) doesn't
// silently win the all-active branch.
type projectsStatusCounts struct {
	active   int
	archived int
	unknown  int
}

// projectsCounts buckets projects by status into the canonical two §B.12
// buckets (plus `unknown` as the catch-all). Walking the slice once here
// keeps the branch / pretext / text helpers cheap and deterministic.
func projectsCounts(projects []projectSummary) projectsStatusCounts {
	var c projectsStatusCounts
	for _, p := range projects {
		switch p.Status {
		case "active":
			c.active++
		case "archived":
			c.archived++
		default:
			c.unknown++
		}
	}
	return c
}

// projectsBranchFor classifies an envelope into one of the three rendered
// §B.12.3 branches. Empty wins first (total=0 / no rows) so an envelope with
// no projects renders the TODO grey card regardless of any echoed `total`.
// Any archived OR unknown-status row promotes the card to mixed (warn), so
// a future CLI status that isn't `active` doesn't silently render as green.
func projectsBranchFor(p projectsPayload, c projectsStatusCounts) projectsBranch {
	if p.Total == 0 || len(p.Projects) == 0 {
		return projectsBranchEmpty
	}
	if c.archived > 0 || c.unknown > 0 {
		return projectsBranchMixed
	}
	return projectsBranchAllActive
}

// projectsColor maps a branch to its §B.12.3 color token.
func projectsColor(branch projectsBranch) string {
	switch branch {
	case projectsBranchMixed:
		return colorWarn
	case projectsBranchEmpty:
		return colorStatusTODO
	default:
		return colorAccent
	}
}

// projectsPretext renders the §B.12.3 single-line aggregate showing only the
// `count>0` buckets in the canonical status order (active → archived).
// Unknown-status rows are suppressed from the pretext (the renderer surfaces
// them inline via the grey-question chip in the table) so a future status
// addition surfaces visibly without polluting the aggregate line. Returns ""
// on the empty branch so the renderer omits Pretext entirely.
func projectsPretext(c projectsStatusCounts) string {
	parts := make([]string, 0, 2)
	if c.active > 0 {
		parts = append(parts, fmt.Sprintf("active ×%d", c.active))
	}
	if c.archived > 0 {
		parts = append(parts, fmt.Sprintf("archived ×%d", c.archived))
	}
	return strings.Join(parts, " · ")
}

// projectsText composes the SlackAttachment.Text body. The empty branch
// shows the §B.12.3 no-projects hint; the two non-empty branches share the
// markdown table form composed by projectsTableText so reviewers can rely
// on positional column comparison across cards.
func projectsText(p projectsPayload, branch projectsBranch) string {
	if branch == projectsBranchEmpty {
		return "_No projects. Create one via fulcrum CLI._"
	}
	return projectsTableText(p.Projects)
}

// projectsTableText composes the §B.12.3 markdown table. Rows beyond
// projectsRowCap are dropped from the table; the user sees `total` and the
// truncation note in the Footer so the cap doesn't silently hide rows.
func projectsTableText(projects []projectSummary) string {
	headers := []string{"Status", "Name", "Default agent", "Tasks (active / total)", "Description"}
	limit := len(projects)
	if limit > projectsRowCap {
		limit = projectsRowCap
	}
	rows := make([][]string, 0, limit)
	for i := 0; i < limit; i++ {
		p := projects[i]
		rows = append(rows, []string{
			projectsStatusChip(p.Status),
			projectsNameCell(p.Name),
			projectsDefaultAgentCell(p.DefaultAgent),
			projectsTaskCountsCell(p.TaskCounts),
			projectsDescriptionCell(p.Description),
		})
	}
	return renderMarkdownTable(headers, rows)
}

// projectsStatusChip renders the Status column emoji+label per spike
// §B.12.3. active=blue Doing chip, archived=black Canceled chip. Unknown
// statuses fall through to the grey-question chip so a future CLI status
// addition still renders without dropping the row entirely; the fallback
// surfaces the new status verbatim so review can spot it as a schema gap
// (§C.5).
func projectsStatusChip(status string) string {
	switch status {
	case "active":
		return ":large_blue_circle: active"
	case "archived":
		return ":black_circle: archived"
	}
	if status == "" {
		return "—"
	}
	return ":grey_question: " + status
}

// projectsNameCell renders the project name as inline code per spike
// §B.12.3. Pipe escaping mirrors jobsNameCell so a project name containing
// `|` (legal but rare) doesn't break the markdown table layout.
func projectsNameCell(name string) string {
	if name == "" {
		return "—"
	}
	if strings.Contains(name, "|") {
		name = strings.ReplaceAll(name, "|", "&#124;")
	}
	return "`" + name + "`"
}

// projectsDefaultAgentCell renders the Default agent column verbatim per
// spike §B.12.3. nil / empty render as "—" so the column width stays
// stable; non-empty values render plain text (no inline-code wrapping) so
// known agents (`claude`, `opencode`) read naturally.
func projectsDefaultAgentCell(agent *string) string {
	if agent == nil || *agent == "" {
		return "—"
	}
	return *agent
}

// projectsTaskCountsCell renders the §B.12.3 "Tasks (active / total)"
// column as `<a> / <t>`. Both numbers always render even when zero so the
// cell width stays stable for column alignment in clients that respect the
// `:---` separator.
func projectsTaskCountsCell(c projectTaskCounts) string {
	return fmt.Sprintf("%d / %d", c.Active, c.Total)
}

// projectsDescriptionCell renders the Description column per spike §B.12.3.
// nil / empty render as "—"; longer descriptions are rune-truncated to
// projectsDescriptionWidth via truncateMD so the table layout stays stable.
// Pipe characters are escaped so a description containing `|` (e.g. a
// multi-clause sentence) doesn't break the markdown table.
func projectsDescriptionCell(desc *string) string {
	if desc == nil || *desc == "" {
		return "—"
	}
	out := truncateMD(*desc, projectsDescriptionWidth)
	if strings.Contains(out, "|") {
		out = strings.ReplaceAll(out, "|", "&#124;")
	}
	return out
}

// projectsFooter renders the §B.12.3 footer plus the row-cap truncation
// note when the rendered table dropped rows. The truncation note names both
// the CLI total and the rendered count so the user knows whether they need
// to drill into individual projects to see the remainder.
func projectsFooter(total, rendered int) string {
	base := fmt.Sprintf("fulcrum/projects · total=%d", total)
	if rendered > projectsRowCap {
		return fmt.Sprintf("%s · showing first %d", base, projectsRowCap)
	}
	return base
}

// projectsActions emits the §B.12.4 button row. Refresh is the only button
// the spike defines for this verb — projects has no mutation CLI verb
// (archive / rename are not in the schema), so per-row buttons are
// deliberately omitted to avoid a card whose buttons can't actually do
// anything.
func projectsActions() []*model.PostAction {
	return []*model.PostAction{
		makeAction("projects_refresh", "Refresh", postActionStyleDefault, projectsRefreshArgv()),
	}
}

// projectsRefreshArgv is the Refresh argv used by both the renderer and the
// colorError card. Centralized so the two paths can never drift.
func projectsRefreshArgv() []string {
	return []string{"projects"}
}
