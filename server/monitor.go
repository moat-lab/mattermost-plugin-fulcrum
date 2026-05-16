package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// monitorPayload mirrors the `data` payload of `fulcrum monitor --json`
// (cli/JSON_SCHEMA.md §monitor). `disk_percent` is nullable on the CLI side
// (host agent may not report a disk reading), so the field is a pointer so the
// renderer can distinguish the `partial` branch (disk missing) from a real
// 0.0% reading. cpu_percent and memory_percent are non-nullable in the CLI
// schema today but kept as pointers as well so a future schema relaxation
// surfaces as `partial` rather than silently rendering 0%.
//
// `MonitorStatus` / `LastSampleAt` / `Since` were added by
// fulcrum#234 / fulcrum PR #235 (merge sha 1093580b) so the plugin can branch
// on the host's reporting state instead of inferring it from byte-identical
// envelopes. They are pointers so an older CLI envelope without the
// discriminator (legacy build still in flight) round-trips to the legacy
// reporting render path rather than crashing or false-classifying.
type monitorPayload struct {
	HostID        string         `json:"host_id"`
	Window        string         `json:"window"`
	MonitorStatus *monitorStatus `json:"monitor_status"`
	LastSampleAt  *string        `json:"last_sample_at"`
	Since         string         `json:"since"`
	CPUPercent    *float64       `json:"cpu_percent"`
	MemoryPercent *float64       `json:"memory_percent"`
	DiskPercent   *float64       `json:"disk_percent"`
}

// monitorStatus is the host-level reporting state discriminator carried in the
// CLI envelope's top-level `monitor_status` field. Three states are exhaustive
// (server side derives them in metrics-collector.ts.getMonitorStatus per
// fulcrum PR #235): the host was never seen (`unconfigured`), the host has
// historical samples but none inside the current window (`no_data_in_window`),
// or there is at least one fresh sample (`reporting`).
type monitorStatus string

const (
	monitorStatusReporting      monitorStatus = "reporting"
	monitorStatusNoDataInWindow monitorStatus = "no_data_in_window"
	monitorStatusUnconfigured   monitorStatus = "unconfigured"
)

// monitorHighThreshold is the §B.10.3 threshold above which a single metric
// promotes the card to colorPriorityHigh and surfaces the high-utilization
// warning line + the View jobs / View apps button rows.
const monitorHighThreshold = 90.0

// monitorMediumThreshold is the §B.10.3 threshold above which a single metric
// promotes the card to colorPriorityMedium (still below the high cutoff).
const monitorMediumThreshold = 70.0

// monitorBranch is the §B.10 four-way card classification of a *reporting*
// host: complete (all three metrics present) with three utilization sub-bands
// (ok / medium / high), and partial (disk_percent=null on a reporting host
// where the agent skipped a disk reading). The error envelope is routed via
// renderMonitorBusinessError before this renderer is reached, and the
// unconfigured / no-data-in-window top-level branches are handled in
// renderMonitor before monitorBranchFor is consulted.
type monitorBranch int

const (
	monitorBranchOK monitorBranch = iota
	monitorBranchMedium
	monitorBranchHigh
	monitorBranchPartial
)

// renderMonitor produces the monitor-snapshot SlackAttachment per spike §B.10
// extended for issue #40's three-state discriminator. Top-level dispatch:
// `unconfigured` and `no_data_in_window` get their own explanatory cards
// (issue #40 acceptance: the operator must be able to tell at a glance which
// of the three "I can't see numbers" root causes they're looking at);
// `reporting` (and legacy envelopes that omit the discriminator entirely) fall
// through to the existing four-branch utility-based render.
func renderMonitor(raw json.RawMessage) (*model.SlackAttachment, error) {
	var p monitorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("monitor payload: %w", err)
	}
	switch monitorEffectiveStatus(p) {
	case monitorStatusUnconfigured:
		return renderMonitorUnconfigured(p), nil
	case monitorStatusNoDataInWindow:
		return renderMonitorNoDataInWindow(p), nil
	default:
		return renderMonitorReporting(p), nil
	}
}

