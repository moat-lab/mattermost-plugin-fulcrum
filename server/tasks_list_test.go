package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// tasksListClock pins every today-tasks-panel test so the Due column's
// relative-time output is stable across runs.
var tasksListClock = time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

// tasksListRaw assembles a `fulcrum tasks list --json` envelope from a payload
// struct so schema drift surfaces as a compile failure here rather than in
// renderTasksList.
func tasksListRaw(t *testing.T, p tasksListPayload) []byte {
	t.Helper()
	body := struct {
		tasksListPayload
		SchemaVersion int    `json:"schema_version"`
		Verb          string `json:"verb"`
	}{
		tasksListPayload: p,
		SchemaVersion:    1,
		Verb:             "tasks.list",
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

func mkTask(id, title, status, priority, due string) taskSummary {
	t := taskSummary{ID: id, Title: title, Status: status}
	if priority != "" {
		p := priority
		t.Priority = &p
	}
	if due != "" {
		d := due
		t.DueDate = &d
	}
	return t
}

// activeFilter is the default filter the CLI returns for `tasks list` with no
// args — the "today" branch.
func activeFilter(page, totalPages, pageSize int) tasksListFilter {
	return tasksListFilter{Status: "active", Page: page, TotalPages: totalPages, PageSize: pageSize}
}

func TestTasksList_Empty_NoFilter(t *testing.T) {
	att, err := renderEnvelopeAt(tasksListRaw(t, tasksListPayload{
		Filter: activeFilter(1, 1, 20),
		Total:  0,
		Tasks:  nil,
	}), tasksListClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorAccent {
		t.Errorf("color: got %q want %q", att.Color, colorAccent)
	}
	if att.Title != "Tasks · today (0)" {
		t.Errorf("title: %q", att.Title)
	}
	if att.Pretext != "" {
		t.Errorf("empty single-page must have no Pretext, got %q", att.Pretext)
	}
	if !strings.Contains(att.Text, "_No active tasks.") {
		t.Errorf("empty text missing: %q", att.Text)
	}
	if !strings.Contains(att.Text, "/f tasks create --title=") {
		t.Errorf("empty text missing create hint: %q", att.Text)
	}
	names := actionNames(att.Actions)
	// Empty + no filter: Refresh + Create task (no Clear filter, no Prev/Next).
	if !equalStringSlice(names, []string{"Refresh", "Create task"}) {
		t.Errorf("empty actions: %v want [Refresh Create task]", names)
	}
	if att.Footer != "fulcrum/tasks.list · page=1/1 · page_size=20" {
		t.Errorf("footer: %q", att.Footer)
	}
}

func TestTasksList_FilteredEmpty(t *testing.T) {
	// status=todo + priority=high + total=0 → filtered-empty branch.
	att, err := renderEnvelopeAt(tasksListRaw(t, tasksListPayload{
		Filter: tasksListFilter{Status: "todo", Priority: strPtr("high"), Page: 1, TotalPages: 1, PageSize: 20},
		Total:  0,
	}), tasksListClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Title != "Tasks · to-do · high pri (0)" {
		t.Errorf("title: %q", att.Title)
	}
	if !strings.Contains(att.Text, "_No tasks match filter `to-do · high pri`") {
		t.Errorf("filtered-empty text: %q", att.Text)
	}
	names := actionNames(att.Actions)
	if !equalStringSlice(names, []string{"Refresh", "Clear filter", "Create task"}) {
		t.Errorf("filtered-empty actions: %v want [Refresh Clear filter Create task]", names)
	}

	got := argvForActionByName(t, att.Actions, "Clear filter")
	if !equalStringSlice(got, []string{"tasks", "list", "--status=active"}) {
		t.Errorf("Clear filter argv: %v", got)
	}
}

func TestTasksList_SinglePage(t *testing.T) {
	tasks := []taskSummary{
		mkTask("t_abc12", "Fix login regression", "IN_PROGRESS", "high", ""),
		mkTask("t_abc34", "Write release notes", "TO_DO", "medium", "2026-05-16"),
	}
	att, err := renderEnvelopeAt(tasksListRaw(t, tasksListPayload{
		Filter: activeFilter(1, 1, 20),
		Total:  2,
		Tasks:  tasks,
	}), tasksListClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Title != "Tasks · today (2)" {
		t.Errorf("title: %q", att.Title)
	}
	if att.Pretext != "" {
		t.Errorf("single-page must not show pagination Pretext, got %q", att.Pretext)
	}
	// Table header presence + each task ID rendered as code-span.
	if !strings.Contains(att.Text, "| Pri | Status | Title | ID | Due |") {
		t.Errorf("table header missing: %q", att.Text)
	}
	for _, want := range []string{"`t_abc12`", "`t_abc34`", "Fix login regression", "Write release notes"} {
		if !strings.Contains(att.Text, want) {
			t.Errorf("text missing %q: %q", want, att.Text)
		}
	}
	if !strings.Contains(att.Text, ":large_blue_circle: IN_PROGRESS") {
		t.Errorf("status chip not rendered: %q", att.Text)
	}
	if !strings.Contains(att.Text, "| H |") {
		t.Errorf("priority letter not rendered: %q", att.Text)
	}
	// Single page → only Refresh; no Prev/Next/Clear/Create.
	names := actionNames(att.Actions)
	if !equalStringSlice(names, []string{"Refresh"}) {
		t.Errorf("single-page actions: %v", names)
	}
}

func TestTasksList_Paginated_FirstPage(t *testing.T) {
	tasks := make([]taskSummary, 0, 20)
	for i := 0; i < 20; i++ {
		tasks = append(tasks, mkTask("t_p1_"+string(rune('a'+i)), "Task", "TO_DO", "low", ""))
	}
	att, err := renderEnvelopeAt(tasksListRaw(t, tasksListPayload{
		Filter: activeFilter(1, 3, 20),
		Total:  47,
		Tasks:  tasks,
	}), tasksListClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Pretext != "page 1/3" {
		t.Errorf("pretext: %q", att.Pretext)
	}
	if att.Title != "Tasks · today (47)" {
		t.Errorf("title: %q", att.Title)
	}
	if att.Footer != "fulcrum/tasks.list · page=1/3 · page_size=20" {
		t.Errorf("footer: %q", att.Footer)
	}
	// First page: no Prev; Next present.
	names := actionNames(att.Actions)
	if !equalStringSlice(names, []string{"Refresh", "Next"}) {
		t.Errorf("first-page actions: %v want [Refresh Next]", names)
	}
}

func TestTasksList_Paginated_MidPage_PrevNextArgv(t *testing.T) {
	att, err := renderEnvelopeAt(tasksListRaw(t, tasksListPayload{
		Filter: activeFilter(2, 3, 20),
		Total:  47,
		Tasks:  []taskSummary{mkTask("t_x", "Task", "TO_DO", "", "")},
	}), tasksListClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Pretext != "page 2/3" {
		t.Errorf("pretext: %q", att.Pretext)
	}
	names := actionNames(att.Actions)
	if !equalStringSlice(names, []string{"Refresh", "Prev", "Next"}) {
		t.Errorf("mid-page actions: %v want [Refresh Prev Next]", names)
	}

	// Verify the Prev / Next / Refresh argv.
	want := map[string][]string{
		"Refresh": {"tasks", "list", "--status=active", "--page=2"},
		"Prev":    {"tasks", "list", "--status=active"}, // page 1 omitted
		"Next":    {"tasks", "list", "--status=active", "--page=3"},
	}
	for label, expected := range want {
		got := argvForActionByName(t, att.Actions, label)
		if !equalStringSlice(got, expected) {
			t.Errorf("%s argv: got %v want %v", label, got, expected)
		}
	}
}

func TestTasksList_Paginated_LastPage_NoNext(t *testing.T) {
	att, err := renderEnvelopeAt(tasksListRaw(t, tasksListPayload{
		Filter: activeFilter(3, 3, 20),
		Total:  47,
		Tasks:  []taskSummary{mkTask("t_x", "Task", "TO_DO", "", "")},
	}), tasksListClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	names := actionNames(att.Actions)
	if !equalStringSlice(names, []string{"Refresh", "Prev"}) {
		t.Errorf("last-page actions: %v want [Refresh Prev]", names)
	}
}

func TestTasksList_FilterSummary_All(t *testing.T) {
	// All four filter dimensions populated — order in the title must be
	// status → priority → project → tag per spike §B.2.3.
	att, err := renderEnvelopeAt(tasksListRaw(t, tasksListPayload{
		Filter: tasksListFilter{
			Status:    "TO_DO",
			Priority:  strPtr("high"),
			ProjectID: strPtr("fulcrum"),
			Tag:       strPtr("p0"),
			Page:      1, TotalPages: 1, PageSize: 20,
		},
		Total: 0,
	}), tasksListClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "Tasks · to-do · high pri · #fulcrum · :label: p0 (0)"
	if att.Title != want {
		t.Errorf("title:\n got %q\nwant %q", att.Title, want)
	}
}

func TestTasksList_Argv_PreservesNonStatusFilters(t *testing.T) {
	att, err := renderEnvelopeAt(tasksListRaw(t, tasksListPayload{
		Filter: tasksListFilter{
			Status:   "doing",
			Priority: strPtr("medium"),
			Tag:      strPtr("backend"),
			Page:     1, TotalPages: 2, PageSize: 20,
		},
		Total: 25,
		Tasks: []taskSummary{mkTask("t_x", "T", "IN_PROGRESS", "medium", "")},
	}), tasksListClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Next argv must thread all non-default filters + the new page.
	got := argvForActionByName(t, att.Actions, "Next")
	want := []string{"tasks", "list", "--status=doing", "--priority=medium", "--tag=backend", "--page=2"}
	if !equalStringSlice(got, want) {
		t.Errorf("Next argv:\n got %v\nwant %v", got, want)
	}
}

func TestTasksList_TableRowCap(t *testing.T) {
	tasks := make([]taskSummary, 0, 25)
	for i := 0; i < 25; i++ {
		tasks = append(tasks, mkTask("t_x"+string(rune('A'+i%26)), "Task", "TO_DO", "", ""))
	}
	att, err := renderEnvelopeAt(tasksListRaw(t, tasksListPayload{
		Filter: activeFilter(1, 2, 20),
		Total:  25,
		Tasks:  tasks,
	}), tasksListClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	dataRows := strings.Count(att.Text, "\n| ")
	if dataRows != 20 {
		t.Errorf("rendered %d data rows, want 20", dataRows)
	}
}

func TestTasksList_TitleTruncation(t *testing.T) {
	longTitle := strings.Repeat("ABCDEFGHIJ", 10) // 100 chars > 60 cap
	att, err := renderEnvelopeAt(tasksListRaw(t, tasksListPayload{
		Filter: activeFilter(1, 1, 20),
		Total:  1,
		Tasks:  []taskSummary{mkTask("t_long", longTitle, "TO_DO", "", "")},
	}), tasksListClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(att.Text, strings.Repeat("ABCDEFGHIJ", 6)+"…") {
		t.Errorf("title not truncated to 60 + ellipsis: %q", att.Text)
	}
}

func TestTasksList_TitlePipeEscape(t *testing.T) {
	// A pipe in a title would otherwise break the markdown table layout.
	att, err := renderEnvelopeAt(tasksListRaw(t, tasksListPayload{
		Filter: activeFilter(1, 1, 20),
		Total:  1,
		Tasks:  []taskSummary{mkTask("t_pipe", "feat: handle a|b cases", "TO_DO", "", "")},
	}), tasksListClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(att.Text, "a|b") {
		t.Errorf("pipe not escaped: %q", att.Text)
	}
	if !strings.Contains(att.Text, "a&#124;b") {
		t.Errorf("HTML-entity-escaped pipe missing: %q", att.Text)
	}
}

func TestTasksList_NullableCells(t *testing.T) {
	// priority=nil + dueDate=nil should both render as "—" so column width
	// stays consistent. The taskSummary helper leaves these nil when args are
	// "".
	att, err := renderEnvelopeAt(tasksListRaw(t, tasksListPayload{
		Filter: activeFilter(1, 1, 20),
		Total:  1,
		Tasks:  []taskSummary{mkTask("t_x", "Untitled", "TO_DO", "", "")},
	}), tasksListClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(att.Text, "| — | :white_circle: TO_DO | Untitled | `t_x` | — |") {
		t.Errorf("nullable cells not dashed: %q", att.Text)
	}
}

func TestTasksList_DueRelative(t *testing.T) {
	// dueDate "2026-05-16" is one day after the pinned clock (2026-05-15 12:00 UTC).
	// parseLooseISO anchors date-only at UTC midnight, so the delta is 12h → "in 12h".
	att, err := renderEnvelopeAt(tasksListRaw(t, tasksListPayload{
		Filter: activeFilter(1, 1, 20),
		Total:  1,
		Tasks:  []taskSummary{mkTask("t_due", "Due tomorrow", "TO_DO", "", "2026-05-16")},
	}), tasksListClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(att.Text, "in 12h") {
		t.Errorf("relative-due not rendered: %q", att.Text)
	}
}

func TestTasksList_BusinessError_UnknownProject(t *testing.T) {
	// envelope.error.code → §0.5 generic business-error form (no apps.list-style
	// per-verb override). The renderer reports the verb in the title and keeps
	// no actions (tasks.list has none in renderBusinessError's switch — that
	// matches spike §B.2.5: error states do not preserve Clear filter on the
	// generic error path because the user might want to re-issue the slash
	// rather than poke the bot card; the card is informational).
	raw := []byte(`{"success":true,"data":{"schema_version":1,"verb":"tasks.list","error":{"code":"unknown_project","message":"project not found"}}}`)
	att, err := renderEnvelopeAt(raw, tasksListClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorError {
		t.Errorf("color: got %q want %q", att.Color, colorError)
	}
	if !strings.Contains(att.Title, "tasks.list — error") {
		t.Errorf("title: %q", att.Title)
	}
	if !strings.Contains(att.Text, "unknown_project") || !strings.Contains(att.Text, "project not found") {
		t.Errorf("text: %q", att.Text)
	}
}

func TestTasksList_RefreshActionWires(t *testing.T) {
	att, err := renderEnvelopeAt(tasksListRaw(t, tasksListPayload{
		Filter: activeFilter(1, 1, 20),
		Total:  1,
		Tasks:  []taskSummary{mkTask("t_x", "T", "TO_DO", "", "")},
	}), tasksListClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(att.Actions) != 1 {
		t.Fatalf("single-page actions: got %d want 1", len(att.Actions))
	}
	a := att.Actions[0]
	if a.Id != "tasks_list_refresh" {
		t.Errorf("action id: %q", a.Id)
	}
	if a.Integration.URL != "/plugins/"+manifestID+"/action" {
		t.Errorf("action url: %q", a.Integration.URL)
	}
	if _, hasDialog := a.Integration.Context[actionContextDialogKey]; hasDialog {
		t.Errorf("Refresh must not carry dialog flag")
	}
}

// argvForActionByName looks up a *model.PostAction by Name and returns the
// decoded argv from its Integration.Context. Used by paginated-button tests
// so each assertion stays one-liner-readable.
func argvForActionByName(t *testing.T, actions []*model.PostAction, name string) []string {
	t.Helper()
	for _, a := range actions {
		if a.Name == name {
			ctx, ok := a.Integration.Context[actionContextArgvKey].([]any)
			if !ok {
				t.Fatalf("%s argv context missing or wrong type: %#v", name, a.Integration.Context)
			}
			out := make([]string, len(ctx))
			for i, v := range ctx {
				s, ok := v.(string)
				if !ok {
					t.Fatalf("%s argv[%d] not a string: %#v", name, i, v)
				}
				out[i] = s
			}
			return out
		}
	}
	t.Fatalf("action %q not found among %v", name, actionNames(actions))
	return nil
}
