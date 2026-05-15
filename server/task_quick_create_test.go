package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// taskQuickCreateClock is the wall clock used by every task-quick-create test
// so the Due relative-time cell and any timestamp-derived assertions stay
// deterministic across runs.
var taskQuickCreateClock = time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

// taskCreateRaw assembles a `fulcrum tasks create --json` envelope from a
// taskSummary. Tests use it instead of hand-writing JSON so schema drift
// surfaces here rather than inside renderTaskQuickCreate.
func taskCreateRaw(t *testing.T, task taskSummary) []byte {
	t.Helper()
	body := struct {
		SchemaVersion int         `json:"schema_version"`
		Verb          string      `json:"verb"`
		Task          taskSummary `json:"task"`
	}{
		SchemaVersion: 1,
		Verb:          "tasks.create",
		Task:          task,
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	out, err := json.Marshal(map[string]any{"success": true, "data": json.RawMessage(data)})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return out
}

// taskCreateErrorRaw assembles a `fulcrum tasks create --json` envelope with
// only the canonical envelope-error object populated (the shape emitEror in
// the CLI's mattermost-verbs.ts produces for MISSING_TITLE / CREATE_FAILED).
func taskCreateErrorRaw(t *testing.T, code, message string) []byte {
	t.Helper()
	body := struct {
		SchemaVersion int    `json:"schema_version"`
		Verb          string `json:"verb"`
		Error         struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{
		SchemaVersion: 1,
		Verb:          "tasks.create",
	}
	body.Error.Code = code
	body.Error.Message = message
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	out, err := json.Marshal(map[string]any{"success": true, "data": json.RawMessage(data)})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return out
}

func sampleCreatedTask() taskSummary {
	priority := "high"
	worktree := "worktree"
	wt := "/srv/worktrees/t_99"
	project := "fulcrum"
	due := "2026-05-16"
	return taskSummary{
		ID:           "t_99",
		Title:        "Wire task-quick-create renderer",
		Status:       "TO_DO",
		Priority:     &priority,
		Type:         &worktree,
		ProjectID:    &project,
		Tags:         []string{"plugin", "render"},
		DueDate:      &due,
		Agent:        "claude",
		WorktreePath: &wt,
		CreatedAt:    "2026-05-15T11:50:00Z",
		UpdatedAt:    "2026-05-15T11:50:00Z",
	}
}

// TestRenderTaskQuickCreate_Success_WithActor exercises the §B.4.3 success
// shape with the slashing user threaded through as the actor mention. Locks
// title, pretext, color, footer, the seven-field grid (§B.4.3 order), and
// the two-button row (§B.4.4).
func TestRenderTaskQuickCreate_Success_WithActor(t *testing.T) {
	att, err := renderEnvelopeAtForRequest(taskCreateRaw(t, sampleCreatedTask()), taskQuickCreateClock, "u_actor", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Color != colorStatusDoing {
		t.Errorf("color: got %q want %q", att.Color, colorStatusDoing)
	}
	wantTitle := ":sparkles: New task · Wire task-quick-create renderer"
	if att.Title != wantTitle {
		t.Errorf("title:\n got: %q\nwant: %q", att.Title, wantTitle)
	}
	if att.Pretext != "created `t_99`" {
		t.Errorf("pretext: %q", att.Pretext)
	}
	if att.Footer != "fulcrum/tasks.create · status=to_do" {
		t.Errorf("footer: %q", att.Footer)
	}
	wantText := "_Created by <@u_actor>. Open the detail card to act on it._"
	if att.Text != wantText {
		t.Errorf("text:\n got: %q\nwant: %q", att.Text, wantText)
	}
	if len(att.Fields) != 7 {
		t.Fatalf("fields len: got %d want 7", len(att.Fields))
	}
	wantFieldOrder := []string{"Status", "Priority", "Project", "Due", "Tags", "Agent", "Type"}
	for i, want := range wantFieldOrder {
		if att.Fields[i].Title != want {
			t.Errorf("field[%d].Title: got %q want %q", i, att.Fields[i].Title, want)
		}
		if !att.Fields[i].Short {
			t.Errorf("field[%d] (%s) must be Short=true", i, want)
		}
	}
	if v := fieldStr(t, att.Fields[0]); !strings.Contains(v, "TO_DO") {
		t.Errorf("Status field: %q", v)
	}
	if v := fieldStr(t, att.Fields[1]); v != ":red_circle: H" {
		t.Errorf("Priority chip: %q", v)
	}
	if v := fieldStr(t, att.Fields[2]); v != "fulcrum" {
		t.Errorf("Project value: %q", v)
	}
	if v := fieldStr(t, att.Fields[3]); v != "in 12h" {
		t.Errorf("Due relative value: %q", v)
	}
	if v := fieldStr(t, att.Fields[4]); v != "plugin, render" {
		t.Errorf("Tags value: %q", v)
	}
	if v := fieldStr(t, att.Fields[5]); v != "claude" {
		t.Errorf("Agent value: %q", v)
	}
	if v := fieldStr(t, att.Fields[6]); v != "worktree" {
		t.Errorf("Type value: %q", v)
	}
	if len(att.Actions) != 2 {
		t.Fatalf("actions len: got %d want 2", len(att.Actions))
	}
	assertActionArgv(t, att.Actions[0], "task_quick_create_open", postActionStylePrimary, []string{"tasks", "get", "t_99"}, false)
	assertActionArgv(t, att.Actions[1], "task_quick_create_view_today", postActionStyleDefault, []string{"tasks", "list", "--status=active"}, false)
}

// TestRenderTaskQuickCreate_Success_NoActor checks the fallback Text shape
// for non-slash entry points or unit tests that don't carry an actor id. The
// "_Created by <@…>_" segment collapses so the line still parses cleanly.
func TestRenderTaskQuickCreate_Success_NoActor(t *testing.T) {
	att, err := renderEnvelopeAt(taskCreateRaw(t, sampleCreatedTask()), taskQuickCreateClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Text != "_Created. Open the detail card to act on it._" {
		t.Errorf("text without actor: %q", att.Text)
	}
}

// TestRenderTaskQuickCreate_NullableFields covers the scratch-task path: no
// priority, no project, no tags, no due, no worktree. Each field collapses to
// "—" so the column grid never breaks, and the Type column reflects the CLI's
// "scratch" value.
func TestRenderTaskQuickCreate_NullableFields(t *testing.T) {
	scratch := "scratch"
	task := taskSummary{
		ID:        "t_scratch",
		Title:     "quick note",
		Status:    "TO_DO",
		Type:      &scratch,
		Agent:     "claude",
		CreatedAt: "2026-05-15T11:50:00Z",
		UpdatedAt: "2026-05-15T11:50:00Z",
	}
	att, err := renderEnvelopeAtForRequest(taskCreateRaw(t, task), taskQuickCreateClock, "u_actor", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Color != colorStatusDoing {
		t.Errorf("color: %q", att.Color)
	}
	if v := fieldStr(t, att.Fields[1]); v != "—" {
		t.Errorf("Priority must dash: %q", v)
	}
	if v := fieldStr(t, att.Fields[2]); v != "—" {
		t.Errorf("Project must dash: %q", v)
	}
	if v := fieldStr(t, att.Fields[3]); v != "—" {
		t.Errorf("Due must dash: %q", v)
	}
	if v := fieldStr(t, att.Fields[4]); v != "—" {
		t.Errorf("Tags must dash: %q", v)
	}
	if v := fieldStr(t, att.Fields[6]); v != "scratch" {
		t.Errorf("Type: %q", v)
	}
}

// TestRenderTaskQuickCreate_MissingTaskID guards against a malformed envelope
// (no task.id) leaking through as an empty-id success card with broken Open
// button. The renderer returns an error so applyEnvelopeToPost falls back to
// the §0.5 generic colorError form rather than emitting a half-rendered card.
func TestRenderTaskQuickCreate_MissingTaskID(t *testing.T) {
	in := []byte(`{
		"success": true,
		"data": {
			"schema_version": 1,
			"verb": "tasks.create",
			"task": { "title": "no id" }
		}
	}`)
	if _, err := renderEnvelope(in); err == nil {
		t.Fatalf("expected error for missing task.id")
	}
}

// TestTaskQuickCreateBusinessErrorMessage_MissingTitle locks the §B.4.5 copy
// for the CLI's MISSING_TITLE code (emitted by cli/src/commands/mattermost-
// verbs.ts:421 with ExitCodes.INVALID_ARGS). Both the uppercase enum form
// and the spike's lowercase form are accepted so a future CLI rename doesn't
// silently break the copy.
func TestTaskQuickCreateBusinessErrorMessage_MissingTitle(t *testing.T) {
	for _, code := range []string{"MISSING_TITLE", "missing_title"} {
		got := taskQuickCreateBusinessErrorMessage(code, "title is required")
		if !strings.Contains(got, "--title=") {
			t.Errorf("%s: got %q must mention --title=", code, got)
		}
	}
}

// TestTaskQuickCreateBusinessErrorMessage_KnownCodes locks one assertion per
// spike-listed code so a future copy refactor surfaces here.
func TestTaskQuickCreateBusinessErrorMessage_KnownCodes(t *testing.T) {
	cases := []struct {
		code     string
		msg      string
		contains string
	}{
		{"unknown_project", `project "ops" not found`, "project"},
		{"unknown_repo", "repo not on disk", "repo"},
		{"invalid_priority", "priority must be one of ...", "high, medium, low"},
		{"invalid_type", "type must be ...", "worktree"},
		{"invalid_due", "use YYYY-MM-DD", "YYYY-MM-DD"},
		{"worktree_create_failed", "git worktree add: fatal", "worktree not created"},
	}
	for _, c := range cases {
		got := taskQuickCreateBusinessErrorMessage(c.code, c.msg)
		if !strings.Contains(got, c.contains) {
			t.Errorf("%s: %q must contain %q", c.code, got, c.contains)
		}
	}
}

// TestTaskQuickCreateBusinessErrorMessage_UnknownCode confirms unknown codes
// fall back to the generic tasks message formatter rather than producing an
// empty ephemeral. This is the §B.4.5 backstop for a future CLI code landing
// before its plugin copy.
func TestTaskQuickCreateBusinessErrorMessage_UnknownCode(t *testing.T) {
	got := taskQuickCreateBusinessErrorMessage("FUTURE_CODE", "some detail")
	if !strings.Contains(got, "FUTURE_CODE") {
		t.Errorf("unknown code must surface in fallback: %q", got)
	}
	if !strings.Contains(got, "tasks.create") {
		t.Errorf("unknown code must surface verb in fallback: %q", got)
	}
}

// TestRenderTaskQuickCreate_EnvelopeError_FallsBackToGenericCard documents the
// defensive path: command.go ephemerals tasks.create envelope errors before
// they reach renderEnvelopeAtForRequest, so this branch only fires if a future
// caller (a button click, a re-render after a CLI schema change) routes an
// envelope-error to the renderer. Render falls back to the §0.5 generic
// colorError card rather than emitting a malformed success card.
func TestRenderTaskQuickCreate_EnvelopeError_FallsBackToGenericCard(t *testing.T) {
	att, err := renderEnvelopeAt(taskCreateErrorRaw(t, "MISSING_TITLE", "title is required"), taskQuickCreateClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Color != colorError {
		t.Errorf("color: got %q want colorError", att.Color)
	}
	if !strings.Contains(att.Title, "tasks.create — error") {
		t.Errorf("title: %q", att.Title)
	}
	if !strings.Contains(att.Text, "MISSING_TITLE") {
		t.Errorf("body missing code: %q", att.Text)
	}
}
