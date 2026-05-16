package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// taskDiffClock is the wall clock used by every task-diff test. The
// task-diff card does not surface any relative-time fields, but the renderer
// is called via renderEnvelopeAt* (which takes `now`), so a deterministic
// clock keeps test assertions stable if a future spike revision adds a
// time-derived cell.
var taskDiffClock = time.Date(2026, 5, 15, 13, 50, 0, 0, time.UTC)

// taskDiffRaw assembles a `fulcrum tasks diff --json` envelope from a payload.
// The CLI emits the entire payload as `data`; the envelope-error object is
// left null because success cards never carry one (cli/JSON_SCHEMA.md
// §tasks.diff).
func taskDiffRaw(t *testing.T, p taskDiffPayload) []byte {
	t.Helper()
	body := struct {
		SchemaVersion int             `json:"schema_version"`
		Verb          string          `json:"verb"`
		TaskID        string          `json:"task_id"`
		Branch        *string         `json:"branch"`
		BaseBranch    *string         `json:"base_branch"`
		Diff          *string         `json:"diff"`
		Summary       taskDiffSummary `json:"summary"`
	}{
		SchemaVersion: 1,
		Verb:          "tasks.diff",
		TaskID:        p.TaskID,
		Branch:        p.Branch,
		BaseBranch:    p.BaseBranch,
		Diff:          p.Diff,
		Summary:       p.Summary,
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

// taskDiffErrorRaw assembles a `fulcrum tasks diff --json` envelope with only
// the canonical envelope-error object populated. Mirrors the §0.5 error
// shape the CLI emits via emitError().
func taskDiffErrorRaw(t *testing.T, taskID, code, message string) []byte {
	t.Helper()
	body := struct {
		SchemaVersion int    `json:"schema_version"`
		Verb          string `json:"verb"`
		TaskID        string `json:"task_id"`
		Error         struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{
		SchemaVersion: 1,
		Verb:          "tasks.diff",
		TaskID:        taskID,
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

// TestRenderTaskDiff_NoWorktree exercises the §B.5 no-worktree branch
// (diff=null). Locks color=colorWarn, no Pretext, four-cell field grid with
// Branch="—", spike-literal Text body, and the no-worktree footer marker.
func TestRenderTaskDiff_NoWorktree(t *testing.T) {
	payload := taskDiffPayload{
		TaskID:     "t_42",
		Branch:     nil,
		BaseBranch: nil,
		Diff:       nil,
		Summary:    taskDiffSummary{},
	}
	att, err := renderEnvelopeAt(taskDiffRaw(t, payload), taskDiffClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Color != colorWarn {
		t.Errorf("color: got %q want %q", att.Color, colorWarn)
	}
	if att.Title != "Diff · task `t_42`" {
		t.Errorf("title: %q", att.Title)
	}
	if att.Pretext != "" {
		t.Errorf("pretext must collapse when refs missing: %q", att.Pretext)
	}
	if att.Footer != "fulcrum/tasks.diff · no-worktree" {
		t.Errorf("footer: %q", att.Footer)
	}
	if att.Text != "_Task has no worktree. Nothing to diff._" {
		t.Errorf("text: %q", att.Text)
	}
	if len(att.Fields) != 4 {
		t.Fatalf("fields len: got %d want 4", len(att.Fields))
	}
	if v := fieldStr(t, att.Fields[3]); v != "—" {
		t.Errorf("Branch field must dash when refs nil: %q", v)
	}
	if v := fieldStr(t, att.Fields[1]); v != "+0" {
		t.Errorf("Insertions: %q", v)
	}
	if v := fieldStr(t, att.Fields[2]); v != "-0" {
		t.Errorf("Deletions: %q", v)
	}
}

// TestRenderTaskDiff_Clean covers the fileCount=0 branch: working tree clean,
// summary present but empty. Locks colorStatusTODO, the spike-literal text
// line, and the files=0 footer.
func TestRenderTaskDiff_Clean(t *testing.T) {
	payload := taskDiffPayload{
		TaskID:     "t_42",
		Branch:     strPtr("feature/x"),
		BaseBranch: strPtr("main"),
		Diff:       strPtr(""),
		Summary:    taskDiffSummary{FileCount: 0},
	}
	att, err := renderEnvelopeAt(taskDiffRaw(t, payload), taskDiffClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Color != colorStatusTODO {
		t.Errorf("color: got %q want %q", att.Color, colorStatusTODO)
	}
	if att.Pretext != "main...feature/x" {
		t.Errorf("pretext: %q", att.Pretext)
	}
	if att.Footer != "fulcrum/tasks.diff · files=0" {
		t.Errorf("footer: %q", att.Footer)
	}
	if att.Text != "_Working tree clean. No file changes._" {
		t.Errorf("text: %q", att.Text)
	}
	if v := fieldStr(t, att.Fields[3]); v != "`feature/x` ← `main`" {
		t.Errorf("Branch field: %q", v)
	}
}

// TestRenderTaskDiff_Small exercises the happy-path body: fileCount ≤ 5 AND
// len(diff) ≤ 6000. Locks colorStatusReview, the full markdown file table,
// the fenced ```diff block, and the +Ins/-Del cell signs.
func TestRenderTaskDiff_Small(t *testing.T) {
	diff := "diff --git a/server/render.go b/server/render.go\n@@\n-old\n+new\n"
	payload := taskDiffPayload{
		TaskID:     "t_42",
		Branch:     strPtr("feature/x"),
		BaseBranch: strPtr("main"),
		Diff:       &diff,
		Summary: taskDiffSummary{
			FileCount:  2,
			Insertions: 42,
			Deletions:  3,
			Files: []taskDiffFile{
				{Path: "server/render.go", Insertions: 12, Deletions: 3},
				{Path: "server/render_test.go", Insertions: 30, Deletions: 0},
			},
		},
	}
	att, err := renderEnvelopeAt(taskDiffRaw(t, payload), taskDiffClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Color != colorStatusReview {
		t.Errorf("color: got %q want %q", att.Color, colorStatusReview)
	}
	if att.Footer != "fulcrum/tasks.diff · files=2" {
		t.Errorf("footer: %q", att.Footer)
	}
	if !strings.Contains(att.Text, "| File | +Ins | -Del |") {
		t.Errorf("text missing table header:\n%s", att.Text)
	}
	if !strings.Contains(att.Text, "| `server/render.go` | 12 | 3 |") {
		t.Errorf("text missing first file row:\n%s", att.Text)
	}
	if !strings.Contains(att.Text, "| `server/render_test.go` | 30 | 0 |") {
		t.Errorf("text missing second file row:\n%s", att.Text)
	}
	if !strings.Contains(att.Text, "```diff\n") {
		t.Errorf("text missing fenced diff block:\n%s", att.Text)
	}
	if !strings.Contains(att.Text, "-old") || !strings.Contains(att.Text, "+new") {
		t.Errorf("text missing diff body lines:\n%s", att.Text)
	}
	if v := fieldStr(t, att.Fields[1]); v != "+42" {
		t.Errorf("Insertions: %q", v)
	}
	if v := fieldStr(t, att.Fields[2]); v != "-3" {
		t.Errorf("Deletions: %q", v)
	}
}

// TestRenderTaskDiff_Small_OverByteCap is the "few files but huge diff" edge:
// fileCount ≤ 5 but len(diff) > 6000. The full file table still renders but
// the fenced diff is replaced by the §B.5.2 truncation note.
func TestRenderTaskDiff_Small_OverByteCap(t *testing.T) {
	body := strings.Repeat("+long line\n", 1000)
	payload := taskDiffPayload{
		TaskID:     "t_42",
		Branch:     strPtr("feature/x"),
		BaseBranch: strPtr("main"),
		Diff:       &body,
		Summary: taskDiffSummary{
			FileCount:  2,
			Insertions: 1000,
			Deletions:  0,
			Files: []taskDiffFile{
				{Path: "f1", Insertions: 500, Deletions: 0},
				{Path: "f2", Insertions: 500, Deletions: 0},
			},
		},
	}
	att, err := renderEnvelopeAt(taskDiffRaw(t, payload), taskDiffClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(att.Text, "```diff") {
		t.Errorf("over-cap small must drop fenced diff:\n%s", att.Text)
	}
	if !strings.Contains(att.Text, "diff truncated to summary") {
		t.Errorf("over-cap small missing truncation note:\n%s", att.Text)
	}
	if !strings.Contains(att.Text, "total 2 files") {
		t.Errorf("truncation note missing file count:\n%s", att.Text)
	}
	if !strings.Contains(att.Text, "| `f1` | 500 | 0 |") {
		t.Errorf("file table missing first row:\n%s", att.Text)
	}
}

// TestRenderTaskDiff_Large_FileCountTruncated exercises the §B.5.2 file-count
// truncation: fileCount > 12 with a small unified diff. Table caps at 12
// entries, "…and N more" trailer mentions the dropped count, and the fenced
// diff is omitted (large branch never includes the fenced block).
func TestRenderTaskDiff_Large_FileCountTruncated(t *testing.T) {
	files := make([]taskDiffFile, 20)
	for i := range files {
		files[i] = taskDiffFile{Path: "f" + itoa(i+1), Insertions: i, Deletions: 0}
	}
	diff := "diff --git a/f1 b/f1\n@@\n+only a few bytes\n"
	payload := taskDiffPayload{
		TaskID:     "t_42",
		Branch:     strPtr("feature/x"),
		BaseBranch: strPtr("main"),
		Diff:       &diff,
		Summary: taskDiffSummary{
			FileCount:  20,
			Insertions: 100,
			Deletions:  0,
			Files:      files,
		},
	}
	att, err := renderEnvelopeAt(taskDiffRaw(t, payload), taskDiffClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Color != colorStatusReview {
		t.Errorf("color: %q", att.Color)
	}
	if strings.Contains(att.Text, "```diff") {
		t.Errorf("large branch must never include fenced diff:\n%s", att.Text)
	}
	if !strings.Contains(att.Text, "| `f12` |") {
		t.Errorf("table must include up to entry 12:\n%s", att.Text)
	}
	if strings.Contains(att.Text, "| `f13` |") {
		t.Errorf("table must not include entry 13:\n%s", att.Text)
	}
	if !strings.Contains(att.Text, "…and 8 more") {
		t.Errorf("missing 'and N more' trailer:\n%s", att.Text)
	}
	if strings.Contains(att.Text, "diff truncated to summary") {
		t.Errorf("byte-cap note must NOT fire when diff fits:\n%s", att.Text)
	}
	if att.Footer != "fulcrum/tasks.diff · files=20" {
		t.Errorf("footer: %q", att.Footer)
	}
}

// TestRenderTaskDiff_Large_DiffOverByteCap covers the second §B.5.2 cap: many
// files AND the unified diff blew the byte budget. Table caps at 4 entries
// (tighter cap), the truncation note fires, and the fenced diff stays out.
func TestRenderTaskDiff_Large_DiffOverByteCap(t *testing.T) {
	files := make([]taskDiffFile, 8)
	for i := range files {
		files[i] = taskDiffFile{Path: "f" + itoa(i+1), Insertions: 1000, Deletions: 0}
	}
	body := strings.Repeat("+x\n", 5000)
	payload := taskDiffPayload{
		TaskID:     "t_42",
		Branch:     strPtr("feature/x"),
		BaseBranch: strPtr("main"),
		Diff:       &body,
		Summary: taskDiffSummary{
			FileCount:  8,
			Insertions: 8000,
			Deletions:  0,
			Files:      files,
		},
	}
	att, err := renderEnvelopeAt(taskDiffRaw(t, payload), taskDiffClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(att.Text, "| `f4` |") {
		t.Errorf("must include entry 4:\n%s", att.Text)
	}
	if strings.Contains(att.Text, "| `f5` |") {
		t.Errorf("must NOT include entry 5 (tight cap):\n%s", att.Text)
	}
	if !strings.Contains(att.Text, "…and 4 more") {
		t.Errorf("trailer should report 8-4=4 dropped:\n%s", att.Text)
	}
	if !strings.Contains(att.Text, "diff truncated to summary") {
		t.Errorf("byte-cap note must fire:\n%s", att.Text)
	}
	if strings.Contains(att.Text, "```diff") {
		t.Errorf("over-cap large must NOT include fenced diff:\n%s", att.Text)
	}
}

// TestTaskDiffActions locks the §B.5.4 button row: Refresh + Back to task,
// both default style, neither dialog-gated. The button id strings are the
// scrollback-facing identifiers; renaming them is a breaking change.
func TestTaskDiffActions(t *testing.T) {
	payload := taskDiffPayload{
		TaskID:     "t_42",
		Branch:     strPtr("feature/x"),
		BaseBranch: strPtr("main"),
		Diff:       strPtr(""),
		Summary:    taskDiffSummary{FileCount: 0},
	}
	att, err := renderEnvelopeAt(taskDiffRaw(t, payload), taskDiffClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(att.Actions) != 2 {
		t.Fatalf("actions len: got %d want 2", len(att.Actions))
	}
	assertActionArgv(t, att.Actions[0], "task_diff_refresh", postActionStyleDefault, []string{"tasks", "diff", "t_42"}, false)
	assertActionArgv(t, att.Actions[1], "task_diff_back", postActionStyleDefault, []string{"tasks", "get", "t_42"}, false)
}

// TestRenderTaskDiff_MissingTaskID guards the §0.5 fallback for a malformed
// envelope (no task_id). Returning an error lets the caller surface the §0.5
// generic colorError card rather than emitting a "Diff · task ``" header.
func TestRenderTaskDiff_MissingTaskID(t *testing.T) {
	in := []byte(`{
		"success": true,
		"data": {
			"schema_version": 1,
			"verb": "tasks.diff",
			"summary": { "fileCount": 0, "insertions": 0, "deletions": 0, "files": [] }
		}
	}`)
	if _, err := renderEnvelope(in); err == nil {
		t.Fatalf("expected error for missing task_id")
	}
}

// TestRenderTaskDiff_GitUnavailable_RoutesToColorErrorCard locks the §B.5.5
// non-ephemeral path: a git_unavailable envelope drives the renderer through
// renderTaskDiffBusinessError so the user sees a colorError card with the
// Refresh + Back to task buttons (and NOT a generic dashboard-shaped business
// error fallback).
func TestRenderTaskDiff_GitUnavailable_RoutesToColorErrorCard(t *testing.T) {
	att, err := renderEnvelopeAt(taskDiffErrorRaw(t, "t_42", "git_unavailable", "git binary not on PATH"), taskDiffClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Color != colorError {
		t.Errorf("color: got %q want %q", att.Color, colorError)
	}
	if !strings.Contains(att.Title, "Diff · task `t_42` — error") {
		t.Errorf("title: %q", att.Title)
	}
	if !strings.Contains(att.Text, "git_unavailable") || !strings.Contains(att.Text, "git binary not on PATH") {
		t.Errorf("text missing code/message: %q", att.Text)
	}
	if len(att.Actions) != 2 {
		t.Fatalf("actions len: got %d want 2", len(att.Actions))
	}
	assertActionArgv(t, att.Actions[0], "task_diff_refresh", postActionStyleDefault, []string{"tasks", "diff", "t_42"}, false)
	assertActionArgv(t, att.Actions[1], "task_diff_back", postActionStyleDefault, []string{"tasks", "get", "t_42"}, false)
}

// TestRenderTaskDiff_TaskNotFound_FallsThroughToGenericCard documents the
// defensive path: command.go ephemerals tasks.diff `task_not_found` before
// it reaches renderEnvelopeAtForRequest, so this branch only fires if a
// future caller routes an ephemeral-eligible code to the renderer. Render
// falls back to the §0.5 generic colorError card.
func TestRenderTaskDiff_TaskNotFound_FallsThroughToGenericCard(t *testing.T) {
	att, err := renderEnvelopeAt(taskDiffErrorRaw(t, "t_999", "task_not_found", "task t_999 not found"), taskDiffClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Color != colorError {
		t.Errorf("color: got %q want colorError", att.Color)
	}
	if !strings.Contains(att.Title, "tasks.diff — error") {
		t.Errorf("title: %q", att.Title)
	}
	if !strings.Contains(att.Text, "task_not_found") {
		t.Errorf("text missing code: %q", att.Text)
	}
}

// TestTaskDiffBusinessErrorMessage_KnownCodes locks the §B.5.5 ephemeral
// copy: task_not_found surfaces the verb, the code, the message, and the
// `/f search <id>` hint so the user has a clear next action.
func TestTaskDiffBusinessErrorMessage_KnownCodes(t *testing.T) {
	got := taskDiffBusinessErrorMessage("task_not_found", "task t_999 not found")
	if !strings.Contains(got, "tasks.diff: task_not_found") {
		t.Errorf("known-code prefix: %q", got)
	}
	if !strings.Contains(got, "task t_999 not found") {
		t.Errorf("must surface message: %q", got)
	}
	if !strings.Contains(got, "/f search") {
		t.Errorf("must hint /f search: %q", got)
	}
}

// TestTaskDiffBusinessErrorMessage_UnknownCode confirms unknown codes fall
// back to the generic tasks message formatter rather than producing an empty
// ephemeral. This is the §B.5.5 backstop for a future CLI code landing
// before its plugin copy.
func TestTaskDiffBusinessErrorMessage_UnknownCode(t *testing.T) {
	got := taskDiffBusinessErrorMessage("future_code", "some detail")
	if !strings.Contains(got, "future_code") {
		t.Errorf("unknown code must surface in fallback: %q", got)
	}
	if !strings.Contains(got, "tasks.diff") {
		t.Errorf("unknown code must surface verb in fallback: %q", got)
	}
}

// TestTaskDiffIDFromArgv covers the shape-matching used by the http.go error
// router to title the colorError card with the originating task id.
func TestTaskDiffIDFromArgv(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"tasks", "diff", "t_42"}, "t_42"},
		{[]string{"tasks", "get", "t_42"}, ""},
		{[]string{"tasks", "diff"}, ""},
		{[]string{}, ""},
	}
	for _, c := range cases {
		if got := taskDiffIDFromArgv(c.argv); got != c.want {
			t.Errorf("taskDiffIDFromArgv(%v) = %q want %q", c.argv, got, c.want)
		}
	}
}

// TestTaskDiffTruncationNote locks the helper's literal output so a future
// reuse (search-results, jobs-panel) doesn't drift the copy independently.
// The helper is the §C.3 shared diff-truncation landing point.
func TestTaskDiffTruncationNote(t *testing.T) {
	got := taskDiffTruncationNote(taskDiffSummary{FileCount: 7, Insertions: 1234, Deletions: 56})
	want := "_[diff truncated to summary; total 7 files, +1234/-56 lines]_"
	if got != want {
		t.Errorf("truncation note:\n got: %q\nwant: %q", got, want)
	}
}

// itoa is a tiny test-side helper so test fixtures can construct path strings
// (`f1`, `f2`, ...) without importing strconv at the top level.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}
