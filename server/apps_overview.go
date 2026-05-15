package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// appsListPayload mirrors the `data` payload of `fulcrum apps list --json`
// (cli/JSON_SCHEMA.md §apps.list). schema_version + verb live on the
// envelopeData parent; only the apps-overview-specific fields are decoded
// here.
type appsListPayload struct {
	Total int          `json:"total"`
	Apps  []appSummary `json:"apps"`
}

// appSummary mirrors the CLI AppSummary shape. Nullable fields (`repository`,
// `lastDeployedAt`, `lastDeployCommit`) use pointer types so the table can
// distinguish "field omitted" from "field present and empty" — both render as
// "—" today, but downstream renderers (`app-detail-view`) will need the
// distinction.
type appSummary struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Status            string  `json:"status"`
	Branch            string  `json:"branch"`
	Repository        *string `json:"repository"`
	LastDeployedAt    *string `json:"lastDeployedAt"`
	LastDeployCommit  *string `json:"lastDeployCommit"`
	AutoDeployEnabled bool    `json:"autoDeployEnabled"`
}

// appsOverviewRowCap caps the rendered table per spike §B.6.3. Overflow
// triggers a footer truncation note rather than dropping rows silently.
const appsOverviewRowCap = 20

// appsOverviewStatusOrder is the pretext-aggregation order called out in
// spike §B.6.3: running first (happy path), then failed (eye-catcher),
// building/pending (transient), stopped last. Unknown statuses are appended
// alphabetically so a future CLI status addition surfaces without losing the
// count.
var appsOverviewStatusOrder = []string{"running", "failed", "building", "pending", "stopped"}

// renderAppsOverview produces the apps-overview SlackAttachment for the four
// state branches called out in mattermost-plugin-fulcrum#9: empty,
// all-running, mixed (with/without failed), and the error envelope (handled
// by renderBusinessError before this renderer is reached). `now` is injected
// so tests can pin the "Last deploy" relative-time column.
func renderAppsOverview(raw json.RawMessage, now time.Time) (*model.SlackAttachment, error) {
	var p appsListPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("apps.list payload: %w", err)
	}
	counts := countAppsByStatus(p.Apps)
	return &model.SlackAttachment{
		Color:   appsOverviewColor(counts),
		Title:   fmt.Sprintf("Apps · %d", p.Total),
		Pretext: appsOverviewPretext(counts),
		Text:    appsOverviewText(p.Apps, now),
		Footer:  appsOverviewFooter(p.Total, len(p.Apps)),
		Actions: []*model.PostAction{
			makeAction("apps_overview_refresh", "Refresh", postActionStyleDefault, []string{"apps", "list"}),
		},
	}, nil
}

func countAppsByStatus(apps []appSummary) map[string]int {
	m := map[string]int{}
	for _, a := range apps {
		m[a.Status]++
	}
	return m
}

// appsOverviewColor implements the spike §B.6.3 color decision table:
// empty → TODO grey, all-running → done green, mixed-with-failed → high red,
// mixed-without-failed → medium amber. The colorError branch is taken before
// reaching this renderer (envelope-error path in renderBusinessError).
func appsOverviewColor(counts map[string]int) string {
	total := 0
	for _, n := range counts {
		total += n
	}
	if total == 0 {
		return colorStatusTODO
	}
	if counts["failed"] > 0 {
		return colorPriorityHigh
	}
	if counts["running"] == total {
		return colorStatusDone
	}
	return colorPriorityMedium
}

// appsOverviewPretext builds the single-line status-bucket aggregation row.
// Only buckets with count > 0 are emitted (per spike §B.6.3) so a mostly
// healthy fleet doesn't render six "×0" columns of noise. Returns empty
// string when there are no apps — the empty branch shows its message in
// Text, not Pretext.
func appsOverviewPretext(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(counts))
	seen := map[string]bool{}
	for _, status := range appsOverviewStatusOrder {
		if n := counts[status]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s ×%d", appStatusChip(status), n))
		}
		seen[status] = true
	}
	extras := make([]string, 0)
	for status := range counts {
		if !seen[status] && counts[status] > 0 {
			extras = append(extras, status)
		}
	}
	sort.Strings(extras)
	for _, s := range extras {
		parts = append(parts, fmt.Sprintf("%s ×%d", appStatusChip(s), counts[s]))
	}
	return strings.Join(parts, "   ")
}

// appsOverviewText emits the markdown table body (or the empty-state
// fallback). The 20-row cap matches spike §B.6.3; overflow rows are dropped
// from the table and the truncation is announced in the footer.
func appsOverviewText(apps []appSummary, now time.Time) string {
	if len(apps) == 0 {
		return "_No apps registered. Use fulcrum CLI to onboard one (`fulcrum apps onboard`)._"
	}
	headers := []string{"Status", "Name", "Branch", "Last deploy"}
	limit := len(apps)
	if limit > appsOverviewRowCap {
		limit = appsOverviewRowCap
	}
	rows := make([][]string, 0, limit)
	for i := 0; i < limit; i++ {
		a := apps[i]
		rows = append(rows, []string{
			appStatusChip(a.Status),
			"`" + a.Name + "`",
			orDash(a.Branch),
			lastDeployValue(a.LastDeployedAt, now),
		})
	}
	return renderMarkdownTable(headers, rows)
}

// appsOverviewFooter produces the spike-mandated footer line. When the apps
// list exceeded the 20-row cap, a "showing first 20" hint is appended so
// users notice the table is truncated even though the total count in the
// header is correct.
func appsOverviewFooter(total, rendered int) string {
	footer := fmt.Sprintf("fulcrum/apps.list · total=%d", total)
	if rendered > appsOverviewRowCap {
		footer += fmt.Sprintf(" · showing first %d", appsOverviewRowCap)
	}
	return footer
}

// lastDeployValue renders the "Last deploy" cell per spike §B.6.3:
// `<YYYY-MM-DD HH:MM> (<rel>)` UTC absolute + parenthesized relative time.
// nil / empty / unparseable timestamps collapse to "—" so column width stays
// consistent.
func lastDeployValue(iso *string, now time.Time) string {
	if iso == nil || *iso == "" {
		return "—"
	}
	t, err := parseLooseISO(*iso)
	if err != nil {
		return "—"
	}
	return fmt.Sprintf("%s (%s)", t.UTC().Format("2006-01-02 15:04"), formatRelTime(*iso, now))
}
