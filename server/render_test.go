package main

import (
	"strings"
	"testing"
)

// TestRenderEnvelope_GenericFallback covers verbs that don't yet have a
// per-verb renderer (issue #17-#19 will retire each one in turn). Until those
// land, the plugin must fall back to a pretty-printed JSON dump so users see
// CLI output rather than a render error. `jobs` is the placeholder verb while
// it lacks a renderer (issue #17); when that lands, switch this test to
// another unsupported verb.
func TestRenderEnvelope_GenericFallback(t *testing.T) {
	in := []byte(`{
		"success": true,
		"data": {
			"schema_version": 1,
			"verb": "jobs",
			"scope": "all"
		}
	}`)
	att, err := renderEnvelope(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Title != "fulcrum jobs" {
		t.Fatalf("title: %q", att.Title)
	}
	if !strings.Contains(att.Text, "\"scope\": \"all\"") {
		t.Fatalf("pretty body missing field: %q", att.Text)
	}
}

func TestRenderEnvelope_Error(t *testing.T) {
	in := []byte(`{
		"success": true,
		"data": {
			"schema_version": 1,
			"verb": "tasks.get",
			"error": { "code": "not_found", "message": "task 42 not found" }
		}
	}`)
	att, err := renderEnvelope(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Title != "fulcrum tasks.get — error" {
		t.Fatalf("title: %q", att.Title)
	}
	if !strings.Contains(att.Text, "not_found") {
		t.Fatalf("body missing code: %q", att.Text)
	}
}

func TestRenderEnvelope_UnsupportedSchema(t *testing.T) {
	in := []byte(`{"success":true,"data":{"schema_version":99,"verb":"x"}}`)
	if _, err := renderEnvelope(in); err == nil {
		t.Fatalf("expected schema mismatch error")
	}
}

func TestRenderEnvelope_BadJSON(t *testing.T) {
	if _, err := renderEnvelope([]byte("not json")); err == nil {
		t.Fatalf("expected error on bad JSON")
	}
}
