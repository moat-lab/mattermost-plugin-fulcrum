package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// rexecdAddrEnv is the deployment-coordinate knob this plugin reads from the
// process environment. The umbrella design (Mouriya-Emma/fulcrum#221 §5.3)
// keeps network coordinates out of System Console; only admin-tunable
// business knobs (DefaultHostID) live in plugin.json's settings_schema.
const rexecdAddrEnv = "REXECD_ADDR"

// configuration mirrors the plugin.json settings_schema. Field names must
// match the `key` of each setting verbatim so Mattermost's
// LoadPluginConfiguration JSON unmarshal binds them.
type configuration struct {
	// DefaultHostID is the host id the plugin injects into `tasks create`
	// argv when the user omits `--host`. fulcrum CLI requires hostId in
	// remote-only deployments (fulcrum/server/routes/tasks.ts: remote-only
	// rejects body without hostId). Empty value means no injection — the
	// user must pass `--host` explicitly or the CLI/server reject is the
	// user-visible surface (preserving prior behavior for installations
	// that haven't configured this setting yet).
	DefaultHostID string `json:"DefaultHostID"`
}

// trimmedDefaultHostID returns DefaultHostID with surrounding whitespace
// removed. Admins sometimes paste host ids with trailing newlines from a
// terminal copy; trimming makes the injection contract robust without
// changing the JSON schema.
func (c configuration) trimmedDefaultHostID() string {
	return strings.TrimSpace(c.DefaultHostID)
}

// resolveRexecdAddr reads the rexecd gRPC endpoint from the process
// environment. It returns an error if the value is missing or blank so the
// plugin can fail OnActivate with a clear message instead of silently dialing
// an empty target.
func resolveRexecdAddr() (string, error) {
	raw := strings.TrimSpace(os.Getenv(rexecdAddrEnv))
	if raw == "" {
		return "", errors.New(rexecdAddrEnv + " is unset; set it on the Mattermost server process (see README)")
	}
	return raw, nil
}

// loadPluginConfiguration deserializes the System Console settings into a
// configuration struct. Wrapping LoadPluginConfiguration keeps the
// Mattermost API contact point in one file so the lifecycle hook in
// plugin.go can stay focused on locking + storage.
func (p *Plugin) loadPluginConfiguration() (configuration, error) {
	var cfg configuration
	if err := p.API.LoadPluginConfiguration(&cfg); err != nil {
		return configuration{}, fmt.Errorf("LoadPluginConfiguration: %w", err)
	}
	return cfg, nil
}
