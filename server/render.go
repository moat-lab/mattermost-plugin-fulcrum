package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mattermost/mattermost/server/public/model"
)

// envelope is the outer fulcrum CLI JSON envelope. See
// fulcrum/cli/JSON_SCHEMA.md for the canonical definition.
type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
}

// envelopeData is the payload subset the plugin needs for routing. The full
// payload is preserved verbatim in the SlackAttachment.Text field for v1; per
// verb renderers come in follow-up issues.
type envelopeData struct {
	SchemaVersion int    `json:"schema_version"`
	Verb          string `json:"verb"`
	Error         *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// renderEnvelope converts CLI stdout into a SlackAttachment ready to attach
// to a Mattermost post. v1 deliberately renders a generic envelope: per-verb
// rich rendering is a follow-up issue, but the plugin already routes by verb
// and schema_version so future renderers can swap in without touching this
// boilerplate.
func renderEnvelope(stdout []byte) (*model.SlackAttachment, error) {
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
		return &model.SlackAttachment{
			Title: fmt.Sprintf("fulcrum %s — error", data.Verb),
			Text:  fmt.Sprintf("`%s` %s", data.Error.Code, data.Error.Message),
			Color: "#B91C1C",
		}, nil
	}

	pretty, err := prettyJSON(env.Data)
	if err != nil {
		return nil, err
	}
	return &model.SlackAttachment{
		Title: "fulcrum " + data.Verb,
		Text:  "```json\n" + pretty + "\n```",
		Color: "#7C3AED",
	}, nil
}

func prettyJSON(b []byte) (string, error) {
	var buf []byte
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
