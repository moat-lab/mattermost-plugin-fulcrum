package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// searchPayload mirrors the `data` payload of `fulcrum search <query> --json`
// (cli/JSON_SCHEMA.md §search). The CLI emits a flat list of results and the
// renderer groups them by entityType per spike §B.9.3; group order, snippet
// truncation, and score formatting are all renderer concerns.
type searchPayload struct {
	Query   string         `json:"query"`
	Total   int            `json:"total"`
	Results []searchResult `json:"results"`
}

// searchResult mirrors one element of `data.results[]`. `metadata` is left as
// a raw map because §B.9.3 does not display any metadata field today; it is
// kept on the struct so a future per-entityType extension can decode without
// changing the payload shape.
type searchResult struct {
	EntityType string         `json:"entityType"`
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	Snippet    string         `json:"snippet"`
	Score      float64        `json:"score"`
	Metadata   map[string]any `json:"metadata"`
}

const (
	// searchDefaultLimit matches the CLI's default in cli/src/commands/
	// mattermost-verbs.ts searchCommand (`limit ? Number(...) : 25`). When the
	// slash invocation omits --limit, the renderer surfaces this value in the
	// Pretext + Footer so the Increase limit button has a deterministic
	// starting point. Diverges from the spike's "default 20" wording because
	// the CLI is the authoritative source for the actual run.
	searchDefaultLimit = 25
	// searchLimitCeiling is the §B.9.4 cap on Increase limit doubling so a
	// runaway click can't escalate the CLI request beyond what a single
	// Mattermost post can render comfortably.
	searchLimitCeiling = 200
	// searchSnippetCap is the §B.9.3 snippet truncation budget (70 runes,
	// rune-aware so a CJK match snippet doesn't truncate mid-character).
	searchSnippetCap = 70
)

// searchEphemeralCodes lists the business error.code values that spike §B.9.5
// routes through the ephemeral path (the channel can't usefully act on a
// query-too-short or invalid-limit failure, so the colorError bot card is
// reserved for envelope-level / unknown codes that the renderer surfaces via
// renderBusinessError).
var searchEphemeralCodes = map[string]bool{
	"query_too_short": true,
	"invalid_limit":   true,
}

// searchGroupOrder is the §B.9.3 group ordering. `app` is included even though
// the current CLI schema (cli/JSON_SCHEMA.md §search) does not list it in the
// entityType enum — the spike's authoritative §B.9.3 says apps appear between
// tasks and projects, so the renderer is ready for it; until the CLI emits
// `entityType=app` the apps group is silently empty and falls through.
var searchGroupOrder = []string{"task", "app", "project", "message", "event", "memory", "conversation"}

// searchGroupLabel renders the heading for one entityType bucket. Plural
// because the heading sits above a multi-row list; capitalized because it is
// a markdown sub-heading. Unknown entityTypes fall through to a Title-cased
// echo so a future CLI addition still renders without a code change here.
func searchGroupLabel(entity string) string {
	switch entity {
	case "task":
		return "Tasks"
	case "app":
		return "Apps"
	case "project":
		return "Projects"
	case "message":
		return "Messages"
	case "event":
		return "Events"
	case "memory":
		return "Memories"
	case "conversation":
		return "Conversations"
	}
	if entity == "" {
		return "Other"
	}
	return strings.ToUpper(entity[:1]) + entity[1:] + "s"
}

// renderSearch produces the search-results SlackAttachment per spike §B.9.3.
// The 4 state branches (empty / single-type / mixed / error) resolve here as:
// error is reached via renderBusinessError before this renderer; the remaining
// three differ on Color (empty → colorStatusTODO, single-type & mixed →
// colorAccent) and Text (empty branch shows a no-match hint, the other two
// share the grouped-list form). `requestArgv` carries the slash argv so the
// renderer can surface the effective --limit even though the CLI envelope
// does not echo it back.
func renderSearch(raw json.RawMessage, requestArgv []string) (*model.SlackAttachment, error) {
	var p searchPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("search payload: %w", err)
	}
	limit := searchLimitFromArgv(requestArgv)
	branch := searchBranchFor(p)
	att := &model.SlackAttachment{
		Color:   searchColor(branch),
		Title:   fmt.Sprintf("Search · %q (%d)", p.Query, p.Total),
		Pretext: fmt.Sprintf("limit=%d", limit),
		Text:    searchText(p, branch),
		Footer:  fmt.Sprintf("fulcrum/search · total=%d", p.Total),
		Actions: searchActions(p.Query, limit, p.Total),
	}
	return att, nil
}

