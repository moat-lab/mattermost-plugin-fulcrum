package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// taskDetailClock is the wall clock used by every task-detail test so the
// relative-time fields and Created/Updated row are deterministic across runs.
var taskDetailClock = time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

// taskDetailRaw assembles a `fulcrum tasks get --json` envelope from a
// payload struct. Tests use it instead of hand-writing JSON so schema drift
// surfaces as a compile failure here rather than in renderTaskDetail.
func taskDetailRaw(t *testing.T, p taskGetPayload) []byte {
	t.Helper()
	body := struct {
		taskGetPayload
		SchemaVersion int    `json:"schema_version"`
		Verb          string `json:"verb"`
	}{
		taskGetPayload: p,
		SchemaVersion:  1,
		Verb:           "tasks.get",
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

func sampleTask() taskSummary {
	worktree := "worktree"
	wt := "/srv/worktrees/t_42"
	priority := "high"
	project := "fulcrum"
	due := "2026-05-16"
	return taskSummary{
		ID:           "t_42",
		Title:        "Implement task detail view",
		Status:       "TO_DO",
		Priority:     &priority,
		Type:         &worktree,
		ProjectID:    &project,
		Tags:         []string{"plugin", "render"},
		DueDate:      &due,
		Agent:        "claude",
		WorktreePath: &wt,
		CreatedAt:    "2026-05-15T07:00:00Z",
		UpdatedAt:    "2026-05-15T11:48:00Z",
	}
}

func TestRenderTaskDetail_TODO_WithWorktree(t *testing.T) {
	task := sampleTask()
	att, err := renderEnvelopeAt(taskDetailRaw(t, taskGetPayload{
		Task: task,
		Actions: []taskAction{
			{ID: "set_status_in_progress", Label: "Start"},
			{ID: "set_status_canceled", Label: "Cancel", Destructive: true},
			{ID: "start_agent", Label: "Start Agent"},
			{ID: "kill_agent", Label: "Kill Agent", Destructive: true},
			{ID: "view_diff", Label: "View Diff"},
		},
	}), taskDetailClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Color != colorStatusTODO {
		t.Errorf("color: got %q want %q", att.Color, colorStatusTODO)
	}
	wantTitle := ":white_circle: TO_DO :red_circle: H Implement task detail view"
	if att.Title != wantTitle {
		t.Errorf("title:\n got: %q\nwant: %q", att.Title, wantTitle)
	}
	if att.Pretext != "task ID `t_42`" {
		t.Errorf("pretext: %q", att.Pretext)
	}
	if !strings.Contains(att.Text, "/f tasks diff t_42") {
		t.Errorf("text tip missing: %q", att.Text)
	}
	if att.Footer != "fulcrum/tasks.get · status=to_do" {
		t.Errorf("footer: %q", att.Footer)
	}
	// Fields: 9 entries in the §B.3.3 order.
	if len(att.Fields) != 9 {
		t.Fatalf("fields len: got %d want 9", len(att.Fields))
	}
	if att.Fields[0].Title != "Status" || !strings.Contains(fieldStr(t, att.Fields[0]), "TO_DO") {
		t.Errorf("Status field: %+v", att.Fields[0])
	}
	if att.Fields[4].Title != "Tags" || fieldStr(t, att.Fields[4]) != "plugin, render" {
		t.Errorf("Tags field: %+v", att.Fields[4])
	}
	if att.Fields[6].Title != "Worktree" || fieldStr(t, att.Fields[6]) != "`/srv/worktrees/t_42`" {
		t.Errorf("Worktree field: %+v", att.Fields[6])
	}
	if att.Fields[8].Title != "Created / Updated" {
		t.Errorf("Created / Updated field: %+v", att.Fields[8])
	}
	if bool(att.Fields[8].Short) {
		t.Errorf("Created / Updated must be Short=false")
	}
	// Action set: 5 CLI-emitted + 1 plugin-appended Refresh = 6 buttons.
	if len(att.Actions) != 6 {
		t.Fatalf("actions len: got %d want 6", len(att.Actions))
	}
	assertActionArgv(t, att.Actions[0], "set_status_in_progress", postActionStylePrimary, []string{"tasks", "set-status", "t_42", "doing"}, false)
	assertActionArgv(t, att.Actions[1], "set_status_canceled", postActionStyleDanger, []string{"tasks", "set-status", "t_42", "canceled"}, true)
	assertActionArgv(t, att.Actions[2], "start_agent", postActionStylePrimary, []string{"tasks", "start-agent", "t_42"}, false)
	assertActionArgv(t, att.Actions[3], "kill_agent", postActionStyleDanger, []string{"tasks", "kill-agent", "t_42"}, true)
	assertActionArgv(t, att.Actions[4], "view_diff", postActionStyleDefault, []string{"tasks", "diff", "t_42"}, false)
	assertActionArgv(t, att.Actions[5], "task_refresh", postActionStyleDefault, []string{"tasks", "get", "t_42"}, false)
}

func TestRenderTaskDetail_InProgress_NoPriority_NoWorktree(t *testing.T) {
	task := sampleTask()
	task.Status = "IN_PROGRESS"
	task.Priority = nil
	task.Type = nil
	task.WorktreePath = nil
	task.Tags = nil
	att, err := renderEnvelopeAt(taskDetailRaw(t, taskGetPayload{
		Task: task,
		Actions: []taskAction{
			{ID: "set_status_in_review", Label: "Review"},
			{ID: "set_status_done", Label: "Done"},
			{ID: "set_status_canceled", Label: "Cancel", Destructive: true},
		},
	}), taskDetailClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Color != colorStatusDoing {
		t.Errorf("color: %q", att.Color)
	}
	// nil priority must be elided from the Title.
	if strings.Contains(att.Title, "—") {
		t.Errorf("title must not contain dash for nil priority: %q", att.Title)
	}
	if !strings.HasPrefix(att.Title, ":large_blue_circle: IN_PROGRESS Implement") {
		t.Errorf("title: %q", att.Title)
	}
	// Worktree=nil ⇒ no Tip text.
	if att.Text != "" {
		t.Errorf("text should be empty for non-worktree task, got %q", att.Text)
	}
	// Field dashes for the empty fields.
	if fieldStr(t, att.Fields[1]) != "—" {
		t.Errorf("Priority field must dash: %q", fieldStr(t, att.Fields[1]))
	}
	if fieldStr(t, att.Fields[4]) != "—" {
		t.Errorf("Tags field must dash: %q", fieldStr(t, att.Fields[4]))
	}
	if fieldStr(t, att.Fields[6]) != "—" {
		t.Errorf("Worktree field must dash: %q", fieldStr(t, att.Fields[6]))
	}
	// Action set: 3 CLI + 1 Refresh = 4.
	if len(att.Actions) != 4 {
		t.Fatalf("actions len: got %d want 4", len(att.Actions))
	}
	assertActionArgv(t, att.Actions[0], "set_status_in_review", postActionStylePrimary, []string{"tasks", "set-status", "t_42", "review"}, false)
	assertActionArgv(t, att.Actions[1], "set_status_done", postActionStylePrimary, []string{"tasks", "set-status", "t_42", "done"}, false)
	assertActionArgv(t, att.Actions[2], "set_status_canceled", postActionStyleDanger, []string{"tasks", "set-status", "t_42", "canceled"}, true)
	assertActionArgv(t, att.Actions[3], "task_refresh", postActionStyleDefault, []string{"tasks", "get", "t_42"}, false)
}

func TestRenderTaskDetail_Done_CollapsedActions(t *testing.T) {
	task := sampleTask()
	task.Status = "DONE"
	att, err := renderEnvelopeAt(taskDetailRaw(t, taskGetPayload{
		Task: task,
		// CLI emits no state-machine actions for DONE (see taskActions() in
		// fulcrum cli/src/commands/mattermost-verbs.ts); worktreePath is still
		// present so the agent + diff buttons remain.
		Actions: []taskAction{
			{ID: "start_agent", Label: "Start Agent"},
			{ID: "kill_agent", Label: "Kill Agent", Destructive: true},
			{ID: "view_diff", Label: "View Diff"},
		},
	}), taskDetailClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Color != colorStatusDone {
		t.Errorf("color: %q", att.Color)
	}
	// 3 CLI + 1 Refresh.
	if len(att.Actions) != 4 {
		t.Fatalf("actions len: got %d want 4", len(att.Actions))
	}
}

func TestRenderTaskDetail_Canceled_OnlyRefresh(t *testing.T) {
	task := sampleTask()
	task.Status = "CANCELED"
	task.Type = nil
	task.WorktreePath = nil
	att, err := renderEnvelopeAt(taskDetailRaw(t, taskGetPayload{
		Task:    task,
		Actions: []taskAction{}, // CLI emits no actions for CANCELED non-worktree
	}), taskDetailClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Color != colorStatusCanceled {
		t.Errorf("color: %q", att.Color)
	}
	if len(att.Actions) != 1 {
		t.Fatalf("actions len: got %d want 1 (Refresh only)", len(att.Actions))
	}
	assertActionArgv(t, att.Actions[0], "task_refresh", postActionStyleDefault, []string{"tasks", "get", "t_42"}, false)
}

func TestRenderTaskDetail_UnknownActionDropped(t *testing.T) {
	task := sampleTask()
	att, err := renderEnvelopeAt(taskDetailRaw(t, taskGetPayload{
		Task: task,
		Actions: []taskAction{
			{ID: "set_status_in_progress", Label: "Start"},
			{ID: "future_unknown_action", Label: "???"},
		},
	}), taskDetailClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Unknown action.id is dropped; only the known one + Refresh survive.
	if len(att.Actions) != 2 {
		t.Fatalf("actions len: got %d want 2", len(att.Actions))
	}
	if att.Actions[0].Id != "set_status_in_progress" {
		t.Errorf("first action id: %q", att.Actions[0].Id)
	}
	if att.Actions[1].Id != "task_refresh" {
		t.Errorf("second action id: %q", att.Actions[1].Id)
	}
}

func TestRenderTaskDetail_BusinessError_TaskNotFound(t *testing.T) {
	in := []byte(`{
		"success": true,
		"data": {
			"schema_version": 1,
			"verb": "tasks.get",
			"error": { "code": "task_not_found", "message": "task t_999 not found" }
		}
	}`)
	att, err := renderEnvelopeAt(in, taskDetailClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Color != colorError {
		t.Errorf("color: %q", att.Color)
	}
	if !strings.Contains(att.Title, "tasks.get — error") {
		t.Errorf("title: %q", att.Title)
	}
	if !strings.Contains(att.Text, "task_not_found") {
		t.Errorf("body missing code: %q", att.Text)
	}
}

func TestTasksBusinessErrorMessage_KnownCodes(t *testing.T) {
	cases := []struct {
		verb     string
		code     string
		msg      string
		contains string
	}{
		{"tasks.get", "task_not_found", "missing", "search"},
		{"tasks.set-status", "invalid_status_transition", "cannot do that", "invalid_status_transition"},
		{"tasks.start-agent", "worktree_missing", "", "no worktree"},
		{"tasks.start-agent", "agent_already_running", "running on terminal t1", "agent_already_running"},
		{"tasks.kill-agent", "agent_not_running", "", "agent_not_running"},
		{"tasks.diff", "unknown_code", "x", "unknown_code"},
	}
	for _, c := range cases {
		got := tasksBusinessErrorMessage(c.verb, c.code, c.msg)
		if !strings.Contains(got, c.contains) {
			t.Errorf("%s/%s: %q must contain %q", c.verb, c.code, got, c.contains)
		}
	}
}

func TestTaskIDFromArgv(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"tasks", "set-status", "t_1", "doing"}, "t_1"},
		{[]string{"tasks", "set-priority", "t_2", "high"}, "t_2"},
		{[]string{"tasks", "start-agent", "t_3"}, "t_3"},
		{[]string{"tasks", "kill-agent", "t_4"}, "t_4"},
		{[]string{"tasks", "diff", "t_5"}, "t_5"},
		{[]string{"tasks", "get", "t_6"}, "t_6"},
		{[]string{"tasks", "list"}, ""},
		{[]string{"apps", "stop", "a_1"}, ""},
		{[]string{}, ""},
	}
	for _, c := range cases {
		if got := taskIDFromArgv(c.argv); got != c.want {
			t.Errorf("taskIDFromArgv(%v) = %q want %q", c.argv, got, c.want)
		}
	}
}

func fieldStr(t *testing.T, f *model.SlackAttachmentField) string {
	t.Helper()
	s, ok := f.Value.(string)
	if !ok {
		t.Fatalf("field %q value not a string: %#v", f.Title, f.Value)
	}
	return s
}

func assertActionArgv(t *testing.T, act *model.PostAction, id, style string, wantArgv []string, wantDialog bool) {
	t.Helper()
	if act.Id != id {
		t.Errorf("action id: got %q want %q", act.Id, id)
	}
	if act.Style != style {
		t.Errorf("action %s style: got %q want %q", id, act.Style, style)
	}
	if act.Type != model.PostActionTypeButton {
		t.Errorf("action %s type: %q", id, act.Type)
	}
	if act.Integration == nil {
		t.Fatalf("action %s missing Integration", id)
	}
	if act.Integration.URL != "/plugins/"+manifestID+"/action" {
		t.Errorf("action %s url: %q", id, act.Integration.URL)
	}
	raw, ok := act.Integration.Context[actionContextArgvKey].([]any)
	if !ok {
		t.Fatalf("action %s argv missing or wrong type: %#v", id, act.Integration.Context[actionContextArgvKey])
	}
	if len(raw) != len(wantArgv) {
		t.Fatalf("action %s argv len: got %d want %d", id, len(raw), len(wantArgv))
	}
	for i, s := range wantArgv {
		if raw[i] != s {
			t.Errorf("action %s argv[%d]: got %v want %q", id, i, raw[i], s)
		}
	}
	dialogVal, hasDialog := act.Integration.Context[actionContextDialogKey]
	if wantDialog {
		if !hasDialog || dialogVal != true {
			t.Errorf("action %s expected dialog flag, got %#v", id, dialogVal)
		}
	} else if hasDialog {
		t.Errorf("action %s should not carry dialog flag, got %#v", id, dialogVal)
	}
}
