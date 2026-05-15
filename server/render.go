package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// envelope is the outer fulcrum CLI JSON envelope. See
// fulcrum/cli/JSON_SCHEMA.md for the canonical definition.
type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

// envelopeData is the payload subset the plugin needs for routing. The full
// payload is preserved verbatim in `data` so per-verb renderers can decode
// the rest of the fields themselves. `Error` is a RawMessage rather than a
// strict {code,message} struct because the CLI's apps.deploy/stop/rollback
// verbs emit `error: "<text>" | null` (operation-level error, see
// cli/src/commands/mattermost-verbs.ts:663) which collides with the canonical
// envelope-error object shape. `envelopeErrorObject` decodes only the
// canonical object shape and treats string/null/missing as "no envelope
// error" so the per-verb renderer can interpret payload-level errors itself.
type envelopeData struct {
	SchemaVersion int             `json:"schema_version"`
	Verb          string          `json:"verb"`
	Error         json.RawMessage `json:"error"`
}

// renderEnvelope converts CLI stdout into a SlackAttachment ready to attach
// to a Mattermost post. It validates the envelope, routes by `data.verb` to a
// per-verb renderer, and falls back to a generic JSON dump for verbs that
// don't have a deliberate renderer yet. The fallback path will be retired
// once every CLI verb has a feature_id sub-issue merged (umbrella
// mattermost-plugin-fulcrum#6).
func renderEnvelope(stdout []byte) (*model.SlackAttachment, error) {
	return renderEnvelopeAtForRequest(stdout, time.Now(), "", nil)
}

// renderEnvelopeAt is renderEnvelope with the wall clock injected for tests
// that need a stable Pretext / relative-time output.
func renderEnvelopeAt(stdout []byte, now time.Time) (*model.SlackAttachment, error) {
	return renderEnvelopeAtForRequest(stdout, now, "", nil)
}

// renderEnvelopeAtForActor preserves the legacy 3-arg entry point used by
// callers that don't carry the invoking argv. New callers should prefer
// renderEnvelopeAtForRequest so apps.logs gets the request-derived tail /
// service hints (spike §B.8 — the CLI envelope omits both fields).
func renderEnvelopeAtForActor(stdout []byte, now time.Time, actorUserID string) (*model.SlackAttachment, error) {
	return renderEnvelopeAtForRequest(stdout, now, actorUserID, nil)
}

// renderEnvelopeAtForRequest is the canonical render entry point: it adds the
// invoking argv on top of renderEnvelopeAtForActor so verbs whose render
// surface depends on request context (today: apps.logs, which spike §B.8
// renders with `tail=<n>` pretext + footer and a Tail more button that
// doubles the current tail) can pull what they need without round-tripping
// through the central dispatcher. `requestArgv` may be nil — callers that
// don't have the argv (e.g. unit tests, generic re-renders) fall back to the
// renderer's own defaults.
func renderEnvelopeAtForRequest(stdout []byte, now time.Time, actorUserID string, requestArgv []string) (*model.SlackAttachment, error) {
	if len(stdout) == 0 {
		return nil, errors.New("empty stdout from fulcrum CLI")
	}
	var env envelope
	if err := json.Unmarshal(stdout, &env); err != nil {
		return nil, fmt.Errorf("envelope: %w", err)
	}
	var data envelopeData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil, fmt.Errorf("envelope.data: %w", err)
	}
	if data.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported schema_version %d (plugin understands 1)", data.SchemaVersion)
	}
	if code, msg, ok := envelopeErrorObject(data.Error); ok {
		if data.Verb == "apps.logs" {
			hints, argvAppID := extractAppLogsHints(requestArgv)
			return renderAppLogsBusinessError(appIDForLogsError(env.Data, argvAppID), code, msg, hints), nil
		}
		return renderBusinessError(data.Verb, code, msg), nil
	}

	switch data.Verb {
	case "dashboard":
		return renderDashboard(env.Data, now)
	case "tasks.list":
		return renderTasksList(env.Data, now)
	case "tasks.get":
		return renderTaskDetail(env.Data, now)
	case "tasks.create":
		return renderTaskQuickCreate(env.Data, now, actorUserID)
	case "apps.list":
		return renderAppsOverview(env.Data, now)
	case "apps.get":
		return renderAppDetail(env.Data, now)
	case "apps.deploy", "apps.stop", "apps.rollback":
		return renderAppMutationResult(data.Verb, env.Data, now, actorUserID)
	case "apps.logs":
		hints, _ := extractAppLogsHints(requestArgv)
		return renderAppLogs(env.Data, hints)
	default:
		return renderGenericVerb(data.Verb, env.Data)
	}
}

