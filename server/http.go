package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	rexec "github.com/Mouriya-Emma/rexec-go"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

const (
	// actionContextArgvKey is the context key Mattermost will send back in
	// an integration action payload. Buttons emitted by render layers must
	// stuff their argv under this key.
	actionContextArgvKey = "argv"

	// headerUserID is the Mattermost-set header on integration callbacks.
	headerUserID = "Mattermost-User-Id"
)

// ServeHTTP routes plugin-local HTTP traffic. v1 exposes two endpoints:
// /action for interactive post buttons and /dialog for confirmation dialog
// submits (spike §0.4 / §C.3). Both endpoints assume Mattermost has already
// authenticated the user — the headerUserID double-check is defense in
// depth.
func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/action":
		p.handleAction(w, r)
	case "/dialog":
		p.handleDialog(w, r)
	default:
		http.NotFound(w, r)
	}
}

// actionRequest mirrors the body Mattermost sends to integration action
// endpoints. Only the fields the plugin uses are decoded; the rest is
// tolerated.
type actionRequest struct {
	UserID    string         `json:"user_id"`
	ChannelID string         `json:"channel_id"`
	TeamID    string         `json:"team_id"`
	PostID    string         `json:"post_id"`
	TriggerID string         `json:"trigger_id"`
	Context   map[string]any `json:"context"`
}