// searchBranch is the §B.9 four-way branch the renderer derives from the
// envelope: empty (no results), single (one entityType), mixed (≥2
// entityTypes), error (handled upstream via renderBusinessError and not
// produced here).
type searchBranch int

const (
	searchBranchEmpty searchBranch = iota
	searchBranchSingle
	searchBranchMixed
)

// searchBranchFor classifies the envelope by counting distinct entityTypes
// among the rendered results. The CLI's total field is authoritative for the
// title chip; this branch is only about color + text shape.
func searchBranchFor(p searchPayload) searchBranch {
	if len(p.Results) == 0 {
		return searchBranchEmpty
	}
	seen := map[string]bool{}
	for _, r := range p.Results {
		if r.EntityType == "" {
			// Treat empty entityType as a distinct bucket so a future CLI emission
			// without a typed bucket doesn't silently collapse into the wrong
			// branch.
			seen["__unknown__"] = true
			continue
		}
		seen[r.EntityType] = true
	}
	if len(seen) <= 1 {
		return searchBranchSingle
	}
	return searchBranchMixed
}

// searchColor maps a branch to the §B.9.3 color palette.
func searchColor(branch searchBranch) string {
	switch branch {
	case searchBranchEmpty:
		return colorStatusTODO
	default:
		return colorAccent
	}
}

// searchText composes the SlackAttachment.Text for the three rendered
// branches. The empty branch renders the §B.9.3 no-match hint with the query
// echoed back; the single / mixed branches share the grouped sub-heading +
// markdown bullet list form.
func searchText(p searchPayload, branch searchBranch) string {
	if branch == searchBranchEmpty {
		return fmt.Sprintf("_No matches for %q._\nTry `/f search <other> --limit=<higher>`.", p.Query)
	}
	groups := groupSearchResults(p.Results)
	var b strings.Builder
	first := true
	for _, entity := range searchGroupOrder {
		rows, ok := groups[entity]
		if !ok || len(rows) == 0 {
			continue
		}
		if !first {
			b.WriteString("\n\n")
		}
		first = false
		b.WriteString("### ")
		b.WriteString(searchGroupLabel(entity))
		for _, r := range rows {
			b.WriteString("\n")
			b.WriteString(formatSearchResult(r))
		}
	}
	// Surface entityTypes outside the canonical order at the tail so a future
	// CLI schema addition still renders without dropping rows. The fallback
	// label preserves the entityType string verbatim so review can spot the
	// drift in a §C.5 schema gap follow-up.
	for entity, rows := range groups {
		if searchGroupIsKnown(entity) {
			continue
		}
		if len(rows) == 0 {
			continue
		}
		if !first {
			b.WriteString("\n\n")
		}
		first = false
		b.WriteString("### ")
		b.WriteString(searchGroupLabel(entity))
		for _, r := range rows {
			b.WriteString("\n")
			b.WriteString(formatSearchResult(r))
		}
	}
	return b.String()
}

// groupSearchResults buckets results by entityType while preserving the
// relative order CLI emitted them in (the CLI sorts by score; the renderer
// inherits that order inside each bucket).
func groupSearchResults(results []searchResult) map[string][]searchResult {
	out := make(map[string][]searchResult, 4)
	for _, r := range results {
		key := r.EntityType
		if key == "" {
			key = "__unknown__"
		}
		out[key] = append(out[key], r)
	}
	return out
}

// searchGroupIsKnown reports whether an entityType is part of the canonical
// §B.9.3 group order (and therefore already rendered in the deterministic
// pass).
func searchGroupIsKnown(entity string) bool {
	for _, k := range searchGroupOrder {
		if k == entity {
			return true
		}
	}
	return false
}

// formatSearchResult renders one result row per §B.9.3:
//
//	- **[<id>]** <title> · _score 0.91_
//	  > …matched snippet…
//
// The score is 2-decimal-fixed; the snippet drops the leading bullet entirely
// when the CLI emitted an empty snippet so the row collapses to one line.
func formatSearchResult(r searchResult) string {
	head := fmt.Sprintf("- **[`%s`]** %s · _score %s_", r.ID, r.Title, formatSearchScore(r.Score))
	snippet := formatSearchSnippet(r.Snippet)
	if snippet == "" {
		return head
	}
	return head + "\n  > " + snippet
}

