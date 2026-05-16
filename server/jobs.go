package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// jobSummary mirrors the canonical JobSummary shape in
// cli/JSON_SCHEMA.md §JobSummary. enabled is a *bool so a future schema
// relaxation that drops the field (or makes it null on systemd-unavailable
// rows) surfaces as `—` rather than rendering an always-false default.
// schedule / nextRun / lastRun are pointers because the CLI schema marks
// them nullable; lastResult is a pointer for the same reason.
type jobSummary struct {
	Name       string  `json:"name"`
	Scope      string  `json:"scope"`
	State      string  `json:"state"`
	Enabled    *bool   `json:"enabled"`
	NextRun    *string `json:"nextRun"`
	LastRun    *string `json:"lastRun"`
	LastResult *string `json:"lastResult"`
	Schedule   *string `json:"schedule"`
}

// jobsPayload mirrors the `data` payload of `fulcrum jobs --json`
// (cli/JSON_SCHEMA.md §jobs). The CLI echoes the effective `scope` so the
// renderer can title / footer / button-argv off the authoritative value
// instead of re-parsing slash argv.
type jobsPayload struct {
	Scope string       `json:"scope"`
	Total int          `json:"total"`
	Jobs  []jobSummary `json:"jobs"`
}

// jobsRowCap caps the rendered markdown table at the spike §B.11.3 ceiling
// (30 rows). Beyond the cap the renderer drops trailing rows and appends a
// footer truncation note so the user knows the count came from `total`, not
// from what they see in the table.
const jobsRowCap = 30

// jobsBranch is the §B.11 five-way card classification: empty (total=0) /
// active-or-inactive (no failed and no waiting) / waiting (no failed, ≥1
// waiting) / failed (≥1 failed) / error (handled by
// renderJobsBusinessError before this enum is reached).
type jobsBranch int

const (
	jobsBranchEmpty jobsBranch = iota
	jobsBranchActiveOrInactive
	jobsBranchWaiting
	jobsBranchFailed
)

// jobsEphemeralCodes lists the business error.code values that spike §B.11.5
// routes through the ephemeral path. `unknown_scope` is purely a malformed
// flag — a colorError bot card would be noise because the channel can't act
// on it. `systemd_unavailable` is NOT ephemeral; it gets the renderer's
// colorError card so the user keeps Refresh visible.
var jobsEphemeralCodes = map[string]bool{
	"unknown_scope": true,
}

// renderJobs produces the jobs-panel SlackAttachment per spike §B.11. The
// envelope-error branch is reached via renderJobsBusinessError before this
// renderer is called; the remaining four branches share the same Title /
// Footer shape and differ on Color + Pretext + Text + scope-filter buttons.
func renderJobs(raw json.RawMessage) (*model.SlackAttachment, error) {
	var p jobsPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("jobs payload: %w", err)
	}
	scope := jobsScope(p.Scope)
	counts := jobsCounts(p.Jobs)
	branch := jobsBranchFor(p, counts)
	att := &model.SlackAttachment{
		Color:   jobsColor(branch),
		Title:   fmt.Sprintf("Jobs · scope=%s (%d)", scope, p.Total),
		Pretext: jobsPretext(counts),
		Text:    jobsText(p, branch),
		Footer:  jobsFooter(scope, p.Total, len(p.Jobs)),
		Actions: jobsActions(scope),
	}
	return att, nil
}

// jobsScope normalizes the CLI-echoed scope field with a defensive fallback
// to "all". The CLI defaults to all when --scope is omitted; the fallback
// keeps the title / footer / Refresh argv coherent against an older CLI that
// omits the echo entirely.
func jobsScope(s string) string {
	if s == "" {
		return "all"
	}
	return s
}

// jobsStateCounts bundles the per-state counts the §B.11 color and pretext
// branches need. `unknown` collects every state outside the canonical four
// so a future CLI state addition (e.g. `queued`) doesn't silently win the
// active-or-inactive branch.
type jobsStateCounts struct {
	active   int
	waiting  int
	failed   int
	inactive int
	unknown  int
}

