package adopt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const priorLefthookFixtures = "testdata/lefthook"

func readPriorLefthookFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(priorLefthookFixtures)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(priorLefthookFixtures, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		fixtures[entry.Name()] = data
	}
	return fixtures
}

func adoptLefthookFixture(t *testing.T, name, lefthook string, force bool) (string, *AdoptReport) {
	t.Helper()
	repoPath := newTestRepo(t, name)
	mustWrite(t, filepath.Join(repoPath, lefthookFile), lefthook)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: force})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	assertNoIssues(t, rep)
	return repoPath, rep
}

// The digest set is replayable in both directions: every fixture is a recognised digest and
// every digest has a fixture, so the set can neither claim bytes nobody can reproduce nor
// silently stop covering a fixture.
func TestPriorLefthookDigests_Positive_ReproducedByFixtures(t *testing.T) {
	fixtures := readPriorLefthookFixtures(t)
	seen := make(map[string]bool, len(fixtures))
	for name, data := range fixtures {
		sum := sha256.Sum256(data)
		digest := hex.EncodeToString(sum[:])
		if _, ok := priorLefthookDigests[digest]; !ok {
			t.Errorf("fixture %s (%s) is not a recognised prior rendering", name, digest)
		}
		seen[digest] = true
	}
	for digest, origin := range priorLefthookDigests {
		if !seen[digest] {
			t.Errorf("digest %s (%s) has no fixture under %s", digest, origin, priorLefthookFixtures)
		}
	}
}

// Positive: an earlier Praetor rendering is migrated to the current one without --force and
// then activated, instead of being treated as foreign and never fixed (BUG-859).
func TestAdopt_Positive_PriorLefthookMigratedAndActivated(t *testing.T) {
	for name, data := range readPriorLefthookFixtures(t) {
		repoPath, rep := adoptLefthookFixture(t, "prior-"+strings.TrimSuffix(name, ".lefthook.yml"), string(data), false)
		if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != buildLefthookYAML() {
			t.Errorf("%s: not migrated to the current rendering:\n%s", name, got)
		}
		if !contains(rep.ReconciledFiles, lefthookFile) {
			t.Errorf("%s: migration not reported: %v", name, rep.ReconciledFiles)
		}
		if !fileExists(filepath.Join(repoPath, ".git", "hooks", preCommitHook)) {
			t.Errorf("%s: migrated configuration was not activated", name)
		}
	}
}

// Negative: an edited copy of an earlier rendering is not exact, so it is preserved and not
// activated, exactly like any other configuration Praetor did not write.
func TestAdopt_Negative_EditedPriorLefthookPreserved(t *testing.T) {
	edited := string(readPriorLefthookFixtures(t)["root-go.lefthook.yml"]) + "# local edit\n"
	repoPath, rep := adoptLefthookFixture(t, "edited-prior", edited, false)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != edited {
		t.Fatal("an edited configuration was rewritten")
	}
	if fileExists(filepath.Join(repoPath, ".git", "hooks", preCommitHook)) {
		t.Fatal("an edited configuration was activated")
	}
	if !hasAction(rep, lefthookFile, actionSkip) {
		t.Fatalf("expected the not-generated skip, got %v", rep.ActionDetails)
	}
}

// Boundary: the current renderings are not prior ones (they need no migration), and a prior
// rendering that lost its final newline is no longer exact.
func TestIsPriorLefthookConfig_Boundary_CurrentAndTruncated(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		current := []byte(buildLefthookYAMLFor(checkpoint))
		if isPriorLefthookConfig(current) {
			t.Errorf("current rendering (checkpoint=%v) listed as prior", checkpoint)
		}
		if classifyLefthookConfig(current, buildLefthookYAML()) != (lefthookIdentity{}) {
			t.Errorf("current rendering (checkpoint=%v) classified as prior or protected", checkpoint)
		}
	}
	prior := readPriorLefthookFixtures(t)["hiss16.lefthook.yml"]
	if isPriorLefthookConfig(prior[:len(prior)-1]) {
		t.Error("a truncated prior rendering was recognised")
	}
}

