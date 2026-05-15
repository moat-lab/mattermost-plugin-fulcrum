package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// taskGetPayload mirrors the `data` payload of `fulcrum tasks get <id> --json`
// (cli/JSON_SCHEMA.md §tasks.get). The renderer never recomputes the action
// set — `Actions` is consumed verbatim per spike §B.3.2.
type taskGetPayload struct {
	Task    taskSummary  `json:"task"`
	Actions []taskAction `json:"actions"`
}

// taskAction is the envelope-emitted action descriptor. The plugin maps
// `id` to (argv, button style, dialog gate) via taskActionPlan — `label` is
// passed through unchanged so a CLI label update propagates without a plugin
// release.
type taskAction struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Destructive bool   `json:"destructive"`
}

// taskMutationVerbs is the set of `data.verb` values whose successful
// envelopes should NOT be rendered onto the original post directly: the
// /action and /dialog handlers re-invoke `tasks.get` to fetch the canonical
// post-mutation TaskSummary + refreshed `actions[]`. Round-tripping (spike
// §B.3.4) keeps the plugin and CLI state machines from diverging.
var taskMutationVerbs = map[string]bool{
	"tasks.set-status":   true,
	"tasks.set-priority": true,
	"tasks.start-agent":  true,
	"tasks.kill-agent":   true,
}

// statusColor maps a CLI task status to its SlackAttachment color per spike
// §0.2. Unknown statuses fall through to the brand accent so an unrecognised
// status still renders rather than displaying as an empty color band.
func statusColor(status string) string {
	switch status {
	case "TO_DO":
		return colorStatusTODO
	case "IN_PROGRESS":
		return colorStatusDoing
	case "IN_REVIEW":
		return colorStatusReview
	case "DONE":
		return colorStatusDone
	case "CANCELED":
		return colorStatusCanceled
	default:
		return colorAccent
	}
}

// renderTaskDetail produces the task-detail-view SlackAttachment per spike
// §B.3.3. `now` is injected so tests can pin the relative-time fields.
func renderTaskDetail(raw json.RawMessage, now time.Time) (*model.SlackAttachment, error) {
	var p taskGetPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("tasks.get payload: %w", err)
	}
	att := &model.SlackAttachment{
		Color:   statusColor(p.Task.Status),
		Title:   renderTaskDetailTitle(p.Task),
		Pretext: fmt.Sprintf("task ID `%s`", p.Task.ID),
		Fields:  renderTaskFieldsDetail(p.Task, now),
		Actions: taskDetailActions(p.Task.ID, p.Actions),
		Footer:  fmt.Sprintf("fulcrum/tasks.get · status=%s", strings.ToLower(p.Task.Status)),
	}
	if tip := renderTaskDetailTipText(p.Task); tip != "" {
		att.Text = tip
	}
	return att, nil
}

// renderTaskDetailTitle composes the Title field per §B.3.3. nil/empty
// priority skips its chip rather than emitting "—" inline; the Fields[]
// section is the canonical place to surface the dash for missing priority.
func renderTaskDetailTitle(t taskSummary) string {
	parts := []string{statusChip(t.Status)}
	if t.Priority != nil && *t.Priority != "" {
		parts = append(parts, priorityChip(t.Priority))
	}
	if t.Title != "" {
		parts = append(parts, t.Title)
	}
	return strings.Join(parts, " ")
}

// renderTaskDetailTipText emits the optional Text block per §B.3.3 — only when
// the task is a worktree with a path the user can `cd` into.
func renderTaskDetailTipText(t taskSummary) string {
	if t.Type == nil || *t.Type != "worktree" {
		return ""
	}
	if t.WorktreePath == nil || *t.WorktreePath == "" {
		return ""
	}
	return fmt.Sprintf("_Tip: `/f tasks diff %s` for a diff summary._", t.ID)
}

