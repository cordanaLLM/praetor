package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/cavemansource"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/paperclip"
)

const declinedPaperclipManifest = legacyManifest + "adoption:\n  decline: [paperclip]\n"

func loadAdoptedManifest(t *testing.T, repoPath string) *config.Manifest {
	t.Helper()
	manifest, err := config.LoadManifest(filepath.Join(repoPath, manifestFile))
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

// TestAdoptDeclinedPaperclipWithoutHarnessBindsNothing: a declined paperclip step writes no
// harness, so neither plain adopt nor --force binds register.sources to one.
func TestAdoptDeclinedPaperclipWithoutHarnessBindsNothing(t *testing.T) {
	for _, force := range []bool{false, true} {
		repoPath := newTestRepo(t, "legacy")
		mustWrite(t, filepath.Join(repoPath, manifestFile), declinedPaperclipManifest)
		report, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, force))
		if err != nil {
			t.Fatalf("force=%v: %v", force, err)
		}
		if _, err := os.Stat(filepath.Join(repoPath, paperclipFile)); !os.IsNotExist(err) {
			t.Fatalf("force=%v: declined paperclip harness written: %v", force, err)
		}
		if manifest := loadAdoptedManifest(t, repoPath); manifest.Register != nil && manifest.Register.Sources != nil {
			t.Fatalf("force=%v: register.sources bound to a harness nothing writes: %+v", force, manifest.Register.Sources)
		}
		if got := reportDetail(report, manifestFile); !strings.Contains(got, "without register.sources") {
			t.Fatalf("force=%v: unbound contract not reported: %q", force, got)
		}
	}
}

// TestAdoptDeclinedPaperclipKeepsExistingHarness: a declined step keeps any harness on disk
// byte for byte, released output included, and binds the contract to those bytes.
func TestAdoptDeclinedPaperclipKeepsExistingHarness(t *testing.T) {
	harness := releasedHarness(t, "harness.json.golden")
	for _, force := range []bool{false, true} {
		repoPath := newTestRepo(t, "legacy")
		mustWrite(t, filepath.Join(repoPath, manifestFile), declinedPaperclipManifest)
		mustWrite(t, filepath.Join(repoPath, paperclipFile), harness)
		if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, force)); err != nil {
			t.Fatalf("force=%v: %v", force, err)
		}
		if got := mustRead(t, filepath.Join(repoPath, paperclipFile)); got != harness {
			t.Fatalf("force=%v: declined harness rewritten:\n%s", force, got)
		}
		stageAdoptPaths(t, repoPath, paperclipFile)
		if _, err := cavemansource.ExtractDeclared(t.Context(), repoPath,
			loadAdoptedManifest(t, repoPath).Register.Sources); err != nil {
			t.Fatalf("force=%v: contract not bound to the kept harness: %v", force, err)
		}
	}
}

// TestAdoptDeclinedPaperclipRejectsContractOnAbsentHarness: a declared contract that selects
// a harness nobody writes fails its own gate.
func TestAdoptDeclinedPaperclipRejectsContractOnAbsentHarness(t *testing.T) {
	repoPath := newTestRepo(t, "legacy")
	bound, err := managedRegisterSources(t.Context(), []byte(releasedHarness(t, "harness.json.golden")))
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(repoPath, manifestFile), declinedPaperclipManifest+manifestSourcesYAML(t, bound))
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, false)); err == nil ||
		!strings.Contains(err.Error(), "fails its configured gate") {
		t.Fatalf("contract on an absent declined harness accepted: %v", err)
	}
}

// TestVerifyDeclaredSourcesOverlaysOnlyBytesThisRunWrites pins the overlay guard: planned
// bytes stand in for an absent harness only when this run writes them, so a plan that holds
// bytes nothing writes never satisfies a contract bound to them.
func TestVerifyDeclaredSourcesOverlaysOnlyBytesThisRunWrites(t *testing.T) {
	repoPath := newTestRepo(t, "legacy")
	data := []byte(releasedHarness(t, "harness.json.golden"))
	bound, err := managedRegisterSources(t.Context(), data)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyDeclaredSources(t.Context(), repoPath, bound, harnessPlan{data: data}); err == nil {
		t.Fatal("planned bytes nothing writes satisfied the contract")
	}
	written := harnessPlan{data: data, write: &paperclip.Harness{}}
	if err := verifyDeclaredSources(t.Context(), repoPath, bound, written); err != nil {
		t.Fatalf("bytes this run writes did not stand in for the absent harness: %v", err)
	}
}

