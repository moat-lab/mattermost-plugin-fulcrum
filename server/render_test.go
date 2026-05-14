package main

import (
	"strings"
	"testing"
)

func TestRenderEnvelope_Success(t *testing.T) {
	in := []byte(`{
		"success": true,
		"data": {
			"schema_version": 1,
			"verb": "dashboard",
			"active_tasks": 6
		}
	}`)
	att, err := renderEnvelope(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Title != "fulcrum dashboard" {
		t.Fatalf("title: %q", att.Title)
	}
	if !strings.Contains(att.Text, "\"active_tasks\": 6") {
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
