package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// appsLogsPayload mirrors the `data` payload of `fulcrum apps logs <id> --json`
// (cli/JSON_SCHEMA.md §apps.logs). The CLI does not echo back the `--tail`
// flag, so the requested tail must be threaded in by the caller from the
// invoking argv — see appLogsRenderHints.
type appsLogsPayload struct {
	AppID   string  `json:"app_id"`
	Service *string `json:"service"`
	Logs    string  `json:"logs"`
}

// appLogsRenderHints carries request-context the apps.logs envelope itself
// doesn't preserve. Spike §B.8 wants the rendered card to show the requested
// tail value and to compute the next "Tail more" doubling; neither is
// derivable from the envelope alone (cli/JSON_SCHEMA.md §apps.logs omits
// `tail`). When the caller doesn't know the values (e.g. a non-request path
// rendering an envelope directly), `RequestedTail == 0` is treated as "use
// default 200" so the card still renders coherently.
type appLogsRenderHints struct {
	RequestedTail    int
	RequestedService string
}

const (
	// appLogsDefaultTail mirrors the slash autocomplete suggestion / spike §B.8
	// initial value. Used when the request hint is zero.
	appLogsDefaultTail = 200

	// appLogsMaxTail is the spike §B.8 doubling ceiling; once the active tail
	// hits this value the Tail more button is omitted to prevent unbounded
	// growth.
	appLogsMaxTail = 2000

	// appLogsTextCap is the §B.8 character ceiling for the rendered code block.
	// Logs longer than this are truncated from the FRONT so the tail (most
	// recent lines) stays visible.
	appLogsTextCap = 7000
)

// appLogsEphemeralCodes lists the business error.code values that spike §B.8.5
// requires to surface ephemerally rather than as a bot card. `logs_unavailable`
// is intentionally absent — it renders as a colorError card with Refresh +
// Back to app per §B.8.5.
var appLogsEphemeralCodes = map[string]bool{
	"app_not_found":     true,
	"service_not_found": true,
}

// renderAppLogs produces the app-logs-view SlackAttachment per spike §B.8.3.
// Color is decided by the §B.8.3 matrix: empty → colorStatusTODO, truncated →
// colorWarn, service-scoped → colorAccent, otherwise colorStatusDoing
// (happy-path "live tail" surface, mirrors `running` apps).
func renderAppLogs(raw json.RawMessage, hints appLogsRenderHints) (*model.SlackAttachment, error) {
	var p appsLogsPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("apps.logs payload: %w", err)
	}
	service := hints.RequestedService
	if p.Service != nil && *p.Service != "" {
		service = *p.Service
	}
	tail := hints.RequestedTail
	if tail <= 0 {
		tail = appLogsDefaultTail
	}

	text, truncated, empty := appLogsTextBody(p.Logs)

	return &model.SlackAttachment{
		Color:   appLogsColor(empty, truncated, service),
		Title:   appLogsTitle(p.AppID, service),
		Pretext: fmt.Sprintf("tail=%d", tail),
		Text:    text,
		Footer:  appLogsFooter(service, tail),
		Actions: appLogsActions(p.AppID, tail, service),
	}, nil
}

// appLogsTextBody assembles the §B.8.3 Text block from the raw `logs` field.
// Returns the rendered text plus the (truncated, empty) flags consumed by the
// color matrix. Empty logs collapse to the spike-literal placeholder; long
// logs get sliced from the FRONT so the most recent lines (most relevant to
// debugging) remain visible.
func appLogsTextBody(logs string) (text string, truncated bool, empty bool) {
	if strings.TrimSpace(logs) == "" {
		return "_No log lines in the requested tail window._", false, true
	}
	body := logs
	if len(body) > appLogsTextCap {
		elided := len(body) - appLogsTextCap
		body = fmt.Sprintf("…[%d chars elided]\n%s", elided, body[len(body)-appLogsTextCap:])
		truncated = true
	}
	return "```\n" + body + "\n```", truncated, false
}

// appLogsColor implements the §B.8.3 color decision. Error renders are
// produced separately by renderAppLogsBusinessError; here we cover the three
// non-error branches (empty, truncated, service-scoped) plus the implicit
// happy-path color which the spike enumeration leaves to the renderer.
func appLogsColor(empty, truncated bool, service string) string {
	switch {
	case empty:
		return colorStatusTODO
	case truncated:
		return colorWarn
	case service != "":
		return colorAccent
	default:
		return colorStatusDoing
	}
}

// appLogsTitle composes "Logs · <app_id>" with the optional service suffix
// per §B.8.3. The leading "Logs · " is a literal so the card is recognizable
// in channel scrollback regardless of app id length.
func appLogsTitle(appID, service string) string {
	title := "Logs · " + appID
	if service != "" {
		title += " · " + service
	}
	return title
}

// appLogsFooter renders the spike-mandated footer line. service collapses to
// the literal "all" when the request had no --service filter, so the footer
// is unambiguous when reading scrollback.
func appLogsFooter(service string, tail int) string {
	scope := "all"
	if service != "" {
		scope = service
	}
	return fmt.Sprintf("fulcrum/apps.logs · service=%s · tail=%d", scope, tail)
}