// jobsCounts buckets jobs by state into the canonical four §B.11 buckets
// (plus `unknown` as the catch-all). Walking the slice once here keeps the
// branch / pretext / text helpers cheap and deterministic.
func jobsCounts(jobs []jobSummary) jobsStateCounts {
	var c jobsStateCounts
	for _, j := range jobs {
		switch j.State {
		case "active":
			c.active++
		case "waiting":
			c.waiting++
		case "failed":
			c.failed++
		case "inactive":
			c.inactive++
		default:
			c.unknown++
		}
	}
	return c
}

// jobsBranchFor classifies an envelope into one of the four rendered §B.11
// branches. Empty wins first (total=0 / no jobs) so an envelope with no rows
// renders the TODO grey card regardless of any echoed `total`. Failed wins
// over waiting because the spec hierarchy is failed > waiting > active /
// inactive. Unknown states fall through into the active-or-inactive bucket
// because they don't carry color signal of their own.
func jobsBranchFor(p jobsPayload, c jobsStateCounts) jobsBranch {
	if p.Total == 0 || len(p.Jobs) == 0 {
		return jobsBranchEmpty
	}
	if c.failed > 0 {
		return jobsBranchFailed
	}
	if c.waiting > 0 {
		return jobsBranchWaiting
	}
	return jobsBranchActiveOrInactive
}

// jobsColor maps a branch to its §B.11.3 color token.
func jobsColor(branch jobsBranch) string {
	switch branch {
	case jobsBranchFailed:
		return colorPriorityHigh
	case jobsBranchWaiting:
		return colorWarn
	case jobsBranchEmpty:
		return colorStatusTODO
	default:
		return colorAccent
	}
}

// jobsPretext renders the §B.11.3 single-line aggregate showing only the
// `count>0` buckets in the canonical state order (active → waiting →
// failed → inactive). Unknown-state rows are suppressed from the pretext
// because the spec only enumerates the canonical four; review will catch
// drift via the `unknown` counter surfacing through fallback chip rendering.
// Returns "" on the empty branch so the renderer omits Pretext entirely
// rather than render a misleading "0×... ".
func jobsPretext(c jobsStateCounts) string {
	parts := make([]string, 0, 4)
	if c.active > 0 {
		parts = append(parts, fmt.Sprintf("active ×%d", c.active))
	}
	if c.waiting > 0 {
		parts = append(parts, fmt.Sprintf("waiting ×%d", c.waiting))
	}
	if c.failed > 0 {
		parts = append(parts, fmt.Sprintf("failed ×%d", c.failed))
	}
	if c.inactive > 0 {
		parts = append(parts, fmt.Sprintf("inactive ×%d", c.inactive))
	}
	return strings.Join(parts, " · ")
}

// jobsText composes the SlackAttachment.Text body. The empty branch shows
// the §B.11.3 no-jobs hint with the active scope echoed back. The three
// non-empty branches share the markdown table form composed by
// jobsTableText so reviewers can rely on positional column comparison
// across cards.
func jobsText(p jobsPayload, branch jobsBranch) string {
	if branch == jobsBranchEmpty {
		return fmt.Sprintf("_No jobs in scope=%s. Try --scope=all._", jobsScope(p.Scope))
	}
	return jobsTableText(p.Jobs)
}

// jobsTableText composes the §B.11.3 markdown table. Rows beyond jobsRowCap
// are dropped from the table; the user sees `total` and the truncation note
// in the Footer so the cap doesn't silently hide rows.
func jobsTableText(jobs []jobSummary) string {
	headers := []string{"State", "Enabled", "Name", "Schedule", "Next run", "Last result"}
	limit := len(jobs)
	if limit > jobsRowCap {
		limit = jobsRowCap
	}
	rows := make([][]string, 0, limit)
	for i := 0; i < limit; i++ {
		j := jobs[i]
		rows = append(rows, []string{
			jobsStateChip(j.State),
			jobsEnabledCell(j.Enabled),
			jobsNameCell(j.Name),
			jobsScheduleCell(j.Schedule),
			jobsNextRunCell(j.NextRun),
			jobsLastResultCell(j.LastResult),
		})
	}
	return renderMarkdownTable(headers, rows)
}

