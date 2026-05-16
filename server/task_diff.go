package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// taskDiffPayload mirrors the `data` payload of `fulcrum tasks diff <id> --json`
// (cli/JSON_SCHEMA.md §tasks.diff). `Diff` is nullable (no-worktree case);
// `Branch` / `BaseBranch` are nullable for the same reason. `Summary` is
// always present per the CLI schema even when fileCount == 0.
type taskDiffPayload struct {
	TaskID     string          `json:"task_id"`
	Branch     *string         `json:"branch"`
	BaseBranch *string         `json:"base_branch"`
	Diff       *string         `json:"diff"`
	Summary    taskDiffSummary `json:"summary"`
}

// taskDiffSummary is the aggregate diff stat block emitted by `tasks.diff`.
// `Files` enumerates the per-file insertions/deletions so the renderer can
// build the §B.5.3 markdown table without re-parsing the unified diff.
type taskDiffSummary struct {
	FileCount  int            `json:"fileCount"`
	Insertions int            `json:"insertions"`
	Deletions  int            `json:"deletions"`
	Files      []taskDiffFile `json:"files"`
}

type taskDiffFile struct {
	Path       string `json:"path"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
}

// taskDiffBranch is the rendered state-machine branch derived from the
// envelope shape per spike §B.5. Computing this once keeps the color / text /
// footer decisions consistent.
type taskDiffBranch int

const (
	taskDiffBranchNoWorktree taskDiffBranch = iota
	taskDiffBranchClean
	taskDiffBranchSmall
	taskDiffBranchLarge
)

const (
	// taskDiffSmallFileCap is the spike §B.5.2 threshold below which the file
	// list is rendered in full (no `…and N more` truncation).
	taskDiffSmallFileCap = 5
	// taskDiffLargeFileCap is the §B.5.2 truncation cap for the large branch:
	// table shows the top 12 entries, the rest collapse into the `…and N more`
	// trailer.
	taskDiffLargeFileCap = 12
	// taskDiffLargeDiffFileCap is the §B.5.2 truncation cap when the unified
	// diff itself exceeds the inline budget — only the first 4 files are
	// listed because the renderer is admitting it can't show the full diff.
	taskDiffLargeDiffFileCap = 4
	// taskDiffMaxDiffBytes is the §B.5.2 character ceiling above which the
	// fenced diff block is dropped in favour of a summary truncation note.
	taskDiffMaxDiffBytes = 6000
)

// taskDiffEphemeralCodes lists the business error.code values that spike
// §B.5.5 routes through the ephemeral path (the original card stays untouched
// and only the clicking/slashing user sees the message). `git_unavailable`
// is intentionally absent — it renders as a colorError card with Refresh +
// Back to task per §B.5.5.
var taskDiffEphemeralCodes = map[string]bool{
	"task_not_found": true,
}

// renderTaskDiff produces the task-diff-view SlackAttachment per spike
// §B.5.3. The state-machine branch is derived from the envelope shape rather
// than echoed back by the CLI (the `tasks.diff` envelope carries the raw
// summary; the spike's four branches are layered on top here).
func renderTaskDiff(raw json.RawMessage) (*model.SlackAttachment, error) {
	var p taskDiffPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("tasks.diff payload: %w", err)
	}
	if p.TaskID == "" {
		return nil, fmt.Errorf("tasks.diff payload: missing task_id")
	}
	branch := taskDiffBranchFor(p)
	att := &model.SlackAttachment{
		Color:   taskDiffColor(branch),
		Title:   fmt.Sprintf("Diff · task `%s`", p.TaskID),
		Pretext: taskDiffPretext(p),
		Fields:  taskDiffFields(p),
		Text:    taskDiffText(p, branch),
		Footer:  taskDiffFooter(branch, p.Summary.FileCount),
		Actions: taskDiffActions(p.TaskID),
	}
	return att, nil
}

// taskDiffBranchFor classifies the envelope into the §B.5 state machine. The
// no-worktree branch fires when the CLI sets `diff: null` (the only signal
// the envelope carries that the task lacks a worktree directory); clean fires
// when there is a worktree but the file count is zero; small/large split on
// the §B.5.2 fileCount threshold.
func taskDiffBranchFor(p taskDiffPayload) taskDiffBranch {
	if p.Diff == nil {
		return taskDiffBranchNoWorktree
	}
	switch {
	case p.Summary.FileCount == 0:
		return taskDiffBranchClean
	case p.Summary.FileCount <= taskDiffSmallFileCap:
		return taskDiffBranchSmall
	default:
		return taskDiffBranchLarge
	}
}

// taskDiffColor maps the §B.5.3 color decisions: no-worktree=colorWarn,
// clean=colorStatusTODO, small/large=colorStatusReview (the spike's "diff
// usually associates with review"); the error branch is rendered separately
// by renderTaskDiffBusinessError.
func taskDiffColor(b taskDiffBranch) string {
	switch b {
	case taskDiffBranchNoWorktree:
		return colorWarn
	case taskDiffBranchClean:
		return colorStatusTODO
	default:
		return colorStatusReview
	}
}

// taskDiffPretext renders `<base_branch>...<branch>` (git diff range
// notation) when BOTH ref strings are non-empty; either side missing
// collapses to an empty pretext so the card doesn't surface a broken range.
func taskDiffPretext(p taskDiffPayload) string {
	base := derefOrEmpty(p.BaseBranch)
	head := derefOrEmpty(p.Branch)
	if base == "" || head == "" {
		return ""
	}
	return fmt.Sprintf("%s...%s", base, head)
}

// taskDiffFields builds the four-cell Fields[] grid per §B.5.3. Insertions /
// Deletions always render with their leading sign so the cell is unambiguous
// in scrollback; Branch collapses to "—" when either ref is missing so the
// pretext-vs-fields rule (both present → pretext; absence → fields show "—")
// stays consistent.
func taskDiffFields(p taskDiffPayload) []*model.SlackAttachmentField {
	return []*model.SlackAttachmentField{
		{Title: "Files", Value: fmt.Sprintf("%d", p.Summary.FileCount), Short: true},
		{Title: "Insertions", Value: fmt.Sprintf("+%d", p.Summary.Insertions), Short: true},
		{Title: "Deletions", Value: fmt.Sprintf("-%d", p.Summary.Deletions), Short: true},
		{Title: "Branch", Value: taskDiffBranchField(p.Branch, p.BaseBranch), Short: true},
	}
}

// taskDiffBranchField renders the spike-literal "`<branch>` ← `<base_branch>`"
// when both refs are present and "—" otherwise. The arrow is the same glyph
// the spike body uses; per-side fallbacks (one ref present, the other null)
// also collapse to "—" because a half-populated range is more misleading than
// no range at all.
func taskDiffBranchField(headPtr, basePtr *string) string {
	head := derefOrEmpty(headPtr)
	base := derefOrEmpty(basePtr)
	if head == "" || base == "" {
		return "—"
	}
	return fmt.Sprintf("`%s` ← `%s`", head, base)
}

// taskDiffText assembles the §B.5.3 Text body. no-worktree / clean produce a
// fixed italic line; small renders a markdown file table plus (when the diff
// fits the cap) a fenced ```diff block; large renders a truncated file table
// plus (when the diff exceeded the cap) the §B.5.2 truncation note.
func taskDiffText(p taskDiffPayload, b taskDiffBranch) string {
	switch b {
	case taskDiffBranchNoWorktree:
		return "_Task has no worktree. Nothing to diff._"
	case taskDiffBranchClean:
		return "_Working tree clean. No file changes._"
	case taskDiffBranchSmall:
		return taskDiffSmallText(p)
	default:
		return taskDiffLargeText(p)
	}
}

// taskDiffSmallText renders the small-branch body: a full file table plus
// the fenced unified diff when it fits the §B.5.2 byte cap. If the diff
// exceeds the cap the fenced block is replaced by the truncation note so a
// rare "few files but huge diff" payload still degrades gracefully.
func taskDiffSmallText(p taskDiffPayload) string {
	cap := len(p.Summary.Files)
	table := renderTaskDiffTable(p.Summary.Files, cap)
	diff := derefOrEmpty(p.Diff)
	if len(diff) <= taskDiffMaxDiffBytes {
		body := strings.TrimRight(diff, "\n")
		return table + "\n\n```diff\n" + body + "\n```"
	}
	return table + "\n\n" + taskDiffTruncationNote(p.Summary)
}

// taskDiffLargeText renders the large-branch body: file table truncated to
// §B.5.2 caps (12 files by default, 4 when the diff itself blew the byte
// cap), with the `…and N more` trailer when entries were dropped. The fenced
// diff block is intentionally never included in the large branch — the file
// list is the artifact that fits in a post.
func taskDiffLargeText(p taskDiffPayload) string {
	diffLen := len(derefOrEmpty(p.Diff))
	cap := taskDiffLargeFileCap
	if diffLen > taskDiffMaxDiffBytes {
		cap = taskDiffLargeDiffFileCap
	}
	table := renderTaskDiffTable(p.Summary.Files, cap)
	if extra := p.Summary.FileCount - cap; extra > 0 {
		table = table + fmt.Sprintf("\n\n…and %d more", extra)
	}
	if diffLen > taskDiffMaxDiffBytes {
		table = table + "\n\n" + taskDiffTruncationNote(p.Summary)
	}
	return table
}

// renderTaskDiffTable produces the §B.5.3 markdown table. headerRow keeps the
// File / +Ins / -Del column order; the body rows are right-aligned for the
// numeric columns via the markdown alignment hints embedded in the
// underline row (mirroring the spike's literal template).
func renderTaskDiffTable(files []taskDiffFile, cap int) string {
	if cap <= 0 || len(files) == 0 {
		return ""
	}
	if cap > len(files) {
		cap = len(files)
	}
	var b strings.Builder
	b.WriteString("| File | +Ins | -Del |\n")
	b.WriteString("|---   |---:  |---:  |")
	for i := 0; i < cap; i++ {
		f := files[i]
		b.WriteString(fmt.Sprintf("\n| `%s` | %d | %d |", f.Path, f.Insertions, f.Deletions))
	}
	return b.String()
}

// taskDiffTruncationNote is the spike §B.5.2 inline message used when the
// unified diff blew the byte cap. Plugin-side first-pass landing point for the
// diff truncation helper called out in §C.3 (shared format helper); future
// diff-bearing renderers (search results, jobs panel) can reuse it without
// reimplementing the byte budget.
func taskDiffTruncationNote(s taskDiffSummary) string {
	return fmt.Sprintf("_[diff truncated to summary; total %d files, +%d/-%d lines]_",
		s.FileCount, s.Insertions, s.Deletions)
}

// taskDiffFooter renders the §B.5.3 footer line. The no-worktree branch
// reports the literal `no-worktree` token instead of `files=0` so reading
// scrollback distinguishes "no worktree" from a clean worktree with no
// changes.
func taskDiffFooter(b taskDiffBranch, fileCount int) string {
	if b == taskDiffBranchNoWorktree {
		return "fulcrum/tasks.diff · no-worktree"
	}
	return fmt.Sprintf("fulcrum/tasks.diff · files=%d", fileCount)
}

// taskDiffActions builds the §B.5.4 button row. Refresh + Back to task are
// the only always-present buttons; the spike's hypothetical Mark Done is
// deferred to the §C.5 follow-up that extends the `tasks.diff` envelope with
// `task.status` — without it the plugin cannot decide whether the button
// should appear, so it stays out.
func taskDiffActions(taskID string) []*model.PostAction {
	return []*model.PostAction{
		makeAction("task_diff_refresh", "Refresh", postActionStyleDefault, []string{"tasks", "diff", taskID}),
		makeAction("task_diff_back", "Back to task", postActionStyleDefault, []string{"tasks", "get", taskID}),
	}
}

// renderTaskDiffBusinessError produces the §B.5.5 colorError card for the
// `git_unavailable` code (and any future tasks.diff business error that
// should surface as a non-ephemeral bot post). The Refresh + Back to task
// buttons mirror the success-card row so the user can retry from the same
// post once the backend recovers. When taskID is empty (envelope omitted
// task_id and argv fallback also yielded ""), the title drops the
// `Diff · task `<id>`` cell entirely to avoid an orphan-backtick artifact
// in the Mattermost-rendered Markdown (issue #39).
func renderTaskDiffBusinessError(taskID, code, message string) *model.SlackAttachment {
	title := "Diff — error"
	if taskID != "" {
		title = fmt.Sprintf("Diff · task `%s` — error", taskID)
	}
	return &model.SlackAttachment{
		Color:   colorError,
		Title:   title,
		Text:    fmt.Sprintf("`%s` %s", code, message),
		Footer:  "fulcrum/tasks.diff · schema_version=1",
		Actions: taskDiffActions(taskID),
	}
}

// taskDiffBusinessErrorMessage formats the ephemeral text shown to the
// clicking / slashing user when a tasks.diff envelope returns a business
// error.code routed through taskDiffEphemeralCodes (today: `task_not_found`).
// Unknown codes fall through to the generic tasks message formatter so a
// future CLI code surfaces as `tasks.diff: <code> — <message>`.
func taskDiffBusinessErrorMessage(code, message string) string {
	switch code {
	case "task_not_found":
		base := "tasks.diff: " + code
		if message != "" {
			base += " — " + message
		}
		return base + " (try `/f search <id>`)"
	}
	return tasksBusinessErrorMessage("tasks.diff", code, message)
}

// derefOrEmpty returns the pointed-at string or "" when the pointer is nil.
// Used by the §B.5.3 field/pretext renderers so missing CLI fields collapse
// to "" rather than panicking.
func derefOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// taskDiffAppIDFromArgv mirrors taskIDFromArgv but only matches the
// `tasks diff <id>` shape, so /action and /dialog can route the
// non-ephemeral `git_unavailable` envelope back into the renderer with the
// originating task id (needed for the colorError card's Title + button argv).
func taskDiffIDFromArgv(argv []string) string {
	if len(argv) >= 3 && argv[0] == "tasks" && argv[1] == "diff" {
		return argv[2]
	}
	return ""
}
