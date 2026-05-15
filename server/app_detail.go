package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// appGetPayload mirrors the `data` payload of `fulcrum apps get <id> --json`
// (cli/JSON_SCHEMA.md §apps.get). The renderer reuses appSummary from the
// apps-overview feature so the schema additions land in one place; only the
// services[] addition is local to this verb.
type appGetPayload struct {
	App      appSummary   `json:"app"`
	Services []appService `json:"services"`
}

// appService mirrors the runtime AppService entry returned alongside an
// AppSummary. Only the two fields the §B.7.3 services list renders are
// decoded; the renderer treats a nil/empty Status as "—" so a service that
// has never reported a state is still listed by name.
type appService struct {
	ServiceName string  `json:"serviceName"`
	Status      *string `json:"status"`
}

// appMutationPayload covers the post-success payload shared by apps.deploy,
// apps.stop, and apps.rollback (cli/JSON_SCHEMA.md §apps.deploy / §apps.stop
// / §apps.rollback). `Success` is the *operation* outcome — distinct from
// the envelope `success` flag — and `Error` is the operation-level message
// (string, not the canonical {code,message} envelope object). `App` is
// present when the CLI managed to refresh the app summary alongside the
// mutation; today the CLI does not include it, but the renderer tolerates
// either presence so a future schema addition (spike §C.5 follow-up) doesn't
// require a plugin release. Likewise `Timestamp` falls back to the wall
// clock when the CLI envelope omits it.
type appMutationPayload struct {
	Success      bool        `json:"success"`
	DeploymentID *string     `json:"deployment_id"`
	Error        *string     `json:"error"`
	App          *appSummary `json:"app"`
	Timestamp    *string     `json:"timestamp"`
}

// appRoundTripMutationVerbs is the set of `data.verb` values whose
// successful envelopes must be replaced on the original post by the
// canonical `apps.get` re-render (spike §B.7.6: "Stop success → round-trip
// apps.get"). apps.deploy and apps.rollback do NOT round-trip because their
// per-verb result card carries deployment_id which the user needs (see
// §B.7.1 and §B.7) — replacing it with apps.get would lose that context.
var appRoundTripMutationVerbs = map[string]bool{
	"apps.stop": true,
}

// appMutationVerbs is the full set of app mutation verbs (no round-trip
// distinction). Used by the action/dialog handlers to detect when payload-
// level errors should go ephemeral rather than render onto the original
// card (spike §B.7.5: business errors keep the existing buttons valid).
var appMutationVerbs = map[string]bool{
	"apps.deploy":   true,
	"apps.stop":     true,
	"apps.rollback": true,
}

// appDetailColor maps a CLI app status to its SlackAttachment color per
// spike §B.7.3. Unknown statuses fall through to the brand accent so a
// future CLI status addition still renders rather than emitting an empty
// color band.
func appDetailColor(status string) string {
	switch status {
	case "running":
		return colorStatusDoing
	case "building", "pending":
		return colorStatusReview
	case "failed":
		return colorPriorityHigh
	case "stopped":
		return colorStatusTODO
	default:
		return colorAccent
	}
}

// renderAppDetail produces the app-detail-view SlackAttachment per spike
// §B.7.3. `now` is injected so tests can pin the relative-time fields.
func renderAppDetail(raw json.RawMessage, now time.Time) (*model.SlackAttachment, error) {
	var p appGetPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("apps.get payload: %w", err)
	}
	att := &model.SlackAttachment{
		Color:   appDetailColor(p.App.Status),
		Title:   fmt.Sprintf("%s App · %s", appStatusChip(p.App.Status), p.App.Name),
		Pretext: fmt.Sprintf("app ID `%s`", p.App.ID),
		Fields:  renderAppFields(p.App, now),
		Actions: appDetailActions(p.App.ID, p.App.Status),
		Footer:  fmt.Sprintf("fulcrum/apps.get · status=%s", p.App.Status),
	}
	if text := appDetailServicesText(p.Services); text != "" {
		att.Text = text
	}
	return att, nil
}