// jobsStateChip renders the state column emoji+label per spike §B.11.3.
// active=blue, waiting=hourglass, failed=red, inactive=black. Unknown
// states fall through to the grey-question chip used elsewhere so a future
// CLI state addition still renders without dropping the row entirely; the
// fallback surfaces the new state verbatim so review can spot it as a
// schema gap (§C.5).
func jobsStateChip(state string) string {
	switch state {
	case "active":
		return ":large_blue_circle: active"
	case "waiting":
		return ":hourglass: waiting"
	case "failed":
		return ":red_circle: failed"
	case "inactive":
		return ":black_circle: inactive"
	}
	if state == "" {
		return "—"
	}
	return ":grey_question: " + state
}

// jobsEnabledCell renders the Enabled column emoji-only per spike §B.11.3.
// nil falls through to "—" so a schema relaxation (enabled becoming
// nullable) doesn't render a misleading :x:.
func jobsEnabledCell(enabled *bool) string {
	if enabled == nil {
		return "—"
	}
	if *enabled {
		return ":white_check_mark:"
	}
	return ":x:"
}

// jobsNameCell renders the unit name as inline code per spike §B.11.3.
// Pipe escaping mirrors tasksListTitleCell so a unit name containing `|`
// (rare for systemd but legal in shell-spawned jobs) doesn't break the
// markdown table layout.
func jobsNameCell(name string) string {
	if name == "" {
		return "—"
	}
	if strings.Contains(name, "|") {
		name = strings.ReplaceAll(name, "|", "&#124;")
	}
	return "`" + name + "`"
}

// jobsScheduleCell wraps the schedule string in inline code so a systemd
// OnCalendar / cron expression (which may contain `*` or `/`) renders
// verbatim without being interpreted as markdown. Empty / nil → "—".
func jobsScheduleCell(schedule *string) string {
	if schedule == nil || *schedule == "" {
		return "—"
	}
	s := *schedule
	if strings.Contains(s, "|") {
		s = strings.ReplaceAll(s, "|", "&#124;")
	}
	return "`" + s + "`"
}

// jobsNextRunCell renders the next-run ISO timestamp verbatim per spike
// §B.11.3. The spike's example renders both an absolute timestamp and a
// relative phrase ("2026-05-16 03:00 (in 18h)") but the CLI envelope only
// carries the absolute ISO-8601 — the renderer surfaces what the CLI
// actually emits and leaves the relative-phrase enrichment to a future CLI
// schema addition (§C.5).
func jobsNextRunCell(nextRun *string) string {
	if nextRun == nil || *nextRun == "" {
		return "—"
	}
	return *nextRun
}

// jobsLastResultCell renders the last-result enum verbatim per spike
// §B.11.3. CLI values today are `success` / `failed` / `unknown`; nil /
// empty render as "—" so the column width stays stable.
func jobsLastResultCell(lastResult *string) string {
	if lastResult == nil || *lastResult == "" {
		return "—"
	}
	return *lastResult
}

// jobsFooter renders the §B.11.3 footer plus the row-cap truncation note
// when the rendered table dropped rows. The truncation note names both the
// CLI total and the rendered count so the user knows whether they need to
// narrow the scope filter to see the remainder.
func jobsFooter(scope string, total, rendered int) string {
	base := fmt.Sprintf("fulcrum/jobs · scope=%s · total=%d", scope, total)
	if rendered > jobsRowCap {
		return fmt.Sprintf("%s · showing first %d", base, jobsRowCap)
	}
	return base
}

