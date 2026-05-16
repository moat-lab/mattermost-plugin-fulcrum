package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// jobsRaw assembles a `fulcrum jobs --json` envelope from a payload so
// schema drift surfaces as a compile failure here rather than in
// renderJobs. Mirrors the monitorRaw / searchRaw helpers so each per-verb
// test file controls its own fixture shape without leaking JSON-encoding
// details into individual cases.
func jobsRaw(t *testing.T, p jobsPayload) []byte {
	t.Helper()
	body := struct {
		jobsPayload
		SchemaVersion int    `json:"schema_version"`
		Verb          string `json:"verb"`
	}{
		jobsPayload:   p,
		SchemaVersion: 1,
		Verb:          "jobs",
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

// jobsErrRaw assembles an envelope-error response for the jobs verb. The
// `scope` field is preserved so the renderer's colorError Refresh argv can
// pin against an echoed scope (the §B.11.5 systemd_unavailable path).
func jobsErrRaw(t *testing.T, scope, code, message string) []byte {
	t.Helper()
	data := map[string]any{
		"schema_version": 1,
		"verb":           "jobs",
		"scope":          scope,
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

// boolPtr is the bool-pointer test helper; defined here because no other
// test file in the package needs it today. strPtr is declared in
// format_test.go and reused across the package.
func boolPtr(v bool) *bool { return &v }

func TestJobs_EmptyBranch(t *testing.T) {
	att, err := renderEnvelope(jobsRaw(t, jobsPayload{
		Scope: "all",
		Total: 0,
		Jobs:  nil,
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorStatusTODO {
		t.Errorf("empty color: got %q want %q", att.Color, colorStatusTODO)
	}
	if att.Title != "Jobs · scope=all (0)" {
		t.Errorf("empty title: %q", att.Title)
	}
	if att.Pretext != "" {
		t.Errorf("empty pretext should be blank: %q", att.Pretext)
	}
	if !strings.Contains(att.Text, "No jobs in scope=all") {
		t.Errorf("empty text missing no-jobs hint: %q", att.Text)
	}
	if att.Footer != "fulcrum/jobs · scope=all · total=0" {
		t.Errorf("empty footer: %q", att.Footer)
	}
	if names := actionNames(att.Actions); !equalStringSlice(names, []string{"Refresh", "View user only", "View system only"}) {
		t.Errorf("empty-card actions: %v", names)
	}
}

func TestJobs_AllActiveOrInactiveBranch(t *testing.T) {
	att, err := renderEnvelope(jobsRaw(t, jobsPayload{
		Scope: "all",
		Total: 3,
		Jobs: []jobSummary{
			{Name: "mattermost.service", Scope: "system", State: "active", Enabled: boolPtr(true)},
			{Name: "legacy-cron.timer", Scope: "system", State: "inactive", Enabled: boolPtr(false), Schedule: strPtr("0 5 * * *")},
			{Name: "agent-runner.service", Scope: "user", State: "active", Enabled: boolPtr(true), LastResult: strPtr("success")},
		},
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorAccent {
		t.Errorf("active-or-inactive color: got %q want %q", att.Color, colorAccent)
	}
	if att.Title != "Jobs · scope=all (3)" {
		t.Errorf("title: %q", att.Title)
	}
	if att.Pretext != "active ×2 · inactive ×1" {
		t.Errorf("pretext should list only count>0 buckets in canonical order: %q", att.Pretext)
	}
	if !strings.Contains(att.Text, "| State") || !strings.Contains(att.Text, "| Enabled") {
		t.Errorf("text missing table header: %q", att.Text)
	}
	if !strings.Contains(att.Text, "`mattermost.service`") {
		t.Errorf("text missing unit name cell: %q", att.Text)
	}
	if !strings.Contains(att.Text, ":large_blue_circle: active") {
		t.Errorf("text missing active state chip: %q", att.Text)
	}
	if !strings.Contains(att.Text, ":black_circle: inactive") {
		t.Errorf("text missing inactive state chip: %q", att.Text)
	}
	if !strings.Contains(att.Text, ":x:") {
		t.Errorf("text missing disabled emoji: %q", att.Text)
	}
}

func TestJobs_WaitingBranch(t *testing.T) {
	att, err := renderEnvelope(jobsRaw(t, jobsPayload{
		Scope: "all",
		Total: 2,
		Jobs: []jobSummary{
			{Name: "backup.timer", Scope: "system", State: "waiting", Enabled: boolPtr(true), Schedule: strPtr("*-*-* 03:00:00"), NextRun: strPtr("2026-05-16T03:00:00Z"), LastResult: strPtr("success")},
			{Name: "log-rotate.service", Scope: "system", State: "active", Enabled: boolPtr(true)},
		},
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorWarn {
		t.Errorf("waiting color: got %q want %q", att.Color, colorWarn)
	}
	if att.Pretext != "active ×1 · waiting ×1" {
		t.Errorf("pretext: %q", att.Pretext)
	}
	if !strings.Contains(att.Text, ":hourglass: waiting") {
		t.Errorf("text missing waiting chip: %q", att.Text)
	}
	if !strings.Contains(att.Text, "`*-*-* 03:00:00`") {
		t.Errorf("text missing schedule cell as inline code: %q", att.Text)
	}
	if !strings.Contains(att.Text, "2026-05-16T03:00:00Z") {
		t.Errorf("text missing nextRun cell: %q", att.Text)
	}
}

func TestJobs_FailedBranchWinsOverWaiting(t *testing.T) {
	att, err := renderEnvelope(jobsRaw(t, jobsPayload{
		Scope: "all",
		Total: 3,
		Jobs: []jobSummary{
			{Name: "backup.timer", Scope: "system", State: "waiting", Enabled: boolPtr(true)},
			{Name: "agent-runner.timer", Scope: "user", State: "failed", Enabled: boolPtr(true), LastResult: strPtr("failed")},
			{Name: "mattermost.service", Scope: "system", State: "active", Enabled: boolPtr(true)},
		},
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorPriorityHigh {
		t.Errorf("failed color must win over waiting: got %q want %q", att.Color, colorPriorityHigh)
	}
	if att.Pretext != "active ×1 · waiting ×1 · failed ×1" {
		t.Errorf("pretext should list all three count>0 buckets: %q", att.Pretext)
	}
	if !strings.Contains(att.Text, ":red_circle: failed") {
		t.Errorf("text missing failed chip: %q", att.Text)
	}
}

func TestJobs_ScopeUserShowsBackButtonOnly(t *testing.T) {
	att, err := renderEnvelope(jobsRaw(t, jobsPayload{
		Scope: "user",
		Total: 1,
		Jobs: []jobSummary{
			{Name: "agent-runner.service", Scope: "user", State: "active", Enabled: boolPtr(true)},
		},
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Title != "Jobs · scope=user (1)" {
		t.Errorf("title: %q", att.Title)
	}
	if att.Footer != "fulcrum/jobs · scope=user · total=1" {
		t.Errorf("footer: %q", att.Footer)
	}
	names := actionNames(att.Actions)
	if !equalStringSlice(names, []string{"Refresh", "Back to all scopes"}) {
		t.Errorf("scope=user actions: %v want [Refresh, Back to all scopes]", names)
	}
}

func TestJobs_RefreshArgvPreservesScope(t *testing.T) {
	att, err := renderEnvelope(jobsRaw(t, jobsPayload{
		Scope: "system",
		Total: 1,
		Jobs: []jobSummary{
			{Name: "ssh.service", Scope: "system", State: "active", Enabled: boolPtr(true)},
		},
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	assertActionArgv(t, att.Actions[0], "jobs_refresh", postActionStyleDefault, []string{"jobs", "--scope=system"}, false)
	assertActionArgv(t, att.Actions[1], "jobs_back_all", postActionStyleDefault, []string{"jobs", "--scope=all"}, false)
}

func TestJobs_ScopeAllArgvLock(t *testing.T) {
	att, err := renderEnvelope(jobsRaw(t, jobsPayload{
		Scope: "all",
		Total: 1,
		Jobs: []jobSummary{
			{Name: "ssh.service", Scope: "system", State: "active", Enabled: boolPtr(true)},
		},
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	assertActionArgv(t, att.Actions[0], "jobs_refresh", postActionStyleDefault, []string{"jobs", "--scope=all"}, false)
	assertActionArgv(t, att.Actions[1], "jobs_view_user", postActionStyleDefault, []string{"jobs", "--scope=user"}, false)
	assertActionArgv(t, att.Actions[2], "jobs_view_system", postActionStyleDefault, []string{"jobs", "--scope=system"}, false)
}

func TestJobs_NullableCellsRenderEmDash(t *testing.T) {
	att, err := renderEnvelope(jobsRaw(t, jobsPayload{
		Scope: "all",
		Total: 1,
		Jobs: []jobSummary{
			{Name: "ephemeral.service", Scope: "system", State: "active", Enabled: nil, Schedule: nil, NextRun: nil, LastResult: nil},
		},
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Each nullable column should render `—` so the row width stays stable.
	if !strings.Contains(att.Text, "| `ephemeral.service` | — | — | — |") {
		t.Errorf("nullable cells should collapse to em-dash; got text:\n%s", att.Text)
	}
}

func TestJobs_UnknownStateFallsThroughToActiveOrInactive(t *testing.T) {
	att, err := renderEnvelope(jobsRaw(t, jobsPayload{
		Scope: "all",
		Total: 1,
		Jobs: []jobSummary{
			{Name: "future.service", Scope: "system", State: "queued", Enabled: boolPtr(true)},
		},
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorAccent {
		t.Errorf("unknown state should not promote color past active-or-inactive accent: got %q", att.Color)
	}
	if !strings.Contains(att.Text, ":grey_question: queued") {
		t.Errorf("unknown state should render via grey-question fallback chip: %q", att.Text)
	}
}

func TestJobs_RowCapTruncationNote(t *testing.T) {
	jobs := make([]jobSummary, jobsRowCap+5)
	for i := range jobs {
		jobs[i] = jobSummary{
			Name:    "row.service",
			Scope:   "system",
			State:   "active",
			Enabled: boolPtr(true),
		}
	}
	att, err := renderEnvelope(jobsRaw(t, jobsPayload{
		Scope: "all",
		Total: len(jobs),
		Jobs:  jobs,
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Table body should be capped at jobsRowCap rows (count `row.service`
	// inline-code occurrences; the truncated rows must not bleed through).
	rendered := strings.Count(att.Text, "`row.service`")
	if rendered != jobsRowCap {
		t.Errorf("rendered table rows: got %d want %d", rendered, jobsRowCap)
	}
	if !strings.Contains(att.Footer, "showing first 30") {
		t.Errorf("footer must surface row-cap truncation: %q", att.Footer)
	}
}

func TestJobs_PipeInNameIsEscaped(t *testing.T) {
	att, err := renderEnvelope(jobsRaw(t, jobsPayload{
		Scope: "all",
		Total: 1,
		Jobs: []jobSummary{
			{Name: "weird|name.service", Scope: "system", State: "active", Enabled: boolPtr(true)},
		},
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(att.Text, "weird|name") {
		t.Errorf("pipe in name should be HTML-escaped to keep the markdown table layout intact: %q", att.Text)
	}
	if !strings.Contains(att.Text, "weird&#124;name.service") {
		t.Errorf("expected escaped pipe in name cell: %q", att.Text)
	}
}

func TestJobs_BusinessErrorSystemdUnavailableRendersColorErrorCard(t *testing.T) {
	att, err := renderEnvelope(jobsErrRaw(t, "user", "systemd_unavailable", "systemctl --user not reachable"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorError {
		t.Errorf("error color: got %q want %q", att.Color, colorError)
	}
	if att.Title != "fulcrum jobs — error" {
		t.Errorf("error title: %q", att.Title)
	}
	if !strings.Contains(att.Text, "`systemd_unavailable`") {
		t.Errorf("error text must embed code: %q", att.Text)
	}
	if !strings.Contains(att.Text, "systemctl --user not reachable") {
		t.Errorf("error text must embed message: %q", att.Text)
	}
	if att.Footer != "fulcrum/jobs · schema_version=1" {
		t.Errorf("error footer: %q", att.Footer)
	}
	// Refresh argv on the error card must reflect the echoed scope so the
	// user retries the exact view they were on (here: --scope=user).
	assertActionArgv(t, att.Actions[0], "jobs_refresh", postActionStyleDefault, []string{"jobs", "--scope=user"}, false)
}

func TestJobs_BusinessErrorSystemdUnavailableFallsBackToScopeAllWhenEnvelopeOmitsScope(t *testing.T) {
	att, err := renderEnvelope(jobsErrRaw(t, "", "systemd_unavailable", "systemctl not reachable"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Empty echoed scope + no argv → renderer must default to "all" so the
	// Refresh button is still well-formed.
	assertActionArgv(t, att.Actions[0], "jobs_refresh", postActionStyleDefault, []string{"jobs", "--scope=all"}, false)
}

func TestJobs_BusinessErrorMessage_UnknownScope(t *testing.T) {
	got := jobsBusinessErrorMessage("unknown_scope", "scope=nope is not a valid bucket")
	if !strings.Contains(got, "scope must be one of: all | user | system") {
		t.Errorf("unknown_scope ephemeral missing canonical hint: %q", got)
	}
	if !strings.Contains(got, "scope=nope is not a valid bucket") {
		t.Errorf("unknown_scope ephemeral should include the CLI message: %q", got)
	}
}

func TestJobs_BusinessErrorMessage_UnknownCode(t *testing.T) {
	got := jobsBusinessErrorMessage("FETCH_FAILED", "underlying systemd query timed out")
	if !strings.Contains(got, "FETCH_FAILED") {
		t.Errorf("unknown code should keep the code visible in ephemeral text: %q", got)
	}
}

func TestJobs_EphemeralCodesRegistry(t *testing.T) {
	if !jobsEphemeralCodes["unknown_scope"] {
		t.Error("unknown_scope must be in jobsEphemeralCodes per §B.11.5")
	}
	if jobsEphemeralCodes["systemd_unavailable"] {
		t.Error("systemd_unavailable must NOT be ephemeral per §B.11.5 — it renders as a colorError card with Refresh")
	}
}

func TestJobs_ScopeFromArgv_EqualsForm(t *testing.T) {
	if got := jobsScopeFromArgv([]string{"jobs", "--scope=user"}); got != "user" {
		t.Errorf("--scope=user: %q", got)
	}
}

func TestJobs_ScopeFromArgv_SpacedForm(t *testing.T) {
	if got := jobsScopeFromArgv([]string{"jobs", "--scope", "system"}); got != "system" {
		t.Errorf("--scope system: %q", got)
	}
}

func TestJobs_ScopeFromArgv_Missing(t *testing.T) {
	if got := jobsScopeFromArgv([]string{"jobs"}); got != "" {
		t.Errorf("bare argv should return empty so caller can fall back: %q", got)
	}
}

func TestJobs_EffectiveScope_PrefersEnvelopeOverArgv(t *testing.T) {
	envelopeData := json.RawMessage(`{"scope":"system"}`)
	if got := jobsEffectiveScope(envelopeData, []string{"jobs", "--scope=user"}); got != "system" {
		t.Errorf("envelope scope must win over argv scope: %q", got)
	}
}

func TestJobs_EffectiveScope_FallsBackToArgvThenAll(t *testing.T) {
	if got := jobsEffectiveScope(json.RawMessage(`{}`), []string{"jobs", "--scope=user"}); got != "user" {
		t.Errorf("argv fallback: %q", got)
	}
	if got := jobsEffectiveScope(json.RawMessage(`{}`), []string{"jobs"}); got != "all" {
		t.Errorf("final fallback should be \"all\": %q", got)
	}
}

// TestJobs_PostActionIntegrationURL pins the integration URL so the bot
// post's buttons route back into the plugin's /action endpoint exactly
// once the user clicks them; a wrong URL silently disables every button.
func TestJobs_PostActionIntegrationURL(t *testing.T) {
	att, err := renderEnvelope(jobsRaw(t, jobsPayload{
		Scope: "all",
		Total: 1,
		Jobs: []jobSummary{
			{Name: "ssh.service", Scope: "system", State: "active", Enabled: boolPtr(true)},
		},
	}))
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