// renderAppFields produces the §B.7.3 Fields[] section. It is also the
// shared §0.7 helper landed on app-detail-view: future per-verb renderers
// that need an app-level Fields layout reuse it instead of redefining the
// dash semantics. Each value collapses to "—" when the underlying CLI field
// is null/empty so the column grid never breaks.
func renderAppFields(a appSummary, now time.Time) []*model.SlackAttachmentField {
	return []*model.SlackAttachmentField{
		{Title: "Status", Value: fmt.Sprintf("%s %s", appStatusChip(a.Status), orDash(a.Status)), Short: true},
		{Title: "Branch", Value: codeOrDashStr(a.Branch), Short: true},
		{Title: "Repository", Value: orDashPtr(a.Repository), Short: true},
		{Title: "Auto-deploy", Value: autoDeployValue(a.AutoDeployEnabled), Short: true},
		{Title: "Last deploy", Value: appLastDeployValue(a.LastDeployedAt, now), Short: true},
		{Title: "Last commit", Value: shortCommitOrDash(a.LastDeployCommit), Short: true},
	}
}

// codeOrDashStr is the non-pointer twin of codeOrDash (server/task_detail.go)
// for the always-present Branch field. Empty branch collapses to "—" so an
// app that hasn't been linked to a branch yet still renders.
func codeOrDashStr(s string) string {
	if s == "" {
		return "—"
	}
	return "`" + s + "`"
}

// autoDeployValue renders the §B.7.3 Auto-deploy field — emoji + literal
// "on"/"off" so the value is readable on Mattermost mobile clients where
// long emoji shortcodes can wrap awkwardly.
func autoDeployValue(enabled bool) string {
	if enabled {
		return ":white_check_mark: on"
	}
	return ":x: off"
}

// appLastDeployValue is the §B.7.3 "Last deploy" cell — relative time only
// (the apps-overview spec adds an absolute prefix because the table column
// affords more space; the detail card prefers brevity).
func appLastDeployValue(iso *string, now time.Time) string {
	if iso == nil || *iso == "" {
		return "—"
	}
	return formatRelTime(*iso, now)
}

// shortCommitOrDash truncates a git SHA pointer to its 7-char prefix per
// §B.7.3 ("Last commit" cell). nil/empty/short input collapses to "—" so the
// column grid stays aligned.
func shortCommitOrDash(commit *string) string {
	if commit == nil || *commit == "" {
		return "—"
	}
	c := *commit
	if len(c) >= 7 {
		c = c[:7]
	}
	return "`" + c + "`"
}

// appDetailActions builds the §B.7.4 button row for the app-detail card.
// The action set is purely a function of `app.status`; CLI-emitted action
// metadata is not used for app verbs today (the CLI does not yet emit an
// `actions[]` field for apps.get). Refresh is always last so the column
// order stays predictable across statuses; Tail logs and Stop sit between
// Deploy and Refresh in the spike order.
func appDetailActions(appID, status string) []*model.PostAction {
	switch status {
	case "running":
		return []*model.PostAction{
			makeAction("app_deploy", "Deploy", postActionStylePrimary, []string{"apps", "deploy", appID}),
			makeAppDangerAction("app_stop", "Stop", []string{"apps", "stop", appID}),
			makeAction("app_tail_logs", "Tail logs", postActionStyleDefault, []string{"apps", "logs", appID, "--tail=200"}),
			makeAction("app_refresh", "Refresh", postActionStyleDefault, []string{"apps", "get", appID}),
		}
	case "building", "pending":
		return []*model.PostAction{
			makeAction("app_refresh", "Refresh", postActionStyleDefault, []string{"apps", "get", appID}),
			makeAction("app_tail_logs", "Tail logs", postActionStyleDefault, []string{"apps", "logs", appID, "--tail=200"}),
		}
	case "failed":
		return []*model.PostAction{
			makeAction("app_deploy", "Deploy", postActionStylePrimary, []string{"apps", "deploy", appID}),
			makeAction("app_tail_logs", "Tail logs", postActionStyleDefault, []string{"apps", "logs", appID, "--tail=200"}),
			makeAction("app_refresh", "Refresh", postActionStyleDefault, []string{"apps", "get", appID}),
		}
	case "stopped":
		return []*model.PostAction{
			makeAction("app_deploy", "Deploy", postActionStylePrimary, []string{"apps", "deploy", appID}),
			makeAction("app_refresh", "Refresh", postActionStyleDefault, []string{"apps", "get", appID}),
		}
	default:
		// Unknown status — only Refresh is safe (mutating buttons could race
		// against an unknown state machine). Spike §B.7.4 doesn't enumerate
		// the unknown branch but the §0.5 invariant (never crash on schema
		// drift) drives this fallback.
		return []*model.PostAction{
			makeAction("app_refresh", "Refresh", postActionStyleDefault, []string{"apps", "get", appID}),
		}
	}
}

