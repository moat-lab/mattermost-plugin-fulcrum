package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// taskCreatePayload mirrors the `data` payload of `fulcrum tasks create --json`
// (cli/JSON_SCHEMA.md §tasks.create). schema_version + verb live on the
// envelopeData parent; here we only decode the per-verb body.
type taskCreatePayload struct {
	Task taskSummary `json:"task"`
}

// renderTaskQuickCreate produces the task-quick-create success SlackAttachment
// per spike §B.4.3. Envelope-level business errors are routed to the slash /
// /action / /dialog ephemeral path BEFORE this renderer fires (per §B.4.5);
// reaching this function with a missing task surfaces as a render error so the
// caller falls back to the §0.5 generic error form rather than emitting an
// empty success card.
func renderTaskQuickCreate(raw json.RawMessage, now time.Time, actorUserID string) (*model.SlackAttachment, error) {
	var p taskCreatePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("tasks.create payload: %w", err)
	}
	if p.Task.ID == "" {
		return nil, fmt.Errorf("tasks.create payload: missing task.id")
	}
	att := &model.SlackAttachment{
		Color:   colorStatusDoing,
		Title:   fmt.Sprintf(":sparkles: New task · %s", p.Task.Title),
		Pretext: fmt.Sprintf("created `%s`", p.Task.ID),
		Fields:  renderTaskQuickCreateFields(p.Task, now),
		Text:    renderTaskQuickCreateText(actorUserID),
		Footer:  fmt.Sprintf("fulcrum/tasks.create · status=%s", strings.ToLower(p.Task.Status)),
		Actions: taskQuickCreateActions(p.Task.ID),
	}
	return att, nil
}

// renderTaskQuickCreateFields builds the compact Fields[] section per spike
// §B.4.3. Order: Status, Priority, Project, Due, Tags, Agent, Type. Each value
// collapses to "—" on nil/empty so the column grid stays stable across the
// three CLI states (worktree task with full metadata, scratch task with no
// project, draft task with no tags).
func renderTaskQuickCreateFields(t taskSummary, now time.Time) []*model.SlackAttachmentField {
	return []*model.SlackAttachmentField{
		{Title: "Status", Value: fmt.Sprintf("%s %s", statusChip(t.Status), orDash(t.Status)), Short: true},
		{Title: "Priority", Value: priorityChip(t.Priority), Short: true},
		{Title: "Project", Value: orDashPtr(t.ProjectID), Short: true},
		{Title: "Due", Value: dueDateValue(t.DueDate, now), Short: true},
		{Title: "Tags", Value: tagsOrDash(t.Tags), Short: true},
		{Title: "Agent", Value: orDash(t.Agent), Short: true},
		{Title: "Type", Value: orDashPtr(t.Type), Short: true},
	}
}

// renderTaskQuickCreateText emits the fixed Text block per spike §B.4.3. When
// the slash invocation carries a Mattermost user id, the line renders the
// mention so the channel sees who created the task; when no actor is wired
// (legacy renderers or unit tests that don't carry the slash context), the
// "by <@…>" segment collapses so the line still parses cleanly.
func renderTaskQuickCreateText(actorUserID string) string {
	if actorUserID == "" {
		return "_Created. Open the detail card to act on it._"
	}
	return fmt.Sprintf("_Created by <@%s>. Open the detail card to act on it._", actorUserID)
}

// taskQuickCreateActions renders the two-button row per spike §B.4.4: the
// primary `Open task` button (always; the freshly created task id is the only
// information the user needs), then a default `View today's tasks` button so
// the user can pivot from "I just made a task" to "here is everything I'm
// holding". No mutation buttons — mutate from task-detail-view, not from the
// creation card.
func taskQuickCreateActions(taskID string) []*model.PostAction {
	return []*model.PostAction{
		makeAction("task_quick_create_open", "Open task", postActionStylePrimary, []string{"tasks", "get", taskID}),
		makeAction("task_quick_create_view_today", "View today's tasks", postActionStyleDefault, []string{"tasks", "list", "--status=active"}),
	}
}

// taskQuickCreateBusinessErrorMessage formats the ephemeral text shown to the
// slashing user when a tasks.create envelope returns a business error.code per
// spike §B.4.5. Unknown codes fall through to the generic tasks message
// formatter so a future CLI code surfaces as `<verb>: <code> — <message>`
// rather than vanishing into a blank ephemeral.
func taskQuickCreateBusinessErrorMessage(code, message string) string {
	switch code {
	case "MISSING_TITLE", "missing_title":
		return `title is required: use --title="..."`
	case "unknown_project":
		if message != "" {
			return fmt.Sprintf(`project not found: %s — use /f projects to list.`, message)
		}
		return "project not found — use /f projects to list."
	case "unknown_repo":
		if message != "" {
			return fmt.Sprintf("repo not found: %s", message)
		}
		return "repo not found"
	case "invalid_priority":
		base := "invalid --priority (allowed: high, medium, low)"
		if message != "" {
			return base + " — " + message
		}
		return base
	case "invalid_type":
		base := "invalid --type (allowed: worktree, scratch, draft)"
		if message != "" {
			return base + " — " + message
		}
		return base
	case "invalid_due":
		base := "invalid --due (use YYYY-MM-DD)"
		if message != "" {
			return base + " — " + message
		}
		return base
	case "worktree_create_failed":
		if message != "" {
			return "worktree not created: " + message
		}
		return "worktree not created"
	}
	return tasksBusinessErrorMessage("tasks.create", code, message)
}
