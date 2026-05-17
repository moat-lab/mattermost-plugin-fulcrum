package main

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
)

func TestActionArgv_OK(t *testing.T) {
	got, err := actionArgv(map[string]any{
		"argv": []any{"tasks", "set-status", "abc123", "done"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"fulcrum", "tasks", "set-status", "abc123", "done", "--json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv mismatch: got %v want %v", got, want)
	}
}

func TestArgvFromContext(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want []string
	}{
		{"nil", nil, nil},
		{"missing key", map[string]any{"other": 1}, nil},
		{"non-array", map[string]any{"argv": "tasks"}, nil},
		{"non-string element", map[string]any{"argv": []any{"tasks", 7}}, nil},
		{"ok", map[string]any{"argv": []any{"tasks", "get", "t_1"}}, []string{"tasks", "get", "t_1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := argvFromContext(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestActionIDFromContext(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want string
	}{
		{"nil", nil, ""},
		{"missing key", map[string]any{"argv": []any{"dashboard"}}, ""},
		{"non-string", map[string]any{"action_id": 7}, ""},
		{"ok", map[string]any{"action_id": "dashboard_refresh"}, "dashboard_refresh"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := actionIDFromContext(c.in); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestMattermostActionIDUsesRouteSafeID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"task_quick_create_open", "taskquickcreateopen"},
		{"dashboard-refresh", "dashboardrefresh"},
		{"appRefresh2", "appRefresh2"},
		{"___", "action"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := mattermostActionID(c.in); got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestIsDialogClick(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want bool
	}{
		{"nil", nil, false},
		{"missing", map[string]any{"argv": []any{"x"}}, false},
		{"false", map[string]any{"dialog": false}, false},
		{"string", map[string]any{"dialog": "true"}, false},
		{"true", map[string]any{"dialog": true}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isDialogClick(c.in); got != c.want {
				t.Errorf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestActionArgv_Errors(t *testing.T) {
	cases := []struct {
		name string
		ctx  map[string]any
	}{
		{"nil", nil},
		{"missing key", map[string]any{"other": 1}},
		{"non-array", map[string]any{"argv": "tasks"}},
		{"non-string element", map[string]any{"argv": []any{"tasks", 7}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := actionArgv(tc.ctx); err == nil {
				t.Fatalf("expected error")
			}
		})
	}
}

func TestWriteActionOKReturnsConcreteUpdatePost(t *testing.T) {
	rr := httptest.NewRecorder()
	writeActionOK(rr, &model.Post{Id: "post_1", UserId: "bot_1"})

	var got model.PostActionIntegrationResponse
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Update == nil {
		t.Fatal("update post is nil")
	}
	if got.Update.Id != "post_1" || got.Update.UserId != "bot_1" {
		t.Fatalf("update post = %#v", got.Update)
	}
}

func TestApplyActionFeedbackMarksRefreshOnly(t *testing.T) {
	now := time.Date(2026, 5, 17, 1, 2, 3, 0, time.UTC)
	att := &model.SlackAttachment{Footer: "fulcrum/dashboard · schema_version=1"}

	applyActionFeedback(att, now, "dashboard_refresh")
	want := "fulcrum/dashboard · schema_version=1 · action refreshed at 2026-05-17T01:02:03Z"
	if att.Footer != want {
		t.Fatalf("footer = %q want %q", att.Footer, want)
	}

	applyActionFeedback(att, now.Add(time.Minute), "dashboard_refresh")
	if strings.Count(att.Footer, "action refreshed at") != 1 {
		t.Fatalf("refresh marker duplicated in %q", att.Footer)
	}
	want = "fulcrum/dashboard · schema_version=1 · action refreshed at 2026-05-17T01:03:03Z"
	if att.Footer != want {
		t.Fatalf("footer after second refresh = %q want %q", att.Footer, want)
	}

	nonRefresh := &model.SlackAttachment{Footer: "fulcrum/help · schema_version=1"}
	applyActionFeedback(nonRefresh, now, "help_open_dashboard")
	if nonRefresh.Footer != "fulcrum/help · schema_version=1" {
		t.Fatalf("non-refresh footer changed to %q", nonRefresh.Footer)
	}
}

func TestApplyEnvelopeToPostWithActionReturnsUpdatedPostWithRefreshFeedback(t *testing.T) {
	api := &plugintest.API{}
	client := pluginapi.NewClient(api, &plugintest.Driver{})
	post := &model.Post{
		Id:        "post_1",
		ChannelId: "channel_1",
		UserId:    "bot_1",
	}

	api.On("GetPost", "post_1").Return(post, nil).Once()
	api.On("UpdatePost", mock.MatchedBy(func(p *model.Post) bool {
		attachments := p.Attachments()
		return p.Id == "post_1" &&
			p.UserId == "bot_1" &&
			len(attachments) == 1 &&
			strings.Contains(attachments[0].Footer, "fulcrum/dashboard") &&
			strings.Contains(attachments[0].Footer, "action refreshed at")
	})).Return(func(p *model.Post) *model.Post {
		return p.Clone()
	}, nil).Once()

	updated, err := applyEnvelopeToPostWithAction(
		client,
		"bot_1",
		"post_1",
		dashboardRaw(t, dashboardPayload{}),
		"",
		[]string{"dashboard"},
		"dashboard_refresh",
	)
	if err != nil {
		t.Fatalf("apply envelope: %v", err)
	}
	if updated == nil {
		t.Fatal("updated post is nil")
	}
	if attachments := updated.Attachments(); len(attachments) != 1 || !strings.Contains(attachments[0].Footer, "action refreshed at") {
		t.Fatalf("updated attachments = %#v", attachments)
	}
	api.AssertExpectations(t)
}
