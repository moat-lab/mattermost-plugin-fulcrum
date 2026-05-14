package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildCLIArgv_Default(t *testing.T) {
	argv, err := buildCLIArgv("/f")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"fulcrum", "dashboard", "--json"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv mismatch: got %v want %v", argv, want)
	}
}

func TestBuildCLIArgv_TasksList(t *testing.T) {
	argv, err := buildCLIArgv("/f tasks list --priority high --tag urgent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"fulcrum", "tasks", "list", "--priority", "high", "--tag", "urgent", "--json"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv mismatch: got %v want %v", argv, want)
	}
}

func TestBuildCLIArgv_EmptyString(t *testing.T) {
	if _, err := buildCLIArgv(""); err == nil {
		t.Fatalf("expected error on empty input")
	}
}

func TestBuildAutocompleteTree_CoversContractVerbs(t *testing.T) {
	root := buildAutocompleteTree()
	if root.Trigger != slashTrigger {
		t.Fatalf("root trigger: got %q want %q", root.Trigger, slashTrigger)
	}
	wantTopLevel := []string{"dashboard", "tasks", "apps", "search", "monitor", "jobs", "projects"}
	got := map[string]bool{}
	for _, sub := range root.SubCommands {
		got[sub.Trigger] = true
	}
	for _, name := range wantTopLevel {
		if !got[name] {
			t.Errorf("autocomplete tree is missing top-level verb %q", name)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Fatalf("no-trim case: %q", got)
	}
	got := truncate("hello world", 5)
	if !strings.HasPrefix(got, "hello") || !strings.HasSuffix(got, "…") {
		t.Fatalf("trim case: %q", got)
	}
}