// monitorEffectiveStatus returns the envelope's monitor_status value or falls
// back to `reporting` when the field is absent. The fallback path keeps a
// newer plugin compatible with an older CLI build that doesn't yet emit the
// discriminator — the legacy four-branch render still fires and produces the
// same em-dash card #40 was filed against, which is strictly no worse than
// today.
func monitorEffectiveStatus(p monitorPayload) monitorStatus {
	if p.MonitorStatus == nil {
		return monitorStatusReporting
	}
	return *p.MonitorStatus
}

// renderMonitorReporting renders the §B.10 four-branch card for a host that
// is actively reporting (or for a legacy envelope without a status
// discriminator, which the plugin treats as reporting per the back-compat
// fallback above). This is the body of the pre-#40 renderMonitor; the
// extraction keeps the §B.10 logic untouched so the existing seven test cases
// continue to lock its behavior verbatim.
func renderMonitorReporting(p monitorPayload) *model.SlackAttachment {
	branch := monitorBranchFor(p)
	att := &model.SlackAttachment{
		Color:   monitorColor(branch),
		Title:   fmt.Sprintf("Monitor · %s (window=%s)", p.HostID, p.Window),
		Pretext: monitorPretext(p),
		Fields:  monitorFields(p),
		Footer:  fmt.Sprintf("fulcrum/monitor · host=%s", p.HostID),
		Actions: monitorActions(p),
	}
	if text := monitorWarningText(p); text != "" {
		att.Text = text
	}
	return att
}

// renderMonitorUnconfigured renders the issue #40 unconfigured card: the host
// has never reported a sample, so the four metric slots are intentionally
// omitted (rendering em-dashes here would re-introduce the exact ambiguity
// #40 was filed to fix). Color is colorWarn so the card visually reads as
// "needs attention" without escalating to the red of colorPriorityHigh —
// reporting an outage is the supervisor's job, not the renderer's.
func renderMonitorUnconfigured(p monitorPayload) *model.SlackAttachment {
	return &model.SlackAttachment{
		Color:   colorWarn,
		Title:   fmt.Sprintf("Monitor · %s — unconfigured", p.HostID),
		Pretext: fmt.Sprintf("Monitor backend not installed on host `%s`.", p.HostID),
		Text:    "Run `fulcrum monitor install` on this host (or onboard it via the supervisor) to start the collector. Until a sample lands no CPU / memory / disk readings are available.",
		Footer:  fmt.Sprintf("fulcrum/monitor · host=%s · status=unconfigured", p.HostID),
		Actions: []*model.PostAction{
			makeAction("monitor_refresh", "Refresh", postActionStyleDefault, []string{"monitor"}),
		},
	}
}

// renderMonitorNoDataInWindow renders the issue #40 no-data-in-window card:
// the host has reported samples in the past but none inside the requested
// window. Surfacing the window lower bound (`since`) and the last sample
// timestamp lets the operator decide between "wait longer", "widen the
// window", and "the agent has stopped" without leaving the card.
func renderMonitorNoDataInWindow(p monitorPayload) *model.SlackAttachment {
	last := monitorLastSampleText(p.LastSampleAt)
	since := monitorIsoOrDash(p.Since)
	return &model.SlackAttachment{
		Color:   colorWarn,
		Title:   fmt.Sprintf("Monitor · %s — no data in window", p.HostID),
		Pretext: fmt.Sprintf("No samples for host `%s` in the last %s.", p.HostID, monitorWindowValue(p.Window)),
		Text:    fmt.Sprintf("Window since: `%s`\nLast sample: %s", since, last),
		Footer:  fmt.Sprintf("fulcrum/monitor · host=%s · status=no_data_in_window", p.HostID),
		Actions: []*model.PostAction{
			makeAction("monitor_refresh", "Refresh", postActionStyleDefault, []string{"monitor"}),
		},
	}
}

// monitorLastSampleText renders the last_sample_at ISO timestamp wrapped in a
// code span, with a `never` fallback when the field is null (which the CLI
// emits for the unconfigured state — defensive here for completeness, since
// the unconfigured branch doesn't reach this helper today).
func monitorLastSampleText(ts *string) string {
	if ts == nil || *ts == "" {
		return "never"
	}
	return fmt.Sprintf("`%s`", *ts)
}