// appIDForLogsError prefers the app_id field embedded in the envelope's data
// (the CLI emits it even for business-error responses per cli/JSON_SCHEMA.md
// §apps.logs) and falls back to the argv-derived id when the envelope omits
// it — that fallback keeps the §B.8.5 colorError card titled correctly even
// against an older CLI that doesn't populate app_id on error.
func appIDForLogsError(raw json.RawMessage, argvAppID string) string {
	var p appsLogsPayload
	if err := json.Unmarshal(raw, &p); err == nil && p.AppID != "" {
		return p.AppID
	}
	return argvAppID
}

// parseEnvelopeOutcome decodes only the routing fields of a CLI envelope:
// verb + envelope-level business error (when present). /action and /dialog
// use it to decide ephemeral-vs-UpdatePost before calling the per-verb
// renderer (per spike §B.3.5: button-triggered failures must not overwrite
// the original card). The data RawMessage is returned alongside so callers
// can hand it to the renderer without re-parsing the outer envelope a second
// time. Payload-level errors emitted by app mutation verbs as plain strings
// are not surfaced here — those callers must use parseAppMutationOutcome.
func parseEnvelopeOutcome(stdout []byte) (verb, errCode, errMsg string, err error) {
	if len(stdout) == 0 {
		return "", "", "", errors.New("empty stdout from fulcrum CLI")
	}
	var env envelope
	if jsonErr := json.Unmarshal(stdout, &env); jsonErr != nil {
		return "", "", "", fmt.Errorf("envelope: %w", jsonErr)
	}
	var data envelopeData
	if jsonErr := json.Unmarshal(env.Data, &data); jsonErr != nil {
		return "", "", "", fmt.Errorf("envelope.data: %w", jsonErr)
	}
	if data.SchemaVersion != 1 {
		return data.Verb, "", "", fmt.Errorf("unsupported schema_version %d (plugin understands 1)", data.SchemaVersion)
	}
	if code, msg, ok := envelopeErrorObject(data.Error); ok {
		return data.Verb, code, msg, nil
	}
	return data.Verb, "", "", nil
}

// envelopeErrorObject returns the {code, message} object embedded in a
// canonical envelope-error field. When the field is null/empty/missing or
// holds a non-object shape (e.g. the string-shaped operation error emitted by
// apps.deploy/stop/rollback per cli/JSON_SCHEMA.md §apps.deploy), it returns
// ok=false so the per-verb renderer can interpret the payload itself.
func envelopeErrorObject(raw json.RawMessage) (code, message string, ok bool) {
	if len(raw) == 0 {
		return "", "", false
	}
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return "", "", false
	}
	if !strings.HasPrefix(s, "{") {
		return "", "", false
	}
	var obj struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", "", false
	}
	return obj.Code, obj.Message, true
}

// renderBusinessError is the §0.5 envelope-error form: a non-ephemeral bot
// post with colorError, the machine-readable code in code-spans, and the
// human-readable message inline. Per-verb renderers may override this
// (e.g. to add a verb-specific Refresh button), but the dashboard renderer
// keeps it generic — dashboard has no per-verb business error today, and a
// future business error from the CLI will still render coherently.
func renderBusinessError(verb, code, message string) *model.SlackAttachment {
	att := &model.SlackAttachment{
		Title: fmt.Sprintf("fulcrum %s — error", verb),
		Text:  fmt.Sprintf("`%s` %s", code, message),
		Color: colorError,
	}
	switch verb {
	case "dashboard":
		att.Actions = []*model.PostAction{
			makeAction("dashboard_refresh", "Refresh", postActionStyleDefault, []string{"dashboard"}),
		}
		att.Footer = "fulcrum/dashboard · schema_version=1"
	case "apps.list":
		// Per spike §B.6.5, business errors on apps.list (notably
		// `backend_unavailable`) keep the Refresh button so the user can
		// retry from the same card once the backend recovers.
		att.Actions = []*model.PostAction{
			makeAction("apps_overview_refresh", "Refresh", postActionStyleDefault, []string{"apps", "list"}),
		}
		att.Footer = "fulcrum/apps.list · schema_version=1"
	}
	return att
}

// renderGenericVerb is the legacy stub for verbs that don't yet have a
// per-verb renderer. It pretty-prints the data payload so users can still
// see CLI output without a frontend panic. Each sub-issue under
// mattermost-plugin-fulcrum#6 will replace one verb with a deliberate
// renderer.
func renderGenericVerb(verb string, data json.RawMessage) (*model.SlackAttachment, error) {
	pretty, err := prettyJSON(data)
	if err != nil {
		return nil, err
	}
	return &model.SlackAttachment{
		Title: "fulcrum " + verb,
		Text:  "```json\n" + pretty + "\n```",
		Color: colorAccent,
	}, nil
}

func prettyJSON(b []byte) (string, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return "", err
	}
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(buf), nil
}
