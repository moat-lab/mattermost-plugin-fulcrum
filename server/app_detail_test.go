package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// appDetailClock is the wall clock used by every app-detail test so the
// "Last deploy" relative-time column and mutation-result footer timestamp
// are deterministic across runs.
var appDetailClock = time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)

// appDetailRaw assembles a `fulcrum apps get --json` envelope from a payload
// struct so schema drift surfaces as a compile failure here rather than in
// renderAppDetail.
func appDetailRaw(t *testing.T, p appGetPayload) []byte {
	t.Helper()
	body := struct {
		appGetPayload
		SchemaVersion int    `json:"schema_version"`
		Verb          string `json:"verb"`
	}{
		appGetPayload: p,
		SchemaVersion: 1,
		Verb:          "apps.get",
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

// sampleApp is a fully-populated app summary used by the running-status test.
// Other status branches mutate copies of it.
func sampleApp() appSummary {
	repo := "Mouriya-Emma/example"
	deployedAt := "2026-05-15T07:00:00Z"
	commit := "abcdef0123456789"
	return appSummary{
		ID:                "app_42",
		Name:              "webapp",
		Status:            "running",
		Branch:            "main",
		Repository:        &repo,
		LastDeployedAt:    &deployedAt,
		LastDeployCommit:  &commit,
		AutoDeployEnabled: true,
	}
}

func TestRenderAppDetail_Running_FullCard(t *testing.T) {
	svcStatus := "running"
	att, err := renderEnvelopeAt(appDetailRaw(t, appGetPayload{
		App: sampleApp(),
		Services: []appService{
			{ServiceName: "web", Status: &svcStatus},
			{ServiceName: "worker", Status: &svcStatus},
		},
	}), appDetailClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorStatusDoing {
		t.Errorf("color: got %q want %q", att.Color, colorStatusDoing)
	}
	wantTitle := ":large_blue_circle: running App · webapp"
	if att.Title != wantTitle {
		t.Errorf("title:\n got: %q\nwant: %q", att.Title, wantTitle)
	}
	if att.Pretext != "app ID `app_42`" {
		t.Errorf("pretext: %q", att.Pretext)
	}
	if att.Footer != "fulcrum/apps.get · status=running" {
		t.Errorf("footer: %q", att.Footer)
	}

	// Fields[]: 6 entries in §B.7.3 order.
	if len(att.Fields) != 6 {
		t.Fatalf("fields len: got %d want 6", len(att.Fields))
	}
	if att.Fields[0].Title != "Status" || !strings.Contains(fieldStr(t, att.Fields[0]), "running") {
		t.Errorf("Status field: %+v", att.Fields[0])
	}
	if att.Fields[1].Title != "Branch" || fieldStr(t, att.Fields[1]) != "`main`" {
		t.Errorf("Branch field: %+v", att.Fields[1])
	}
	if att.Fields[2].Title != "Repository" || fieldStr(t, att.Fields[2]) != "Mouriya-Emma/example" {
		t.Errorf("Repository field: %+v", att.Fields[2])
	}
	if att.Fields[3].Title != "Auto-deploy" || fieldStr(t, att.Fields[3]) != ":white_check_mark: on" {
		t.Errorf("Auto-deploy field: %+v", att.Fields[3])
	}
	if att.Fields[4].Title != "Last deploy" || fieldStr(t, att.Fields[4]) != "5h ago" {
		t.Errorf("Last deploy field: %+v", att.Fields[4])
	}
	if att.Fields[5].Title != "Last commit" || fieldStr(t, att.Fields[5]) != "`abcdef0`" {
		t.Errorf("Last commit field: %+v", att.Fields[5])
	}

	// Services block.
	if !strings.Contains(att.Text, "**Services** (2):") {
		t.Errorf("services header missing: %q", att.Text)
	}
	if !strings.Contains(att.Text, "- `web` · running") {
		t.Errorf("web service line missing: %q", att.Text)
	}
	if !strings.Contains(att.Text, "- `worker` · running") {
		t.Errorf("worker service line missing: %q", att.Text)
	}

	// Action set per §B.7.4 running row: Deploy(primary), Stop(danger,dialog),
	// Tail logs(default), Refresh(default).
	if len(att.Actions) != 4 {
		t.Fatalf("actions len: got %d want 4", len(att.Actions))
	}
	assertActionArgv(t, att.Actions[0], "app_deploy", postActionStylePrimary, []string{"apps", "deploy", "app_42"}, false)
	assertActionArgv(t, att.Actions[1], "app_stop", postActionStyleDanger, []string{"apps", "stop", "app_42"}, true)
	assertActionArgv(t, att.Actions[2], "app_tail_logs", postActionStyleDefault, []string{"apps", "logs", "app_42", "--tail=200"}, false)
	assertActionArgv(t, att.Actions[3], "app_refresh", postActionStyleDefault, []string{"apps", "get", "app_42"}, false)
}

func TestRenderAppDetail_Building_NoMutationButtons(t *testing.T) {
	app := sampleApp()
	app.Status = "building"
	att, err := renderEnvelopeAt(appDetailRaw(t, appGetPayload{App: app}), appDetailClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorStatusReview {
		t.Errorf("building color: got %q want %q", att.Color, colorStatusReview)
	}
	names := actionNames(att.Actions)
	if sliceContains(names, "Deploy") || sliceContains(names, "Stop") {
		t.Errorf("building must not expose Deploy/Stop, got %v", names)
	}
	if !sliceContains(names, "Refresh") || !sliceContains(names, "Tail logs") {
		t.Errorf("building must expose Refresh + Tail logs, got %v", names)
	}
	if len(att.Actions) != 2 {
		t.Errorf("building actions len: got %d want 2", len(att.Actions))
	}
}

func TestRenderAppDetail_Failed_DeployRetryButton(t *testing.T) {
	app := sampleApp()
	app.Status = "failed"
	att, err := renderEnvelopeAt(appDetailRaw(t, appGetPayload{App: app}), appDetailClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorPriorityHigh {
		t.Errorf("failed color: got %q want %q", att.Color, colorPriorityHigh)
	}
	names := actionNames(att.Actions)
	if !sliceContains(names, "Deploy") || !sliceContains(names, "Tail logs") || !sliceContains(names, "Refresh") {
		t.Errorf("failed must expose Deploy + Tail logs + Refresh, got %v", names)
	}
	if sliceContains(names, "Stop") {
		t.Errorf("failed must NOT expose Stop, got %v", names)
	}
}

func TestRenderAppDetail_Stopped_DeployRestartOnly(t *testing.T) {
	app := sampleApp()
	app.Status = "stopped"
	att, err := renderEnvelopeAt(appDetailRaw(t, appGetPayload{App: app}), appDetailClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorStatusTODO {
		t.Errorf("stopped color: got %q want %q", att.Color, colorStatusTODO)
	}
	names := actionNames(att.Actions)
	if !equalStringSlice(names, []string{"Deploy", "Refresh"}) {
		t.Errorf("stopped action set: got %v want [Deploy, Refresh]", names)
	}
}

func TestRenderAppDetail_Pending_RefreshAndTailLogs(t *testing.T) {
	app := sampleApp()
	app.Status = "pending"
	att, err := renderEnvelopeAt(appDetailRaw(t, appGetPayload{App: app}), appDetailClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorStatusReview {
		t.Errorf("pending color: got %q want %q", att.Color, colorStatusReview)
	}
	names := actionNames(att.Actions)
	if !equalStringSlice(names, []string{"Refresh", "Tail logs"}) {
		t.Errorf("pending action set: got %v want [Refresh, Tail logs]", names)
	}
}

func TestRenderAppDetail_NullableFields_Dash(t *testing.T) {
	// repository / lastDeployedAt / lastDeployCommit are CLI-nullable per
	// AppSummary schema. The renderer must collapse them to "—" without
	// crashing. Branch="" is also allowed and should dash.
	att, err := renderEnvelopeAt(appDetailRaw(t, appGetPayload{
		App: appSummary{
			ID:                "app_x",
			Name:              "ghost",
			Status:            "stopped",
			Branch:            "",
			AutoDeployEnabled: false,
		},
	}), appDetailClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if fieldStr(t, att.Fields[1]) != "—" {
		t.Errorf("Branch dash: %q", fieldStr(t, att.Fields[1]))
	}
	if fieldStr(t, att.Fields[2]) != "—" {
		t.Errorf("Repository dash: %q", fieldStr(t, att.Fields[2]))
	}
	if fieldStr(t, att.Fields[3]) != ":x: off" {
		t.Errorf("Auto-deploy off: %q", fieldStr(t, att.Fields[3]))
	}
	if fieldStr(t, att.Fields[4]) != "—" {
		t.Errorf("Last deploy dash: %q", fieldStr(t, att.Fields[4]))
	}
	if fieldStr(t, att.Fields[5]) != "—" {
		t.Errorf("Last commit dash: %q", fieldStr(t, att.Fields[5]))
	}
}

func TestRenderAppDetail_NoServices_NoTextBlock(t *testing.T) {
	att, err := renderEnvelopeAt(appDetailRaw(t, appGetPayload{App: sampleApp()}), appDetailClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Text != "" {
		t.Errorf("empty services should yield empty Text, got %q", att.Text)
	}
}

func TestRenderAppDetail_ServiceWithoutStatus_DashState(t *testing.T) {
	att, err := renderEnvelopeAt(appDetailRaw(t, appGetPayload{
		App: sampleApp(),
		Services: []appService{
			{ServiceName: "migrator"}, // status: nil
		},
	}), appDetailClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(att.Text, "- `migrator` · —") {
		t.Errorf("nil service status should dash: %q", att.Text)
	}
}

func TestRenderAppDetail_BusinessError_AppNotFound(t *testing.T) {
	in := []byte(`{
		"success": true,
		"data": {
			"schema_version": 1,
			"verb": "apps.get",
			"error": { "code": "app_not_found", "message": "app app_999 not found" }
		}
	}`)
	att, err := renderEnvelopeAt(in, appDetailClock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if att.Color != colorError {
		t.Errorf("color: got %q want %q", att.Color, colorError)
	}
	if !strings.Contains(att.Title, "apps.get — error") {
		t.Errorf("title: %q", att.Title)
	}
	if !strings.Contains(att.Text, "app_not_found") {
		t.Errorf("body missing code: %q", att.Text)
	}
}

// ---------------------------------------------------------------------------
// apps.deploy / apps.stop / apps.rollback mutation result tests
// ---------------------------------------------------------------------------

// appMutationRaw assembles a mutation envelope. The CLI emits payload-level
// `success`, `deployment_id`, and `error` (string) — see
// cli/JSON_SCHEMA.md §apps.deploy. Tests build the envelope from these
// fields directly so the string-shaped error is exercised end-to-end.
func appMutationRaw(t *testing.T, verb string, success bool, deploymentID *string, errStr *string) []byte {
	t.Helper()
	payload := map[string]any{
		"schema_version": 1,
		"verb":           verb,
		"success":        success,
		"deployment_id":  nilable(deploymentID),
		"error":          nilable(errStr),
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

// nilable converts a *string into the JSON-friendly any value (nil for nil
// pointer, the dereferenced string otherwise). Used by appMutationRaw so the
// test fixture matches the CLI's `string | null` shape exactly.
func nilable(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestRenderAppMutation_DeploySuccess_WithRollbackButton(t *testing.T) {
	dep := "d_12345"
	in := appMutationRaw(t, "apps.deploy", true, &dep, nil)
	att, err := renderEnvelopeAtForActor(in, appDetailClock, "u_alice")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorStatusDone {
		t.Errorf("color: got %q want %q", att.Color, colorStatusDone)
	}
	if !strings.HasPrefix(att.Title, "Deployed · ") {
		t.Errorf("title prefix: %q", att.Title)
	}
	if !strings.Contains(att.Pretext, "deployment `d_12345`") {
		t.Errorf("pretext: %q", att.Pretext)
	}
	if !strings.Contains(att.Text, "Tail logs to follow progress") {
		t.Errorf("success body missing tip: %q", att.Text)
	}
	// Fields: App ID, Deployment ID, Initiated by.
	if len(att.Fields) != 3 {
		t.Fatalf("fields len: got %d want 3", len(att.Fields))
	}
	if att.Fields[1].Title != "Deployment ID" || fieldStr(t, att.Fields[1]) != "`d_12345`" {
		t.Errorf("Deployment ID field: %+v", att.Fields[1])
	}
	if att.Fields[2].Title != "Initiated by" || fieldStr(t, att.Fields[2]) != "<@u_alice>" {
		t.Errorf("Initiated by field: %+v", att.Fields[2])
	}
	if !strings.HasPrefix(att.Footer, "fulcrum/apps.deploy · ts=") {
		t.Errorf("footer: %q", att.Footer)
	}
	// Actions: Tail logs, Open app detail, Rollback this deployment.
	names := actionNames(att.Actions)
	if !equalStringSlice(names, []string{"Tail logs", "Open app detail", "Rollback this deployment"}) {
		t.Errorf("deploy success action set: %v", names)
	}
	// Rollback button must carry dialog flag + correct argv.
	rb := att.Actions[2]
	if rb.Style != postActionStyleDanger {
		t.Errorf("Rollback style: %q", rb.Style)
	}
	if dlg, _ := rb.Integration.Context[actionContextDialogKey].(bool); !dlg {
		t.Errorf("Rollback must carry dialog flag")
	}
	rawArgv, _ := rb.Integration.Context[actionContextArgvKey].([]any)
	if len(rawArgv) != 4 || rawArgv[0] != "apps" || rawArgv[1] != "rollback" || rawArgv[3] != "d_12345" {
		t.Errorf("Rollback argv: %v", rawArgv)
	}
}

func TestRenderAppMutation_DeployFail_NoRollback(t *testing.T) {
	errMsg := "build failed: image not pushed"
	in := appMutationRaw(t, "apps.deploy", false, nil, &errMsg)
	att, err := renderEnvelopeAtForActor(in, appDetailClock, "u_alice")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorError {
		t.Errorf("color: got %q want %q", att.Color, colorError)
	}
	if !strings.HasPrefix(att.Title, "Deploy failed · ") {
		t.Errorf("title: %q", att.Title)
	}
	if !strings.Contains(att.Pretext, "error: build failed") {
		t.Errorf("pretext: %q", att.Pretext)
	}
	if !strings.Contains(att.Text, "build failed: image not pushed") {
		t.Errorf("body missing error text: %q", att.Text)
	}
	names := actionNames(att.Actions)
	if sliceContains(names, "Rollback this deployment") {
		t.Errorf("deploy fail must NOT expose Rollback, got %v", names)
	}
}

func TestRenderAppMutation_DeployPartial_WarnColor(t *testing.T) {
	// success=true with an error string → §B.7.1 partial branch (colorWarn).
	dep := "d_1"
	warn := "ports remapped: 8080 -> 8081"
	in := appMutationRaw(t, "apps.deploy", true, &dep, &warn)
	att, err := renderEnvelopeAt(in, appDetailClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorWarn {
		t.Errorf("partial color: got %q want %q", att.Color, colorWarn)
	}
	if !strings.HasPrefix(att.Title, "Deployed · ") {
		t.Errorf("partial title (still success-shape): %q", att.Title)
	}
	if !strings.Contains(att.Pretext, "deployment `d_1`") || !strings.Contains(att.Pretext, "error: ports remapped") {
		t.Errorf("partial pretext should carry both deploy id and warning: %q", att.Pretext)
	}
}

func TestRenderAppMutation_StopSuccess_NoDeploymentField(t *testing.T) {
	in := appMutationRaw(t, "apps.stop", true, nil, nil)
	att, err := renderEnvelopeAtForActor(in, appDetailClock, "u_bob")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorStatusDone {
		t.Errorf("color: got %q want %q", att.Color, colorStatusDone)
	}
	if !strings.HasPrefix(att.Title, "Stopped · ") {
		t.Errorf("title: %q", att.Title)
	}
	// Stop never carries Deployment ID — only App ID + Initiated by.
	if len(att.Fields) != 2 {
		t.Fatalf("stop fields len: got %d want 2", len(att.Fields))
	}
	for _, f := range att.Fields {
		if f.Title == "Deployment ID" {
			t.Errorf("stop must not surface Deployment ID, found field %+v", f)
		}
	}
	if att.Fields[1].Title != "Initiated by" || fieldStr(t, att.Fields[1]) != "<@u_bob>" {
		t.Errorf("Initiated by: %+v", att.Fields[1])
	}
	names := actionNames(att.Actions)
	if !equalStringSlice(names, []string{"Tail logs", "Open app detail"}) {
		t.Errorf("stop action set: got %v want [Tail logs, Open app detail]", names)
	}
}

func TestRenderAppMutation_RollbackSuccess_DeploymentIDPresent(t *testing.T) {
	dep := "d_old"
	in := appMutationRaw(t, "apps.rollback", true, &dep, nil)
	att, err := renderEnvelopeAtForActor(in, appDetailClock, "u_carol")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorStatusDone {
		t.Errorf("color: %q", att.Color)
	}
	if !strings.HasPrefix(att.Title, "Rolled back · ") {
		t.Errorf("title: %q", att.Title)
	}
	// Rollback surfaces Deployment ID but no further Rollback button — the
	// rollback's *own* deployment_id is not the kind of identifier users
	// typically rollback to (would just undo the rollback).
	names := actionNames(att.Actions)
	if sliceContains(names, "Rollback this deployment") {
		t.Errorf("rollback success must NOT expose another Rollback, got %v", names)
	}
}

func TestRenderAppMutation_NoActor_DashInitiatedBy(t *testing.T) {
	in := appMutationRaw(t, "apps.stop", true, nil, nil)
	att, err := renderEnvelopeAt(in, appDetailClock) // no actor
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Initiated by is the last field (index 1 for stop).
	last := att.Fields[len(att.Fields)-1]
	if last.Title != "Initiated by" || fieldStr(t, last) != "—" {
		t.Errorf("Initiated by without actor must dash: %+v", last)
	}
}

// ---------------------------------------------------------------------------
// envelope tolerance + outcome decoding
// ---------------------------------------------------------------------------

func TestEnvelopeErrorObject_Tolerance(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		ok   bool
		code string
	}{
		{"missing", "", false, ""},
		{"null", "null", false, ""},
		{"string-shaped (apps.deploy operation error)", `"deploy aborted"`, false, ""},
		{"object", `{"code":"X","message":"y"}`, true, "X"},
		{"array (malformed)", `["X"]`, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, _, ok := envelopeErrorObject(json.RawMessage(c.raw))
			if ok != c.ok || code != c.code {
				t.Errorf("got (%q, %v) want (%q, %v)", code, ok, c.code, c.ok)
			}
		})
	}
}

func TestRenderEnvelope_AppsDeploy_StringErrorTolerated(t *testing.T) {
	// CLI emits `error: "<text>" | null` for apps.deploy/stop/rollback
	// (cli/JSON_SCHEMA.md §apps.deploy). The envelope decoder must NOT
	// blow up on this string-shaped error — instead it routes the payload
	// to the per-verb renderer which interprets it as an operation-level
	// failure. Without the tolerant decoder this envelope would fail at
	// the json.Unmarshal of envelopeData.Error.
	in := []byte(`{"success":true,"data":{"schema_version":1,"verb":"apps.deploy","success":false,"deployment_id":null,"error":"daemon offline"}}`)
	att, err := renderEnvelope(in)
	if err != nil {
		t.Fatalf("render must tolerate string error, got: %v", err)
	}
	if att.Color != colorError {
		t.Errorf("string error must render as error card: %q", att.Color)
	}
	if !strings.Contains(att.Text, "daemon offline") {
		t.Errorf("error text missing: %q", att.Text)
	}
}

func TestParseAppMutationOutcome_Success(t *testing.T) {
	dep := "d_99"
	in := appMutationRaw(t, "apps.deploy", true, &dep, nil)
	verb, p, ok := parseAppMutationOutcome(in)
	if !ok {
		t.Fatalf("expected ok")
	}
	if verb != "apps.deploy" {
		t.Errorf("verb: %q", verb)
	}
	if !p.Success {
		t.Errorf("success should be true")
	}
	if p.DeploymentID == nil || *p.DeploymentID != "d_99" {
		t.Errorf("deployment_id: %v", p.DeploymentID)
	}
}

func TestParseAppMutationOutcome_Failure(t *testing.T) {
	errMsg := "rollback target unknown"
	in := appMutationRaw(t, "apps.rollback", false, nil, &errMsg)
	verb, p, ok := parseAppMutationOutcome(in)
	if !ok {
		t.Fatalf("expected ok")
	}
	if verb != "apps.rollback" {
		t.Errorf("verb: %q", verb)
	}
	if p.Success {
		t.Errorf("success should be false")
	}
	if p.Error == nil || *p.Error != errMsg {
		t.Errorf("error string: %v", p.Error)
	}
}

func TestParseAppMutationOutcome_NonAppVerb(t *testing.T) {
	in := []byte(`{"success":true,"data":{"schema_version":1,"verb":"tasks.get"}}`)
	_, _, ok := parseAppMutationOutcome(in)
	if ok {
		t.Errorf("non-app verb must return ok=false")
	}
}

// ---------------------------------------------------------------------------
// helpers + business error messages
// ---------------------------------------------------------------------------

func TestAppsBusinessErrorMessage_KnownCodes(t *testing.T) {
	cases := []struct {
		verb     string
		code     string
		msg      string
		contains string
	}{
		{"apps.deploy", "app_not_found", "missing", "/f apps list"},
		{"apps.deploy", "deploy_in_progress", "wait", "already running"},
		{"apps.stop", "stop_failed_running_jobs", "jobs", "blocked by running jobs"},
		{"apps.rollback", "unknown_code", "x", "unknown_code"},
	}
	for _, c := range cases {
		got := appsBusinessErrorMessage(c.verb, c.code, c.msg)
		if !strings.Contains(got, c.contains) {
			t.Errorf("%s/%s: %q must contain %q", c.verb, c.code, got, c.contains)
		}
	}
}

func TestVerbBusinessErrorMessage_RoutesByPrefix(t *testing.T) {
	app := verbBusinessErrorMessage("apps.deploy", "deploy_in_progress", "")
	if !strings.Contains(app, "already running") {
		t.Errorf("apps.* must route to apps formatter: %q", app)
	}
	task := verbBusinessErrorMessage("tasks.get", "task_not_found", "")
	if !strings.Contains(task, "/f search") {
		t.Errorf("tasks.* must route to tasks formatter: %q", task)
	}
}

func TestAppIDFromArgv(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"apps", "deploy", "a_1"}, "a_1"},
		{[]string{"apps", "stop", "a_2"}, "a_2"},
		{[]string{"apps", "rollback", "a_3", "d_42"}, "a_3"},
		{[]string{"apps", "get", "a_4"}, "a_4"},
		{[]string{"apps", "logs", "a_5", "--tail=200"}, "a_5"},
		{[]string{"apps", "list"}, ""},
		{[]string{"tasks", "get", "t_1"}, ""},
		{[]string{}, ""},
	}
	for _, c := range cases {
		if got := appIDFromArgv(c.argv); got != c.want {
			t.Errorf("appIDFromArgv(%v) = %q want %q", c.argv, got, c.want)
		}
	}
}

func TestAppMutationFooter_FallsBackToWallClock(t *testing.T) {
	in := appMutationRaw(t, "apps.stop", true, nil, nil)
	att, err := renderEnvelopeAt(in, appDetailClock)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "fulcrum/apps.stop · ts=" + appDetailClock.UTC().Format(time.RFC3339)
	if att.Footer != want {
		t.Errorf("footer wall-clock fallback:\n got: %q\nwant: %q", att.Footer, want)
	}
}
