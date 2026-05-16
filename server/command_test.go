package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
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

// TestBuildAutocompleteTree_TasksCreateExposesHost guards AC #1 of issue #38:
// the slash autocomplete dropdown must list `host` as a named arg of
// `tasks create` so the user (or System Console default) can satisfy
// fulcrum CLI's remote-only hostId requirement.
func TestBuildAutocompleteTree_TasksCreateExposesHost(t *testing.T) {
	root := buildAutocompleteTree()

	var tasks *model.AutocompleteData
	for _, sub := range root.SubCommands {
		if sub.Trigger == "tasks" {
			tasks = sub
			break
		}
	}
	if tasks == nil {
		t.Fatalf("autocomplete tree missing `tasks` subtree")
	}
	var tasksCreate *model.AutocompleteData
	for _, sub := range tasks.SubCommands {
		if sub.Trigger == "create" {
			tasksCreate = sub
			break
		}
	}
	if tasksCreate == nil {
		t.Fatalf("autocomplete tree missing `tasks create` leaf")
	}

	wantArgs := []string{"title", "project", "priority", "host"}
	gotArgs := map[string]bool{}
	for _, arg := range tasksCreate.Arguments {
		if arg.Name != "" {
			gotArgs[arg.Name] = true
		}
	}
	for _, name := range wantArgs {
		if !gotArgs[name] {
			t.Errorf("tasks create autocomplete missing named arg %q (have %v)", name, gotArgs)
		}
	}
}

func TestInjectDefaultHostIfNeeded(t *testing.T) {
	tests := []struct {
		name          string
		argv          []string
		defaultHostID string
		want          []string
	}{
		{
			name:          "empty default is no-op",
			argv:          []string{"fulcrum", "tasks", "create", "--title", "T", "--json"},
			defaultHostID: "",
			want:          []string{"fulcrum", "tasks", "create", "--title", "T", "--json"},
		},
		{
			name:          "non tasks-create is no-op",
			argv:          []string{"fulcrum", "tasks", "list", "--json"},
			defaultHostID: "vctcn-app1",
			want:          []string{"fulcrum", "tasks", "list", "--json"},
		},
		{
			name:          "explicit --host space form is no-op",
			argv:          []string{"fulcrum", "tasks", "create", "--title", "T", "--host", "other-host", "--json"},
			defaultHostID: "vctcn-app1",
			want:          []string{"fulcrum", "tasks", "create", "--title", "T", "--host", "other-host", "--json"},
		},
		{
			name:          "explicit --host=value form is no-op",
			argv:          []string{"fulcrum", "tasks", "create", "--title", "T", "--host=other-host", "--json"},
			defaultHostID: "vctcn-app1",
			want:          []string{"fulcrum", "tasks", "create", "--title", "T", "--host=other-host", "--json"},
		},
		{
			name:          "injects before --json",
			argv:          []string{"fulcrum", "tasks", "create", "--title", "T", "--json"},
			defaultHostID: "vctcn-app1",
			want:          []string{"fulcrum", "tasks", "create", "--title", "T", "--host", "vctcn-app1", "--json"},
		},
		{
			name:          "injects at end when no --json trailing",
			argv:          []string{"fulcrum", "tasks", "create", "--title", "T"},
			defaultHostID: "vctcn-app1",
			want:          []string{"fulcrum", "tasks", "create", "--title", "T", "--host", "vctcn-app1"},
		},
		{
			name:          "short argv is no-op",
			argv:          []string{"fulcrum", "tasks"},
			defaultHostID: "vctcn-app1",
			want:          []string{"fulcrum", "tasks"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := injectDefaultHostIfNeeded(append([]string{}, tc.argv...), tc.defaultHostID)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("argv mismatch:\n got=%v\nwant=%v", got, tc.want)
			}
		})
	}
}

func TestInjectDefaultHostIfNeeded_BuildCLIArgvRoundtrip(t *testing.T) {
	// End-to-end of the plugin's argv-prep path: slash string → buildCLIArgv
	// → injectDefaultHostIfNeeded. Mirrors the call sequence in
	// ExecuteCommand so the AC #2 contract (`/f tasks create --title=X` with
	// admin DefaultHostID set hits the CLI with `--host` populated) has a
	// regression test even though the live network call is exercised by
	// agent-browser at L4.
	argv, err := buildCLIArgv("/f tasks create --title=audit-test-task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := injectDefaultHostIfNeeded(argv, "vctcn-app1")
	want := []string{"fulcrum", "tasks", "create", "--title=audit-test-task", "--host", "vctcn-app1", "--json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestHasHostArg(t *testing.T) {
	cases := []struct {
		argv []string
		want bool
	}{
		{[]string{}, false},
		{[]string{"fulcrum", "tasks", "create", "--title", "T"}, false},
		{[]string{"--host", "x"}, true},
		{[]string{"--host=x"}, true},
		{[]string{"--host=value", "--title", "T"}, true},
		{[]string{"--hostfoo", "bar"}, false}, // not a prefix-style false-positive
		{[]string{"prefix--host", "x"}, false}, // not somewhere in middle of token
	}
	for _, tc := range cases {
		got := hasHostArg(tc.argv)
		if got != tc.want {
			t.Errorf("hasHostArg(%v) = %v want %v", tc.argv, got, tc.want)
		}
	}
}

func TestIsTasksCreateArgv(t *testing.T) {
	cases := []struct {
		argv []string
		want bool
	}{
		{[]string{"fulcrum", "tasks", "create"}, true},
		{[]string{"fulcrum", "tasks", "create", "--title", "T", "--json"}, true},
		{[]string{"fulcrum", "tasks", "list"}, false},
		{[]string{"fulcrum", "dashboard"}, false},
		{[]string{"fulcrum", "tasks"}, false},
		{[]string{}, false},
		{[]string{"tasks", "create"}, false}, // missing leading fulcrum
	}
	for _, tc := range cases {
		got := isTasksCreateArgv(tc.argv)
		if got != tc.want {
			t.Errorf("isTasksCreateArgv(%v) = %v want %v", tc.argv, got, tc.want)
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