// formatSearchScore renders a search score as a fixed-2-decimal float. NaN /
// Inf collapse to "—" so a future schema drift doesn't render `score NaN`.
func formatSearchScore(score float64) string {
	if score != score { // NaN
		return "—"
	}
	return strconv.FormatFloat(score, 'f', 2, 64)
}

// formatSearchSnippet is the spike §C.3 search-snippet helper: collapse
// internal whitespace + truncate to searchSnippetCap runes + append "…" on
// overflow. The helper lives in this file (not format.go) because no other
// feature consumes it today, but the named function makes the spike contract
// explicit and lets future jobs / projects renderers import it directly.
func formatSearchSnippet(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	// Collapse runs of whitespace (newlines / tabs / spaces) to a single space
	// so a multi-line snippet doesn't break the markdown blockquote.
	collapsed := strings.Join(strings.Fields(trimmed), " ")
	return truncateMD(collapsed, searchSnippetCap)
}

// searchActions renders the §B.9.4 button row: Refresh always; Increase limit
// when total exceeds the current limit and the next doubling is still under
// the §B.9.4 ceiling. No per-result button per the spike's MVP decision
// (search hits cross 7 entityTypes and not all have detail-view sub-features).
func searchActions(query string, currentLimit, total int) []*model.PostAction {
	actions := []*model.PostAction{
		makeAction("search_refresh", "Refresh", postActionStyleDefault, searchArgv(query, currentLimit)),
	}
	if next := nextSearchLimit(currentLimit); next > 0 && total > currentLimit {
		actions = append(actions, makeAction("search_increase_limit", "Increase limit", postActionStyleDefault, searchArgv(query, next)))
	}
	return actions
}

// nextSearchLimit returns the doubled limit capped at searchLimitCeiling, or 0
// when the current limit is already at/above the ceiling. The 0 sentinel lets
// searchActions suppress the Increase limit button without leaking the cap to
// the user via a no-op click.
func nextSearchLimit(current int) int {
	if current <= 0 {
		current = searchDefaultLimit
	}
	if current >= searchLimitCeiling {
		return 0
	}
	doubled := current * 2
	if doubled > searchLimitCeiling {
		doubled = searchLimitCeiling
	}
	return doubled
}

// searchArgv reconstructs the slash argv for a Refresh / Increase limit
// re-run. The query is preserved verbatim including any spaces — Mattermost
// integration callbacks already JSON-encode the context array so embedded
// spaces survive the round-trip.
func searchArgv(query string, limit int) []string {
	if limit <= 0 {
		limit = searchDefaultLimit
	}
	return []string{"search", query, fmt.Sprintf("--limit=%d", limit)}
}

// searchLimitFromArgv extracts the effective --limit from the slash argv.
// Accepts both `--limit=N` and the `--limit N` two-token form (the
// AutocompleteData accepts `--limit=<n>` but a manually typed slash may use
// the spaced form). Falls back to searchDefaultLimit when the argv omits the
// flag or carries an unparseable value so the renderer always has a
// deterministic starting point for the doubling button.
func searchLimitFromArgv(argv []string) int {
	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		if strings.HasPrefix(tok, "--limit=") {
			if n, err := strconv.Atoi(strings.TrimPrefix(tok, "--limit=")); err == nil && n > 0 {
				return n
			}
			return searchDefaultLimit
		}
		if tok == "--limit" && i+1 < len(argv) {
			if n, err := strconv.Atoi(argv[i+1]); err == nil && n > 0 {
				return n
			}
			return searchDefaultLimit
		}
	}
	return searchDefaultLimit
}

// searchBusinessErrorMessage formats the ephemeral text shown to the slashing
// user when a `search` envelope returns one of the §B.9.5 ephemeral business
// error codes. Other codes (FETCH_FAILED, unknown) fall through to the
// generic envelope error renderer via renderBusinessError so the user still
// sees the failure as a colorError card.
func searchBusinessErrorMessage(code, message string) string {
	switch code {
	case "query_too_short":
		base := "search query is too short (need at least 2 characters)"
		if message != "" {
			return base + " — " + message
		}
		return base
	case "invalid_limit":
		base := "invalid --limit (must be a positive integer)"
		if message != "" {
			return base + " — " + message
		}
		return base
	}
	if message != "" {
		return fmt.Sprintf("search: %s — %s", code, message)
	}
	return "search: " + code
}