func (p *Plugin) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	mmUser := r.Header.Get(headerUserID)
	if mmUser == "" {
		// Mattermost rejects un-authenticated integration calls upstream;
		// double-check defensively because plugin HTTP can be reached
		// directly via /plugins/<id>/... if a future host build forgets
		// the header.
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	var req actionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	argv, err := actionArgv(req.Context)
	if err != nil {
		writeActionError(w, err.Error())
		return
	}

	client := p.getClient()
	rc := p.getRexec()
	botID := p.getBotUserID()
	if client == nil || rc == nil || botID == "" {
		writeActionError(w, "fulcrum plugin not fully activated")
		return
	}

	// Dialog-gated buttons (spike §0.4) never invoke the CLI directly: the
	// click opens a confirmation dialog and the actual mutation runs from
	// /dialog after the user submits. The original post stays untouched so
	// the buttons remain visible if the user backs out.
	if isDialogClick(req.Context) {
		bareArgv := argvFromContext(req.Context)
		dlg, dialogErr := buildOpenDialogRequest(req.TriggerID, bareArgv, req.PostID, req.ChannelID)
		if dialogErr != nil {
			writeActionError(w, "build dialog: "+dialogErr.Error())
			return
		}
		if err := client.Frontend.OpenInteractiveDialog(dlg); err != nil {
			writeActionError(w, "open dialog: "+err.Error())
			return
		}
		writeActionOK(w)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), rexecRunTimeout)
	defer cancel()
	res, runErr := rc.Run(ctx, argv, rexec.WithTimeout(rexecRunTimeout))
	if runErr != nil {
		writeActionError(w, fmt.Sprintf("fulcrum unreachable: %v", runErr))
		return
	}
	if res.ExitCode != 0 {
		writeActionError(w, "fulcrum error: "+truncate(strings.TrimSpace(string(res.Stderr)), 200))
		return
	}

	verb, errCode, errMsg, parseErr := parseEnvelopeOutcome(res.Stdout)
	if parseErr != nil {
		writeActionError(w, "render error: "+parseErr.Error())
		return
	}
	// Envelope-shaped {code,message} business errors on a button-triggered
	// verb leave the original card alone (§B.3.5 / §B.7.5): the entity state
	// didn't change, the existing buttons are still valid, and only the
	// clicking user needs to know what failed. apps.logs and tasks.diff are
	// the two exceptions: `logs_unavailable` (§B.8.5) and `git_unavailable`
	// (§B.5.5) render as colorError cards replacing the current post, so
	// callers fall through to the renderer for those codes.
	if errCode != "" {
		if verb == "apps.logs" && !appLogsEphemeralCodes[errCode] {
			if err := applyEnvelopeToPostWithRequest(client, botID, req.PostID, res.Stdout, req.UserID, argvFromContext(req.Context)); err != nil {
				writeActionError(w, err.Error())
				return
			}
			writeActionOK(w)
			return
		}
		if verb == "apps.logs" {
			hints, argvAppID := extractAppLogsHints(argvFromContext(req.Context))
			writeActionError(w, appLogsBusinessErrorMessage(errCode, errMsg, argvAppID, hints.RequestedService))
			return
		}
		if verb == "tasks.diff" && !taskDiffEphemeralCodes[errCode] {
			if err := applyEnvelopeToPostWithRequest(client, botID, req.PostID, res.Stdout, req.UserID, argvFromContext(req.Context)); err != nil {
				writeActionError(w, err.Error())
				return
			}
			writeActionOK(w)
			return
		}
		if verb == "tasks.diff" {
			writeActionError(w, taskDiffBusinessErrorMessage(errCode, errMsg))
			return
		}
		// search routes query_too_short / invalid_limit ephemerally per spike
		// §B.9.5; other codes fall through to renderBusinessError on the
		// existing card so the user keeps the Refresh + Increase limit
		// buttons in view.
		if verb == "search" && !searchEphemeralCodes[errCode] {
			if err := applyEnvelopeToPostWithRequest(client, botID, req.PostID, res.Stdout, req.UserID, argvFromContext(req.Context)); err != nil {
				writeActionError(w, err.Error())
				return
			}
			writeActionOK(w)
			return
		}
		if verb == "search" {
			writeActionError(w, searchBusinessErrorMessage(errCode, errMsg))
			return
		}
		// jobs routes unknown_scope ephemerally per spike §B.11.5; other
		// codes (notably systemd_unavailable) fall through to the renderer
		// so the user keeps the Refresh button in view on the existing
		// card.
		if verb == "jobs" && !jobsEphemeralCodes[errCode] {
			if err := applyEnvelopeToPostWithRequest(client, botID, req.PostID, res.Stdout, req.UserID, argvFromContext(req.Context)); err != nil {
				writeActionError(w, err.Error())
				return
			}
			writeActionOK(w)
			return
		}
		if verb == "jobs" {
			writeActionError(w, jobsBusinessErrorMessage(errCode, errMsg))
			return
		}
		writeActionError(w, verbBusinessErrorMessage(verb, errCode, errMsg))
		return
	}

	// App mutation verbs (apps.deploy/stop/rollback) emit operation-level
	// failure as `{success:false, error:"<text>"}` in the payload, not as the
	// canonical envelope error object. Surface those ephemerally for the same
	// reason as §B.7.5: the original card's buttons are still valid because
	// the app state didn't transition.
	if appMutationVerbs[verb] {
		if _, outcome, ok := parseAppMutationOutcome(res.Stdout); ok && !outcome.Success {
			writeActionError(w, appsPayloadErrorMessage(verb, outcome))
			return
		}
	}

	// Task mutation verbs round-trip `tasks.get` so the rendered card carries
	// the canonical post-mutation TaskSummary AND the refreshed `actions[]`.
	// The fall-through render of the mutation's own envelope handles the
	// (rare) case where the second CLI call fails — we still want to show
	// the user something, just without the freshly-derived action set.
	if taskMutationVerbs[verb] {
		if err := refreshTaskPost(ctx, p, client, rc, botID, req.PostID, taskIDFromArgv(argvFromContext(req.Context)), res.Stdout); err != nil {
			writeActionError(w, err.Error())
			return
		}
		writeActionOK(w)
		return
	}

	// App round-trip mutations (apps.stop) re-render with the canonical
	// `apps.get` so the action set reflects the new state per spike §B.7.6.
	// apps.deploy and apps.rollback DO NOT round-trip — their per-verb
	// result card carries the deployment_id the user needs to act on.
	if appRoundTripMutationVerbs[verb] {
		if err := refreshAppPost(ctx, p, client, rc, botID, req.PostID, appIDFromArgv(argvFromContext(req.Context)), res.Stdout, req.UserID); err != nil {
			writeActionError(w, err.Error())
			return
		}
		writeActionOK(w)
		return
	}

	// Non-mutation success path (Refresh / view_diff / cross-verb buttons,
	// plus apps.deploy / apps.rollback success which render the per-verb
	// mutation result card on the same post): render the envelope directly,
	// passing the clicking user's id so the §B.7.1 "Initiated by" mention is
	// the actor, not the bot. Argv is threaded through so apps.logs Refresh /
	// Tail more re-renders carry the right tail / service hints (spike §B.8).
	if err := applyEnvelopeToPostWithRequest(client, botID, req.PostID, res.Stdout, req.UserID, argvFromContext(req.Context)); err != nil {
		writeActionError(w, err.Error())
		return
	}
	writeActionOK(w)
}

