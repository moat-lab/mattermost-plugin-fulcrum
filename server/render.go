package main

import (
	"encoding/json"
	"errors"
	"fmt"
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
// the rest of the fields themselves.
type envelopeData struct {
	SchemaVersion int    `json:"schema_version"`
	Verb          string `json:"verb"`
	Error         *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// renderEnvelope converts CLI stdout into a SlackAttachment ready to attach
// to a Mattermost post. It validates the envelope, routes by `data.verb` to a
// per-verb renderer, and falls back to a generic JSON dump for verbs that
// don't have a deliberate renderer yet. The fallback path will be retired
// once every CLI verb has a feature_id sub-issue merged (umbrella
// mattermost-plugin-fulcrum#6).
func renderEnvelope(stdout []byte) (*model.SlackAttachment, error) {
	return renderEnvelopeAt(stdout, time.Now())
}

// renderEnvelopeAt is renderEnvelope with the wall clock injected for tests
// that need a stable Pretext / relative-time output.
func renderEnvelopeAt(stdout []byte, now time.Time) (*model.SlackAttachment, error) {
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
	if data.Error != nil {
		return renderBusinessError(data.Verb, data.Error.Code, data.Error.Message), nil
	}

	switch data.Verb {
	case "dashboard":
		return renderDashboard(env.Data, now)
	case "tasks.get":
		return renderTaskDetail(env.Data, now)
	default:
		return renderGenericVerb(data.Verb, env.Data)
	}
}

// parseEnvelopeOutcome decodes only the routing fields of a CLI envelope:
// verb + business error (when present). /action and /dialog use it to decide
// ephemeral-vs-UpdatePost before calling the per-verb renderer (per spike
// §B.3.5: button-triggered failures must not overwrite the original card).
// The data RawMessage is returned alongside so callers can hand it to the
// renderer without re-parsing the outer envelope a second time.
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
	if data.Error != nil {
		return data.Verb, data.Error.Code, data.Error.Message, nil
	}
	return data.Verb, "", "", nil
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
	if verb == "dashboard" {
		att.Actions = []*model.PostAction{
			makeAction("dashboard_refresh", "Refresh", postActionStyleDefault, []string{"dashboard"}),
		}
		att.Footer = "fulcrum/dashboard · schema_version=1"
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
