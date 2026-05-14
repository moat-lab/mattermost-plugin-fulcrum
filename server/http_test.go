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
