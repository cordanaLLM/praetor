package main

import (
	"context"
	"strings"
	"testing"
)

func TestOperationalRejectsUnsupportedAndIncompleteCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"sync"}, {"publish", "plan"}, {"sync", "publish"}, {"sync", "plan"}, {"sync", "plan", "unexpected"}, {"sync", "prepare", "--unknown"},
		{"sync", "init"}, {"sync", "init", "--owner", "example"}, {"sync", "init", "--owner-path", t.TempDir()}, {"sync", "init", "unexpected"}} {
		if err := runOperational(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if err := runOperationalSync(context.Background(), "plan", []string{"--owner-path=", "--source-path="}); err == nil {
		t.Fatal("empty paths accepted")
	}
}

func TestOperationalUsageNamesEveryStage(t *testing.T) {
	err := runOperational(nil)
	if err == nil {
		t.Fatal("usage expected")
	}
	for _, stage := range []string{"sync init", "plan|prepare", "--owner OWNER"} {
		if !strings.Contains(err.Error(), stage) {
			t.Errorf("usage omits %q: %v", stage, err)
		}
	}
}

// A directory that is no checkout reaches the engine through the init flags and is refused there,
// which proves the flag wiring without building a repository in this package.
func TestOperationalInitReachesTheEngine(t *testing.T) {
	err := runOperationalSync(context.Background(), "init", []string{"--owner-path", t.TempDir(), "--owner", "example"})
	if err == nil || strings.Contains(err.Error(), "supported stages") || strings.Contains(err.Error(), "valid operational repository owner") {
		t.Fatalf("init flags did not reach the checkout inspection: %v", err)
	}
	err = runOperationalSync(context.Background(), "init", []string{"--owner-path", t.TempDir(), "--owner", "example", "--source-sha", strings.Repeat("a", 40)})
	if err == nil || !strings.Contains(err.Error(), "init accepts only") {
		t.Fatalf("init accepted a later-stage flag: %v", err)
	}
}
