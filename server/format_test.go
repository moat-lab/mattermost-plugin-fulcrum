package main

import (
	"strings"
	"testing"
	"time"
)

func strPtr(s string) *string { return &s }

func TestStatusChip(t *testing.T) {
	cases := map[string]string{
		"":            "—",
		"TO_DO":       ":white_circle: TO_DO",
		"IN_PROGRESS": ":large_blue_circle: IN_PROGRESS",
		"IN_REVIEW":   ":purple_circle: IN_REVIEW",
		"DONE":        ":white_check_mark: DONE",
		"CANCELED":    ":black_circle: CANCELED",
		"WAT":         ":grey_question: WAT",
	}
	for in, want := range cases {
		if got := statusChip(in); got != want {
			t.Errorf("statusChip(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPriorityChip(t *testing.T) {
	if got := priorityChip(nil); got != "—" {
		t.Errorf("priorityChip(nil) = %q, want —", got)
	}
	empty := ""
	if got := priorityChip(&empty); got != "—" {
		t.Errorf("priorityChip(&\"\") = %q, want —", got)
	}
	high := "high"
	if got := priorityChip(&high); got != ":red_circle: H" {
		t.Errorf("priorityChip(high) = %q", got)
	}
	med := "medium"
	if got := priorityChip(&med); got != ":large_orange_diamond: M" {
		t.Errorf("priorityChip(medium) = %q", got)
	}
	low := "low"
	if got := priorityChip(&low); got != ":large_blue_diamond: L" {
		t.Errorf("priorityChip(low) = %q", got)
	}
	zzz := "zzz"
	if got := priorityChip(&zzz); got != ":grey_question: zzz" {
		t.Errorf("priorityChip(unknown) = %q", got)
	}
}

func TestFormatTaskTitleLine(t *testing.T) {
	high := "high"
	got := formatTaskTitleLine(taskSummary{ID: "t_123", Title: "Fix login", Priority: &high})
	want := ":red_circle: H Fix login  ·  `t_123`"
	if got != want {
		t.Errorf("formatTaskTitleLine = %q, want %q", got, want)
	}
	// nil priority still renders the line with "—" chip.
	got2 := formatTaskTitleLine(taskSummary{ID: "t_x", Title: "No pri"})
	if !strings.Contains(got2, "— No pri") || !strings.Contains(got2, "`t_x`") {
		t.Errorf("formatTaskTitleLine nil pri = %q", got2)
	}
}

func TestFormatRelTime(t *testing.T) {
	now := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want string
	}{
		{"", "—"},
		{"not-a-time", "—"},
		{"2026-05-15T11:55:00Z", "5m ago"},
		{"2026-05-15T09:00:00Z", "3h ago"},
		{"2026-05-15T12:00:30Z", "just now"},
		{"2026-05-15T15:00:00Z", "in 3h"},
		{"2026-05-14T12:00:00Z", "1d ago"},
		{"2026-05-16", "in 12h"}, // date-only → UTC midnight 2026-05-16
		{"2026-05-14", "1d ago"}, // date-only → UTC midnight 2026-05-14 (36h prior)
	}
	for _, c := range cases {
		if got := formatRelTime(c.in, now); got != c.want {
			t.Errorf("formatRelTime(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTruncateMD(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"", 10, ""},
		{"abc", 10, "abc"},
		{"abcdef", 3, "abc…"},
		{"日本語テスト", 3, "日本語…"},
		{"keep all", 0, "keep all"},
		{"keep all", -1, "keep all"},
	}
	for _, c := range cases {
		if got := truncateMD(c.in, c.n); got != c.want {
			t.Errorf("truncateMD(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestJoinNonEmpty(t *testing.T) {
	if got := joinNonEmpty(" · ", "a", "", "b"); got != "a · b" {
		t.Errorf("joinNonEmpty = %q", got)
	}
	if got := joinNonEmpty(" · ", "", "", ""); got != "" {
		t.Errorf("joinNonEmpty all empty = %q", got)
	}
}
