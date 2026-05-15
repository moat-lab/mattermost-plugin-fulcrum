package main

import (
	"reflect"
	"testing"
)

func TestActionArgv_OK(t *testing.T) {
	got, err := actionArgv(map[string]any{
		"argv": []any{"tasks", "set-status", "abc123", "done"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"fulcrum", "tasks", "set-status", "abc123", "done", "--json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv mismatch: got %v want %v", got, want)
	}
}

func TestArgvFromContext(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want []string
	}{
		{"nil", nil, nil},
		{"missing key", map[string]any{"other": 1}, nil},
		{"non-array", map[string]any{"argv": "tasks"}, nil},
		{"non-string element", map[string]any{"argv": []any{"tasks", 7}}, nil},
		{"ok", map[string]any{"argv": []any{"tasks", "get", "t_1"}}, []string{"tasks", "get", "t_1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := argvFromContext(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestIsDialogClick(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want bool
	}{
		{"nil", nil, false},
		{"missing", map[string]any{"argv": []any{"x"}}, false},
		{"false", map[string]any{"dialog": false}, false},
		{"string", map[string]any{"dialog": "true"}, false},
		{"true", map[string]any{"dialog": true}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isDialogClick(c.in); got != c.want {
				t.Errorf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestActionArgv_Errors(t *testing.T) {
	cases := []struct {
		name string
		ctx  map[string]any
	}{
		{"nil", nil},
		{"missing key", map[string]any{"other": 1}},
		{"non-array", map[string]any{"argv": "tasks"}},
		{"non-string element", map[string]any{"argv": []any{"tasks", 7}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := actionArgv(tc.ctx); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}
