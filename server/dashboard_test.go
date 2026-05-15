package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// dashboardClock is a fixed wall clock used by every dashboard test so the
// Pretext relative-time block is deterministic.
var dashboardClock = time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

// dashboardRaw assembles the CLI envelope a real `fulcrum dashboard --json`
// call would produce. Tests use it instead of hand-writing JSON each time so
// schema drift surfaces as compile failures here rather than in dashboard.go.
func dashboardRaw(t *testing.T, payload dashboardPayload) []byte {
	t.Helper()
	body := struct {
		dashboardPayload
		SchemaVersion int    `json:"schema_version"`
		Verb          string `json:"verb"`
	}{
		dashboardPayload: payload,
		SchemaVersion:    1,
		Verb:             "dashboard",
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

func fieldValueString(t *testing.T, f *model.SlackAttachmentField) string {
	t.Helper()
	s, ok := f.Value.(string)
	if !ok {
		t.Fatalf("field %q value not a string: %#v", f.Title, f.Value)
	}
	return s
}

func actionNames(actions []*model.PostAction) []string {
	out := make([]string, len(actions))
	for i, a := range actions {
		out[i] = a.Name
	}
	return out
}

func sliceContains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDashboard_EmptyState(t *testing.T) {
	att, err := renderEnvelopeAt(dashboardRaw(t, dashboardPayload{}), dashboardClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorAccent {
		t.Errorf("color = %q, want %q", att.Color, colorAccent)
	}
	if att.Title != "Fulcrum dashboard · 0 active · 0 apps" {
		t.Errorf("title = %q", att.Title)
	}
	if !strings.Contains(att.Pretext, ":sparkles:") || !strings.Contains(att.Pretext, "2026-05-15 12:00 UTC") {
		t.Errorf("pretext = %q", att.Pretext)
	}
	if len(att.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(att.Fields))
	}
	taskField := fieldValueString(t, att.Fields[0])
	appField := fieldValueString(t, att.Fields[1])
	if !strings.Contains(taskField, "_no tasks tracked yet_") {
		t.Errorf("task field = %q", taskField)
	}
	if !strings.Contains(appField, "_no apps tracked yet_") {
		t.Errorf("apps field = %q", appField)
	}
	if att.Text != "" {
		t.Errorf("text should be empty in empty state, got %q", att.Text)
	}
	names := actionNames(att.Actions)
	if !equalStringSlice(names, []string{"Refresh", "Help"}) {
		t.Errorf("empty-state actions = %v, want [Refresh Help]", names)
	}
	if att.Footer != "fulcrum/dashboard · schema_version=1" {
		t.Errorf("footer = %q", att.Footer)
	}
}

func TestDashboard_TasksOnly(t *testing.T) {
	att, err := renderEnvelopeAt(dashboardRaw(t, dashboardPayload{
		TasksByStatus: map[string]int{"TO_DO": 3, "IN_PROGRESS": 2},
		ActiveTasks:   5,
	}), dashboardClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(att.Title, "5 active · 0 apps") {
		t.Errorf("title = %q", att.Title)
	}
	taskField := fieldValueString(t, att.Fields[0])
	appField := fieldValueString(t, att.Fields[1])
	if !strings.Contains(taskField, "TO_DO") || !strings.Contains(taskField, "×3") {
		t.Errorf("tasks bucket = %q", taskField)
	}
	if !strings.Contains(appField, "_no apps tracked yet_") {
		t.Errorf("apps bucket = %q", appField)
	}
	names := actionNames(att.Actions)
	if !sliceContains(names, "View today's tasks") {
		t.Errorf("expected View today's tasks button, got %v", names)
	}
	if sliceContains(names, "View apps") {
		t.Errorf("View apps must be hidden when total_apps == 0, got %v", names)
	}
}

func TestDashboard_AppsOnly(t *testing.T) {
	att, err := renderEnvelopeAt(dashboardRaw(t, dashboardPayload{
		AppsByStatus: map[string]int{"running": 4, "failed": 1},
		TotalApps:    5,
	}), dashboardClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(att.Title, "0 active · 5 apps") {
		t.Errorf("title = %q", att.Title)
	}
	taskField := fieldValueString(t, att.Fields[0])
	appField := fieldValueString(t, att.Fields[1])
	if !strings.Contains(appField, "running") || !strings.Contains(appField, "failed") {
		t.Errorf("apps bucket missing rows: %q", appField)
	}
	if !strings.Contains(taskField, "_no tasks tracked yet_") {
		t.Errorf("tasks bucket = %q", taskField)
	}
	names := actionNames(att.Actions)
	if !sliceContains(names, "View apps") {
		t.Errorf("expected View apps button, got %v", names)
	}
	if sliceContains(names, "View today's tasks") {
		t.Errorf("View today's tasks must be hidden when active_tasks == 0, got %v", names)
	}
}

func TestDashboard_FullStateWithDueToday(t *testing.T) {
	high := "high"
	due := []taskSummary{
		{ID: "t_1", Title: "Fix login regression", Status: "IN_PROGRESS", Priority: &high},
		{ID: "t_2", Title: "Write release notes", Status: "TO_DO"},
		{ID: "t_3", Title: "Refactor auth", Status: "IN_REVIEW"},
		{ID: "t_4", Title: "Ship feature flag", Status: "TO_DO"},
		{ID: "t_5", Title: "Patch CI", Status: "DONE"},
		{ID: "t_6", Title: "Overflow item one", Status: "TO_DO"},
		{ID: "t_7", Title: "Overflow item two", Status: "TO_DO"},
	}
	att, err := renderEnvelopeAt(dashboardRaw(t, dashboardPayload{
		TasksByStatus: map[string]int{"TO_DO": 3, "IN_PROGRESS": 2, "IN_REVIEW": 1, "DONE": 12, "CANCELED": 1},
		ActiveTasks:   6,
		AppsByStatus:  map[string]int{"running": 4, "failed": 1},
		TotalApps:     5,
		DueToday:      due,
	}), dashboardClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(att.Text, "**Due today** (7):") {
		t.Errorf("due today header missing: %q", att.Text)
	}
	if !strings.Contains(att.Text, "Fix login regression") {
		t.Errorf("first due today item missing: %q", att.Text)
	}
	if !strings.Contains(att.Text, "…and 2 more") {
		t.Errorf("overflow line missing for 7 items: %q", att.Text)
	}
	if strings.Contains(att.Text, "Overflow item two") {
		t.Errorf("overflow item should not render in body: %q", att.Text)
	}
	names := actionNames(att.Actions)
	wantAll := []string{"Refresh", "View today's tasks", "View apps", "Help"}
	for _, w := range wantAll {
		if !sliceContains(names, w) {
			t.Errorf("full-state actions missing %q (got %v)", w, names)
		}
	}
	taskField := fieldValueString(t, att.Fields[0])
	if !strings.Contains(taskField, "DONE") || !strings.Contains(taskField, "×12") {
		t.Errorf("DONE bucket count missing: %q", taskField)
	}
}

func TestDashboard_BusinessError(t *testing.T) {
	// envelope.error.code path → §0.5 colorError attachment with Refresh
	// button. Verb-specific override in renderBusinessError adds the button.
	raw := []byte(`{"success":true,"data":{"schema_version":1,"verb":"dashboard","error":{"code":"db_unavailable","message":"upstream postgres unreachable"}}}`)
	att, err := renderEnvelopeAt(raw, dashboardClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorError {
		t.Errorf("color = %q, want %q", att.Color, colorError)
	}
	if !strings.Contains(att.Title, "dashboard — error") {
		t.Errorf("title = %q", att.Title)
	}
	if !strings.Contains(att.Text, "db_unavailable") || !strings.Contains(att.Text, "upstream postgres") {
		t.Errorf("text = %q", att.Text)
	}
	names := actionNames(att.Actions)
	if !sliceContains(names, "Refresh") {
		t.Errorf("error card should keep Refresh button, got %v", names)
	}
}

func TestDashboard_ActionArgvWiresThroughIntegrationContext(t *testing.T) {
	att, err := renderEnvelopeAt(dashboardRaw(t, dashboardPayload{ActiveTasks: 1, TotalApps: 1}), dashboardClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	got := map[string][]string{}
	for _, a := range att.Actions {
		ctx, ok := a.Integration.Context[actionContextArgvKey].([]any)
		if !ok {
			t.Fatalf("action %q missing argv context: %#v", a.Name, a.Integration.Context)
		}
		argv := make([]string, len(ctx))
		for i, v := range ctx {
			argv[i] = v.(string)
		}
		got[a.Name] = argv
		if a.Integration.URL != "/plugins/fulcrum/action" {
			t.Errorf("action %q url = %q", a.Name, a.Integration.URL)
		}
	}
	wantArgv := map[string][]string{
		"Refresh":            {"dashboard"},
		"View today's tasks": {"tasks", "list", "--status=active"},
		"View apps":          {"apps", "list"},
		"Help":               {"help"},
	}
	for name, want := range wantArgv {
		gotArgv, ok := got[name]
		if !ok {
			t.Errorf("action %q missing", name)
			continue
		}
		if !equalStringSlice(gotArgv, want) {
			t.Errorf("action %q argv = %v, want %v", name, gotArgv, want)
		}
	}
}
