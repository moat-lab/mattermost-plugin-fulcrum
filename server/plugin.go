package main

import (
	"fmt"
	"sync"

	rexec "github.com/Mouriya-Emma/rexec-go"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

const (
	botUsername    = "fulcrum"
	botDisplayName = "Fulcrum"
	botDescription = "Fulcrum slash + interactive cards"
	slashTrigger   = "f"
)

// Plugin is the Mattermost plugin entry point.
type Plugin struct {
	plugin.MattermostPlugin

	mu        sync.RWMutex
	client    *pluginapi.Client
	rexec     *rexec.Client
	rexecAddr string
	botUserID string
}

// OnActivate wires the plugin into the host server: it constructs the
// pluginapi client, ensures the bot user exists, registers the slash command
// with its AutocompleteData tree, and dials rexecd.
func (p *Plugin) OnActivate() error {
	client := pluginapi.NewClient(p.API, p.Driver)

	botID, err := client.Bot.EnsureBot(&model.Bot{
		Username:    botUsername,
		DisplayName: botDisplayName,
		Description: botDescription,
	})
	if err != nil {
		return fmt.Errorf("ensure bot: %w", err)
	}

	addr, err := resolveRexecdAddr()
	if err != nil {
		return fmt.Errorf("resolve rexecd addr: %w", err)
	}

	rc, err := rexec.New(addr)
	if err != nil {
		return fmt.Errorf("rexec client for %q: %w", addr, err)
	}

	cmd := &model.Command{
		Trigger:          slashTrigger,
		AutoComplete:     true,
		AutoCompleteDesc: "Run Fulcrum verbs and act on cards.",
		AutoCompleteHint: "[verb] [...args]",
		DisplayName:      botDisplayName,
		Description:      botDescription,
		AutocompleteData: buildAutocompleteTree(),
	}
	if err := client.SlashCommand.Register(cmd); err != nil {
		_ = rc.Close()
		return fmt.Errorf("register slash /%s: %w", slashTrigger, err)
	}

	p.mu.Lock()
	p.client = client
	p.rexec = rc
	p.rexecAddr = addr
	p.botUserID = botID
	p.mu.Unlock()

	client.Log.Info("fulcrum plugin activated",
		"bot_user_id", botID,
		"rexecd_addr", addr,
	)
	return nil
}

// OnDeactivate releases the rexec gRPC connection. The Mattermost host
// unregisters the slash command automatically on plugin teardown.
func (p *Plugin) OnDeactivate() error {
	p.mu.Lock()
	rc := p.rexec
	p.rexec = nil
	p.mu.Unlock()
	if rc != nil {
		return rc.Close()
	}
	return nil
}

func (p *Plugin) getClient() *pluginapi.Client {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.client
}

func (p *Plugin) getRexec() *rexec.Client {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.rexec
}

func (p *Plugin) getBotUserID() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.botUserID
}
