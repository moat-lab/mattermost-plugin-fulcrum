package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// appLogsClock pins the wall clock for renderer entry points that still take
// time.Time; appsLogs itself does not surface relative time, but the central
// renderEnvelopeAt entry path requires the parameter.
var appLogsClock = time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

// appLogsRaw assembles a `fulcrum apps logs --json` envelope from a payload
// so schema drift surfaces as a compile failure here rather than in
// renderAppLogs. The envelope-error path uses appLogsRawWithError below.
func appLogsRaw(t *testing.T, p appsLogsPayload) []byte {
	t.Helper()
	body := struct {
		appsLogsPayload
		SchemaVersion int    `json:"schema_version"`
		Verb          string `json:"verb"`
	}{
		appsLogsPayload: p,
		SchemaVersion:   1,
		Verb:            "apps.logs",
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

// appLogsRawWithError assembles a business-error envelope for apps.logs. The
// CLI populates `error: {code, message}` alongside the data fields per
// JSON_SCHEMA.md §apps.logs; this helper produces that shape.
func appLogsRawWithError(t *testing.T, appID, service, code, message string) []byte {
	t.Helper()
	var svc *string
	if service != "" {
		svc = &service
	}
	payload := map[string]any{
		"schema_version": 1,
		"verb":           "apps.logs",
		"app_id":         appID,
		"service":        svc,
		"logs":           "",
		"error":          map[string]any{"code": code, "message": message},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	out, err := json.Marshal(map[string]any{"success": true, "data": json.RawMessage(data)})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return out
}

// TestRenderAppLogs_HappyPath_NoService covers the §B.8 "all-services" branch:
// non-empty body, no --service filter, body below the 7000-char cap.
// Renderer must emit Color=colorStatusDoing (live tail), Title="Logs ·
// <app_id>", Pretext="tail=200", Text fenced code block, Footer "service=all",
// Actions = Refresh + Tail more + Back.
func TestRenderAppLogs_HappyPath_NoService(t *testing.T) {
	att, err := renderEnvelopeAtForRequest(appLogsRaw(t, appsLogsPayload{
		AppID: "app_42",
		Logs:  "2026-05-15 09:14:01 INFO ready\n2026-05-15 09:14:02 INFO listening",
	}), appLogsClock, "", []string{"apps", "logs", "app_42", "--tail=200"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorStatusDoing {
		t.Errorf("color: got %q want %q", att.Color, colorStatusDoing)
	}
	if att.Title != "Logs · app_42" {
		t.Errorf("title: %q", att.Title)
	}
	if att.Pretext != "tail=200" {
		t.Errorf("pretext: %q", att.Pretext)
	}
	if !strings.HasPrefix(att.Text, "```\n") || !strings.HasSuffix(att.Text, "\n```") {
		t.Errorf("text not fenced: %q", att.Text)
	}
	if !strings.Contains(att.Text, "listening") {
		t.Errorf("text missing tail line: %q", att.Text)
	}
	if att.Footer != "fulcrum/apps.logs · service=all · tail=200" {
		t.Errorf("footer: %q", att.Footer)
	}
	if len(att.Actions) != 3 {
		t.Fatalf("actions len: got %d want 3", len(att.Actions))
	}
	assertActionArgv(t, att.Actions[0], "app_logs_refresh", postActionStyleDefault, []string{"apps", "logs", "app_42", "--tail=200"}, false)
	assertActionArgv(t, att.Actions[1], "app_logs_tail_more", postActionStyleDefault, []string{"apps", "logs", "app_42", "--tail=400"}, false)
	assertActionArgv(t, att.Actions[2], "app_logs_back", postActionStyleDefault, []string{"apps", "get", "app_42"}, false)
}

// TestRenderAppLogs_ServiceScoped exercises the §B.8 service-scoped branch.
// Color must be colorAccent, title must include "· <service>", footer must
// carry the service name, and every action argv must propagate --service.
func TestRenderAppLogs_ServiceScoped(t *testing.T) {
	svc := "web"
	att, err := renderEnvelopeAtForRequest(appLogsRaw(t, appsLogsPayload{
		AppID:   "app_42",
		Service: &svc,
		Logs:    "2026-05-15 09:14:01 INFO ready",
	}), appLogsClock, "", []string{"apps", "logs", "app_42", "--tail=200", "--service=web"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorAccent {
		t.Errorf("color: got %q want %q", att.Color, colorAccent)
	}
	if att.Title != "Logs · app_42 · web" {
		t.Errorf("title: %q", att.Title)
	}
	if att.Footer != "fulcrum/apps.logs · service=web · tail=200" {
		t.Errorf("footer: %q", att.Footer)
	}
	for _, act := range att.Actions {
		if act.Id == "app_logs_back" {
			continue
		}
		argvList, ok := act.Integration.Context[actionContextArgvKey].([]any)
		if !ok {
			t.Fatalf("action %s argv not []any", act.Id)
		}
		seen := false
		for _, v := range argvList {
			if s, _ := v.(string); s == "--service=web" {
				seen = true
			}
		}
		if !seen {
			t.Errorf("action %s missing --service=web flag: %v", act.Id, argvList)
		}
	}
}

// TestRenderAppLogs_Empty exercises the §B.8 empty branch: no log content
// renders the spike-literal placeholder, color=colorStatusTODO, and Tail more
// still appears because the user might want to extend the window.
func TestRenderAppLogs_Empty(t *testing.T) {
	att, err := renderEnvelopeAtForRequest(appLogsRaw(t, appsLogsPayload{
		AppID: "app_42",
		Logs:  "",
	}), appLogsClock, "", []string{"apps", "logs", "app_42", "--tail=200"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorStatusTODO {
		t.Errorf("color: got %q want %q", att.Color, colorStatusTODO)
	}
	if att.Text != "_No log lines in the requested tail window._" {
		t.Errorf("text: %q", att.Text)
	}
	names := actionNames(att.Actions)
	if !sliceContains(names, "Refresh") || !sliceContains(names, "Tail more") || !sliceContains(names, "Back to app") {
		t.Errorf("empty action set incomplete: %v", names)
	}
}

// TestRenderAppLogs_Truncated covers the §B.8 truncated branch. With a body
// of 7500 characters, the renderer must keep the trailing 7000 (most recent),
// prepend "…[500 chars elided]\n", and switch the color band to colorWarn.
func TestRenderAppLogs_Truncated(t *testing.T) {
	body := strings.Repeat("a", 7500)
	att, err := renderEnvelopeAtForRequest(appLogsRaw(t, appsLogsPayload{
		AppID: "app_42",
		Logs:  body,
	}), appLogsClock, "", []string{"apps", "logs", "app_42", "--tail=200"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorWarn {
		t.Errorf("color: got %q want %q", att.Color, colorWarn)
	}
	if !strings.Contains(att.Text, "…[500 chars elided]") {
		t.Errorf("missing truncation note: %q", att.Text[:200])
	}
	// Body inside fences plus leading note plus closing fence:
	inner := strings.TrimSuffix(strings.TrimPrefix(att.Text, "```\n"), "\n```")
	// The kept portion must be exactly the last 7000 chars of the original.
	expectedTail := body[len(body)-appLogsTextCap:]
	if !strings.HasSuffix(inner, expectedTail) {
		t.Errorf("kept tail mismatch (last 60 chars): inner=%q want suffix=%q", inner[len(inner)-60:], expectedTail[len(expectedTail)-60:])
	}
	// The note line must be the first line.
	firstNL := strings.IndexByte(inner, '\n')
	if firstNL < 0 || inner[:firstNL] != "…[500 chars elided]" {
		t.Errorf("truncation note position wrong: first line %q", inner[:firstNL])
	}
}

// TestRenderAppLogs_TailMoreDoubles_AndCeiling pins the §B.8.4 Tail more
// behavior: argv doubles on each click until it hits the 2000 ceiling, after
// which the button disappears. 1500 → 2000 (capped) → button gone.
func TestRenderAppLogs_TailMoreDoubles_AndCeiling(t *testing.T) {
	cases := []struct {
		name          string
		argv          []string
		wantTailMore  bool
		wantNextArgv  []string
		wantFooterStr string
	}{
		{"tail-200", []string{"apps", "logs", "app_42", "--tail=200"}, true, []string{"apps", "logs", "app_42", "--tail=400"}, "tail=200"},
		{"tail-1500-cap", []string{"apps", "logs", "app_42", "--tail=1500"}, true, []string{"apps", "logs", "app_42", "--tail=2000"}, "tail=1500"},
		{"tail-2000-no-more", []string{"apps", "logs", "app_42", "--tail=2000"}, false, nil, "tail=2000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			att, err := renderEnvelopeAtForRequest(appLogsRaw(t, appsLogsPayload{
				AppID: "app_42",
				Logs:  "ok",
			}), appLogsClock, "", c.argv)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if !strings.Contains(att.Footer, c.wantFooterStr) {
				t.Errorf("footer missing %q: %q", c.wantFooterStr, att.Footer)
			}
			has := sliceContains(actionNames(att.Actions), "Tail more")
			if has != c.wantTailMore {
				t.Errorf("Tail more present: got %v want %v (actions=%v)", has, c.wantTailMore, actionNames(att.Actions))
			}
			if c.wantTailMore {
				var tailMore *struct {
					Argv []any
				}
				for _, a := range att.Actions {
					if a.Id == "app_logs_tail_more" {
						raw, _ := a.Integration.Context[actionContextArgvKey].([]any)
						tailMore = &struct{ Argv []any }{Argv: raw}
					}
				}
				if tailMore == nil {
					t.Fatalf("tail_more action missing")
				}
				if len(tailMore.Argv) != len(c.wantNextArgv) {
					t.Fatalf("tail_more argv len: got %d want %d", len(tailMore.Argv), len(c.wantNextArgv))
				}
				for i, want := range c.wantNextArgv {
					if tailMore.Argv[i] != want {
						t.Errorf("tail_more argv[%d]: got %v want %q", i, tailMore.Argv[i], want)
					}
				}
			}
		})
	}
}

// TestRenderAppLogs_DefaultTailWhenHintMissing pins the fallback for callers
// that omit request argv: tail defaults to 200 so the card still renders a
// usable footer and a Tail more button against the default.
func TestRenderAppLogs_DefaultTailWhenHintMissing(t *testing.T) {
	att, err := renderEnvelopeAt(appLogsRaw(t, appsLogsPayload{AppID: "app_42", Logs: "x"}), appLogsClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Pretext != "tail=200" {
		t.Errorf("default pretext: %q", att.Pretext)
	}
	if !strings.Contains(att.Footer, "tail=200") {
		t.Errorf("default footer: %q", att.Footer)
	}
}

// TestRenderAppLogs_BusinessError_LogsUnavailable covers the §B.8.5 logs_
// unavailable branch: colorError card with Refresh + Back to app, code +
// message inside Text, NO Tail more button.
func TestRenderAppLogs_BusinessError_LogsUnavailable(t *testing.T) {
	att, err := renderEnvelopeAtForRequest(
		appLogsRawWithError(t, "app_42", "", "logs_unavailable", "logging stack offline"),
		appLogsClock, "", []string{"apps", "logs", "app_42", "--tail=400"},
	)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorError {
		t.Errorf("color: got %q want %q", att.Color, colorError)
	}
	if !strings.Contains(att.Title, "— error") {
		t.Errorf("title missing error suffix: %q", att.Title)
	}
	if !strings.Contains(att.Text, "logs_unavailable") || !strings.Contains(att.Text, "logging stack offline") {
		t.Errorf("text missing code/message: %q", att.Text)
	}
	names := actionNames(att.Actions)
	if !equalStringSlice(names, []string{"Refresh", "Back to app"}) {
		t.Errorf("error action set: got %v want [Refresh, Back to app]", names)
	}
	// Refresh must preserve the active tail hint so retry replays the same query.
	refresh := att.Actions[0]
	raw, _ := refresh.Integration.Context[actionContextArgvKey].([]any)
	wantArgv := []any{"apps", "logs", "app_42", "--tail=400"}
	if !reflect.DeepEqual(raw, wantArgv) {
		t.Errorf("refresh argv: got %v want %v", raw, wantArgv)
	}
}

// TestExtractAppLogsHints_AllArgvShapes verifies the hint extractor handles
// the three concrete argv shapes that show up in the plugin: slash-built
// argv (leading "fulcrum", trailing "--json"), button-context argv (bare),
// and dialog-state argv (bare). Each shape must yield the same hints and
// app id.
func TestExtractAppLogsHints_AllArgvShapes(t *testing.T) {
	cases := []struct {
		name        string
		argv        []string
		wantTail    int
		wantService string
		wantAppID   string
	}{
		{"bare", []string{"apps", "logs", "app_42", "--tail=400", "--service=web"}, 400, "web", "app_42"},
		{"slash-with-fulcrum-json", []string{"fulcrum", "apps", "logs", "app_42", "--tail=400", "--service=web", "--json"}, 400, "web", "app_42"},
		{"no-flags", []string{"apps", "logs", "app_42"}, 0, "", "app_42"},
		{"only-tail", []string{"apps", "logs", "app_42", "--tail=800"}, 800, "", "app_42"},
		{"only-service", []string{"apps", "logs", "app_42", "--service=worker"}, 0, "worker", "app_42"},
		{"empty", nil, 0, "", ""},
		{"non-logs", []string{"apps", "get", "app_42"}, 0, "", ""},
		{"malformed-tail", []string{"apps", "logs", "app_42", "--tail=abc"}, 0, "", "app_42"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hints, appID := extractAppLogsHints(c.argv)
			if hints.RequestedTail != c.wantTail {
				t.Errorf("tail: got %d want %d", hints.RequestedTail, c.wantTail)
			}
			if hints.RequestedService != c.wantService {
				t.Errorf("service: got %q want %q", hints.RequestedService, c.wantService)
			}
			if appID != c.wantAppID {
				t.Errorf("appID: got %q want %q", appID, c.wantAppID)
			}
		})
	}
}

// TestAppLogsBusinessErrorMessage_Ephemeral verifies the ephemeral-text
// formatting for the §B.8.5 codes that DO go ephemeral. app_not_found gets a
// hint pointing at apps list; service_not_found surfaces the requested
// service name and app id.
func TestAppLogsBusinessErrorMessage_Ephemeral(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		message string
		appID   string
		service string
		want    string
	}{
		{"app_not_found", "app_not_found", "no such app", "app_42", "", "apps.logs: app_not_found — no such app (try `/f apps list`)"},
		{"service_not_found-with-message", "service_not_found", `service "web" not found on app app_42`, "app_42", "web", `apps.logs: service_not_found — service "web" not found on app app_42`},
		{"service_not_found-no-message", "service_not_found", "", "app_42", "web", `apps.logs: service "web" not found on app app_42`},
		{"unknown-code", "weirdo", "anything", "app_42", "", "apps.logs: weirdo — anything"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := appLogsBusinessErrorMessage(c.code, c.message, c.appID, c.service)
			if got != c.want {
				t.Errorf("got %q\nwant %q", got, c.want)
			}
		})
	}
}

// TestAppLogsEphemeralCodes_SpikeContract pins which codes go ephemeral. The
// set is intentionally narrow — adding a code here is a deliberate spike
// §B.8.5 evolution and should land alongside renderer changes.
func TestAppLogsEphemeralCodes_SpikeContract(t *testing.T) {
	for code, want := range map[string]bool{
		"app_not_found":     true,
		"service_not_found": true,
		"logs_unavailable":  false,
		"":                  false,
		"unknown":           false,
	} {
		if got := appLogsEphemeralCodes[code]; got != want {
			t.Errorf("appLogsEphemeralCodes[%q]: got %v want %v", code, got, want)
		}
	}
}
