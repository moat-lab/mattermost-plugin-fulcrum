package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// dashboardPayload mirrors the `data` payload of `fulcrum dashboard --json`
// (cli/JSON_SCHEMA.md §dashboard). schema_version + verb live on the
// envelopeData parent; here we only decode the dashboard-specific fields.
type dashboardPayload struct {
	TasksByStatus map[string]int `json:"tasks_by_status"`
	ActiveTasks   int            `json:"active_tasks"`
	AppsByStatus  map[string]int `json:"apps_by_status"`
	TotalApps     int            `json:"total_apps"`
	DueToday      []taskSummary  `json:"due_today"`
}

// dueTodayCap caps the inline due_today list at the value the spike spec
// (§B.1.3) calls for. Overflow rolls into a "…and N more" suffix that
// re-directs the user to `today-tasks-panel` once that feature lands.
const dueTodayCap = 5

// taskStatusOrder is the rendering order for `Tasks · by status` field
// values. Status keys not in this list are appended in deterministic
// alphabetical order so unknown statuses still surface.
var taskStatusOrder = []string{"TO_DO", "IN_PROGRESS", "IN_REVIEW", "DONE", "CANCELED"}

// appStatusOrder is the rendering order for `Apps · by status` field
// values. Same overflow handling as taskStatusOrder.
var appStatusOrder = []string{"running", "building", "pending", "stopped", "failed"}

// appStatusChip renders an app-status enum as an emoji+label chip. Unknown
// statuses surface a grey question chip to flag CLI schema drift without
// dropping the count.
func appStatusChip(status string) string {
	switch status {
	case "running":
		return ":large_blue_circle: running"
	case "building":
		return ":large_orange_diamond: building"
	case "pending":
		return ":hourglass_flowing_sand: pending"
	case "stopped":
		return ":white_circle: stopped"
	case "failed":
		return ":red_circle: failed"
	default:
		return ":grey_question: " + status
	}
}

// renderDashboard produces the dashboard-home SlackAttachment for the five
// state branches called out in mattermost-plugin-fulcrum#7: empty,
// tasks-only, apps-only, full, error. `now` is injected so tests can pin
// the relative-time pretext.
func renderDashboard(raw json.RawMessage, now time.Time) (*model.SlackAttachment, error) {
	var d dashboardPayload
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("dashboard payload: %w", err)
	}

	att := &model.SlackAttachment{
		Color:   colorAccent,
		Title:   fmt.Sprintf("Fulcrum dashboard · %d active · %d apps", d.ActiveTasks, d.TotalApps),
		Pretext: ":sparkles: " + now.UTC().Format("2006-01-02 15:04 UTC"),
		Fields: []*model.SlackAttachmentField{
			{Title: "Tasks · by status", Value: renderStatusBucket(d.TasksByStatus, taskStatusOrder, statusBucketTask, "_no tasks tracked yet_"), Short: true},
			{Title: "Apps · by status", Value: renderStatusBucket(d.AppsByStatus, appStatusOrder, statusBucketApp, "_no apps tracked yet_"), Short: true},
		},
		Footer:  "fulcrum/dashboard · schema_version=1",
		Actions: dashboardActions(d),
	}

	if text := renderDueToday(d.DueToday); text != "" {
		att.Text = text
	}

	return att, nil
}

type statusBucketKind int

const (
	statusBucketTask statusBucketKind = iota
	statusBucketApp
)

// renderStatusBucket prints "<chip> ×<count>" lines for buckets with
// count > 0, in the spec order. Empty buckets render the supplied
// placeholder so the field never disappears (preserves the two-column
// layout on Mattermost mobile clients).
func renderStatusBucket(bucket map[string]int, order []string, kind statusBucketKind, emptyPlaceholder string) string {
	if totalCount(bucket) == 0 {
		return emptyPlaceholder
	}
	seen := map[string]bool{}
	lines := make([]string, 0, len(bucket))
	for _, key := range order {
		if n := bucket[key]; n > 0 {
			lines = append(lines, bucketLine(kind, key, n))
		}
		seen[key] = true
	}
	// Append unknown keys in alphabetical order so future CLI additions surface
	// without ordering ambiguity.
	extras := make([]string, 0)
	for key := range bucket {
		if !seen[key] && bucket[key] > 0 {
			extras = append(extras, key)
		}
	}
	sort.Strings(extras)
	for _, key := range extras {
		lines = append(lines, bucketLine(kind, key, bucket[key]))
	}
	return strings.Join(lines, "\n")
}

func bucketLine(kind statusBucketKind, key string, n int) string {
	switch kind {
	case statusBucketApp:
		return fmt.Sprintf("%s ×%d", appStatusChip(key), n)
	default:
		return fmt.Sprintf("%s ×%d", statusChip(key), n)
	}
}

func totalCount(bucket map[string]int) int {
	sum := 0
	for _, v := range bucket {
		sum += v
	}
	return sum
}

// renderDueToday composes the **Due today** Text block per spike §B.1.3.
// Returns empty string when no items so the renderer omits the Text field
// entirely (avoids an empty markdown block under Fields[]).
func renderDueToday(items []taskSummary) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**Due today** (%d):", len(items))
	displayed := items
	overflow := 0
	if len(items) > dueTodayCap {
		displayed = items[:dueTodayCap]
		overflow = len(items) - dueTodayCap
	}
	for _, t := range displayed {
		fmt.Fprintf(&b, "\n- %s  ·  %s", formatTaskTitleLine(t), statusChip(t.Status))
	}
	if overflow > 0 {
		fmt.Fprintf(&b, "\n…and %d more — `/f tasks list --tag=due-today`", overflow)
	}
	return b.String()
}

// Mattermost's integration_action.go validates Style against a fixed string
// set. Centralizing the literals lets the typechecker still catch typos when
// future renderers reach for them.
const (
	postActionStyleDefault = "default"
	postActionStylePrimary = "primary"
	postActionStyleDanger  = "danger"
)

// dashboardActions builds the button row called out in spike §B.1.4. Refresh
// + Help are always present; View today's tasks / View apps appear only when
// the underlying entity has any rows so empty-state cards don't show buttons
// that lead to empty panels.
func dashboardActions(d dashboardPayload) []*model.PostAction {
	actions := []*model.PostAction{
		makeAction("dashboard_refresh", "Refresh", postActionStyleDefault, []string{"dashboard"}),
	}
	if d.ActiveTasks > 0 {
		actions = append(actions, makeAction("dashboard_view_today", "View today's tasks", postActionStylePrimary, []string{"tasks", "list", "--status=active"}))
	}
	if d.TotalApps > 0 {
		actions = append(actions, makeAction("dashboard_view_apps", "View apps", postActionStyleDefault, []string{"apps", "list"}))
	}
	actions = append(actions, makeAction("dashboard_help", "Help", postActionStyleDefault, []string{"help"}))
	return actions
}

// makeAction wires a fulcrum button into the existing /plugins/fulcrum/action
// endpoint. argv is stuffed into Integration.Context[actionContextArgvKey]
// per the contract in server/http.go (which prepends "fulcrum" and appends
// "--json" before invoking rexec).
func makeAction(id, label, style string, argv []string) *model.PostAction {
	ctxArgv := make([]any, len(argv))
	for i, v := range argv {
		ctxArgv[i] = v
	}
	return &model.PostAction{
		Id:    id,
		Name:  label,
		Type:  model.PostActionTypeButton,
		Style: style,
		Integration: &model.PostActionIntegration{
			URL: "/plugins/" + manifestID + "/action",
			Context: map[string]any{
				actionContextArgvKey: ctxArgv,
			},
		},
	}
}