// TestAdoptUpgradesCRLFReleasedHarness: a Windows checkout with core.autocrlf=true holds the
// released harness with CRLF endings; plain adopt still recognises and refreshes it.
func TestAdoptUpgradesCRLFReleasedHarness(t *testing.T) {
	repoPath := newTestRepo(t, "legacy")
	crlf := func(text string) string { return strings.ReplaceAll(text, "\n", "\r\n") }
	mustWrite(t, filepath.Join(repoPath, manifestFile), legacyManifest)
	mustWrite(t, filepath.Join(repoPath, paperclipFile), crlf(releasedHarness(t, "harness.json.golden")))
	mustWrite(t, filepath.Join(repoPath, ".paperclip", "rules.md"), crlf(releasedHarness(t, "rules.md.golden")))
	if _, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, false)); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, filepath.Join(repoPath, paperclipFile)); strings.Contains(got, releasedIOInvariant) {
		t.Fatalf("CRLF released harness was not refreshed:\n%s", got)
	}
	if got := mustRead(t, filepath.Join(repoPath, ".paperclip", "rules.md")); strings.Contains(got, releasedIOInvariant) {
		t.Fatalf("CRLF released rules were not refreshed:\n%s", got)
	}
	requirePassingSourceGate(t, repoPath, paperclipFile)
}

// TestAdoptRefreshKeepsRemovedRulesAbsent: refreshing released harness.json does not recreate
// a rules.md the operator removed, and the report says so.
func TestAdoptRefreshKeepsRemovedRulesAbsent(t *testing.T) {
	repoPath := newTestRepo(t, "legacy")
	mustWrite(t, filepath.Join(repoPath, manifestFile), legacyManifest)
	mustWrite(t, filepath.Join(repoPath, paperclipFile), releasedHarness(t, "harness.json.golden"))
	report, err := Adopt(t.Context(), sourceAdoptOptions(t, repoPath, false))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repoPath, ".paperclip", "rules.md")); !os.IsNotExist(err) {
		t.Fatalf("removed rules.md recreated by the refresh: %v", err)
	}
	if got := reportDetail(report, paperclipFile); !strings.Contains(got, "rules.md stays absent") {
		t.Fatalf("refresh without rules.md not reported: %q", got)
	}
	requirePassingSourceGate(t, repoPath, paperclipFile)
}

// TestPlannedManifestUnderForceIsTheKeptManifest: --force keeps an existing manifest, so the
// dry-run and DevContainer planners read that manifest, not a freshly detected one.
func TestPlannedManifestUnderForceIsTheKeptManifest(t *testing.T) {
	repoPath := newTestRepo(t, "legacy")
	declared := strings.Replace(legacyManifest, "owner: acme", "owner: declared-owner", 1)
	mustWrite(t, filepath.Join(repoPath, manifestFile), declared)
	for _, force := range []bool{false, true} {
		s := &adoptSession{repoPath: repoPath, repoName: "legacy", arch: "framework", report: &AdoptReport{},
			opts: AdoptOptions{Force: force}}
		planned, err := plannedManifestBytes(t.Context(), s)
		if err != nil {
			t.Fatalf("force=%v: %v", force, err)
		}
		if !strings.Contains(string(planned), "owner: declared-owner") || !strings.Contains(string(planned), "sources:") {
			t.Fatalf("force=%v: planned manifest is not the kept manifest:\n%s", force, planned)
		}
	}
	s := &adoptSession{repoPath: t.TempDir(), repoName: "fresh", arch: "framework", report: &AdoptReport{},
		identity: repoIdentity{owner: "acme", name: "fresh"}, opts: AdoptOptions{Force: true}}
	if planned, err := plannedManifestBytes(t.Context(), s); err != nil || !strings.Contains(string(planned), "name: fresh") {
		t.Fatalf("missing manifest boundary: planned=%s err=%v", planned, err)
	}
}