// monitorIsoOrDash renders an ISO timestamp as a code span or em-dash when
// the field is empty. Kept separate from monitorLastSampleText so the
// `since` field (always populated by the new CLI envelope) can stay
// unconditionally wrapped without inheriting the "never" fallback that
// makes sense only for `last_sample_at`.
func monitorIsoOrDash(ts string) string {
	if ts == "" {
		return "—"
	}
	return ts
}

// monitorBranchFor classifies a reporting envelope into one of the four §B.10
// branches. `partial` takes precedence over the max-based utilization
// classification because a missing reading already signals an incomplete
// picture — surfacing it as the more attention-grabbing high band would imply
// confidence the renderer doesn't have. View jobs still appears on partial
// via the button guard so the operator can pivot to the job queue when
// monitor data is thin.
func monitorBranchFor(p monitorPayload) monitorBranch {
	if p.DiskPercent == nil {
		return monitorBranchPartial
	}
	max := monitorMaxUtilization(p)
	switch {
	case max >= monitorHighThreshold:
		return monitorBranchHigh
	case max >= monitorMediumThreshold:
		return monitorBranchMedium
	default:
		return monitorBranchOK
	}
}

// monitorColor maps a branch to its §B.10.3 color token.
func monitorColor(branch monitorBranch) string {
	switch branch {
	case monitorBranchHigh:
		return colorPriorityHigh
	case monitorBranchMedium:
		return colorPriorityMedium
	case monitorBranchPartial:
		return colorWarn
	default:
		return colorStatusDone
	}
}

// monitorPretext renders the single-line inline summary required by §B.10.3:
// `cpu <c>% · mem <m>% · disk <d>%`. Missing metrics render as `—` so a future
// CLI schema relaxation (memory_percent becoming nullable, etc.) doesn't crash
// the renderer.
func monitorPretext(p monitorPayload) string {
	return fmt.Sprintf("cpu %s · mem %s · disk %s",
		monitorPercentValue(p.CPUPercent),
		monitorPercentValue(p.MemoryPercent),
		monitorPercentValue(p.DiskPercent),
	)
}

// monitorFields renders the §B.10.3 four-field block. All fields are short so
// the Mattermost client renders the card in a two-column layout. Order is
// fixed (CPU, Memory, Disk, Window) so reviewers can rely on positional
// reasoning when comparing snapshots.
func monitorFields(p monitorPayload) []*model.SlackAttachmentField {
	return []*model.SlackAttachmentField{
		{Title: "CPU", Value: monitorPercentValue(p.CPUPercent), Short: true},
		{Title: "Memory", Value: monitorPercentValue(p.MemoryPercent), Short: true},
		{Title: "Disk", Value: monitorPercentValue(p.DiskPercent), Short: true},
		{Title: "Window", Value: monitorWindowValue(p.Window), Short: true},
	}
}

// monitorPercentValue renders a nullable percent value as `<n>%` (or `—` when
// null). The 1-decimal precision matches the CLI envelope's typical
// `cpu_percent: 12.5` shape and keeps the field cell from ballooning to many
// decimal places when the host agent reports `12.50000001`.
func monitorPercentValue(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%s%%", monitorFormatPercent(*v))
}

// monitorFormatPercent renders a float as a 1-decimal string with trailing
// zero trimmed when the value is a whole number — so `12.5` stays `12.5` but
// `40.0` collapses to `40`. Tests pin both shapes.
func monitorFormatPercent(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	if strings.HasSuffix(s, ".0") {
		s = strings.TrimSuffix(s, ".0")
	}
	return s
}

// monitorWindowValue renders the CLI `window` field with an empty fallback so
// a CLI that omits the field doesn't surface as a blank table cell.
func monitorWindowValue(window string) string {
	if window == "" {
		return "—"
	}
	return window
}

// monitorWarningText renders the §B.10.3 high-utilization line when any single
// metric crosses monitorHighThreshold. Picks the highest-value metric so the
// warning names the worst offender; tiebreak order is cpu > memory > disk so
// the message stays deterministic across reruns of the same envelope. Returns
// empty string when no metric crosses the threshold so the renderer omits the
// Text field entirely.
func monitorWarningText(p monitorPayload) string {
	hi := monitorHighestMetric(p)
	if hi.value < monitorHighThreshold {
		return ""
	}
	return fmt.Sprintf(":warning: %s usage is high (%s%%). Consider /f apps logs / /f jobs to triage.",
		hi.label, monitorFormatPercent(hi.value))
}