// appLogsActions builds the §B.8.4 button row. Refresh + Back to app are
// always present; Tail more appears only while the current tail is below the
// §B.8.4 ceiling. Every button's argv carries the explicit --tail and
// --service flags so subsequent clicks remain self-describing without the
// renderer leaning on dialog-style server-side state.
func appLogsActions(appID string, tail int, service string) []*model.PostAction {
	out := make([]*model.PostAction, 0, 3)
	out = append(out, makeAction("app_logs_refresh", "Refresh", postActionStyleDefault, appLogsArgv(appID, tail, service)))
	if tail < appLogsMaxTail {
		next := tail * 2
		if next > appLogsMaxTail {
			next = appLogsMaxTail
		}
		out = append(out, makeAction("app_logs_tail_more", "Tail more", postActionStyleDefault, appLogsArgv(appID, next, service)))
	}
	out = append(out, makeAction("app_logs_back", "Back to app", postActionStyleDefault, []string{"apps", "get", appID}))
	return out
}

// appLogsArgv builds the argv form used by Refresh / Tail more buttons. tail
// is always emitted (the renderer always knows it) so each click is
// reproducible from the button context alone; --service is appended only when
// the active request was scoped, keeping the unscoped path one flag shorter.
func appLogsArgv(appID string, tail int, service string) []string {
	argv := []string{"apps", "logs", appID, "--tail=" + strconv.Itoa(tail)}
	if service != "" {
		argv = append(argv, "--service="+service)
	}
	return argv
}

// renderAppLogsBusinessError produces the §B.8.5 colorError card for the
// `logs_unavailable` code (and any future apps.logs business error that
// should surface as a non-ephemeral bot post). The Refresh button reuses the
// active hints so the user can retry the same query once the backend
// recovers; Back to app is the canonical escape hatch out of the error.
func renderAppLogsBusinessError(appID, code, message string, hints appLogsRenderHints) *model.SlackAttachment {
	tail := hints.RequestedTail
	if tail <= 0 {
		tail = appLogsDefaultTail
	}
	return &model.SlackAttachment{
		Color:   colorError,
		Title:   appLogsTitle(appID, hints.RequestedService) + " — error",
		Pretext: fmt.Sprintf("tail=%d", tail),
		Text:    fmt.Sprintf("`%s` %s", code, message),
		Footer:  appLogsFooter(hints.RequestedService, tail),
		Actions: []*model.PostAction{
			makeAction("app_logs_refresh", "Refresh", postActionStyleDefault, appLogsArgv(appID, tail, hints.RequestedService)),
			makeAction("app_logs_back", "Back to app", postActionStyleDefault, []string{"apps", "get", appID}),
		},
	}
}

// appLogsBusinessErrorMessage formats the ephemeral text shown to the
// clicking / slashing user when an apps.logs envelope returns a business
// error.code that spike §B.8.5 routes to ephemeral (app_not_found,
// service_not_found). Unknown codes fall through to the generic
// "<verb>: <code>" shape so a future CLI addition still surfaces.
func appLogsBusinessErrorMessage(code, message, appID, service string) string {
	switch code {
	case "app_not_found":
		base := fmt.Sprintf("apps.logs: %s", code)
		if message != "" {
			base += " — " + message
		}
		return base + " (try `/f apps list`)"
	case "service_not_found":
		if message != "" {
			return fmt.Sprintf("apps.logs: %s — %s", code, message)
		}
		s := service
		if s == "" {
			s = "?"
		}
		id := appID
		if id == "" {
			id = "?"
		}
		return fmt.Sprintf(`apps.logs: service "%s" not found on app %s`, s, id)
	}
	base := fmt.Sprintf("apps.logs: %s", code)
	if message != "" {
		base += " — " + message
	}
	return base
}

// extractAppLogsHints parses the active --tail and --service flags out of an
// argv that can take any of three shapes: the slash-command argv with leading
// "fulcrum" and trailing "--json", the bare button-context argv, or the
// dialog-state argv. The id-bearing positional is index 2 (after `apps logs`)
// in every shape — the function returns the hints derived from flag values
// and the app id so callers can pass them to the renderer without re-parsing.
func extractAppLogsHints(argv []string) (appLogsRenderHints, string) {
	bare := normalizeAppLogsArgv(argv)
	var hints appLogsRenderHints
	appID := ""
	if len(bare) >= 3 && bare[0] == "apps" && bare[1] == "logs" {
		appID = bare[2]
	}
	for _, tok := range bare {
		switch {
		case strings.HasPrefix(tok, "--tail="):
			if n, err := strconv.Atoi(strings.TrimPrefix(tok, "--tail=")); err == nil && n > 0 {
				hints.RequestedTail = n
			}
		case strings.HasPrefix(tok, "--service="):
			hints.RequestedService = strings.TrimPrefix(tok, "--service=")
		}
	}
	return hints, appID
}

// normalizeAppLogsArgv strips the optional leading "fulcrum" and trailing
// "--json" tokens so the caller can treat every argv shape uniformly when
// looking for `apps logs <id>` plus flags. Returns the argv unchanged when no
// trimming is needed so the hint extractor doesn't depend on the caller's
// exact shape.
func normalizeAppLogsArgv(argv []string) []string {
	out := argv
	if len(out) > 0 && out[0] == "fulcrum" {
		out = out[1:]
	}
	if len(out) > 0 && out[len(out)-1] == "--json" {
		out = out[:len(out)-1]
	}
	return out
}
