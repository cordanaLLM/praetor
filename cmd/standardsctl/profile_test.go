package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
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
// source bundle lacks exits non-zero naming the bundle's catalog version, before any gate runs.
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
	if code == 0 || !strings.Contains(out, "catalog v0.0.0+catalog.") || strings.Contains(out, declarationGatesHeading) {
		t.Fatalf("an absent profile must fail naming the catalog version, before any gate runs: exit %d\n%s", code, out)
	}
}

// declarationGatesHeading opens the section profile set prints for the declaration gates.
const declarationGatesHeading = "Audit gates for files derived from the declaration"

// TestProfileSetReportsDerivedDrift_3D (#123 review): profile set writes no file adoption derives
// from the declaration, so after a profile change it names each audit gate that now fails and the
// refresh. The dry run predicts the failing DevContainer gate against the planned declaration
// (boundary); the real run reports it against the written one and exits 0, and the named
// refresh, adopt --force, clears it, after which a re-pin reports every gate passing (positive).
// The gate verdicts are the audit's own: the same run's audit fails on the DevContainer
// (negative).
func TestProfileSetReportsDerivedDrift_3D(t *testing.T) {
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
	const drift = "[FAIL] DevContainer out of sync with declared standards"
	refresh := "praetorctl adopt --force --dry-run --lock-source-root=" + source
	code, out := praetorctl(t, "profile", "set", "os-image", "--path", root, "--lock-source-root", source, "--dry-run")
	if code != 0 || !strings.Contains(out, "checked against the planned declaration") || !strings.Contains(out, drift) ||
		!strings.Contains(out, "would fail once profile set runs without --dry-run") {
		t.Fatalf("the dry run must predict the DevContainer drift: exit %d\n%s", code, out)
	}
	code, out = praetorctl(t, "profile", "set", "os-image", "--path", root, "--lock-source-root", source)
	if code != 0 || !strings.Contains(out, "checked against the declaration now written") || !strings.Contains(out, drift) ||
		!strings.Contains(out, "1 gate(s) fail") || !strings.Contains(out, refresh) {
		t.Fatalf("the real run must report the drift and the refresh: exit %d\n%s", code, out)
	}
	if err := auditDevContainerQuiet(t, root); err == nil || !strings.Contains(err.Error(), drift) {
		t.Fatalf("the audit must fail on the reported DevContainer drift: %v", err)
	}
	if code, out := praetorctl(t, "adopt", "--force", "--path", root, "--lock-source-root", source); code != 0 {
		t.Fatalf("adopt --force: exit %d\n%s", code, out)
	}
	code, out = praetorctl(t, "profile", "set", "--path", root, "--lock-source-root", source)
	if code != 0 || !strings.Contains(out, "[PASS] DevContainer configuration verified") || strings.Contains(out, "gate(s) fail") {
		t.Fatalf("after the refresh every declaration gate must pass: exit %d\n%s", code, out)
	}
}

// auditDevContainerQuiet runs the audit's DevContainer gate on root's committed declaration.
func auditDevContainerQuiet(t *testing.T, root string) error {
	t.Helper()
	effective, err := auditManifestAndLockfileQuiet(t, root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = captureStdout(t, func() error {
		return auditDevContainer(t.Context(), effective.Manifest, &auditOptions{rootDir: root, effective: effective})
	})
	return err
}

// TestCheckDeclarationGates_3D: an audit fixture passes every declaration gate (positive); a
// declared docs:seo-portal without the documentation assets fails that gate alone (negative); an
// unreadable baseline counts as one failure while the gates that do not read it still run, and a
// report without an effective policy is refused (boundary).
func TestCheckDeclarationGates_3D(t *testing.T) {
	f := newAuditFixture(t)
	effective, err := auditManifestAndLockfileQuiet(t, f.dir)
	if err != nil {
		t.Fatal(err)
	}
	gates := func() (string, int) {
		failed := 0
		out, err := captureStdout(t, func() error { failed = checkDeclarationGates(t.Context(), f.dir, effective); return nil })
		if err != nil {
			t.Fatal(err)
		}
		return out, failed
	}
	if out, failed := gates(); failed != 0 || !strings.Contains(out, "[PASS] Branch protection") {
		t.Fatalf("the fixture must pass every declaration gate: %d failed\n%s", failed, out)
	}
	declared := effective.Manifest.Facets
	effective.Manifest.Facets = append(slices.Clone(declared), "docs:seo-portal")
	if out, failed := gates(); failed != 1 || !strings.Contains(out, "[FAIL] Documentation gate asset") {
		t.Fatalf("an enabled documentation facet without its assets must fail that gate alone: %d failed\n%s", failed, out)
	}
	effective.Manifest.Facets = declared
	writeFixtureFile(t, f.dir, ".standards-baseline.json", "{")
	if out, failed := gates(); failed != 1 || !strings.Contains(out, "[FAIL] Baseline audit failed") ||
		!strings.Contains(out, "[PASS] Branch protection") {
		t.Fatalf("an unreadable baseline must count once and leave the other gates running: %d failed\n%s", failed, out)
	}
	if err := reportDeclarationGates(t.Context(), &adopt.AdoptReport{}, adopt.ProfileSetOptions{Path: f.dir}); err == nil ||
		!strings.Contains(err.Error(), "no effective policy") {
		t.Fatalf("a report without an effective policy must be refused: %v", err)
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
