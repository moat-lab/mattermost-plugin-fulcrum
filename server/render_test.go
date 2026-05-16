package main

import (
	"strings"
	"testing"
)

// TestRenderEnvelope_GenericFallback exercises the default arm of the verb
// dispatcher. Every spike-enumerated verb (B.1–B.13) now has a per-verb
// renderer, so the only way to land on the fallback is a synthetic verb the
// CLI emits before a matching renderer ships — the fallback keeps users
// seeing CLI output (pretty-printed) rather than a render error in that
// transition window.
func TestRenderEnvelope_GenericFallback(t *testing.T) {
	in := []byte(`{
		"success": true,
		"data": {
			"schema_version": 1,
			"verb": "unknown_verb_for_test",
			"total": 0
		}
	}`)
	att, err := renderEnvelope(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Title != "fulcrum unknown_verb_for_test" {
		t.Fatalf("title: %q", att.Title)
	}
	if !strings.Contains(att.Text, "\"total\": 0") {
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