// monitorMetricRanking pairs a metric label with its current value so callers
// (warning text, button guards) can reason about the highest reading without
// repeating the cpu / mem / disk pointer dance.
type monitorMetricRanking struct {
	label string
	value float64
}

// monitorHighestMetric returns the highest of the three present metrics in
// label order (cpu > memory > disk) so equal-value ties favor cpu — same
// tiebreak the spike §B.10.3 warning line implicitly requires by writing
// `<which>` in the singular. Missing metrics contribute -1 so they never win
// against a present 0.0 reading.
func monitorHighestMetric(p monitorPayload) monitorMetricRanking {
	cpu := monitorMetricRanking{label: "CPU", value: pointerOrSentinel(p.CPUPercent)}
	mem := monitorMetricRanking{label: "Memory", value: pointerOrSentinel(p.MemoryPercent)}
	disk := monitorMetricRanking{label: "Disk", value: pointerOrSentinel(p.DiskPercent)}
	hi := cpu
	if mem.value > hi.value {
		hi = mem
	}
	if disk.value > hi.value {
		hi = disk
	}
	return hi
}

// pointerOrSentinel returns the dereferenced value or -1 when nil so missing
// metrics lose every comparison against a real reading (including 0.0).
func pointerOrSentinel(v *float64) float64 {
	if v == nil {
		return -1
	}
	return *v
}

// monitorMaxUtilization is the cpu / mem / disk max used for the §B.10.3 color
// classification. Missing metrics contribute -1 so a partial envelope falls to
// monitorBranchOK on the max-based test (but partial is detected earlier in
// monitorBranchFor anyway, so this path is only exercised when the partial
// gate is upstream-bypassed by future code).
func monitorMaxUtilization(p monitorPayload) float64 {
	return monitorHighestMetric(p).value
}

// monitorActions renders the §B.10.4 button row. Refresh is always present;
// View jobs appears when any metric crosses monitorHighThreshold OR when the
// envelope is partial (disk_percent=null); View apps appears only when cpu OR
// memory crosses monitorHighThreshold (disk high alone does not trigger the
// apps-list pivot — that's the §B.10.4 explicit guard).
func monitorActions(p monitorPayload) []*model.PostAction {
	actions := []*model.PostAction{
		makeAction("monitor_refresh", "Refresh", postActionStyleDefault, []string{"monitor"}),
	}
	if monitorShouldShowJobs(p) {
		actions = append(actions, makeAction("monitor_view_jobs", "View jobs", postActionStyleDefault, []string{"jobs", "--scope=all"}))
	}
	if monitorShouldShowApps(p) {
		actions = append(actions, makeAction("monitor_view_apps", "View apps", postActionStyleDefault, []string{"apps", "list"}))
	}
	return actions
}

// monitorShouldShowJobs encodes the §B.10.4 View jobs guard: partial envelope
// OR any single metric >= monitorHighThreshold.
func monitorShouldShowJobs(p monitorPayload) bool {
	if p.DiskPercent == nil {
		return true
	}
	return monitorMaxUtilization(p) >= monitorHighThreshold
}

// monitorShouldShowApps encodes the §B.10.4 View apps guard: cpu OR memory
// >= monitorHighThreshold. Disk crossing the threshold or a partial envelope
// alone does not surface the button.
func monitorShouldShowApps(p monitorPayload) bool {
	return pointerOrSentinel(p.CPUPercent) >= monitorHighThreshold ||
		pointerOrSentinel(p.MemoryPercent) >= monitorHighThreshold
}

// renderMonitorBusinessError is the §B.10.5 colorError variant: the canonical
// envelope error form with monitor's Refresh button preserved so the user can
// retry from the same card once the host agent recovers. Routed via
// renderBusinessError's verb switch so the per-verb action set lives next to
// the other verb-aware error cards.
func renderMonitorBusinessError(code, message string) *model.SlackAttachment {
	return &model.SlackAttachment{
		Title: "fulcrum monitor — error",
		Text:  fmt.Sprintf("`%s` %s", code, message),
		Color: colorError,
		Actions: []*model.PostAction{
			makeAction("monitor_refresh", "Refresh", postActionStyleDefault, []string{"monitor"}),
		},
		Footer: "fulcrum/monitor · schema_version=1",
	}
}
