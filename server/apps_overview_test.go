package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// appsOverviewClock is the wall clock used by every apps-overview test so the
// "Last deploy" relative-time column is deterministic across runs.
var appsOverviewClock = time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

// appsListRaw assembles a `fulcrum apps list --json` envelope from a payload
// struct so schema drift surfaces as a compile failure here rather than in
// renderAppsOverview.
func appsListRaw(t *testing.T, p appsListPayload) []byte {
	t.Helper()
	body := struct {
		appsListPayload
		SchemaVersion int    `json:"schema_version"`
		Verb          string `json:"verb"`
	}{
		appsListPayload: p,
		SchemaVersion:   1,
		Verb:            "apps.list",
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

func mkApp(name, status, branch, lastDeployed string) appSummary {
	a := appSummary{ID: "app_" + name, Name: name, Status: status, Branch: branch, AutoDeployEnabled: true}
	if lastDeployed != "" {
		ld := lastDeployed
		a.LastDeployedAt = &ld
	}
	return a
}

func TestAppsOverview_Empty(t *testing.T) {
	att, err := renderEnvelopeAt(appsListRaw(t, appsListPayload{Total: 0, Apps: nil}), appsOverviewClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorStatusTODO {
		t.Errorf("color: got %q want %q", att.Color, colorStatusTODO)
	}
	if att.Title != "Apps · 0" {
		t.Errorf("title: %q", att.Title)
	}
	if att.Pretext != "" {
		t.Errorf("pretext should be empty in empty state, got %q", att.Pretext)
	}
	if !strings.Contains(att.Text, "_No apps registered.") {
		t.Errorf("text empty fallback missing: %q", att.Text)
	}
	if !strings.Contains(att.Text, "fulcrum apps onboard") {
		t.Errorf("text empty fallback CLI hint missing: %q", att.Text)
	}
	if att.Footer != "fulcrum/apps.list · total=0" {
		t.Errorf("footer: %q", att.Footer)
	}
	if len(att.Actions) != 1 || att.Actions[0].Name != "Refresh" {
		t.Fatalf("empty-state actions: %+v", att.Actions)
	}
}

func TestAppsOverview_AllRunning(t *testing.T) {
	att, err := renderEnvelopeAt(appsListRaw(t, appsListPayload{
		Total: 3,
		Apps: []appSummary{
			mkApp("webapp", "running", "main", "2026-05-14T15:30:00Z"),
			mkApp("worker", "running", "main", "2026-05-15T09:00:00Z"),
			mkApp("api", "running", "main", "2026-05-15T11:00:00Z"),
		},
	}), appsOverviewClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorStatusDone {
		t.Errorf("color: got %q want %q (all-running ⇒ done green)", att.Color, colorStatusDone)
	}
	if att.Title != "Apps · 3" {
		t.Errorf("title: %q", att.Title)
	}
	if !strings.Contains(att.Pretext, "running") || !strings.Contains(att.Pretext, "×3") {
		t.Errorf("pretext: %q", att.Pretext)
	}
	if strings.Contains(att.Pretext, "failed") || strings.Contains(att.Pretext, "building") {
		t.Errorf("pretext must not include zero-count buckets: %q", att.Pretext)
	}
	// Table contains all three apps.
	for _, name := range []string{"webapp", "worker", "api"} {
		if !strings.Contains(att.Text, "`"+name+"`") {
			t.Errorf("text missing %q row: %q", name, att.Text)
		}
	}
	if !strings.Contains(att.Text, "| Status | Name | Branch | Last deploy |") {
		t.Errorf("table header missing: %q", att.Text)
	}
	if att.Footer != "fulcrum/apps.list · total=3" {
		t.Errorf("footer: %q", att.Footer)
	}
}

func TestAppsOverview_MixedWithFailed(t *testing.T) {
	att, err := renderEnvelopeAt(appsListRaw(t, appsListPayload{
		Total: 3,
		Apps: []appSummary{
			mkApp("webapp", "running", "main", "2026-05-14T15:30:00Z"),
			mkApp("worker", "failed", "dev", "2026-05-15T02:00:00Z"),
			mkApp("api", "building", "main", "2026-05-15T11:00:00Z"),
		},
	}), appsOverviewClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorPriorityHigh {
		t.Errorf("color: got %q want %q (mixed-with-failed ⇒ high red)", att.Color, colorPriorityHigh)
	}
	// Pretext lists all three buckets in spike order: running, failed, building.
	r := strings.Index(att.Pretext, "running")
	f := strings.Index(att.Pretext, "failed")
	b := strings.Index(att.Pretext, "building")
	if r < 0 || f < 0 || b < 0 {
		t.Fatalf("pretext missing one of running/failed/building: %q", att.Pretext)
	}
	if !(r < f && f < b) {
		t.Errorf("pretext order want running<failed<building, got %q", att.Pretext)
	}
	// Status chip uses red_circle for failed apps in the table row.
	if !strings.Contains(att.Text, ":red_circle: failed") {
		t.Errorf("failed chip missing from table: %q", att.Text)
	}
}

func TestAppsOverview_MixedWithoutFailed(t *testing.T) {
	att, err := renderEnvelopeAt(appsListRaw(t, appsListPayload{
		Total: 2,
		Apps: []appSummary{
			mkApp("webapp", "running", "main", "2026-05-14T15:30:00Z"),
			mkApp("api", "building", "main", "2026-05-15T11:00:00Z"),
		},
	}), appsOverviewClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorPriorityMedium {
		t.Errorf("color: got %q want %q (mixed-no-failed ⇒ medium amber)", att.Color, colorPriorityMedium)
	}
}

func TestAppsOverview_NullableFields(t *testing.T) {
	// repository / lastDeployedAt / lastDeployCommit are all CLI-nullable per
	// AppSummary schema. The renderer must collapse them to "—" without
	// crashing.
	att, err := renderEnvelopeAt(appsListRaw(t, appsListPayload{
		Total: 1,
		Apps: []appSummary{
			{ID: "app_x", Name: "ghost", Status: "stopped", Branch: ""}, // no nullables set
		},
	}), appsOverviewClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Branch="" must render as "—" in the table.
	if !strings.Contains(att.Text, "| `ghost` | — | — |") {
		t.Errorf("nullable cells should dash: %q", att.Text)
	}
}

func TestAppsOverview_LastDeployValueFormat(t *testing.T) {
	// "Last deploy" cell is `YYYY-MM-DD HH:MM (rel)` per spike §B.6.3.
	att, err := renderEnvelopeAt(appsListRaw(t, appsListPayload{
		Total: 1,
		Apps: []appSummary{
			mkApp("webapp", "running", "main", "2026-05-14T15:30:00Z"),
		},
	}), appsOverviewClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "2026-05-14 15:30 (20h ago)"
	if !strings.Contains(att.Text, want) {
		t.Errorf("last-deploy format mismatch: want substring %q, got %q", want, att.Text)
	}
}

func TestAppsOverview_RowCap(t *testing.T) {
	apps := make([]appSummary, 0, 25)
	for i := 0; i < 25; i++ {
		apps = append(apps, mkApp("a"+string(rune('A'+i%26)), "running", "main", ""))
	}
	att, err := renderEnvelopeAt(appsListRaw(t, appsListPayload{Total: 25, Apps: apps}), appsOverviewClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Footer must announce the truncation; total stays at 25.
	if !strings.Contains(att.Footer, "total=25") {
		t.Errorf("footer must keep true total: %q", att.Footer)
	}
	if !strings.Contains(att.Footer, "showing first 20") {
		t.Errorf("footer must announce truncation: %q", att.Footer)
	}
	// Table renders exactly 20 data rows (count "|---" only appears once in
	// the divider, so count `|` `\n` `|` data-row separators instead).
	dataRows := strings.Count(att.Text, "\n| ") // each data row prefix
	if dataRows != 20 {
		t.Errorf("rendered %d data rows, want 20", dataRows)
	}
}

func TestAppsOverview_BusinessError_BackendUnavailable(t *testing.T) {
	// envelope.error.code path → §0.5 + §B.6.5: colorError card, Refresh
	// button preserved so the user can retry once the backend recovers.
	raw := []byte(`{"success":true,"data":{"schema_version":1,"verb":"apps.list","error":{"code":"backend_unavailable","message":"deploy daemon offline"}}}`)
	att, err := renderEnvelopeAt(raw, appsOverviewClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorError {
		t.Errorf("color: got %q want %q", att.Color, colorError)
	}
	if !strings.Contains(att.Title, "apps.list — error") {
		t.Errorf("title: %q", att.Title)
	}
	if !strings.Contains(att.Text, "backend_unavailable") || !strings.Contains(att.Text, "deploy daemon offline") {
		t.Errorf("text: %q", att.Text)
	}
	names := actionNames(att.Actions)
	if !sliceContains(names, "Refresh") {
		t.Errorf("error card should keep Refresh button, got %v", names)
	}
	if att.Footer != "fulcrum/apps.list · schema_version=1" {
		t.Errorf("error footer: %q", att.Footer)
	}
}

func TestAppsOverview_RefreshActionArgvWires(t *testing.T) {
	att, err := renderEnvelopeAt(appsListRaw(t, appsListPayload{
		Total: 1,
		Apps:  []appSummary{mkApp("webapp", "running", "main", "")},
	}), appsOverviewClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(att.Actions) != 1 {
		t.Fatalf("apps-overview actions: got %d want 1 (Refresh only)", len(att.Actions))
	}
	a := att.Actions[0]
	if a.Id != "apps_overview_refresh" {
		t.Errorf("action id: %q", a.Id)
	}
	if a.Style != postActionStyleDefault {
		t.Errorf("action style: %q", a.Style)
	}
	ctx, ok := a.Integration.Context[actionContextArgvKey].([]any)
	if !ok {
		t.Fatalf("argv context missing: %#v", a.Integration.Context)
	}
	argv := make([]string, len(ctx))
	for i, v := range ctx {
		argv[i] = v.(string)
	}
	if !equalStringSlice(argv, []string{"apps", "list"}) {
		t.Errorf("argv = %v, want [apps list]", argv)
	}
	if a.Integration.URL != "/plugins/"+manifestID+"/action" {
		t.Errorf("action url: %q", a.Integration.URL)
	}
	// Apps-overview has no dialog button — verify the dialog flag is absent.
	if _, hasDialog := a.Integration.Context[actionContextDialogKey]; hasDialog {
		t.Errorf("Refresh must not carry dialog flag")
	}
}