// makeAppDangerAction wraps makeAction and stamps the Integration.Context
// with the dialog flag the /action handler keys off to route the click into
// OpenInteractiveDialog instead of a direct CLI call. Mirror of
// makeTaskAction(... dialog=true) — kept separate so the apps and tasks
// renderers don't accidentally cross-pollinate id namespaces.
func makeAppDangerAction(id, label string, argv []string) *model.PostAction {
	act := makeAction(id, label, postActionStyleDanger, argv)
	act.Integration.Context[actionContextDialogKey] = true
	return act
}

// appDetailServicesText emits the optional Text block per §B.7.3. Empty
// services list returns "" so the renderer omits the Text field rather than
// emitting an empty markdown block. Each service line uses `name` in a code
// span so service names with hyphens / dots stay legible; status (when
// present) follows separated by a middle-dot per the spike spec.
func appDetailServicesText(services []appService) string {
	if len(services) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**Services** (%d):", len(services))
	for _, s := range services {
		state := "—"
		if s.Status != nil && *s.Status != "" {
			state = *s.Status
		}
		fmt.Fprintf(&b, "\n- `%s` · %s", s.ServiceName, state)
	}
	return b.String()
}

// renderAppMutationResult produces the mutation-result SlackAttachment per
// spike §B.7.1. It covers apps.deploy / apps.stop / apps.rollback using a
// single layout because all three share the success/fail/partial color
// matrix and the same downstream actions (Tail logs, Open app detail; deploy
// success additionally exposes Rollback). `actorUserID` is the Mattermost
// user id of whoever clicked the button or ran the slash command — empty
// collapses to "—" in the Initiated by field rather than emitting an
// uninterpretable empty mention.
func renderAppMutationResult(verb string, raw json.RawMessage, now time.Time, actorUserID string) (*model.SlackAttachment, error) {
	var p appMutationPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%s payload: %w", verb, err)
	}
	appID := appIDFromMutationPayload(p)
	appName := appNameFromMutationPayload(p)
	att := &model.SlackAttachment{
		Color:   appMutationColor(p),
		Title:   appMutationTitle(verb, appName, p),
		Pretext: appMutationPretext(p),
		Text:    appMutationText(verb, p),
		Fields:  appMutationFields(verb, appID, p, actorUserID),
		Footer:  appMutationFooter(verb, p, now),
		Actions: appMutationActions(verb, appID, p),
	}
	return att, nil
}

// appMutationColor implements the §B.7.1 color matrix:
// success=colorStatusDone, error (success=false)=colorError, partial
// (success=true and error non-nil) = colorWarn. The partial branch is rare
// — it appears when the CLI declares the operation succeeded but still
// surfaces a warning string — keeping it as colorWarn (orange, not red)
// matches the §0.2 colorWarn intent ("partial / 弱警告").
func appMutationColor(p appMutationPayload) string {
	if !p.Success {
		return colorError
	}
	if p.Error != nil && *p.Error != "" {
		return colorWarn
	}
	return colorStatusDone
}

// appMutationTitle picks the §B.7.1 title per verb + outcome. Title is
// gated only on payload.Success — the operation either happened or did not.
// The partial branch (success=true with a warning string) keeps the success
// title because the action DID complete; the warning color band + pretext
// carry the partial signal so the title doesn't misrepresent state.
func appMutationTitle(verb, appName string, p appMutationPayload) string {
	if appName == "" {
		appName = "—"
	}
	switch verb {
	case "apps.deploy":
		if p.Success {
			return "Deployed · " + appName
		}
		return "Deploy failed · " + appName
	case "apps.stop":
		if p.Success {
			return "Stopped · " + appName
		}
		return "Stop failed · " + appName
	case "apps.rollback":
		if p.Success {
			return "Rolled back · " + appName
		}
		return "Rollback failed · " + appName
	}
	return "fulcrum " + verb
}

