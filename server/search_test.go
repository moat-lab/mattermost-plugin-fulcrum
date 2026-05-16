package main

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// searchClock pins time.Now() for renderEnvelopeAtForRequest callers — the
// search renderer itself doesn't surface relative time, but the entry points
// accept a clock so this stays stable if a future spike revision adds one.
var searchClock = time.Date(2026, 5, 16, 1, 58, 0, 0, time.UTC)

// searchRaw assembles a `fulcrum search --json` envelope around the given
// payload. Keeps the success path; envelope-error variants build their own
// envelopes via searchErrorRaw.
func searchRaw(t *testing.T, p searchPayload) []byte {
	t.Helper()
	body := struct {
		SchemaVersion int            `json:"schema_version"`
		Verb          string         `json:"verb"`
		Query         string         `json:"query"`
		Total         int            `json:"total"`
		Results       []searchResult `json:"results"`
	}{
		SchemaVersion: 1,
		Verb:          "search",
		Query:         p.Query,
		Total:         p.Total,
		Results:       p.Results,
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

// searchErrorRaw assembles a `fulcrum search --json` envelope with only the
// canonical envelope-error object populated. Mirrors what the CLI emits via
// emitError('search', code, message).
func searchErrorRaw(t *testing.T, code, message string) []byte {
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
		Verb:          "search",
		Error: struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{Code: code, Message: message},
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

func TestRenderSearch_EmptyBranch(t *testing.T) {
	raw := searchRaw(t, searchPayload{Query: "nope", Total: 0, Results: nil})
	att, err := renderEnvelopeAtForRequest(raw, searchClock, "", []string{"fulcrum", "search", "nope", "--json"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorStatusTODO {
		t.Errorf("empty color = %q, want %q", att.Color, colorStatusTODO)
	}
	if got, want := att.Title, `Search · "nope" (0)`; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}
	if !strings.Contains(att.Text, `_No matches for "nope"._`) {
		t.Errorf("empty text missing no-match line: %q", att.Text)
	}
	if !strings.Contains(att.Text, "Try `/f search <other> --limit=<higher>`") {
		t.Errorf("empty text missing hint: %q", att.Text)
	}
	if got, want := att.Pretext, "limit=25"; got != want {
		t.Errorf("default limit pretext = %q, want %q", got, want)
	}
	if att.Footer != "fulcrum/search · total=0" {
		t.Errorf("footer = %q", att.Footer)
	}
	if len(att.Actions) != 1 {
		t.Fatalf("empty branch actions = %d, want 1 (Refresh)", len(att.Actions))
	}
	if att.Actions[0].Name != "Refresh" {
		t.Errorf("empty branch action 0 = %q, want Refresh", att.Actions[0].Name)
	}
}

func TestRenderSearch_SingleTypeBranch(t *testing.T) {
	raw := searchRaw(t, searchPayload{
		Query: "login",
		Total: 2,
		Results: []searchResult{
			{EntityType: "task", ID: "t_abc12", Title: "Fix login regression", Snippet: "matched login regression in description", Score: 0.91},
			{EntityType: "task", ID: "t_def34", Title: "Audit OAuth login redirect", Snippet: "login flow review", Score: 0.74},
		},
	})
	att, err := renderEnvelopeAtForRequest(raw, searchClock, "", []string{"fulcrum", "search", "login", "--limit=25", "--json"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorAccent {
		t.Errorf("single-type color = %q, want %q", att.Color, colorAccent)
	}
	if !strings.Contains(att.Text, "### Tasks") {
		t.Errorf("missing Tasks heading: %q", att.Text)
	}
	if strings.Contains(att.Text, "### Apps") {
		t.Errorf("single-type should not include Apps heading: %q", att.Text)
	}
	wantRow := "- **[`t_abc12`]** Fix login regression · _score 0.91_\n  > matched login regression in description"
	if !strings.Contains(att.Text, wantRow) {
		t.Errorf("missing first row line:\n  want %q\n  got  %q", wantRow, att.Text)
	}
	if !strings.Contains(att.Text, "_score 0.74_") {
		t.Errorf("missing second row score: %q", att.Text)
	}
	if att.Footer != "fulcrum/search · total=2" {
		t.Errorf("footer = %q", att.Footer)
	}
}

func TestRenderSearch_MixedBranch_GroupOrderAndButton(t *testing.T) {
	raw := searchRaw(t, searchPayload{
		Query: "web",
		Total: 50,
		Results: []searchResult{
			{EntityType: "project", ID: "p_web", Title: "Webapp rewrite", Snippet: "", Score: 0.55},
			{EntityType: "task", ID: "t_web", Title: "Add web onboarding", Snippet: "tap web flow", Score: 0.80},
			{EntityType: "memory", ID: "m_1", Title: "Web design retro", Snippet: "retro notes", Score: 0.20},
		},
	})
	att, err := renderEnvelopeAtForRequest(raw, searchClock, "", []string{"fulcrum", "search", "web", "--limit=20", "--json"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorAccent {
		t.Errorf("mixed color = %q, want %q", att.Color, colorAccent)
	}
	taskIdx := strings.Index(att.Text, "### Tasks")
	projIdx := strings.Index(att.Text, "### Projects")
	memIdx := strings.Index(att.Text, "### Memories")
	if taskIdx < 0 || projIdx < 0 || memIdx < 0 {
		t.Fatalf("mixed text missing one of Tasks/Projects/Memories: %q", att.Text)
	}
	if !(taskIdx < projIdx && projIdx < memIdx) {
		t.Errorf("group order wrong: tasks=%d projects=%d memories=%d", taskIdx, projIdx, memIdx)
	}
	if got, want := att.Pretext, "limit=20"; got != want {
		t.Errorf("pretext = %q, want %q", got, want)
	}
	// total=50 > limit=20 → Increase limit button appears, doubled to 40.
	if len(att.Actions) != 2 {
		t.Fatalf("mixed actions = %d, want 2 (Refresh + Increase limit)", len(att.Actions))
	}
	if att.Actions[1].Name != "Increase limit" {
		t.Errorf("action 1 name = %q, want Increase limit", att.Actions[1].Name)
	}
	argv := actionArgvSlice(t, att.Actions[1])
	wantArgv := []string{"search", "web", "--limit=40"}
	if !equalStrSlice(argv, wantArgv) {
		t.Errorf("increase limit argv = %v, want %v", argv, wantArgv)
	}
}

func TestRenderSearch_IncreaseLimitSuppressedWhenAtCeiling(t *testing.T) {
	raw := searchRaw(t, searchPayload{
		Query: "foo",
		Total: 300,
		Results: []searchResult{
			{EntityType: "task", ID: "t1", Title: "a", Score: 1.0},
		},
	})
	att, err := renderEnvelopeAtForRequest(raw, searchClock, "", []string{"fulcrum", "search", "foo", "--limit=200", "--json"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(att.Actions) != 1 {
		t.Fatalf("at-ceiling actions = %d, want 1 (Refresh only)", len(att.Actions))
	}
	if att.Actions[0].Name != "Refresh" {
		t.Errorf("at-ceiling action 0 = %q, want Refresh", att.Actions[0].Name)
	}
}

func TestRenderSearch_IncreaseLimitSuppressedWhenTotalUnderLimit(t *testing.T) {
	raw := searchRaw(t, searchPayload{
		Query: "foo",
		Total: 3,
		Results: []searchResult{
			{EntityType: "task", ID: "t1", Title: "a", Score: 1.0},
		},
	})
	att, err := renderEnvelopeAtForRequest(raw, searchClock, "", []string{"fulcrum", "search", "foo", "--limit=25", "--json"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(att.Actions) != 1 {
		t.Errorf("total<limit actions = %d, want 1 (Refresh only)", len(att.Actions))
	}
}

func TestRenderSearch_EnvelopeBusinessError_NonEphemeral(t *testing.T) {
	raw := searchErrorRaw(t, "FETCH_FAILED", "backend timeout")
	att, err := renderEnvelopeAtForRequest(raw, searchClock, "", []string{"fulcrum", "search", "x", "--json"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorError {
		t.Errorf("error color = %q, want %q", att.Color, colorError)
	}
	if !strings.Contains(att.Text, "FETCH_FAILED") {
		t.Errorf("error text missing code: %q", att.Text)
	}
	if !strings.Contains(att.Text, "backend timeout") {
		t.Errorf("error text missing message: %q", att.Text)
	}
}

func TestSearchEphemeralCodes(t *testing.T) {
	for _, code := range []string{"query_too_short", "invalid_limit"} {
		if !searchEphemeralCodes[code] {
			t.Errorf("expected %q in searchEphemeralCodes", code)
		}
	}
	if searchEphemeralCodes["FETCH_FAILED"] {
		t.Error("FETCH_FAILED should NOT be ephemeral (falls to renderBusinessError)")
	}
}

func TestSearchBusinessErrorMessage(t *testing.T) {
	cases := []struct {
		code    string
		message string
		want    string
	}{
		{"query_too_short", "", "search query is too short (need at least 2 characters)"},
		{"query_too_short", "q='a'", "search query is too short (need at least 2 characters) — q='a'"},
		{"invalid_limit", "", "invalid --limit (must be a positive integer)"},
		{"invalid_limit", "got=-5", "invalid --limit (must be a positive integer) — got=-5"},
		{"FETCH_FAILED", "boom", "search: FETCH_FAILED — boom"},
		{"FETCH_FAILED", "", "search: FETCH_FAILED"},
	}
	for _, tc := range cases {
		got := searchBusinessErrorMessage(tc.code, tc.message)
		if got != tc.want {
			t.Errorf("code=%q msg=%q → %q, want %q", tc.code, tc.message, got, tc.want)
		}
	}
}

func TestFormatSearchSnippet(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace-only", "   \t\n", ""},
		{"short", "hi there", "hi there"},
		{"multiline collapse", "first\nsecond\tthird", "first second third"},
		{"truncate over cap", strings.Repeat("a", searchSnippetCap+10), strings.Repeat("a", searchSnippetCap) + "…"},
		{"cjk truncate", strings.Repeat("中", searchSnippetCap+5), strings.Repeat("中", searchSnippetCap) + "…"},
	}
	for _, tc := range cases {
		got := formatSearchSnippet(tc.in)
		if got != tc.want {
			t.Errorf("%s: %q → %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestFormatSearchScore(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.91, "0.91"},
		{0.0, "0.00"},
		{1.234567, "1.23"},
		{math.NaN(), "—"},
	}
	for _, tc := range cases {
		got := formatSearchScore(tc.in)
		if got != tc.want {
			t.Errorf("score %v → %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSearchLimitFromArgv(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want int
	}{
		{"missing", []string{"fulcrum", "search", "q", "--json"}, searchDefaultLimit},
		{"equals form", []string{"fulcrum", "search", "q", "--limit=42", "--json"}, 42},
		{"space form", []string{"fulcrum", "search", "q", "--limit", "60", "--json"}, 60},
		{"unparseable equals", []string{"fulcrum", "search", "q", "--limit=abc", "--json"}, searchDefaultLimit},
		{"unparseable space", []string{"fulcrum", "search", "q", "--limit", "abc", "--json"}, searchDefaultLimit},
		{"trailing --limit no value", []string{"fulcrum", "search", "q", "--limit"}, searchDefaultLimit},
		{"zero treated as default", []string{"fulcrum", "search", "q", "--limit=0", "--json"}, searchDefaultLimit},
	}
	for _, tc := range cases {
		got := searchLimitFromArgv(tc.argv)
		if got != tc.want {
			t.Errorf("%s: %v → %d, want %d", tc.name, tc.argv, got, tc.want)
		}
	}
}

func TestNextSearchLimit(t *testing.T) {
	cases := []struct {
		current int
		want    int
	}{
		{20, 40},
		{25, 50},
		{100, 200},
		{150, 200}, // capped
		{200, 0},   // already at ceiling
		{250, 0},   // above ceiling
		{0, 50},    // 0 → default(25) → doubled
		{-5, 50},   // negative → default(25) → doubled
	}
	for _, tc := range cases {
		got := nextSearchLimit(tc.current)
		if got != tc.want {
			t.Errorf("current=%d → %d, want %d", tc.current, got, tc.want)
		}
	}
}

func TestSearchArgv_ReconstructsLimit(t *testing.T) {
	got := searchArgv("hello world", 50)
	want := []string{"search", "hello world", "--limit=50"}
	if !equalStrSlice(got, want) {
		t.Errorf("argv = %v, want %v", got, want)
	}
}

func TestSearchActions_RefreshArgvUsesCurrentLimit(t *testing.T) {
	actions := searchActions("q", 20, 10)
	if len(actions) != 1 {
		t.Fatalf("actions = %d, want 1", len(actions))
	}
	argv := actionArgvSliceFromAction(t, actions[0])
	want := []string{"search", "q", "--limit=20"}
	if !equalStrSlice(argv, want) {
		t.Errorf("refresh argv = %v, want %v", argv, want)
	}
}

func TestRenderSearch_GroupOrderEdgeCase_AppsBucketTolerated(t *testing.T) {
	// §B.9.3 lists `app` in the group order even though the current CLI schema
	// does not emit entityType=app. The renderer must still place an apps-
	// group between tasks and projects if the CLI ever starts emitting them.
	raw := searchRaw(t, searchPayload{
		Query: "x",
		Total: 3,
		Results: []searchResult{
			{EntityType: "project", ID: "p1", Title: "proj", Score: 0.5},
			{EntityType: "task", ID: "t1", Title: "task", Score: 0.9},
			{EntityType: "app", ID: "a1", Title: "app", Score: 0.7},
		},
	})
	att, err := renderEnvelopeAtForRequest(raw, searchClock, "", []string{"fulcrum", "search", "x", "--json"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	taskIdx := strings.Index(att.Text, "### Tasks")
	appIdx := strings.Index(att.Text, "### Apps")
	projIdx := strings.Index(att.Text, "### Projects")
	if taskIdx < 0 || appIdx < 0 || projIdx < 0 {
		t.Fatalf("missing heading: %q", att.Text)
	}
	if !(taskIdx < appIdx && appIdx < projIdx) {
		t.Errorf("apps not between tasks and projects: tasks=%d apps=%d projects=%d", taskIdx, appIdx, projIdx)
	}
}

func TestRenderSearch_UnknownEntityRendersAtTail(t *testing.T) {
	raw := searchRaw(t, searchPayload{
		Query: "x",
		Total: 2,
		Results: []searchResult{
			{EntityType: "task", ID: "t1", Title: "task", Score: 0.9},
			{EntityType: "widget", ID: "w1", Title: "widget", Score: 0.4},
		},
	})
	att, err := renderEnvelopeAtForRequest(raw, searchClock, "", []string{"fulcrum", "search", "x", "--json"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	taskIdx := strings.Index(att.Text, "### Tasks")
	widgetIdx := strings.Index(att.Text, "### Widgets")
	if taskIdx < 0 || widgetIdx < 0 {
		t.Fatalf("missing heading: %q", att.Text)
	}
	if widgetIdx < taskIdx {
		t.Errorf("unknown entity should render after tasks: tasks=%d widgets=%d", taskIdx, widgetIdx)
	}
}

func TestRenderSearch_SnippetOmitted_NoBlockquote(t *testing.T) {
	raw := searchRaw(t, searchPayload{
		Query: "x",
		Total: 1,
		Results: []searchResult{
			{EntityType: "task", ID: "t1", Title: "no snippet task", Snippet: "", Score: 0.5},
		},
	})
	att, err := renderEnvelopeAtForRequest(raw, searchClock, "", []string{"fulcrum", "search", "x", "--json"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(att.Text, "  >") {
		t.Errorf("expected no blockquote when snippet empty, got: %q", att.Text)
	}
	if !strings.Contains(att.Text, "- **[`t1`]** no snippet task · _score 0.50_") {
		t.Errorf("missing row: %q", att.Text)
	}
}

// actionArgvSlice extracts the argv array from a PostAction's
// Integration.Context["argv"]. The context stores argv as []any of strings
// (see makeAction); this helper unwraps the type assertion chain so tests can
// compare against a flat []string.
func actionArgvSlice(t *testing.T, action *model.PostAction) []string {
	t.Helper()
	return actionArgvSliceFromAction(t, action)
}

func actionArgvSliceFromAction(t *testing.T, action *model.PostAction) []string {
	t.Helper()
	if action.Integration == nil {
		t.Fatal("action has no Integration")
	}
	raw, ok := action.Integration.Context[actionContextArgvKey]
	if !ok {
		t.Fatalf("action context missing %q", actionContextArgvKey)
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("action context %q is not []any (got %T)", actionContextArgvKey, raw)
	}
	out := make([]string, 0, len(list))
	for i, v := range list {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("action context %q[%d] is not string (got %T)", actionContextArgvKey, i, v)
		}
		out = append(out, s)
	}
	return out
}

func equalStrSlice(a, b []string) bool {
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
