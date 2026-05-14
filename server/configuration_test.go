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
