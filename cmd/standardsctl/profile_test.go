package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// TestParseProfileSetOptions_3D: a profile and flags parse into the options (positive); --facets=
// replaces the declared facets with none while an omitted flag keeps them, and no profile keeps
// the declared one (boundary); a second profile is refused (negative).
func TestParseProfileSetOptions_3D(t *testing.T) {
	opts, err := parseProfileSetOptions([]string{"os-image", "--facets=security:high,agent:sandboxed",
		"--lock-source-root", "/src", "--dry-run", "--path=/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Profile != "os-image" || !opts.SetFacets || !slices.Equal(opts.Facets, []string{"security:high", "agent:sandboxed"}) ||
		opts.LockSourceRoot != "/src" || !opts.DryRun || opts.Path != "/repo" {
		t.Fatalf("parsed %+v", opts)
	}
	empty, err := parseProfileSetOptions([]string{"--facets=", "--lock-source-root=/src"})
	if err != nil || !empty.SetFacets || len(empty.Facets) != 0 || empty.Profile != "" {
		t.Fatalf("--facets= must declare no facets and keep the profile: %+v, %v", empty, err)
	}
	kept, err := parseProfileSetOptions([]string{"framework", "--lock-source-root=/src"})
	if err != nil || kept.SetFacets || kept.Facets != nil || kept.Path != "." {
		t.Fatalf("an omitted --facets must keep the declared facets: %+v, %v", kept, err)
	}
	if _, err := parseProfileSetOptions([]string{"framework", "os-image"}); err == nil || !strings.Contains(err.Error(), "at most one profile") {
		t.Fatalf("two profiles must be refused: %v", err)
	}
}

// TestRunProfile_Dispatch_3D: help prints the usage and succeeds (positive); no action is refused
// with the usage (boundary); an unknown action is refused (negative).
func TestRunProfile_Dispatch_3D(t *testing.T) {
	out, err := captureStdout(t, func() error { return runProfile([]string{"--help"}) })
	if err != nil || !strings.Contains(out, "praetorctl profile set") {
		t.Fatalf("help: %v\n%s", err, out)
	}
	if err := runProfile(nil); err == nil || !strings.Contains(err.Error(), "usage: praetorctl profile set") {
		t.Fatalf("no action must print the usage: %v", err)
	}
	if err := runProfile([]string{"show"}); err == nil || !strings.Contains(err.Error(), `unknown profile action "show"`) {
		t.Fatalf("an unknown action must be refused: %v", err)
	}
}

// End to end through the praetorctl process and the shipped catalog (#123): a repository adopted
// under one profile moves to another. The dry run previews the change and writes nothing; the
// real run leaves a manifest and lock the audit's policy and lock gates accept; a profile the
// source bundle lacks exits non-zero naming the bundle's catalog version.
func TestProfileSetEndToEnd(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	initGitFixture(t, root)
	if code, out := praetorctl(t, "adopt", "--path", root, "--profile", "planning-artifacts", "--facets", "security:high",
		"--lock-source-root", source); code != 0 {
		t.Fatalf("adopt: exit %d\n%s", code, out)
	}
	const archetype = ".config/archetypes/os-image.yaml"
	before := readFixtureFile(t, root, ".standards.yaml")
	code, out := praetorctl(t, "profile", "set", "os-image", "--path", root, "--lock-source-root", source, "--dry-run")
	if code != 0 || !strings.Contains(out, "--- Preview: .standards.yaml (update)") || !strings.Contains(out, "+  - os-image") {
		t.Fatalf("dry run: exit %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(archetype))); readFixtureFile(t, root, ".standards.yaml") != before ||
		!errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a dry run wrote the declaration or the archetype: %v", err)
	}
	if code, out := praetorctl(t, "profile", "set", "os-image", "--path", root, "--lock-source-root", source); code != 0 {
		t.Fatalf("profile set: exit %d\n%s", code, out)
	}
	effective, err := auditManifestAndLockfileQuiet(t, root)
	if err != nil || !slices.Equal(effective.Manifest.Profiles, []string{"os-image"}) {
		t.Fatalf("the audit must accept the changed profile: %v", err)
	}
	readFixtureFile(t, root, archetype)
	code, out = praetorctl(t, "profile", "set", "no-such-profile", "--path", root, "--lock-source-root", source)
	if code == 0 || !strings.Contains(out, "catalog v0.0.0+catalog.") {
		t.Fatalf("an absent profile must fail naming the catalog version: exit %d\n%s", code, out)
	}
}

// TestAdoptHelpStatesTheDefaultFacets (#596): adopt --help names the facets adoption writes when
// it creates .standards.yaml, the set config.DefaultFacets holds when --facets is omitted, says
// an existing manifest keeps the facets it declares (manifestForLock), and where to change them
// (positive); --help still exits 0 (boundary); the flag keeps an empty default, so the text is
// the one that states it in prose (negative: no flag default).
func TestAdoptHelpStatesTheDefaultFacets(t *testing.T) {
	code, out := praetorctl(t, "adopt", "--help")
	want := "default when omitted: " + strings.Join(config.DefaultFacets(), ",")
	for _, phrase := range []string{want, "when adoption creates .standards.yaml", "an existing .standards.yaml keeps the facets it declares",
		"praetorctl profile set --facets"} {
		if code != 0 || !strings.Contains(out, phrase) {
			t.Fatalf("adopt --help: exit %d, want %q\n%s", code, phrase, out)
		}
	}
	_, usage, _ := strings.Cut(out, "-facets string")
	usage, _, _ = strings.Cut(usage, "\n  -")
	if strings.Contains(usage, `(default "`) {
		t.Fatalf("adopt --facets must keep an empty flag default, so an omitted flag stays distinguishable:\n%s", usage)
	}
}
