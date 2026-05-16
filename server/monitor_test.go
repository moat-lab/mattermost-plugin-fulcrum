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