// appMutationPretext is the single-line pretext per §B.7.1. Success →
// `deployment <id>`; failure → `error: <msg>`; partial (success + warning
// string) → both lines so the user sees what completed and what didn't.
func appMutationPretext(p appMutationPayload) string {
	deployStr := ""
	if p.DeploymentID != nil && *p.DeploymentID != "" {
		deployStr = fmt.Sprintf("deployment `%s`", *p.DeploymentID)
	}
	errStr := ""
	if p.Error != nil && *p.Error != "" {
		errStr = "error: " + truncate(*p.Error, 200)
	}
	switch {
	case deployStr != "" && errStr != "":
		return deployStr + " · " + errStr
	case deployStr != "":
		return deployStr
	case errStr != "":
		return errStr
	}
	return ""
}

// appMutationText is the body block per §B.7.1: success → standard "Deploy
// started" tip; fail → truncated error string for context. Stop has its own
// tip because "Deploy started" is wrong wording for a stop.
func appMutationText(verb string, p appMutationPayload) string {
	success := p.Success && (p.Error == nil || *p.Error == "")
	if !success {
		if p.Error != nil && *p.Error != "" {
			return "```\n" + truncateMD(*p.Error, 1000) + "\n```"
		}
		return ""
	}
	switch verb {
	case "apps.deploy":
		return "_Deploy started. Tail logs to follow progress._"
	case "apps.stop":
		return "_App stopped. Use Refresh on the app detail to confirm._"
	case "apps.rollback":
		return "_Rollback started. Tail logs to follow progress._"
	}
	return ""
}

// appMutationFields builds the §B.7.1 Fields[] row: App ID always, Deployment
// ID when the verb produces one (deploy/rollback), and Initiated by mention.
// Stop intentionally omits Deployment ID even if a future schema includes it
// — stop is not a deploy, surfacing a deployment_id would imply mutability
// the user can act on (rollback) which §B.7 explicitly disallows from outside
// a fresh deploy result card.
func appMutationFields(verb, appID string, p appMutationPayload, actorUserID string) []*model.SlackAttachmentField {
	fields := []*model.SlackAttachmentField{
		{Title: "App ID", Value: orDash(appID), Short: true},
	}
	if verb == "apps.deploy" || verb == "apps.rollback" {
		dep := "—"
		if p.DeploymentID != nil && *p.DeploymentID != "" {
			dep = "`" + *p.DeploymentID + "`"
		}
		fields = append(fields, &model.SlackAttachmentField{Title: "Deployment ID", Value: dep, Short: true})
	}
	fields = append(fields, &model.SlackAttachmentField{Title: "Initiated by", Value: actorMention(actorUserID), Short: true})
	return fields
}

// actorMention renders the §B.7.1 Initiated by value as a Mattermost user
// mention (`<@user_id>` is rewritten to a clickable @username on the client).
// Empty actor → "—" so the slash-command-direct path (no actor threaded
// through) doesn't render an empty mention.
func actorMention(userID string) string {
	if userID == "" {
		return "—"
	}
	return fmt.Sprintf("<@%s>", userID)
}

// appMutationFooter renders the spike-mandated footer line. Per §B.7.1 the
// timestamp is the mutation envelope's `ts` field — when absent (today's CLI
// schema does not include it) the wall clock fills in so the footer always
// carries a timestamp the user can correlate against logs.
func appMutationFooter(verb string, p appMutationPayload, now time.Time) string {
	ts := now.UTC().Format(time.RFC3339)
	if p.Timestamp != nil && *p.Timestamp != "" {
		ts = *p.Timestamp
	}
	return fmt.Sprintf("fulcrum/%s · ts=%s", verb, ts)
}