// jobsActions emits the §B.11.4 button row. Refresh is always present.
// `View user only` / `View system only` appear when the current card is on
// scope=all so the user can drill into one bucket; `Back to all scopes`
// appears on the inverse so a scoped card has a one-click return path.
func jobsActions(scope string) []*model.PostAction {
	actions := []*model.PostAction{
		makeAction("jobs_refresh", "Refresh", postActionStyleDefault, jobsRefreshArgv(scope)),
	}
	if scope == "all" {
		actions = append(actions,
			makeAction("jobs_view_user", "View user only", postActionStyleDefault, []string{"jobs", "--scope=user"}),
			makeAction("jobs_view_system", "View system only", postActionStyleDefault, []string{"jobs", "--scope=system"}),
		)
	} else {
		actions = append(actions,
			makeAction("jobs_back_all", "Back to all scopes", postActionStyleDefault, []string{"jobs", "--scope=all"}),
		)
	}
	return actions
}

// jobsRefreshArgv reconstructs the Refresh argv for the current scope. The
// CLI accepts a bare `jobs` for the default all-scope branch, but the
// renderer always emits the explicit `--scope=<s>` flag so the button stays
// deterministic against a future change to the CLI's default scope.
func jobsRefreshArgv(scope string) []string {
	return []string{"jobs", "--scope=" + scope}
}

// renderJobsBusinessError is the §B.11.5 colorError variant for
// systemd_unavailable (and any future non-ephemeral jobs business code).
// Keeps the Refresh button so the user can retry from the same card once
// the systemd backend recovers, mirroring the monitor / apps.list error
// cards.
func renderJobsBusinessError(scope, code, message string) *model.SlackAttachment {
	return &model.SlackAttachment{
		Title: "fulcrum jobs — error",
		Text:  fmt.Sprintf("`%s` %s", code, message),
		Color: colorError,
		Actions: []*model.PostAction{
			makeAction("jobs_refresh", "Refresh", postActionStyleDefault, jobsRefreshArgv(jobsScope(scope))),
		},
		Footer: "fulcrum/jobs · schema_version=1",
	}
}

// jobsBusinessErrorMessage formats the ephemeral text shown to the
// clicking / slashing user when the envelope's business error is one of
// the §B.11.5 ephemeral codes. Other codes fall through to the renderer's
// colorError card via renderJobsBusinessError.
func jobsBusinessErrorMessage(code, message string) string {
	if code == "unknown_scope" {
		base := "scope must be one of: all | user | system"
		if message != "" {
			return base + " — " + message
		}
		return base
	}
	if message != "" {
		return fmt.Sprintf("jobs: %s — %s", code, message)
	}
	return "jobs: " + code
}

// jobsScopeFromEnvelope decodes only the `scope` field of a jobs envelope
// so the /action and /dialog code paths can compute the Refresh argv for a
// systemd_unavailable colorError card without re-parsing the full payload.
// Returns "" on JSON failure so callers can fall back to argv-derived scope
// via jobsScopeFromArgv.
func jobsScopeFromEnvelope(raw json.RawMessage) string {
	var p struct {
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return ""
	}
	return p.Scope
}

// jobsEffectiveScope is the canonical scope-resolution helper used by the
// renderer's error-branch dispatcher. Prefers the envelope-echoed scope
// (authoritative), falls back to the slash argv (covers an older CLI that
// omits the echo on error envelopes), and finally to "all" so the Refresh
// argv on the colorError card is always well-formed.
func jobsEffectiveScope(rawData json.RawMessage, requestArgv []string) string {
	if s := jobsScopeFromEnvelope(rawData); s != "" {
		return s
	}
	if s := jobsScopeFromArgv(requestArgv); s != "" {
		return s
	}
	return "all"
}

// jobsScopeFromArgv pulls --scope=<s> from the slash / button argv. Accepts
// both `--scope=all` and the two-token `--scope all` form (the
// AutocompleteData accepts both). Returns "" when the argv omits the flag
// so callers (Refresh argv reconstruction on the error card) can fall back
// to the canonical default.
func jobsScopeFromArgv(argv []string) string {
	for i := 0; i < len(argv); i++ {
		tok := argv[i]
		if strings.HasPrefix(tok, "--scope=") {
			return strings.TrimPrefix(tok, "--scope=")
		}
		if tok == "--scope" && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}
