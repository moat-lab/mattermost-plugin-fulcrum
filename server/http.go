package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	rexec "github.com/Mouriya-Emma/rexec-go"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

const (
	// actionContextArgvKey is the context key Mattermost will send back in
	// an integration action payload. Buttons emitted by render layers must
	// stuff their argv under this key.
	actionContextArgvKey = "argv"

	// headerUserID is the Mattermost-set header on integration callbacks.
	headerUserID = "Mattermost-User-Id"
)

// ServeHTTP routes plugin-local HTTP traffic. v1 exposes a single endpoint
// for interactive post actions; future endpoints (dialog submit, dynamic
// autocomplete listings) hang off the same router.
func (p *Plugin) ServeHTTP(_ *plugin.Context, w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/action":
		p.handleAction(w, r)
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

	att, renderErr := renderEnvelope(res.Stdout)
	if renderErr != nil {
		writeActionError(w, "render error: "+renderErr.Error())
		return
	}

	// Pull the existing post so the host can apply the attachments update
	// without losing other props.
	post, getErr := client.Post.GetPost(req.PostID)
	if getErr != nil {
		writeActionError(w, fmt.Sprintf("get post: %v", getErr))
		return
	}
	// UpdatePost only succeeds because the original post's UserId is the
	// bot — that is the entire reason this plugin exists; if this stops
	// working in production it almost certainly means the underlying post
	// is user-owned (legacy outgoing-webhook artifact) and the user needs
	// to re-issue the slash command to get a real bot post.
	if post.UserId != botID {
		writeActionError(w, "this post is not owned by the fulcrum bot (re-run the slash command)")
		return
	}
	post.Props = map[string]any{}
	model.ParseSlackAttachment(post, []*model.SlackAttachment{att})
	if err := client.Post.UpdatePost(post); err != nil {
		writeActionError(w, fmt.Sprintf("update post: %v", err))
		return
	}

	writeActionOK(w)
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