// appsPayloadErrorMessage formats the ephemeral text shown to the clicking
// user when an apps.* mutation envelope reports `{success:false, error:...}`
// at the payload level (CLI emits the operation error as a string, not as
// the canonical envelope error object — see cli/JSON_SCHEMA.md §apps.deploy).
// Falls back to a generic "<verb>: failed (no detail)" when the CLI omits
// the error string entirely so the user always sees that the click did
// something even if the daemon wasn't specific.
func appsPayloadErrorMessage(verb string, outcome appMutationPayload) string {
	if outcome.Error != nil && *outcome.Error != "" {
		return fmt.Sprintf("%s: %s", verb, truncate(*outcome.Error, 200))
	}
	return verb + ": failed (no detail)"
}

// argvFromContext returns the bare argv (no leading "fulcrum", no trailing
// "--json") stored in the action context. Empty when the context is missing
// or malformed — callers that need a hard failure should use actionArgv.
func argvFromContext(ctx map[string]any) []string {
	if ctx == nil {
		return nil
	}
	list, ok := ctx[actionContextArgvKey].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		s, ok := v.(string)
		if !ok {
			return nil
		}
		out = append(out, s)
	}
	return out
}

// isDialogClick returns true when the button's Integration.Context flags it
// for confirmation-dialog routing (set by makeTaskAction with dialog=true).
func isDialogClick(ctx map[string]any) bool {
	if ctx == nil {
		return false
	}
	v, ok := ctx[actionContextDialogKey]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

func actionArgv(ctx map[string]any) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("missing context")
	}
	raw, ok := ctx[actionContextArgvKey]
	if !ok {
		return nil, fmt.Errorf("missing context.%s", actionContextArgvKey)
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("context.%s must be a JSON array", actionContextArgvKey)
	}
	argv := make([]string, 0, len(list)+2)
	argv = append(argv, "fulcrum")
	for i, v := range list {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("context.%s[%d] is not a string", actionContextArgvKey, i)
		}
		argv = append(argv, s)
	}
	argv = append(argv, "--json")
	return argv, nil
}

// applyEnvelopeToPost renders the given CLI envelope onto the existing bot
// post and applies UpdatePost. Preserves the 5-arg shape for callers (round-
// trip refreshers) that don't carry the originating argv; new request-aware
// callers should prefer applyEnvelopeToPostWithRequest so apps.logs renders
// keep the right tail / service hints across UpdatePost rounds.
func applyEnvelopeToPost(client *pluginapi.Client, botID, postID string, stdout []byte, actorUserID string) error {
	return applyEnvelopeToPostWithRequest(client, botID, postID, stdout, actorUserID, nil)
}

