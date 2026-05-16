package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// monitorRaw assembles a `fulcrum monitor --json` envelope from a monitor
// payload so schema drift surfaces as a compile failure here rather than in
// renderMonitor. Tests pass *float64 pointers explicitly so the partial branch
// (nil disk_percent) is exercised by the same helper as the complete branch.
func monitorRaw(t *testing.T, p monitorPayload) []byte {
	t.Helper()
	body := struct {
		monitorPayload
		SchemaVersion int    `json:"schema_version"`
		Verb          string `json:"verb"`
	}{
		monitorPayload: p,
		SchemaVersion:  1,
		Verb:           "monitor",
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

// monitorErrRaw assembles an envelope-error response for the monitor verb so
// the §B.10.5 colorError render path is reachable from the same dispatcher
// the production code uses.
func monitorErrRaw(t *testing.T, code, message string) []byte {
	t.Helper()
	data := map[string]any{
		"schema_version": 1,
		"verb":           "monitor",
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

func pct(v float64) *float64 { return &v }

// statusPtr is the *monitorStatus helper used by the issue #40 three-state
// testcases. Kept tiny so each test reads as `MonitorStatus: statusPtr("...")`
// rather than dragging a named local around.
func statusPtr(s monitorStatus) *monitorStatus { return &s }


func fieldValue(t *testing.T, fields []*model.SlackAttachmentField, title string) string {
	t.Helper()
	for _, f := range fields {
		if f.Title == title {
			s, ok := f.Value.(string)
			if !ok {
				t.Fatalf("field %q is not a string: %#v", title, f.Value)
			}
			return s
		}
	}
	t.Fatalf("field %q not found in %#v", title, fields)
	return ""
}

func TestMonitor_HealthyAllLow(t *testing.T) {
	att, err := renderEnvelope(monitorRaw(t, monitorPayload{
		HostID:        "host-vctcn",
		Window:        "1h",
		CPUPercent:    pct(12.5),
		MemoryPercent: pct(40.2),
		DiskPercent:   pct(55),
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorStatusDone {
		t.Errorf("color: got %q want %q (all metrics < 70)", att.Color, colorStatusDone)
	}
	if att.Title != "Monitor · host-vctcn (window=1h)" {
		t.Errorf("title: %q", att.Title)
	}
	if att.Pretext != "cpu 12.5% · mem 40.2% · disk 55%" {
		t.Errorf("pretext: %q", att.Pretext)
	}
	if att.Text != "" {
		t.Errorf("text should be empty when no metric crosses 90%%: %q", att.Text)
	}
	if att.Footer != "fulcrum/monitor · host=host-vctcn" {
		t.Errorf("footer: %q", att.Footer)
	}
	if got := fieldValue(t, att.Fields, "CPU"); got != "12.5%" {
		t.Errorf("CPU field: %q", got)
	}
	if got := fieldValue(t, att.Fields, "Memory"); got != "40.2%" {
		t.Errorf("Memory field: %q", got)
	}
	if got := fieldValue(t, att.Fields, "Disk"); got != "55%" {
		t.Errorf("Disk field: %q", got)
	}
	if got := fieldValue(t, att.Fields, "Window"); got != "1h" {
		t.Errorf("Window field: %q", got)
	}
	names := actionNames(att.Actions)
	if !equalStringSlice(names, []string{"Refresh"}) {
		t.Errorf("actions: %v, want [Refresh] only on healthy card", names)
	}
}

func TestMonitor_MediumBand(t *testing.T) {
	att, err := renderEnvelope(monitorRaw(t, monitorPayload{
		HostID:        "h1",
		Window:        "1h",
		CPUPercent:    pct(72),
		MemoryPercent: pct(85),
		DiskPercent:   pct(40),
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorPriorityMedium {
		t.Errorf("color: got %q want %q (max 85 ⇒ medium band)", att.Color, colorPriorityMedium)
	}
	if att.Text != "" {
		t.Errorf("text must be empty below the 90%% high threshold: %q", att.Text)
	}
	if names := actionNames(att.Actions); !equalStringSlice(names, []string{"Refresh"}) {
		t.Errorf("medium band must not show View jobs / View apps: %v", names)
	}
}

func TestMonitor_HighCPUTriggersWarningAndAppsButton(t *testing.T) {
	att, err := renderEnvelope(monitorRaw(t, monitorPayload{
		HostID:        "h1",
		Window:        "1h",
		CPUPercent:    pct(95),
		MemoryPercent: pct(50),
		DiskPercent:   pct(40),
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorPriorityHigh {
		t.Errorf("color: got %q want %q (cpu 95 crosses high)", att.Color, colorPriorityHigh)
	}
	if !strings.HasPrefix(att.Text, ":warning: CPU usage is high (95%)") {
		t.Errorf("text: %q", att.Text)
	}
	if !strings.Contains(att.Text, "/f apps logs") || !strings.Contains(att.Text, "/f jobs") {
		t.Errorf("text must reference both triage commands: %q", att.Text)
	}
	if names := actionNames(att.Actions); !equalStringSlice(names, []string{"Refresh", "View jobs", "View apps"}) {
		t.Errorf("actions: %v", names)
	}
}

func TestMonitor_HighDiskTriggersJobsButtonOnly(t *testing.T) {
	att, err := renderEnvelope(monitorRaw(t, monitorPayload{
		HostID:        "h1",
		Window:        "1h",
		CPUPercent:    pct(20),
		MemoryPercent: pct(30),
		DiskPercent:   pct(96),
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorPriorityHigh {
		t.Errorf("color: got %q want %q (disk 96 crosses high)", att.Color, colorPriorityHigh)
	}
	if !strings.Contains(att.Text, "Disk usage is high (96%)") {
		t.Errorf("warning must name Disk: %q", att.Text)
	}
	if names := actionNames(att.Actions); !equalStringSlice(names, []string{"Refresh", "View jobs"}) {
		t.Errorf("disk-only high should expose Refresh + View jobs (no View apps): %v", names)
	}
}

func TestMonitor_HighMemoryWarningPicksMemoryNotCPU(t *testing.T) {
	att, err := renderEnvelope(monitorRaw(t, monitorPayload{
		HostID:        "h1",
		Window:        "5m",
		CPUPercent:    pct(91),
		MemoryPercent: pct(98),
		DiskPercent:   pct(20),
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(att.Text, "Memory usage is high (98%)") {
		t.Errorf("highest metric should win the warning slot: %q", att.Text)
	}
	if names := actionNames(att.Actions); !equalStringSlice(names, []string{"Refresh", "View jobs", "View apps"}) {
		t.Errorf("actions: %v", names)
	}
}

func TestMonitor_PartialDiskMissing(t *testing.T) {
	att, err := renderEnvelope(monitorRaw(t, monitorPayload{
		HostID:        "h1",
		Window:        "1h",
		CPUPercent:    pct(12),
		MemoryPercent: pct(40),
		DiskPercent:   nil,
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorWarn {
		t.Errorf("partial card color: got %q want %q", att.Color, colorWarn)
	}
	if att.Pretext != "cpu 12% · mem 40% · disk —" {
		t.Errorf("pretext on partial: %q", att.Pretext)
	}
	if got := fieldValue(t, att.Fields, "Disk"); got != "—" {
		t.Errorf("Disk field on partial: %q", got)
	}
	if att.Text != "" {
		t.Errorf("partial card without a high metric must have empty text, got %q", att.Text)
	}
	if names := actionNames(att.Actions); !equalStringSlice(names, []string{"Refresh", "View jobs"}) {
		t.Errorf("partial card must surface View jobs but not View apps (cpu/mem are low): %v", names)
	}
}

func TestMonitor_PartialAndHighCPU(t *testing.T) {
	att, err := renderEnvelope(monitorRaw(t, monitorPayload{
		HostID:        "h1",
		Window:        "1h",
		CPUPercent:    pct(94),
		MemoryPercent: pct(20),
		DiskPercent:   nil,
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorWarn {
		t.Errorf("partial branch overrides max-based color even with a high cpu: got %q want %q", att.Color, colorWarn)
	}
	if !strings.Contains(att.Text, "CPU usage is high (94%)") {
		t.Errorf("partial+high should still emit the warning line for the present metric: %q", att.Text)
	}
	if names := actionNames(att.Actions); !equalStringSlice(names, []string{"Refresh", "View jobs", "View apps"}) {
		t.Errorf("partial+high cpu actions: %v", names)
	}
}

func TestMonitor_RefreshArgvLock(t *testing.T) {
	att, err := renderEnvelope(monitorRaw(t, monitorPayload{
		HostID:        "h1",
		Window:        "1h",
		CPUPercent:    pct(10),
		MemoryPercent: pct(20),
		DiskPercent:   pct(30),
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Actions[0].Name != "Refresh" {
		t.Fatalf("first action must be Refresh: %+v", att.Actions[0])
	}
	argv := actionArgvList(t, att.Actions[0])
	if !equalStringSlice(argv, []string{"monitor"}) {
		t.Errorf("Refresh argv: %v want [monitor]", argv)
	}
}

func TestMonitor_ViewJobsArgvLock(t *testing.T) {
	att, err := renderEnvelope(monitorRaw(t, monitorPayload{
		HostID:        "h1",
		Window:        "1h",
		CPUPercent:    pct(95),
		MemoryPercent: pct(20),
		DiskPercent:   pct(30),
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	found := false
	for _, a := range att.Actions {
		if a.Name == "View jobs" {
			found = true
			argv := actionArgvList(t, a)
			if !equalStringSlice(argv, []string{"jobs", "--scope=all"}) {
				t.Errorf("View jobs argv: %v want [jobs --scope=all]", argv)
			}
		}
	}
	if !found {
		t.Fatal("View jobs button missing on high-cpu card")
	}
}

func TestMonitor_ViewAppsArgvLock(t *testing.T) {
	att, err := renderEnvelope(monitorRaw(t, monitorPayload{
		HostID:        "h1",
		Window:        "1h",
		CPUPercent:    pct(95),
		MemoryPercent: pct(20),
		DiskPercent:   pct(30),
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, a := range att.Actions {
		if a.Name == "View apps" {
			argv := actionArgvList(t, a)
			if !equalStringSlice(argv, []string{"apps", "list"}) {
				t.Errorf("View apps argv: %v want [apps list]", argv)
			}
			return
		}
	}
	t.Fatal("View apps button missing on high-cpu card")
}

func TestMonitor_BusinessErrorMonitorUnavailable(t *testing.T) {
	att, err := renderEnvelope(monitorErrRaw(t, "monitor_unavailable", "host agent has not reported in 5m"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorError {
		t.Errorf("error color: got %q want %q", att.Color, colorError)
	}
	if att.Title != "fulcrum monitor — error" {
		t.Errorf("error title: %q", att.Title)
	}
	if !strings.Contains(att.Text, "`monitor_unavailable`") {
		t.Errorf("error text must embed code: %q", att.Text)
	}
	if !strings.Contains(att.Text, "host agent has not reported in 5m") {
		t.Errorf("error text must embed message: %q", att.Text)
	}
	if names := actionNames(att.Actions); !equalStringSlice(names, []string{"Refresh"}) {
		t.Errorf("error card must keep Refresh: %v", names)
	}
	if att.Footer != "fulcrum/monitor · schema_version=1" {
		t.Errorf("error footer: %q", att.Footer)
	}
}

func TestMonitor_FormatPercentTrimsTrailingZero(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{12.5, "12.5"},
		{40.0, "40"},
		{99.9, "99.9"},
		{0.0, "0"},
		{100.0, "100"},
	}
	for _, tc := range cases {
		got := monitorFormatPercent(tc.in)
		if got != tc.want {
			t.Errorf("monitorFormatPercent(%v) = %q want %q", tc.in, got, tc.want)
		}
	}
}

// TestMonitor_Unconfigured locks the issue #40 unconfigured branch: the
// envelope tells us the host has never reported, so the renderer must drop
// the four-slot metric field block entirely (em-dash slots would re-introduce
// the exact ambiguity #40 was filed to fix), surface a human-readable
// "backend not installed" pretext, and keep Refresh as the only action — the
// jobs / apps triage pivots make no sense on a host that has no collector.
func TestMonitor_Unconfigured(t *testing.T) {
	att, err := renderEnvelope(monitorRaw(t, monitorPayload{
		HostID:        "ghost-host",
		Window:        "1h",
		MonitorStatus: statusPtr(monitorStatusUnconfigured),
		LastSampleAt:  nil,
		Since:         "2026-05-16T09:30:00Z",
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorWarn {
		t.Errorf("color: got %q want %q (unconfigured ⇒ colorWarn)", att.Color, colorWarn)
	}
	if att.Title != "Monitor · ghost-host — unconfigured" {
		t.Errorf("title: %q", att.Title)
	}
	if !strings.Contains(att.Pretext, "Monitor backend not installed on host `ghost-host`.") {
		t.Errorf("pretext should name the host + 'not installed', got %q", att.Pretext)
	}
	if !strings.Contains(att.Text, "fulcrum monitor install") {
		t.Errorf("text should reference the install command, got %q", att.Text)
	}
	if len(att.Fields) != 0 {
		t.Errorf("unconfigured card must not render the CPU/Memory/Disk/Window field block (would re-introduce ambiguous em-dashes), got %d fields", len(att.Fields))
	}
	if att.Footer != "fulcrum/monitor · host=ghost-host · status=unconfigured" {
		t.Errorf("footer: %q", att.Footer)
	}
	if names := actionNames(att.Actions); !equalStringSlice(names, []string{"Refresh"}) {
		t.Errorf("unconfigured card must show only Refresh (no jobs/apps pivot — there is no collector), got %v", names)
	}
	argv := actionArgvList(t, att.Actions[0])
	if !equalStringSlice(argv, []string{"monitor"}) {
		t.Errorf("Refresh argv on unconfigured card: %v want [monitor]", argv)
	}
}

// TestMonitor_NoDataInWindow locks the issue #40 no-data-in-window branch:
// the host has historical samples but none in the current window, so the
// renderer surfaces the window lower bound and the last sample timestamp so
// the operator can decide between "wait longer" / "widen window" / "the
// agent stopped" without leaving the card.
func TestMonitor_NoDataInWindow(t *testing.T) {
	att, err := renderEnvelope(monitorRaw(t, monitorPayload{
		HostID:        "stale-host",
		Window:        "1h",
		MonitorStatus: statusPtr(monitorStatusNoDataInWindow),
		LastSampleAt:  strPtr("2026-05-15T09:32:51Z"),
		Since:         "2026-05-16T08:32:51Z",
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorWarn {
		t.Errorf("color: got %q want %q (no_data_in_window ⇒ colorWarn)", att.Color, colorWarn)
	}
	if att.Title != "Monitor · stale-host — no data in window" {
		t.Errorf("title: %q", att.Title)
	}
	if !strings.Contains(att.Pretext, "No samples for host `stale-host` in the last 1h.") {
		t.Errorf("pretext should name the host + window, got %q", att.Pretext)
	}
	if !strings.Contains(att.Text, "2026-05-16T08:32:51Z") {
		t.Errorf("text must surface the `since` timestamp, got %q", att.Text)
	}
	if !strings.Contains(att.Text, "2026-05-15T09:32:51Z") {
		t.Errorf("text must surface the last_sample_at timestamp, got %q", att.Text)
	}
	if len(att.Fields) != 0 {
		t.Errorf("no-data-in-window card must not render the metric field block (would re-introduce ambiguous em-dashes), got %d fields", len(att.Fields))
	}
	if att.Footer != "fulcrum/monitor · host=stale-host · status=no_data_in_window" {
		t.Errorf("footer: %q", att.Footer)
	}
	if names := actionNames(att.Actions); !equalStringSlice(names, []string{"Refresh"}) {
		t.Errorf("no-data-in-window card must show only Refresh, got %v", names)
	}
}

// TestMonitor_NoDataInWindow_LastSampleNever covers the defensive `never`
// fallback: the upstream contract emits last_sample_at as a non-null ISO
// string for no_data_in_window today, but the field is typed as nullable, so
// a stricter older server that emits null should not crash the renderer.
func TestMonitor_NoDataInWindow_LastSampleNever(t *testing.T) {
	att, err := renderEnvelope(monitorRaw(t, monitorPayload{
		HostID:        "edge-case-host",
		Window:        "30m",
		MonitorStatus: statusPtr(monitorStatusNoDataInWindow),
		LastSampleAt:  nil,
		Since:         "2026-05-16T09:00:00Z",
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(att.Text, "Last sample: never") {
		t.Errorf("text should fall back to `never` for null last_sample_at, got %q", att.Text)
	}
}

// TestMonitor_Reporting_WithStatus pins that an explicit `reporting` status
// envelope still routes through the §B.10 four-branch render — the new
// discriminator opts in to the branch dispatch but does not replace the
// utilization-based render for the reporting case (issue #40 AC #4: do not
// regress the reporting golden).
func TestMonitor_Reporting_WithStatus(t *testing.T) {
	att, err := renderEnvelope(monitorRaw(t, monitorPayload{
		HostID:        "vctcn-app1",
		Window:        "1h",
		MonitorStatus: statusPtr(monitorStatusReporting),
		LastSampleAt:  strPtr("2026-05-16T10:00:00Z"),
		Since:         "2026-05-16T09:00:00Z",
		CPUPercent:    pct(22.76),
		MemoryPercent: pct(76.98),
		DiskPercent:   pct(93.46),
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorPriorityHigh {
		t.Errorf("color: got %q want %q (disk 93.46 crosses high)", att.Color, colorPriorityHigh)
	}
	if att.Title != "Monitor · vctcn-app1 (window=1h)" {
		t.Errorf("title: %q", att.Title)
	}
	if !strings.Contains(att.Pretext, "cpu 22.8% · mem 77% · disk 93.5%") {
		t.Errorf("pretext should preserve the §B.10 metric summary, got %q", att.Pretext)
	}
	if len(att.Fields) != 4 {
		t.Fatalf("reporting card must keep the four-field block, got %d", len(att.Fields))
	}
	if got := fieldValue(t, att.Fields, "Disk"); got != "93.5%" {
		t.Errorf("Disk field on explicit-reporting card: %q", got)
	}
	if !strings.Contains(att.Text, "Disk usage is high (93.5%)") {
		t.Errorf("warning text on explicit-reporting card: %q", att.Text)
	}
	if att.Footer != "fulcrum/monitor · host=vctcn-app1" {
		t.Errorf("footer must remain the §B.10 form (status is not appended on the reporting card): %q", att.Footer)
	}
	if names := actionNames(att.Actions); !equalStringSlice(names, []string{"Refresh", "View jobs"}) {
		t.Errorf("disk-only high actions: %v", names)
	}
}

// TestMonitor_LegacyEnvelopeFallback locks the issue #40 back-compat
// requirement: a pre-fulcrum#234 CLI envelope (no monitor_status field) must
// still render via the §B.10 four-branch path — the new plugin must not
// crash, must not classify it as unconfigured, and must produce the same
// em-dash-shaped card today's deployed plugin does. The existing §B.10
// testcases above are an implicit cover for this path (they all omit
// MonitorStatus), but this test is the explicit lock so the back-compat
// invariant is named in the test file.
func TestMonitor_LegacyEnvelopeFallback(t *testing.T) {
	att, err := renderEnvelope(monitorRaw(t, monitorPayload{
		HostID:        "legacy-cli-host",
		Window:        "1h",
		MonitorStatus: nil, // pre-#234 envelope omits the discriminator
		LastSampleAt:  nil,
		Since:         "",
		CPUPercent:    nil,
		MemoryPercent: nil,
		DiskPercent:   nil, // legacy all-null envelope ⇒ §B.10 partial branch
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorWarn {
		t.Errorf("legacy all-null envelope must land on §B.10 partial (colorWarn), got %q", att.Color)
	}
	if att.Title != "Monitor · legacy-cli-host (window=1h)" {
		t.Errorf("legacy envelope must keep the §B.10 title form (no `— unconfigured` suffix): %q", att.Title)
	}
	if att.Pretext != "cpu — · mem — · disk —" {
		t.Errorf("legacy envelope must keep the §B.10 em-dash pretext for back-compat: %q", att.Pretext)
	}
	if len(att.Fields) != 4 {
		t.Errorf("legacy envelope must keep the four-field block (back-compat with today's prod card): got %d", len(att.Fields))
	}
}

func TestMonitor_FieldsAreShort(t *testing.T) {
	att, err := renderEnvelope(monitorRaw(t, monitorPayload{
		HostID:        "h",
		Window:        "1h",
		CPUPercent:    pct(1),
		MemoryPercent: pct(1),
		DiskPercent:   pct(1),
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(att.Fields) != 4 {
		t.Fatalf("expected 4 fields, got %d", len(att.Fields))
	}
	for _, f := range att.Fields {
		if !f.Short {
			t.Errorf("field %q should be short=true for the two-column layout", f.Title)
		}
	}
}

// actionArgvList extracts argv from a PostAction's Integration.Context for
// argv-lock testcases. Mirrors search_test.go's pattern of decoding the
// []any → []string round-trip without leaking JSON-encoding details into
// every testcase.
func actionArgvList(t *testing.T, a *model.PostAction) []string {
	t.Helper()
	if a.Integration == nil {
		t.Fatalf("action %q has no Integration", a.Name)
	}
	ctx := a.Integration.Context
	raw, ok := ctx[actionContextArgvKey]
	if !ok {
		t.Fatalf("action %q context missing %q", a.Name, actionContextArgvKey)
	}
	list, ok := raw.([]any)
	if !ok {
		t.Fatalf("action %q context.%s not []any: %#v", a.Name, actionContextArgvKey, raw)
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("action %q argv item not string: %#v", a.Name, v)
		}
		out = append(out, s)
	}
	return out
}
