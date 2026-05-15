package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	rexec "github.com/Mouriya-Emma/rexec-go"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

const (
	// actionContextDialogKey is the Integration.Context flag a renderer stamps
	// on a button to ask the /action handler to open a confirmation dialog
	// instead of invoking the CLI directly. The dialog's submit POST hits
	// /dialog with the encoded state below.
	actionContextDialogKey = "dialog"

	// dialogCallbackID labels the OpenDialogRequest so future fulcrum dialogs
	// (e.g. apps.stop / apps.rollback) share the same callback shape on the
	// /dialog endpoint without colliding with non-fulcrum plugin dialogs in
	// shared Mattermost telemetry.
	dialogCallbackID = "fulcrum_confirm"
)

// dialogState is the envelope persisted in Dialog.State so the submit handler
// can re-locate the original bot post and replay the argv without holding
// server-side dialog session state. base64(JSON) keeps Mattermost's State
// field opaque while still letting the plugin recover the structured fields.
type dialogState struct {
	Argv      []string `json:"argv"`
	PostID    string   `json:"post_id"`
	ChannelID string   `json:"channel_id"`
}

func encodeDialogState(s dialogState) (string, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("marshal state: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func decodeDialogState(b64 string) (dialogState, error) {
	var out dialogState
	if b64 == "" {
		return out, errors.New("empty state")
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return out, fmt.Errorf("decode state: %w", err)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("unmarshal state: %w", err)
	}
	if len(out.Argv) == 0 {
		return out, errors.New("state.argv is empty")
	}
	if out.PostID == "" {
		return out, errors.New("state.post_id is empty")
	}
	return out, nil
}

// dialogPrompt holds the three user-visible strings of a confirmation dialog
// derived from a particular argv shape.
type dialogPrompt struct {
	Title        string
	Introduction string
	SubmitLabel  string
}

// dialogPromptFor maps argv shape → confirmation copy per spike §0.4. The
// recognized shapes today are the two task-detail danger paths and the two
// app-detail-view danger paths (Stop and Rollback). The generic fallback
// exists so a future danger verb still opens a usable dialog before its
// copy lands; missing copy is a soft regression, not a runtime failure.
func dialogPromptFor(argv []string) dialogPrompt {
	if len(argv) >= 4 && argv[0] == "tasks" && argv[1] == "set-status" && argv[3] == "canceled" {
		id := argv[2]
		return dialogPrompt{
			Title:        fmt.Sprintf("Confirm: tasks.set-status canceled %s", id),
			Introduction: fmt.Sprintf("Cancel task `%s`?\nThis sets the status to **CANCELED**; the action set collapses to Reopen/Refresh.", id),
			SubmitLabel:  "Cancel task",
		}
	}
	if len(argv) >= 3 && argv[0] == "tasks" && argv[1] == "kill-agent" {
		id := argv[2]
		return dialogPrompt{
			Title:        fmt.Sprintf("Confirm: tasks.kill-agent %s", id),
			Introduction: fmt.Sprintf("Kill the agent attached to task `%s`?\nAny unsaved terminal state will be lost.", id),
			SubmitLabel:  "Kill agent",
		}
	}
	if len(argv) >= 3 && argv[0] == "apps" && argv[1] == "stop" {
		id := argv[2]
		return dialogPrompt{
			Title:        fmt.Sprintf("Confirm: apps.stop %s", id),
			Introduction: fmt.Sprintf("Stop app `%s`?\nThe app's services will be brought down. Use **Deploy** on the app detail to bring it back up.", id),
			SubmitLabel:  "Stop app",
		}
	}
	if len(argv) >= 4 && argv[0] == "apps" && argv[1] == "rollback" {
		id := argv[2]
		dep := argv[3]
		return dialogPrompt{
			Title:        fmt.Sprintf("Confirm: apps.rollback %s %s", id, dep),
			Introduction: fmt.Sprintf("Roll back app `%s` to deployment `%s`?\nThe currently-running deployment will be replaced. This is reversible by deploying the latest branch again.", id, dep),
			SubmitLabel:  "Roll back",
		}
	}
	joined := strings.Join(argv, " ")
	return dialogPrompt{
		Title:        "Confirm: " + joined,
		Introduction: fmt.Sprintf("This action is destructive: `%s`", joined),
		SubmitLabel:  "Confirm",
	}
}

// buildOpenDialogRequest assembles the OpenDialogRequest sent through the
// Mattermost frontend API. The dialog URL is the plugin-local /dialog
// endpoint; State carries the encoded dialogState so the submit handler can
// replay the argv against the original post.
func buildOpenDialogRequest(triggerID string, argv []string, postID, channelID string) (model.OpenDialogRequest, error) {
	state, err := encodeDialogState(dialogState{Argv: argv, PostID: postID, ChannelID: channelID})
	if err != nil {
		return model.OpenDialogRequest{}, err
	}
	prompt := dialogPromptFor(argv)
	return model.OpenDialogRequest{
		TriggerId: triggerID,
		URL:       "/plugins/" + manifestID + "/dialog",
		Dialog: model.Dialog{
			CallbackId:       dialogCallbackID,
			Title:            prompt.Title,
			IntroductionText: prompt.Introduction,
			Elements:         []model.DialogElement{},
			SubmitLabel:      prompt.SubmitLabel,
			NotifyOnCancel:   false,
			State:            state,
		},
	}, nil
}

// handleDialog services the OpenInteractiveDialog submit POST. Cancellations
// are no-ops by design (spike §0.4); submissions replay the argv against the
// original bot post and round-trip `tasks.get` to refresh the action set.
func (p *Plugin) handleDialog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	userID := r.Header.Get(headerUserID)
	if userID == "" {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	var sub model.SubmitDialogRequest
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if sub.Cancelled {
		writeDialogOK(w)
		return
	}

	client := p.getClient()
	rc := p.getRexec()
	botID := p.getBotUserID()
	if client == nil || rc == nil || botID == "" {
		sendDialogEphemeral(client, botID, sub.ChannelId, userID, "fulcrum plugin not fully activated")
		writeDialogOK(w)
		return
	}

	st, err := decodeDialogState(sub.State)
	if err != nil {
		sendDialogEphemeral(client, botID, sub.ChannelId, userID, "dialog state invalid: "+err.Error())
		writeDialogOK(w)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), rexecRunTimeout)
	defer cancel()
	res, runErr := rc.Run(ctx, prependFulcrumJSON(st.Argv), rexec.WithTimeout(rexecRunTimeout))
	if runErr != nil {
		sendDialogEphemeral(client, botID, st.ChannelID, userID, fmt.Sprintf("fulcrum unreachable: %v", runErr))
		writeDialogOK(w)
		return
	}
	if res.ExitCode != 0 {
		sendDialogEphemeral(client, botID, st.ChannelID, userID, "fulcrum error: "+truncate(strings.TrimSpace(string(res.Stderr)), 200))
		writeDialogOK(w)
		return
	}

	verb, errCode, errMsg, parseErr := parseEnvelopeOutcome(res.Stdout)
	if parseErr != nil {
		sendDialogEphemeral(client, botID, st.ChannelID, userID, "render error: "+parseErr.Error())
		writeDialogOK(w)
		return
	}
	if errCode != "" {
		sendDialogEphemeral(client, botID, st.ChannelID, userID, verbBusinessErrorMessage(verb, errCode, errMsg))
		writeDialogOK(w)
		return
	}

	// App mutation verbs emit operation-level failure as
	// `{success:false, error:"<text>"}` in the payload (cli/JSON_SCHEMA.md
	// §apps.deploy / stop / rollback). Surface those ephemerally per spike
	// §B.7.5 so the original card's buttons stay valid — the app didn't
	// actually transition.
	if appMutationVerbs[verb] {
		if _, outcome, ok := parseAppMutationOutcome(res.Stdout); ok && !outcome.Success {
			sendDialogEphemeral(client, botID, st.ChannelID, userID, appsPayloadErrorMessage(verb, outcome))
			writeDialogOK(w)
			return
		}
	}

	// Successful mutation: route by verb family.
	// - Task mutations round-trip `tasks.get` (§B.3.4).
	// - App round-trip mutations (apps.stop) round-trip `apps.get` (§B.7.6).
	// - Other app mutations (apps.rollback today; apps.deploy is not dialog-
	//   gated but rides this same path if it ever becomes one) render their
	//   per-verb result card directly with the actor mention.
	switch {
	case taskMutationVerbs[verb]:
		if err := refreshTaskPost(ctx, p, client, rc, botID, st.PostID, taskIDFromArgv(st.Argv), res.Stdout); err != nil {
			sendDialogEphemeral(client, botID, st.ChannelID, userID, err.Error())
		}
	case appRoundTripMutationVerbs[verb]:
		if err := refreshAppPost(ctx, p, client, rc, botID, st.PostID, appIDFromArgv(st.Argv), res.Stdout, userID); err != nil {
			sendDialogEphemeral(client, botID, st.ChannelID, userID, err.Error())
		}
	default:
		if err := applyEnvelopeToPost(client, botID, st.PostID, res.Stdout, userID); err != nil {
			sendDialogEphemeral(client, botID, st.ChannelID, userID, err.Error())
		}
	}
	writeDialogOK(w)
}

func writeDialogOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(model.SubmitDialogResponse{})
}

func sendDialogEphemeral(client *pluginapi.Client, botID, channelID, userID, text string) {
	if client == nil || channelID == "" || userID == "" {
		return
	}
	post := &model.Post{
		ChannelId: channelID,
		UserId:    botID,
		Message:   text,
	}
	client.Post.SendEphemeralPost(userID, post)
}

// prependFulcrumJSON converts a bare CLI argv ("tasks","set-status",...) into
// the full invocation the rexec-go client expects: leading "fulcrum" + trailing
// "--json". Centralized so /action and /dialog stay in lockstep with the
// envelope contract.
func prependFulcrumJSON(argv []string) []string {
	out := make([]string, 0, len(argv)+2)
	out = append(out, "fulcrum")
	out = append(out, argv...)
	out = append(out, "--json")
	return out
}
