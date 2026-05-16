package main

import (
	"testing"
)

func TestResolveRexecdAddr_Set(t *testing.T) {
	t.Setenv(rexecdAddrEnv, "dns:///rexecd.internal:50051")
	got, err := resolveRexecdAddr()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "dns:///rexecd.internal:50051" {
		t.Fatalf("addr mismatch: %q", got)
	}
}

func TestResolveRexecdAddr_Blank(t *testing.T) {
	for _, v := range []string{"", "   "} {
		t.Setenv(rexecdAddrEnv, v)
		if _, err := resolveRexecdAddr(); err == nil {
			t.Fatalf("expected error for %q", v)
		}
	}
}

func TestConfiguration_TrimmedDefaultHostID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"vctcn-app1", "vctcn-app1"},
		{"  vctcn-app1  ", "vctcn-app1"},
		{"\tvctcn-app1\n", "vctcn-app1"},
		{"   ", ""},
	}
	for _, tc := range cases {
		cfg := configuration{DefaultHostID: tc.in}
		got := cfg.trimmedDefaultHostID()
		if got != tc.want {
			t.Errorf("trimmedDefaultHostID(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}
