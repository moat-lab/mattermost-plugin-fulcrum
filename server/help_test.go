package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// helpRaw assembles a `fulcrum help --json` envelope from a payload so schema
// drift surfaces as a compile failure here rather than in renderHelp. Mirrors
// jobsRaw / projectsRaw so each per-verb test file controls its own fixture
// shape without leaking envelope-encoding details into individual cases.
func helpRaw(t *testing.T, p helpPayload) []byte {
	t.Helper()
	body := struct {
		helpPayload
		SchemaVersion int    `json:"schema_version"`
		Verb          string `json:"verb"`
	}{
		helpPayload:   p,
		SchemaVersion: 1,
		Verb:          "help",
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	out, err := json.Marshal(map[string]any{"success": true, "data": json.RawMessage(data)})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return out
}

// helpErrRaw assembles an envelope-error response for the help verb. The
// spike §B.13.5 notes the CLI does not emit business errors today, but the
// renderer's colorError arm is still exercised here so a future schema
// addition surfaces with Refresh visible instead of dropping the user on the
// generic fallback card.
func helpErrRaw(t *testing.T, code, message string) []byte {
	t.Helper()
	data := map[string]any{
		"schema_version": 1,
		"verb":           "help",
		"error":          map[string]string{"code": code, "message": message},
	}
	dataJSON, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	out, err := json.Marshal(map[string]any{"success": true, "data": json.RawMessage(dataJSON)})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return out
}

// fullVerbCatalog returns the §B.13.3 sample verb list verbatim so the
// renderer's bullet output can be compared against the spike's literal
// expectation. Any reordering or chip insertion would surface as a string
// mismatch here.
func fullVerbCatalog() []helpVerbEntry {
	return []helpVerbEntry{
		{Name: "dashboard", Description: "Aggregate task/app summary."},
		{Name: "tasks", Description: "Task verbs for Mattermost plugin contract."},
		{Name: "apps", Description: "App verbs for Mattermost plugin contract."},
		{Name: "projects", Description: "List projects."},
		{Name: "search", Description: "Cross-entity search."},
		{Name: "monitor", Description: "Host resource snapshot."},
		{Name: "jobs", Description: "List background jobs."},
		{Name: "status", Description: "Show server status."},
		{Name: "doctor", Description: "Check dependencies and system status."},
		{Name: "help", Description: "Show this list."},
	}
}

func TestHelp_HappyPathRendersSpikeBulletList(t *testing.T) {
	att, err := renderEnvelope(helpRaw(t, helpPayload{Verbs: fullVerbCatalog()}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorAccent {
		t.Errorf("help color: got %q want %q", att.Color, colorAccent)
	}
	if att.Title != "Fulcrum · command surface (10 verbs)" {
		t.Errorf("title: %q", att.Title)
	}
	if att.Pretext != "Tip: type `/f <verb>` then press Tab for arguments." {
		t.Errorf("pretext: %q", att.Pretext)
	}
	if att.Footer != "fulcrum/help · schema_version=1" {
		t.Errorf("footer: %q", att.Footer)
	}
	if len(att.Fields) != 0 {
		t.Errorf("§B.13.3 mandates no Fields; got %d", len(att.Fields))
	}
	// First and last bullet must match the spike's literal lines so a typo
	// in the renderer's separator (em-dash) or chip wrappers surfaces here.
	if !strings.HasPrefix(att.Text, "- **`dashboard`** — Aggregate task/app summary.") {
		t.Errorf("first bullet line drifted from spike §B.13.3:\n%s", att.Text)
	}
	if !strings.Contains(att.Text, "- **`help`** — Show this list.") {
		t.Errorf("last bullet missing:\n%s", att.Text)
	}
}

func TestHelp_PreservesEnvelopeOrder(t *testing.T) {
	// Per §B.13.3 the plugin must not re-rank verbs — the CLI is the single
	// source of truth. A reversed input must surface in the rendered Text
	// in the same reversed order.
	verbs := []helpVerbEntry{
		{Name: "help", Description: "Show this list."},
		{Name: "dashboard", Description: "Aggregate task/app summary."},
	}
	att, err := renderEnvelope(helpRaw(t, helpPayload{Verbs: verbs}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	helpIdx := strings.Index(att.Text, "- **`help`**")
	dashboardIdx := strings.Index(att.Text, "- **`dashboard`**")
	if helpIdx == -1 || dashboardIdx == -1 {
		t.Fatalf("missing bullets in %q", att.Text)
	}
	if helpIdx >= dashboardIdx {
		t.Errorf("plugin must preserve envelope order; got help after dashboard in:\n%s", att.Text)
	}
}

func TestHelp_EmptyVerbList(t *testing.T) {
	att, err := renderEnvelope(helpRaw(t, helpPayload{Verbs: nil}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Title != "Fulcrum · command surface (0 verbs)" {
		t.Errorf("title on empty list: %q", att.Title)
	}
	if !strings.Contains(att.Text, "No verbs reported by CLI.") {
		t.Errorf("empty-list text missing placeholder: %q", att.Text)
	}
	if names := actionNames(att.Actions); !equalStringSlice(names, []string{"Refresh", "Open dashboard"}) {
		t.Errorf("buttons must remain visible even on empty list: %v", names)
	}
}

func TestHelp_ActionsLockArgvAndStyle(t *testing.T) {
	att, err := renderEnvelope(helpRaw(t, helpPayload{Verbs: fullVerbCatalog()}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if names := actionNames(att.Actions); !equalStringSlice(names, []string{"Refresh", "Open dashboard"}) {
		t.Fatalf("§B.13.4 button row: got %v want [Refresh Open dashboard]", names)
	}
	assertActionArgv(t, att.Actions[0], "help_refresh", postActionStyleDefault, []string{"help"}, false)
	assertActionArgv(t, att.Actions[1], "help_open_dashboard", postActionStylePrimary, []string{"dashboard"}, false)
}

func TestHelp_NoPerVerbButtons(t *testing.T) {
	// §B.13.4 forbids per-verb buttons (each verb needs different args; a
	// static button would land users on ephemeral validation errors).
	// Exactly two buttons must render regardless of verb count.
	verbs := append(fullVerbCatalog(), helpVerbEntry{Name: "extra", Description: "An extra verb to inflate the list."})
	att, err := renderEnvelope(helpRaw(t, helpPayload{Verbs: verbs}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got := len(att.Actions); got != 2 {
		t.Errorf("§B.13.4 forbids per-verb buttons; got %d actions for %d verbs", got, len(verbs))
	}
}

func TestHelp_PostActionIntegrationURL(t *testing.T) {
	att, err := renderEnvelope(helpRaw(t, helpPayload{Verbs: fullVerbCatalog()}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, a := range att.Actions {
		if a.Integration == nil || a.Integration.URL != "/plugins/"+manifestID+"/action" {
			t.Errorf("action %q integration URL: %#v", a.Name, a.Integration)
		}
		if a.Type != model.PostActionTypeButton {
			t.Errorf("action %q type: %q", a.Name, a.Type)
		}
	}
}

func TestHelp_BusinessErrorRendersColorErrorCardWithRefresh(t *testing.T) {
	// Spike §B.13.5 notes the CLI does not emit business errors today, but
	// if a future schema addition (e.g. backend_unavailable) lands, the
	// renderer must keep Refresh visible — Open dashboard is dropped on the
	// error card since the dashboard is unaffected by an error on the help
	// catalog and mixing two CTAs on a fault card adds noise.
	att, err := renderEnvelope(helpErrRaw(t, "backend_unavailable", "help backend not reachable"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorError {
		t.Errorf("error color: got %q want %q", att.Color, colorError)
	}
	if att.Title != "fulcrum help — error" {
		t.Errorf("error title: %q", att.Title)
	}
	if !strings.Contains(att.Text, "`backend_unavailable`") {
		t.Errorf("error text must embed code: %q", att.Text)
	}
	if !strings.Contains(att.Text, "help backend not reachable") {
		t.Errorf("error text must embed message: %q", att.Text)
	}
	if att.Footer != "fulcrum/help · schema_version=1" {
		t.Errorf("error footer: %q", att.Footer)
	}
	if names := actionNames(att.Actions); !equalStringSlice(names, []string{"Refresh"}) {
		t.Errorf("error card actions: got %v want [Refresh]", names)
	}
	assertActionArgv(t, att.Actions[0], "help_refresh", postActionStyleDefault, []string{"help"}, false)
}

func TestHelp_DescriptionWithMarkdownIsPreservedVerbatim(t *testing.T) {
	// The renderer must not strip or re-escape backticks / asterisks in the
	// description — the CLI is the source of truth for how verbs are
	// described, and the bullet template (`**`<name>`**`) already wraps the
	// name as inline code so a description that itself contains code spans
	// (e.g. "Run `make dist`") must round-trip unchanged.
	verbs := []helpVerbEntry{
		{Name: "doctor", Description: "Check dependencies (run `make dist` if missing)."},
	}
	att, err := renderEnvelope(helpRaw(t, helpPayload{Verbs: verbs}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(att.Text, "Check dependencies (run `make dist` if missing).") {
		t.Errorf("description must round-trip verbatim:\n%s", att.Text)
	}
}