// appMutationActions builds the §B.7.1 action row. Tail logs + Open app
// detail are present on every mutation result regardless of outcome (the
// user almost always wants to see logs after a failed deploy, and "Open app
// detail" is the way back to the canonical card). Rollback is only added to
// successful apps.deploy results because the spike makes deploy result the
// **only** entry point for rollback (§B.7 + §B.7.1) — surfacing it on stop
// or on a failed deploy would invite acting on a stale or invalid
// deployment_id.
func appMutationActions(verb, appID string, p appMutationPayload) []*model.PostAction {
	out := []*model.PostAction{
		makeAction("app_mutation_tail_logs", "Tail logs", postActionStyleDefault, []string{"apps", "logs", appID, "--tail=200"}),
		makeAction("app_mutation_open_detail", "Open app detail", postActionStyleDefault, []string{"apps", "get", appID}),
	}
	if verb == "apps.deploy" && p.Success && (p.Error == nil || *p.Error == "") && p.DeploymentID != nil && *p.DeploymentID != "" {
		out = append(out, makeAppDangerAction("app_mutation_rollback", "Rollback this deployment", []string{"apps", "rollback", appID, *p.DeploymentID}))
	}
	return out
}

// appsBusinessErrorMessage formats the ephemeral text shown to the clicking
// user when an apps.* mutation envelope returns a business error.code (per
// spike §B.7.5). The known codes today are app_not_found, deploy_in_progress,
// stop_failed_running_jobs; unknown codes fall through to the generic
// "<verb>: <code>" so a future CLI addition still surfaces.
func appsBusinessErrorMessage(verb, code, message string) string {
	base := fmt.Sprintf("%s: %s", verb, code)
	if message != "" {
		base = base + " — " + message
	}
	switch code {
	case "app_not_found":
		return base + " (try `/f apps list`)"
	case "deploy_in_progress":
		return base + " (a deploy is already running; wait or refresh)"
	case "stop_failed_running_jobs":
		return base + " (stop blocked by running jobs; cancel them first)"
	}
	return base
}

// verbBusinessErrorMessage routes a business-error envelope to the verb
// family's message formatter. /action and /dialog use it instead of calling
// tasksBusinessErrorMessage directly so the apps.* branch can carry its own
// known-code copy without leaking task-specific hints into app errors.
func verbBusinessErrorMessage(verb, code, message string) string {
	if strings.HasPrefix(verb, "apps.") {
		return appsBusinessErrorMessage(verb, code, message)
	}
	return tasksBusinessErrorMessage(verb, code, message)
}

// appIDFromArgv extracts the app id from a recognized apps.* argv shape so
// the /action and /dialog handlers can round-trip `apps.get` after a
// mutation. Empty string when the argv shape isn't a known app verb.
func appIDFromArgv(argv []string) string {
	if len(argv) >= 3 && argv[0] == "apps" {
		switch argv[1] {
		case "deploy", "stop", "rollback", "get", "logs":
			return argv[2]
		}
	}
	return ""
}

// appIDFromMutationPayload prefers the embedded App.ID (when the CLI
// schema is later extended to include it, spike §C.5) and otherwise returns
// "" so callers can fall back to the argv-derived id.
func appIDFromMutationPayload(p appMutationPayload) string {
	if p.App != nil && p.App.ID != "" {
		return p.App.ID
	}
	return ""
}

// appNameFromMutationPayload returns the human-readable app name for the
// title; empty when the CLI schema does not carry the App in the mutation
// envelope (today's case — appMutationTitle handles the empty fallback).
func appNameFromMutationPayload(p appMutationPayload) string {
	if p.App != nil && p.App.Name != "" {
		return p.App.Name
	}
	return ""
}

// parseAppMutationOutcome decodes the operation-level success / error /
// deployment_id from an apps.deploy/stop/rollback envelope. /action and
// /dialog use it for the §B.7.5 invariant — payload-level errors stay
// ephemeral so the original card's buttons remain valid. Returns the empty
// outcome when stdout is non-mutation-shaped or fails to parse; callers
// must check ok.
func parseAppMutationOutcome(stdout []byte) (verb string, outcome appMutationPayload, ok bool) {
	var env envelope
	if err := json.Unmarshal(stdout, &env); err != nil {
		return "", appMutationPayload{}, false
	}
	var meta envelopeData
	if err := json.Unmarshal(env.Data, &meta); err != nil {
		return "", appMutationPayload{}, false
	}
	if !appMutationVerbs[meta.Verb] {
		return meta.Verb, appMutationPayload{}, false
	}
	var p appMutationPayload
	if err := json.Unmarshal(env.Data, &p); err != nil {
		return meta.Verb, appMutationPayload{}, false
	}
	return meta.Verb, p, true
}
