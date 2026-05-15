package main

import (
	"fmt"
	"strings"
	"time"
)

// taskSummary mirrors the CLI `TaskSummary` shape (cli/JSON_SCHEMA.md). Only
// the fields the plugin renders are decoded; unknown fields are tolerated.
// Pointer types mark every CLI-nullable field so renderers can distinguish
// "field omitted" from "field present and empty" — chip helpers below treat
// nil as "—" and string-empty as "—" identically.
type taskSummary struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Status       string   `json:"status"`
	Priority     *string  `json:"priority"`
	Type         *string  `json:"type"`
	ProjectID    *string  `json:"projectId"`
	Tags         []string `json:"tags"`
	DueDate      *string  `json:"dueDate"`
	Agent        string   `json:"agent"`
	WorktreePath *string  `json:"worktreePath"`
	PrURL        *string  `json:"prUrl"`
	StartedAt    *string  `json:"startedAt"`
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"updatedAt"`
}

// statusChipMap is the canonical emoji+label render for a task status. Keys
// are the CLI's enum strings (`TO_DO`, `IN_PROGRESS`, `IN_REVIEW`, `DONE`,
// `CANCELED`); unknown statuses fall through to a neutral marker so a future
// CLI status addition doesn't crash rendering — review will surface the
// fallback as a CLI schema gap (spike §C.5).
var statusChipMap = map[string]string{
	"TO_DO":       ":white_circle: TO_DO",
	"IN_PROGRESS": ":large_blue_circle: IN_PROGRESS",
	"IN_REVIEW":   ":purple_circle: IN_REVIEW",
	"DONE":        ":white_check_mark: DONE",
	"CANCELED":    ":black_circle: CANCELED",
}

// statusChip returns the markdown chip for a CLI task status. An empty or
// unknown status renders as "—" so downstream table cells stay aligned.
func statusChip(status string) string {
	if status == "" {
		return "—"
	}
	if chip, ok := statusChipMap[status]; ok {
		return chip
	}
	return ":grey_question: " + status
}

// priorityChipMap renders a CLI priority enum as a one-letter token with
// emoji. nil / empty / unknown all collapse to "—" via priorityChip.
var priorityChipMap = map[string]string{
	"high":   ":red_circle: H",
	"medium": ":large_orange_diamond: M",
	"low":    ":large_blue_diamond: L",
}

// priorityChip mirrors statusChip for the CLI's priority enum. The pointer
// argument matches taskSummary.Priority (nullable in the CLI schema); nil and
// empty both render as "—".
func priorityChip(p *string) string {
	if p == nil || *p == "" {
		return "—"
	}
	if chip, ok := priorityChipMap[*p]; ok {
		return chip
	}
	return ":grey_question: " + *p
}

// formatTaskTitleLine renders the "<priority> <title> · `<id>`" header used
// by dashboard-home's due_today section and by other compact list contexts.
// It deliberately keeps the spec's exact spacing (two spaces around the
// middle separator) so visual output is stable across feature consumers.
func formatTaskTitleLine(t taskSummary) string {
	return fmt.Sprintf("%s %s  ·  `%s`", priorityChip(t.Priority), t.Title, t.ID)
}

// formatRelTime renders an ISO-8601 timestamp as a short relative string
// ("5m ago", "in 3h"). Empty / invalid input renders as "—". `now` lets tests
// pin the clock; production callers pass time.Now().
func formatRelTime(iso string, now time.Time) string {
	if iso == "" {
		return "—"
	}
	t, err := parseLooseISO(iso)
	if err != nil {
		return "—"
	}
	delta := t.Sub(now)
	future := delta >= 0
	abs := delta
	if !future {
		abs = -delta
	}
	switch {
	case abs < time.Minute:
		return "just now"
	case abs < time.Hour:
		return relString(int(abs/time.Minute), "m", future)
	case abs < 24*time.Hour:
		return relString(int(abs/time.Hour), "h", future)
	case abs < 30*24*time.Hour:
		return relString(int(abs/(24*time.Hour)), "d", future)
	default:
		return relString(int(abs/(30*24*time.Hour)), "mo", future)
	}
}

func relString(n int, unit string, future bool) string {
	if future {
		return fmt.Sprintf("in %d%s", n, unit)
	}
	return fmt.Sprintf("%d%s ago", n, unit)
}

// parseLooseISO accepts both full ISO-8601 timestamps (createdAt/updatedAt)
// and date-only strings (YYYY-MM-DD, as emitted by TaskSummary.dueDate). The
// date-only branch anchors to UTC midnight so "today" comparisons work
// regardless of the user's local timezone.
func parseLooseISO(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unrecognized time %q", s)
}

// truncateMD shortens a markdown-bearing string to at most n runes, appending
// a single "…" when truncation actually happened. It is rune-aware so a CJK
// title truncated mid-character doesn't produce an invalid sequence. n <= 0
// is treated as "no limit" so callers can pass a configured cap unchecked.
func truncateMD(s string, n int) string {
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// joinNonEmpty concatenates non-empty segments with a separator. Used by
// chip-laden titles so missing chips don't produce double spaces.
func joinNonEmpty(sep string, parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