// renderTaskFieldsDetail builds the two-column Fields[] section per §B.3.3.
// Exported (lowercase package, but symbol is usable across the package) so the
// next sub-issues (task-quick-create, task-diff-view) can reuse the layout
// without redefining the dash semantics. Each value collapses to "—" when the
// underlying CLI field is null/empty so the column grid never breaks.
func renderTaskFieldsDetail(t taskSummary, now time.Time) []*model.SlackAttachmentField {
	return []*model.SlackAttachmentField{
		{Title: "Status", Value: fmt.Sprintf("%s %s", statusChip(t.Status), orDash(t.Status)), Short: true},
		{Title: "Priority", Value: priorityChip(t.Priority), Short: true},
		{Title: "Project", Value: orDashPtr(t.ProjectID), Short: true},
		{Title: "Due", Value: dueDateValue(t.DueDate, now), Short: true},
		{Title: "Tags", Value: tagsOrDash(t.Tags), Short: true},
		{Title: "Agent", Value: orDash(t.Agent), Short: true},
		{Title: "Worktree", Value: codeOrDash(t.WorktreePath), Short: true},
		{Title: "PR", Value: orDashPtr(t.PrURL), Short: true},
		{Title: "Created / Updated", Value: createdUpdatedLine(t.CreatedAt, t.UpdatedAt, now), Short: false},
	}
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func orDashPtr(p *string) string {
	if p == nil || *p == "" {
		return "—"
	}
	return *p
}

func tagsOrDash(tags []string) string {
	kept := make([]string, 0, len(tags))
	for _, t := range tags {
		if strings.TrimSpace(t) != "" {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		return "—"
	}
	return strings.Join(kept, ", ")
}

func codeOrDash(p *string) string {
	if p == nil || *p == "" {
		return "—"
	}
	return "`" + *p + "`"
}

func dueDateValue(p *string, now time.Time) string {
	if p == nil || *p == "" {
		return "—"
	}
	return formatRelTime(*p, now)
}

// createdUpdatedLine renders the combined "<abs> (<rel>) / <abs> (<rel>)"
// value per §B.3.3. Both timestamps are required by the CLI schema
// (TaskSummary.createdAt / updatedAt are non-nullable); an empty / unparseable
// value collapses to "—" but only on that half of the pair.
func createdUpdatedLine(createdISO, updatedISO string, now time.Time) string {
	return fmt.Sprintf("%s (%s) / %s (%s)",
		formatAbsTime(createdISO),
		formatRelTime(createdISO, now),
		formatAbsTime(updatedISO),
		formatRelTime(updatedISO, now),
	)
}

// formatAbsTime is the UTC absolute-time helper for the Created/Updated row.
// Empty input → "—"; unparseable input falls back to the raw string so a
// schema drift is visible without crashing the renderer.
func formatAbsTime(iso string) string {
	if iso == "" {
		return "—"
	}
	t, err := parseLooseISO(iso)
	if err != nil {
		return iso
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

// taskActionPlan maps a CLI-emitted action.id to the argv that should be
// invoked, the button Style, and whether the click must be gated by a
// confirmation dialog. The set is closed: action IDs the CLI never emits
// (see cli/src/commands/mattermost-verbs.ts) are dropped at render time so
// the plugin never invents a state-machine transition the CLI doesn't bless.
func taskActionPlan(action taskAction, taskID string) (argv []string, style string, dialog, ok bool) {
	switch action.ID {
	case "set_status_in_progress":
		return []string{"tasks", "set-status", taskID, "doing"}, postActionStylePrimary, false, true
	case "set_status_in_review":
		return []string{"tasks", "set-status", taskID, "review"}, postActionStylePrimary, false, true
	case "set_status_done":
		return []string{"tasks", "set-status", taskID, "done"}, postActionStylePrimary, false, true
	case "set_status_canceled":
		return []string{"tasks", "set-status", taskID, "canceled"}, postActionStyleDanger, true, true
	case "start_agent":
		return []string{"tasks", "start-agent", taskID}, postActionStylePrimary, false, true
	case "kill_agent":
		return []string{"tasks", "kill-agent", taskID}, postActionStyleDanger, true, true
	case "view_diff":
		return []string{"tasks", "diff", taskID}, postActionStyleDefault, false, true
	}
	return nil, "", false, false
}

// taskDetailActions renders the button row for a task-detail post. CLI-emitted
// actions[] drive the state-machine buttons (in declared order); the plugin
// then appends a universal Refresh button per spike §B.3.4 ("Refresh, always").
func taskDetailActions(taskID string, actions []taskAction) []*model.PostAction {
	out := make([]*model.PostAction, 0, len(actions)+1)
	for _, a := range actions {
		argv, style, dialog, ok := taskActionPlan(a, taskID)
		if !ok {
			continue
		}
		out = append(out, makeTaskAction(a.ID, a.Label, style, argv, dialog))
	}
	out = append(out, makeAction("task_refresh", "Refresh", postActionStyleDefault, []string{"tasks", "get", taskID}))
	return out
}

// makeTaskAction wraps makeAction and (when dialog is true) stamps the
// Integration.Context with the dialog flag the /action handler keys off to
// route the click into OpenInteractiveDialog instead of a direct CLI call.
func makeTaskAction(id, label, style string, argv []string, dialog bool) *model.PostAction {
	act := makeAction(id, label, style, argv)
	if dialog {
		act.Integration.Context[actionContextDialogKey] = true
	}
	return act
}

// tasksBusinessErrorMessage formats the ephemeral text shown to the clicking
// user when a `tasks.*` mutation envelope returns a business error.code per
// spike §B.3.5. Unknown codes fall through to a generic "<verb>: <code>"
// rather than rendering as a bot post — keeping the original card untouched
// is the §B.3.5 invariant (the task state didn't change).
func tasksBusinessErrorMessage(verb, code, message string) string {
	base := fmt.Sprintf("%s: %s", verb, code)
	if message != "" {
		base = base + " — " + message
	}
	switch code {
	case "task_not_found":
		return base + " (try `/f search <id>`)"
	case "invalid_status_transition":
		return base
	case "worktree_missing":
		return base + " (task has no worktree)"
	case "agent_already_running":
		return base
	case "agent_not_running":
		return base
	}
	return base
}

// taskIDFromArgv extracts the task id from a recognized tasks.* argv shape so
// the /action and /dialog handlers can round-trip `tasks.get` after a
// mutation. Empty string when the argv shape isn't a known task verb.
func taskIDFromArgv(argv []string) string {
	if len(argv) >= 3 && argv[0] == "tasks" {
		switch argv[1] {
		case "set-status", "set-priority", "start-agent", "kill-agent", "diff", "get":
			return argv[2]
		}
	}
	return ""
}
