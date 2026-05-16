package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
)

// projectsRaw assembles a `fulcrum projects --json` envelope from a payload
// so schema drift surfaces as a compile failure here rather than in
// renderProjects. Mirrors the jobsRaw / monitorRaw helpers so each per-verb
// test file controls its own fixture shape without leaking JSON-encoding
// details into individual cases.
func projectsRaw(t *testing.T, p projectsPayload) []byte {
	t.Helper()
	body := struct {
		projectsPayload
		SchemaVersion int    `json:"schema_version"`
		Verb          string `json:"verb"`
	}{
		projectsPayload: p,
		SchemaVersion:   1,
		Verb:            "projects",
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	out, err := json.Marshal(map[string]any{"success": true, "data": json.RawMessage(data)})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return out
}

// projectsErrRaw assembles an envelope-error response for the projects
// verb. Used by the §B.12.5 backend_unavailable colorError path.
func projectsErrRaw(t *testing.T, code, message string) []byte {
	t.Helper()
	data := map[string]any{
		"schema_version": 1,
		"verb":           "projects",
		"error":          map[string]string{"code": code, "message": message},
	}
	dataJSON, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	out, err := json.Marshal(map[string]any{"success": true, "data": json.RawMessage(dataJSON)})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return out
}

func TestProjects_EmptyBranch(t *testing.T) {
	att, err := renderEnvelope(projectsRaw(t, projectsPayload{
		Total:    0,
		Projects: nil,
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorStatusTODO {
		t.Errorf("empty color: got %q want %q", att.Color, colorStatusTODO)
	}
	if att.Title != "Projects (0)" {
		t.Errorf("empty title: %q", att.Title)
	}
	if att.Pretext != "" {
		t.Errorf("empty pretext should be blank: %q", att.Pretext)
	}
	if !strings.Contains(att.Text, "No projects. Create one via fulcrum CLI") {
		t.Errorf("empty text missing no-projects hint: %q", att.Text)
	}
	if att.Footer != "fulcrum/projects · total=0" {
		t.Errorf("empty footer: %q", att.Footer)
	}
	if names := actionNames(att.Actions); !equalStringSlice(names, []string{"Refresh"}) {
		t.Errorf("empty-card actions: %v", names)
	}
}

func TestProjects_AllActiveBranch(t *testing.T) {
	att, err := renderEnvelope(projectsRaw(t, projectsPayload{
		Total: 2,
		Projects: []projectSummary{
			{ID: "p_1", Name: "fulcrum-core", Status: "active", DefaultAgent: strPtr("claude"),
				TaskCounts: projectTaskCounts{Active: 4, Total: 12},
				Description: strPtr("Core orchestrator project")},
			{ID: "p_2", Name: "fulcrum-cli", Status: "active", DefaultAgent: strPtr("opencode"),
				TaskCounts: projectTaskCounts{Active: 1, Total: 3},
				Description: strPtr("CLI surface")},
		},
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorAccent {
		t.Errorf("all-active color: got %q want %q", att.Color, colorAccent)
	}
	if att.Title != "Projects (2)" {
		t.Errorf("title: %q", att.Title)
	}
	if att.Pretext != "active ×2" {
		t.Errorf("pretext should list only count>0 buckets: %q", att.Pretext)
	}
	if !strings.Contains(att.Text, "| Status") || !strings.Contains(att.Text, "| Default agent") {
		t.Errorf("text missing table header: %q", att.Text)
	}
	if !strings.Contains(att.Text, "`fulcrum-core`") || !strings.Contains(att.Text, "`fulcrum-cli`") {
		t.Errorf("text missing project name cells: %q", att.Text)
	}
	if !strings.Contains(att.Text, ":large_blue_circle: active") {
		t.Errorf("text missing active status chip: %q", att.Text)
	}
	if !strings.Contains(att.Text, "4 / 12") {
		t.Errorf("text missing task count cell: %q", att.Text)
	}
}

func TestProjects_MixedBranchWithArchived(t *testing.T) {
	att, err := renderEnvelope(projectsRaw(t, projectsPayload{
		Total: 2,
		Projects: []projectSummary{
			{ID: "p_1", Name: "fulcrum-core", Status: "active", DefaultAgent: strPtr("claude"),
				TaskCounts: projectTaskCounts{Active: 4, Total: 12}},
			{ID: "p_2", Name: "legacy-ui", Status: "archived", DefaultAgent: nil,
				TaskCounts:  projectTaskCounts{Active: 0, Total: 6},
				Description: strPtr("Sunset 2025")},
		},
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorWarn {
		t.Errorf("mixed color: got %q want %q", att.Color, colorWarn)
	}
	if att.Pretext != "active ×1 · archived ×1" {
		t.Errorf("pretext should list both count>0 buckets in canonical order: %q", att.Pretext)
	}
	if !strings.Contains(att.Text, ":black_circle: archived") {
		t.Errorf("text missing archived status chip: %q", att.Text)
	}
	if !strings.Contains(att.Text, "Sunset 2025") {
		t.Errorf("text missing archived description: %q", att.Text)
	}
}

func TestProjects_NullableCellsRenderEmDash(t *testing.T) {
	att, err := renderEnvelope(projectsRaw(t, projectsPayload{
		Total: 1,
		Projects: []projectSummary{
			{ID: "p_1", Name: "ephemeral", Status: "active", DefaultAgent: nil, Description: nil,
				TaskCounts: projectTaskCounts{Active: 0, Total: 0}},
		},
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// nil DefaultAgent and nil Description must collapse to "—" so the row
	// width stays stable (mirrors jobs-panel's nullable-cell contract).
	if !strings.Contains(att.Text, "| `ephemeral` | — | 0 / 0 | — |") {
		t.Errorf("nullable cells should collapse to em-dash; got text:\n%s", att.Text)
	}
}

func TestProjects_DescriptionTruncatedAtWidth(t *testing.T) {
	long := strings.Repeat("a", projectsDescriptionWidth+25)
	att, err := renderEnvelope(projectsRaw(t, projectsPayload{
		Total: 1,
		Projects: []projectSummary{
			{ID: "p_1", Name: "long-desc", Status: "active",
				TaskCounts:  projectTaskCounts{Active: 0, Total: 0},
				Description: strPtr(long)},
		},
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := strings.Repeat("a", projectsDescriptionWidth) + "…"
	if !strings.Contains(att.Text, want) {
		t.Errorf("description should be truncated to %d runes + …; got text:\n%s", projectsDescriptionWidth, att.Text)
	}
	if strings.Contains(att.Text, strings.Repeat("a", projectsDescriptionWidth+1)) {
		t.Errorf("description must not exceed cap; got text:\n%s", att.Text)
	}
}

func TestProjects_UnknownStatusFallsThroughToMixed(t *testing.T) {
	att, err := renderEnvelope(projectsRaw(t, projectsPayload{
		Total: 1,
		Projects: []projectSummary{
			{ID: "p_1", Name: "future-state", Status: "paused",
				TaskCounts: projectTaskCounts{Active: 0, Total: 1}},
		},
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorWarn {
		t.Errorf("unknown status must promote color to mixed warn so a future status addition surfaces; got %q", att.Color)
	}
	if !strings.Contains(att.Text, ":grey_question: paused") {
		t.Errorf("unknown status should render via grey-question fallback chip: %q", att.Text)
	}
}

func TestProjects_RowCapTruncationNote(t *testing.T) {
	projects := make([]projectSummary, projectsRowCap+5)
	for i := range projects {
		projects[i] = projectSummary{
			ID:         "p_x",
			Name:       "row-project",
			Status:     "active",
			TaskCounts: projectTaskCounts{Active: 0, Total: 0},
		}
	}
	att, err := renderEnvelope(projectsRaw(t, projectsPayload{
		Total:    len(projects),
		Projects: projects,
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	rendered := strings.Count(att.Text, "`row-project`")
	if rendered != projectsRowCap {
		t.Errorf("rendered table rows: got %d want %d", rendered, projectsRowCap)
	}
	if !strings.Contains(att.Footer, "showing first 50") {
		t.Errorf("footer must surface row-cap truncation: %q", att.Footer)
	}
}

func TestProjects_PipeInNameAndDescriptionEscaped(t *testing.T) {
	att, err := renderEnvelope(projectsRaw(t, projectsPayload{
		Total: 1,
		Projects: []projectSummary{
			{ID: "p_1", Name: "weird|name", Status: "active",
				TaskCounts:  projectTaskCounts{Active: 0, Total: 0},
				Description: strPtr("a | b")},
		},
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(att.Text, "`weird|name`") {
		t.Errorf("pipe in name should be HTML-escaped: %q", att.Text)
	}
	if !strings.Contains(att.Text, "weird&#124;name") {
		t.Errorf("expected escaped pipe in name cell: %q", att.Text)
	}
	if strings.Contains(att.Text, "| a | b |") {
		t.Errorf("pipe in description should be HTML-escaped to keep table layout: %q", att.Text)
	}
	if !strings.Contains(att.Text, "a &#124; b") {
		t.Errorf("expected escaped pipe in description cell: %q", att.Text)
	}
}

func TestProjects_RefreshArgvLock(t *testing.T) {
	att, err := renderEnvelope(projectsRaw(t, projectsPayload{
		Total: 1,
		Projects: []projectSummary{
			{ID: "p_1", Name: "pinned", Status: "active",
				TaskCounts: projectTaskCounts{Active: 0, Total: 1}},
		},
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	assertActionArgv(t, att.Actions[0], "projects_refresh", postActionStyleDefault, []string{"projects"}, false)
}

func TestProjects_BusinessErrorBackendUnavailableRendersColorErrorCard(t *testing.T) {
	att, err := renderEnvelope(projectsErrRaw(t, "backend_unavailable", "projects backend not reachable"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorError {
		t.Errorf("error color: got %q want %q", att.Color, colorError)
	}
	if att.Title != "fulcrum projects — error" {
		t.Errorf("error title: %q", att.Title)
	}
	if !strings.Contains(att.Text, "`backend_unavailable`") {
		t.Errorf("error text must embed code: %q", att.Text)
	}
	if !strings.Contains(att.Text, "projects backend not reachable") {
		t.Errorf("error text must embed message: %q", att.Text)
	}
	if att.Footer != "fulcrum/projects · schema_version=1" {
		t.Errorf("error footer: %q", att.Footer)
	}
	assertActionArgv(t, att.Actions[0], "projects_refresh", postActionStyleDefault, []string{"projects"}, false)
}

func TestProjects_BusinessErrorUnknownCodePreservesRefresh(t *testing.T) {
	// A future business code that the spike didn't enumerate must still
	// reach the user with Refresh visible — there are no ephemeral codes
	// for projects, so every code lands on the colorError card.
	att, err := renderEnvelope(projectsErrRaw(t, "FETCH_FAILED", "upstream timed out"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if att.Color != colorError {
		t.Errorf("unknown-code color: got %q want %q", att.Color, colorError)
	}
	if !strings.Contains(att.Text, "FETCH_FAILED") {
		t.Errorf("unknown-code text must keep code visible: %q", att.Text)
	}
	assertActionArgv(t, att.Actions[0], "projects_refresh", postActionStyleDefault, []string{"projects"}, false)
}

// TestProjects_PostActionIntegrationURL pins the integration URL so the bot
// post's buttons route back into the plugin's /action endpoint exactly
// once the user clicks them; a wrong URL silently disables every button.
func TestProjects_PostActionIntegrationURL(t *testing.T) {
	att, err := renderEnvelope(projectsRaw(t, projectsPayload{
		Total: 1,
		Projects: []projectSummary{
			{ID: "p_1", Name: "pinned", Status: "active",
				TaskCounts: projectTaskCounts{Active: 0, Total: 1}},
		},
	}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, a := range att.Actions {
		if a.Integration == nil || a.Integration.URL != "/plugins/"+manifestID+"/action" {
			t.Errorf("action %q integration URL: %#v", a.Name, a.Integration)
		}
		if a.Type != model.PostActionTypeButton {
			t.Errorf("action %q type: %q", a.Name, a.Type)
		}
	}
}