// unidentifiedManifest names no owner or name; with no origin remote either, the harness
// platform has nothing to name (BUG-852).
const unidentifiedManifest = "version: 1\nprofiles: [framework]\n"

func unidentifiedRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)
	repoPath := filepath.Join(t.TempDir(), "dev", "orphan")
	mustWrite(t, filepath.Join(repoPath, manifestFile), unidentifiedManifest)
	initTestGit(t, repoPath)
	return repoPath
}

// TestAdoptFreshManifestReportsUnboundSources: a first adoption without an identity writes a
// manifest without register.sources, and the created note says why, as the existing-manifest
// path does. With an identity the contract is bound and the note carries no such reason.
func TestAdoptFreshManifestReportsUnboundSources(t *testing.T) {
	repoPath := unidentifiedRepo(t)
	if err := os.Remove(filepath.Join(repoPath, manifestFile)); err != nil {
		t.Fatal(err)
	}
	opts := sourceAdoptOptions(t, repoPath, false)
	opts.SkipGitValidation, opts.SkipHookActivation = true, true
	report, err := Adopt(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if manifest := loadAdoptedManifest(t, repoPath); manifest.Register != nil {
		t.Fatalf("register bound without a harness: %+v", manifest.Register)
	}
	if got := reportDetail(report, manifestFile); !strings.Contains(got,
		"register.sources not added: repository identity is unresolved") {
		t.Fatalf("fresh manifest note hides the missing contract: %q", got)
	}
	identified := newTestRepo(t, "fresh")
	report, err = Adopt(t.Context(), sourceAdoptOptions(t, identified, false))
	if err != nil {
		t.Fatal(err)
	}
	if got := reportDetail(report, manifestFile); strings.Contains(got, "register.sources not added") {
		t.Fatalf("bound contract reported as missing: %q", got)
	}
	requirePassingSourceGate(t, identified, paperclipFile)
}

// TestAdoptUnresolvedIdentityBindsOnlyAnExistingHarness: without an identity adoption writes
// no harness, so an existing manifest keeps no register.sources and the report says why; a
// harness already on disk stays byte for byte and the contract binds to it, --force included.
func TestAdoptUnresolvedIdentityBindsOnlyAnExistingHarness(t *testing.T) {
	for _, force := range []bool{false, true} {
		repoPath := unidentifiedRepo(t)
		opts := sourceAdoptOptions(t, repoPath, force)
		opts.SkipGitValidation, opts.SkipHookActivation = true, true
		report, err := Adopt(t.Context(), opts)
		if err != nil {
			t.Fatalf("force=%v: %v", force, err)
		}
		if _, err := os.Stat(filepath.Join(repoPath, paperclipFile)); !os.IsNotExist(err) {
			t.Fatalf("force=%v: harness written without an identity: %v", force, err)
		}
		if manifest := loadAdoptedManifest(t, repoPath); manifest.Register != nil && manifest.Register.Sources != nil {
			t.Fatalf("force=%v: register.sources bound to a harness nothing writes: %+v", force, manifest.Register.Sources)
		}
		if got := reportDetail(report, manifestFile); !strings.Contains(got, "repository identity is unresolved") {
			t.Fatalf("force=%v: unbound contract reason not reported: %q", force, got)
		}
		// Boundary: an existing harness is kept and the contract binds to its bytes.
		harness := releasedHarness(t, "harness.json.golden")
		mustWrite(t, filepath.Join(repoPath, paperclipFile), harness)
		if _, err := Adopt(t.Context(), opts); err != nil {
			t.Fatalf("force=%v: re-run with a harness on disk: %v", force, err)
		}
		if got := mustRead(t, filepath.Join(repoPath, paperclipFile)); got != harness {
			t.Fatalf("force=%v: harness rewritten without an identity:\n%s", force, got)
		}
		stageAdoptPaths(t, repoPath, paperclipFile)
		if _, err := cavemansource.ExtractDeclared(t.Context(), repoPath,
			loadAdoptedManifest(t, repoPath).Register.Sources); err != nil {
			t.Fatalf("force=%v: contract not bound to the kept harness: %v", force, err)
		}
	}
}