// applyEnvelopeToPostWithRequest is the canonical UpdatePost helper used by
// /action and /dialog when the originating argv is known. Reused by /action's
// non-mutation path and as the second-leg renderer of refreshTaskPost /
// refreshAppPost so the bot-ownership invariant and the
// model.ParseSlackAttachment call live in one place. `actorUserID` threads
// through to verb renderers that surface an "Initiated by" mention
// (apps.deploy/stop/rollback per §B.7.1); other verbs ignore it.
// `requestArgv` carries the invoking argv so apps.logs renders pick up the
// active --tail / --service hints (spike §B.8 — CLI envelope omits both).
func applyEnvelopeToPostWithRequest(client *pluginapi.Client, botID, postID string, stdout []byte, actorUserID string, requestArgv []string) error {
	att, renderErr := renderEnvelopeAtForRequest(stdout, time.Now(), actorUserID, requestArgv)
	if renderErr != nil {
		return fmt.Errorf("render error: %v", renderErr)
	}
	post, getErr := client.Post.GetPost(postID)
	if getErr != nil {
		return fmt.Errorf("get post: %v", getErr)
	}
	// UpdatePost only succeeds because the original post's UserId is the
	// bot — that is the entire reason this plugin exists; if this stops
	// working in production it almost certainly means the underlying post
	// is user-owned (legacy outgoing-webhook artifact) and the user needs
	// to re-issue the slash command to get a real bot post.
	if post.UserId != botID {
		return errors.New("this post is not owned by the fulcrum bot (re-run the slash command)")
	}
	post.Props = map[string]any{}
	model.ParseSlackAttachment(post, []*model.SlackAttachment{att})
	if err := client.Post.UpdatePost(post); err != nil {
		return fmt.Errorf("update post: %v", err)
	}
	return nil
}

// refreshTaskPost is the post-mutation round-trip (§B.3.4): re-invoke
// `fulcrum tasks get <id>` and render that envelope onto the original post.
// `originalStdout` is the mutation verb's own envelope — when taskID is
// empty (unrecognized argv shape) or the round-trip fails, the renderer
// falls back to rendering the mutation envelope directly so the user at
// least sees that something happened. Returns a non-nil error only when both
// the round-trip and the fallback failed; callers surface that to the user.
func refreshTaskPost(ctx context.Context, _ *Plugin, client *pluginapi.Client, rc *rexec.Client, botID, postID, taskID string, originalStdout []byte) error {
	if taskID != "" {
		refreshRes, refreshErr := rc.Run(ctx, prependFulcrumJSON([]string{"tasks", "get", taskID}), rexec.WithTimeout(rexecRunTimeout))
		if refreshErr == nil && refreshRes.ExitCode == 0 {
			// Task-detail render does not surface an actor; pass "" so
			// applyEnvelopeToPost stays in lockstep with the legacy contract.
			if err := applyEnvelopeToPost(client, botID, postID, refreshRes.Stdout, ""); err == nil {
				return nil
			}
			// fall through to the mutation envelope render below
		}
	}
	return applyEnvelopeToPost(client, botID, postID, originalStdout, "")
}

// refreshAppPost is the post-mutation round-trip for app verbs (§B.7.6):
// re-invoke `fulcrum apps get <id>` and render that envelope onto the
// original post so the action set reflects the post-mutation status. The
// fallback strategy mirrors refreshTaskPost — when the round-trip fails or
// the appID is empty, render the mutation's own envelope directly so the
// user sees evidence the click did something. The actor id is threaded
// through only for the fallback path because apps.get does not surface an
// "Initiated by" field; the round-trip render passes "" (apps.get has no
// actor field).
func refreshAppPost(ctx context.Context, _ *Plugin, client *pluginapi.Client, rc *rexec.Client, botID, postID, appID string, originalStdout []byte, actorUserID string) error {
	if appID != "" {
		refreshRes, refreshErr := rc.Run(ctx, prependFulcrumJSON([]string{"apps", "get", appID}), rexec.WithTimeout(rexecRunTimeout))
		if refreshErr == nil && refreshRes.ExitCode == 0 {
			if err := applyEnvelopeToPost(client, botID, postID, refreshRes.Stdout, ""); err == nil {
				return nil
			}
			// fall through to the mutation envelope render below
		}
	}
	return applyEnvelopeToPost(client, botID, postID, originalStdout, actorUserID)
}

type actionResponse struct {
	Update *struct{} `json:"update,omitempty"`
	// EphemeralText surfaces an error to the clicking user without
	// touching the original post. Mattermost recognizes this field on
	// integration responses.
	EphemeralText string `json:"ephemeral_text,omitempty"`
}

func writeActionOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(actionResponse{Update: &struct{}{}})
}

func writeActionError(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(actionResponse{EphemeralText: text})
}
