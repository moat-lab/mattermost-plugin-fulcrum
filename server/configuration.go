package main

import (
	"errors"
	"os"
	"strings"
)

// rexecdAddrEnv is the only deployment-side knob this plugin reads.
// The umbrella design (Mouriya-Emma/fulcrum#221 §5.3) exposes no business
// settings to admins; the rexecd network coordinate is provisioned by IaC
// (pve-vctcn Mattermost compose) as a process env var, so the plugin keeps
// plugin.json's settings_schema empty.
const rexecdAddrEnv = "REXECD_ADDR"

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
