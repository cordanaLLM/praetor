package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicDogfoodMCPRequiresExplicitRemoteOptIn(t *testing.T) {
	srv, _ := newFixtureServer(t)
	result := callTool(t, srv, "standards_dogfood", map[string]any{"public_loop": true})
	expectError(t, "public remote guard", result, "remote benchmark clones are disabled")
	for _, args := range []map[string]any{{"public_loop": "yes"}, {"public_repos": "https://github.com/spf13/cobra"}, {"dry_run": false}} {
		if result := callTool(t, srv, "standards_dogfood", args); !result.IsError {
			t.Fatalf("inert or mistyped arguments accepted: %+v", args)
		}
	}
}

func TestPublicDogfoodMCPBoundsAndConfinement(t *testing.T) {
	srv, root := newFixtureServer(t)
	srv.opts.AllowRemoteBenchmarks = true
	args := map[string]any{"public_repos": "https://github.com/spf13/cobra", "artifact_dir": "evidence", "max_attempts": float64(3)}
	opts, err := srv.parsePublicDogfood(args)
	if err != nil || opts.Apply || opts.MaxAttempts != 3 || opts.SourceRoot != root {
		t.Fatalf("defaults/boundary: %+v %v", opts, err)
	}
	args["dry_run"] = false
	opts, err = srv.parsePublicDogfood(args)
	if err != nil || !opts.Apply {
		t.Fatalf("apply control inert: %+v %v", opts, err)
	}
	for _, value := range []any{1, 4, 2.5, "2", nil} {
		args["max_attempts"] = value
		if _, err := srv.parsePublicDogfood(args); err == nil {
			t.Fatalf("invalid attempts accepted: %v", value)
		}
	}
	delete(args, "max_attempts")
	args["artifact_dir"] = t.TempDir()
	if _, err := srv.parsePublicDogfood(args); err == nil {
		t.Fatal("outside evidence accepted")
	}
	args["artifact_dir"] = "evidence"
	args["source_root"] = t.TempDir()
	if _, err := srv.parsePublicDogfood(args); err == nil {
		t.Fatal("outside source accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "evidence")); !os.IsNotExist(err) {
		t.Fatalf("parsing mutated root: %v", err)
	}
}

func TestAdoptMCPMissingSourceRetainsPartialReport(t *testing.T) {
	srv, root := newFixtureServer(t)
	if err := os.Remove(filepath.Join(root, ".standards.lock")); err != nil {
		t.Fatal(err)
	}
	result := callTool(t, srv, "standards_adopt", nil)
	expectError(t, "missing source", result, "lock source root")
	if !strings.Contains(result.Content[0].Text, "Created Files:") {
		t.Fatal("partial adoption report was discarded")
	}
	if _, err := os.Stat(filepath.Join(root, ".standards.lock")); !os.IsNotExist(err) {
		t.Fatal("missing source produced placeholder lock")
	}
}
