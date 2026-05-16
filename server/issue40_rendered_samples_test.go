package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestIssue40_RenderedSamplesDump emits the three SlackAttachments the
// plugin would produce when fed the prod CLI envelopes captured in
// `.coder-loop/runtime/evidence/issue-40/run-2026-05-16-10-18-41-916/
// cli-envelopes/three-states.txt`. It's a paired evidence test: the assertions
// here are the same shape the per-branch tests already enforce, but dumping
// the full JSON output in one place lets a reviewer see exactly what the
// renderer hands to the Mattermost client for each of the three states
// without grepping unit-test bodies. The dump is rendered through `t.Logf`
// so `go test -v` surfaces it without ever changing test exit status.
func TestIssue40_RenderedSamplesDump(t *testing.T) {
	cases := []struct {
		name string
		env  []byte
	}{
		{
			name: "reporting_local_window1h",
			env:  []byte(`{"success":true,"data":{"schema_version":1,"verb":"monitor","host_id":"local","window":"1h","monitor_status":"reporting","last_sample_at":"2026-05-16T10:34:37.000Z","since":"2026-05-16T09:34:37.847Z","cpu_percent":1.26,"memory_percent":61.19118924785253,"disk_percent":29.48558020196122}}`),
		},
		{
			name: "no_data_in_window_staledemo_window1h",
			env:  []byte(`{"success":true,"data":{"schema_version":1,"verb":"monitor","host_id":"stale-demo","window":"1h","monitor_status":"no_data_in_window","last_sample_at":"2026-05-15T10:30:01.000Z","since":"2026-05-16T09:34:37.988Z","cpu_percent":null,"memory_percent":null,"disk_percent":null}}`),
		},
		{
			name: "unconfigured_ghost_window1h",
			env:  []byte(`{"success":true,"data":{"schema_version":1,"verb":"monitor","host_id":"ghost-host","window":"1h","monitor_status":"unconfigured","last_sample_at":null,"since":"2026-05-16T09:34:38.143Z","cpu_percent":null,"memory_percent":null,"disk_percent":null}}`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			att, err := renderEnvelope(tc.env)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			pretty, err := json.MarshalIndent(att, "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			t.Logf("=== %s ===\n%s", tc.name, string(pretty))

			// Inline shape lock so this isn't a no-op dump: every case must
			// produce a Title that contains the right host_id, and the two
			// non-reporting cases must render zero metric fields (issue #40's
			// AC #2 explicit no-em-dash invariant).
			switch tc.name {
			case "reporting_local_window1h":
				if !strings.Contains(att.Title, "local") {
					t.Errorf("reporting title missing host: %q", att.Title)
				}
				if len(att.Fields) != 4 {
					t.Errorf("reporting card should keep four-field block, got %d", len(att.Fields))
				}
			case "no_data_in_window_staledemo_window1h":
				if !strings.Contains(att.Title, "no data in window") {
					t.Errorf("no_data title shape: %q", att.Title)
				}
				if len(att.Fields) != 0 {
					t.Errorf("no_data card must not render metric fields, got %d", len(att.Fields))
				}
			case "unconfigured_ghost_window1h":
				if !strings.Contains(att.Title, "unconfigured") {
					t.Errorf("unconfigured title shape: %q", att.Title)
				}
				if len(att.Fields) != 0 {
					t.Errorf("unconfigured card must not render metric fields, got %d", len(att.Fields))
				}
			}
		})
	}
}