const canonicalRootLefthook = "min_version: 2.1.12\nassert_lefthook_installed: true\nextends:\n  - .config/lefthook/praetor.yml\n"

// Negative: --force never replaces a configuration that extends the canonical policy, nor
// the vendored interceptor beside it, and says why (BUG-858).
func TestAdopt_Negative_ForceKeepsCanonicalLefthookAndInterceptor(t *testing.T) {
	repoPath := newTestRepo(t, "canonical-lefthook")
	mustWrite(t, filepath.Join(repoPath, lefthookFile), canonicalRootLefthook)
	vendored := "# vendored canonical interceptor\n"
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(evasionHookFile)), vendored)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true})
	if err != nil {
		t.Fatalf("Adopt --force: %v", err)
	}
	assertNoIssues(t, rep)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != canonicalRootLefthook {
		t.Fatalf("--force replaced a configuration extending the canonical policy:\n%s", got)
	}
	if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(evasionHookFile))); got != vendored {
		t.Fatal("--force replaced the vendored interceptor with the generated one")
	}
	if !hasAction(rep, lefthookFile, actionSkip) || !strings.Contains(strings.Join(rep.Warnings, "\n"), canonicalLefthookPolicy) {
		t.Fatalf("the protection is not reported: %v", rep.Warnings)
	}
	if fileExists(filepath.Join(repoPath, ".git", "hooks", preCommitHook)) {
		t.Fatal("a configuration adoption did not write was activated")
	}
}

// Negative: --force never replaces a configuration that holds every generated job plus more,
// and the reason names the jobs it would have dropped.
func TestAdopt_Negative_ForceKeepsLefthookJobSuperset(t *testing.T) {
	extended := buildLefthookYAML() + "commit-msg:\n  commands:\n    conventional:\n      run: ./scripts/check-msg {1}\n"
	repoPath, rep := adoptLefthookFixture(t, "superset-lefthook", extended, true)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != extended {
		t.Fatal("--force dropped the repository's extra jobs")
	}
	if !strings.Contains(strings.Join(rep.Warnings, "\n"), "commit-msg/commands/conventional") {
		t.Fatalf("the preserved job is not named: %v", rep.Warnings)
	}
}

// Boundary: extends is recognised as a string, as a list and with a ./ prefix; a different
// extends target and a configuration missing one generated job keep the --force contract.
func TestClassifyLefthookConfig_Boundary_ExtendsAndSupersetEdges(t *testing.T) {
	current := buildLefthookYAML()
	for _, body := range []string{
		"extends: .config/lefthook/praetor.yml\n",
		"extends:\n  - other.yml\n  - ./.config/lefthook/praetor.yml\n",
	} {
		if got := classifyLefthookConfig([]byte(body), current); !got.canonical || got.reason == "" {
			t.Errorf("extends not recognised in %q: %+v", body, got)
		}
	}
	missingOne := strings.Replace(current, "    gate:\n", "    gate-renamed:\n", 1) + "extra:\n  commands:\n    x:\n      run: 'true'\n"
	for _, body := range []string{"extends:\n  - .config/lefthook/other.yml\n", missingOne, "not: [valid yaml\n"} {
		if got := classifyLefthookConfig([]byte(body), current); got != (lefthookIdentity{}) {
			t.Errorf("%q wrongly protected or migrated: %+v", body, got)
		}
	}
	many := current
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		many += "hook-" + name + ":\n  commands:\n    job:\n      run: 'true'\n"
	}
	if reason := classifyLefthookConfig([]byte(many), current).reason; !strings.Contains(reason, "plus 7 more") || !strings.Contains(reason, ", ...") {
		t.Errorf("a long superset is not counted and truncated: %q", reason)
	}
}
