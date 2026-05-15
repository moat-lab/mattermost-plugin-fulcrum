package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestDialogState_RoundTrip(t *testing.T) {
	want := dialogState{
		Argv:      []string{"tasks", "set-status", "t_42", "canceled"},
		PostID:    "post_id_abc",
		ChannelID: "chan_id_xyz",
	}
	enc, err := encodeDialogState(want)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if enc == "" {
		t.Fatalf("encoded state is empty")
	}
	got, err := decodeDialogState(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round-trip mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestDialogState_DecodeErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"non base64", "!!!"},
		// base64 of "not json" — valid base64, invalid JSON.
		{"bad json", "bm90IGpzb24="},
		// base64 of `{}` — valid JSON but missing required fields.
		{"empty json", "e30="},
		// base64 of `{"argv":["x"]}` — argv set but post_id missing.
		{"no post id", "eyJhcmd2IjpbInQiXX0="},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := decodeDialogState(c.in); err == nil {
				t.Fatalf("expected error for %q", c.in)
			}
		})
	}
}

func TestDialogPromptFor(t *testing.T) {
	got := dialogPromptFor([]string{"tasks", "set-status", "t_42", "canceled"})
	if !strings.Contains(got.Title, "tasks.set-status canceled t_42") {
		t.Errorf("cancel title: %q", got.Title)
	}
	if got.SubmitLabel != "Cancel task" {
		t.Errorf("cancel submit_label: %q", got.SubmitLabel)
	}
	if !strings.Contains(got.Introduction, "Cancel task `t_42`") {
		t.Errorf("cancel intro: %q", got.Introduction)
	}

	got = dialogPromptFor([]string{"tasks", "kill-agent", "t_42"})
	if !strings.Contains(got.Title, "tasks.kill-agent t_42") {
		t.Errorf("kill title: %q", got.Title)
	}
	if got.SubmitLabel != "Kill agent" {
		t.Errorf("kill submit_label: %q", got.SubmitLabel)
	}
	if !strings.Contains(got.Introduction, "unsaved terminal state") {
		t.Errorf("kill intro: %q", got.Introduction)
	}

	// Unknown argv → generic confirmation copy.
	got = dialogPromptFor([]string{"apps", "stop", "a_1"})
	if got.SubmitLabel != "Confirm" {
		t.Errorf("generic submit_label: %q", got.SubmitLabel)
	}
	if !strings.Contains(got.Introduction, "apps stop a_1") {
		t.Errorf("generic intro: %q", got.Introduction)
	}
}

func TestBuildOpenDialogRequest(t *testing.T) {
	req, err := buildOpenDialogRequest("trig123", []string{"tasks", "kill-agent", "t_42"}, "post_99", "chan_88")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if req.TriggerId != "trig123" {
		t.Errorf("trigger_id: %q", req.TriggerId)
	}
	if req.URL != "/plugins/"+manifestID+"/dialog" {
		t.Errorf("url: %q", req.URL)
	}
	if req.Dialog.CallbackId != dialogCallbackID {
		t.Errorf("callback_id: %q", req.Dialog.CallbackId)
	}
	if req.Dialog.NotifyOnCancel {
		t.Errorf("notify_on_cancel must be false")
	}
	if len(req.Dialog.Elements) != 0 {
		t.Errorf("elements must be empty, got %d", len(req.Dialog.Elements))
	}
	// State must decode back to the same argv/post/channel.
	st, err := decodeDialogState(req.Dialog.State)
	if err != nil {
		t.Fatalf("state decode: %v", err)
	}
	if !reflect.DeepEqual(st.Argv, []string{"tasks", "kill-agent", "t_42"}) {
		t.Errorf("state argv: %v", st.Argv)
	}
	if st.PostID != "post_99" || st.ChannelID != "chan_88" {
		t.Errorf("state post/channel: %s / %s", st.PostID, st.ChannelID)
	}
}

func TestPrependFulcrumJSON(t *testing.T) {
	got := prependFulcrumJSON([]string{"tasks", "set-status", "t_1", "doing"})
	want := []string{"fulcrum", "tasks", "set-status", "t_1", "doing", "--json"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("prependFulcrumJSON = %v want %v", got, want)
	}
}
