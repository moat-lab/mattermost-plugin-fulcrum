package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// helpVerbEntry mirrors one entry in the `fulcrum help --json` envelope's
// `verbs` array (cli/JSON_SCHEMA.md §help). Per spike §B.13.2 the plugin does
// not filter or re-order the list — every CLI verb (plugin- or operator-
// facing) is surfaced verbatim so the rendered card matches the CLI's
// `--json=false` table 1:1.
type helpVerbEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// helpPayload mirrors the `data` payload of `fulcrum help --json`. schema_version
// + verb live on the envelopeData parent; only the help-specific `verbs` field
// is decoded here.
type helpPayload struct {
	Verbs []helpVerbEntry `json:"verbs"`
}

// renderHelp produces the help-surface SlackAttachment per spike §B.13. The
// envelope-error branch is reached via renderBusinessError before this
// renderer is called; the §B.13.5 spec notes the CLI help verb does not
// currently emit business errors, but the dispatcher arm keeps Refresh
// reachable so a future schema addition (e.g. `backend_unavailable`) still
// surfaces consistently with monitor / projects / apps.list.
func renderHelp(raw json.RawMessage) (*model.SlackAttachment, error) {
	var p helpPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("help payload: %w", err)
	}
	att := &model.SlackAttachment{
		Color:   colorAccent,
		Title:   fmt.Sprintf("Fulcrum · command surface (%d verbs)", len(p.Verbs)),
		Pretext: "Tip: type `/f <verb>` then press Tab for arguments.",
		Text:    helpText(p.Verbs),
		Footer:  "fulcrum/help · schema_version=1",
		Actions: helpActions(),
	}
	return att, nil
}

// helpText composes the §B.13.3 bullet list. Order is preserved from the
// envelope so the plugin never re-ranks CLI verbs — the CLI is the single
// source of truth for verb ordering (and for whether a verb is plugin- or
// operator-only). An empty `verbs` array renders a placeholder so the card
// still surfaces a tip about the CLI source of truth instead of an empty body.
func helpText(verbs []helpVerbEntry) string {
	if len(verbs) == 0 {
		return "_No verbs reported by CLI._"
	}
	lines := make([]string, 0, len(verbs))
	for _, v := range verbs {
		lines = append(lines, fmt.Sprintf("- **`%s`** — %s", v.Name, v.Description))
	}
	return strings.Join(lines, "\n")
}

// helpActions emits the §B.13.4 button row. Refresh re-fetches the same card
// via UpdatePost; Open dashboard opens a fresh dashboard-home post (per spike
// §B.13.6 dashboard does not replace help so the user keeps the verb catalog
// open as reference). No per-verb buttons — every verb takes different args,
// and a static button would land the user on an ephemeral error.
func helpActions() []*model.PostAction {
	return []*model.PostAction{
		makeAction("help_refresh", "Refresh", postActionStyleDefault, helpRefreshArgv()),
		makeAction("help_open_dashboard", "Open dashboard", postActionStylePrimary, []string{"dashboard"}),
	}
}

// helpRefreshArgv is the Refresh argv used by both the renderer and the
// colorError card. Centralized so the two paths can never drift.
func helpRefreshArgv() []string {
	return []string{"help"}
}
