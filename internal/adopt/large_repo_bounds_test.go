package adopt

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/editor"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/util"
)

// A large repository used to stop adoption at the editor language scan's own fixed 4096-file
// bound, and `praetorctl paperclip harness` at the verification walk's default, because neither
// walk saw --verification-max-entries (issue #535). Each walk applies the entry bound it is
// given, so the fixtures below sit around fixtureEntries, set as the flag sets it: a fixture
// sized to the default would grow with every raise of it.
// TestDiscoveryDefaults_Boundary_UnsetBoundSelectsTheSharedDefault pins what an unset bound
// resolves to.

// fixtureEntries is the entry bound the walk tests set in place of the default.
const fixtureEntries = 64

// entriesHint is the remedy an exceeded entry bound below its ceiling must name.
const entriesHint = "raise max_entries with --verification-max-entries, up to 200000"

// largeGoRepo writes go.mod and fill files under src/ into root: fill+2 walk entries in all.
func largeGoRepo(t *testing.T, root string, fill int) string {
	t.Helper()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	fillEntries(t, filepath.Join(root, "src"), fill)
	return root
}

// withEntries sets only the entry bound, as `--verification-max-entries=n` does.
func withEntries(n int) *VerificationLimits {
	limits := DefaultVerificationLimits()
	limits.MaxEntries = n
	return &limits
}

// Positive: the caller's entry bound reaches the harness language detection and the whole
// adoption run, whose verification plan carries it to the editor step that used to stop at its
// own bound.
func TestLargeRepository_Positive_RaisedEntryBoundReachesEveryWalk(t *testing.T) {
	root := largeGoRepo(t, t.TempDir(), fixtureEntries)
	if got, _, err := RepositoryHISSFacts(t.Context(), root, withEntries(fixtureEntries+2)); err != nil || got.Languages != hisscatalog.LanguageGo {
		t.Fatalf("RepositoryHISSFacts under a raised bound = %+v, %v; want Go", got, err)
	}
	repo := largeGoRepo(t, newTestRepo(t, "large-widget"), 2*fixtureEntries)
	opts := AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, DryRun: true, VerificationLimits: withEntries(fixtureEntries)}
	if _, err := Adopt(context.Background(), opts); err == nil || !strings.Contains(err.Error(), entriesHint) {
		t.Fatalf("adoption past its entry bound = %v; want it to name %q", err, entriesHint)
	}
	opts.VerificationLimits = withEntries(8 * fixtureEntries)
	rep, err := Adopt(context.Background(), opts)
	if err != nil {
		t.Fatalf("adoption under a raised entry bound failed: %v", err)
	}
	if rep.Verification == nil || rep.Verification.Limits == nil || rep.Verification.Limits.MaxEntries != 8*fixtureEntries {
		t.Fatalf("the verification plan did not carry the raised bound: %+v", rep.Verification)
	}
	if !contains(rep.CreatedFiles, ".vscode/settings.json") {
		t.Fatalf("the editor step did not run under the raised bound: created %v", rep.CreatedFiles)
	}
}

// Negative: past the bound each walk still stops, and names the flag that raises it; a value
// past the ceiling is refused, naming the flag and its range, before any walk starts.
func TestLargeRepository_Negative_ExceededBoundNamesItsFlag(t *testing.T) {
	root := largeGoRepo(t, t.TempDir(), fixtureEntries)
	if _, _, err := RepositoryHISSFacts(t.Context(), root, withEntries(fixtureEntries+1)); err == nil || !strings.Contains(err.Error(), entriesHint) {
		t.Fatalf("bounded harness detection = %v; want it to name %q", err, entriesHint)
	}
	s, _ := catalogSession(t)
	fillEntries(t, filepath.Join(s.repoPath, "src"), fixtureEntries+1)
	s.verification = &VerificationPlan{Limits: withEntries(fixtureEntries)}
	err := reconcileEditors(t.Context(), s)
	if !errors.Is(err, editor.ErrWorkspaceScanBound) || !strings.Contains(err.Error(), entriesHint) {
		t.Fatalf("bounded editor step = %v; want ErrWorkspaceScanBound naming %q", err, entriesHint)
	}
	_, _, err = RepositoryHISSFacts(t.Context(), root, withEntries(VerificationEntriesCeiling+1))
	if err == nil || !strings.Contains(err.Error(), "max_entries (--verification-max-entries) must be 1..200000") {
		t.Fatalf("over-ceiling bound = %v; want a refusal naming the flag and its range", err)
	}
}

// Boundary: exactly the bound's number of entries passes; the editor step obeys the bound the
// session's verification plan resolved, admitting the files it refused once that bound is
// higher; a bound already at its ceiling says so instead of advising a flag that cannot raise it.
func TestLargeRepository_Boundary_BoundsAreExact(t *testing.T) {
	root := largeGoRepo(t, t.TempDir(), fixtureEntries-2)
	if _, _, err := RepositoryHISSFacts(t.Context(), root, withEntries(fixtureEntries)); err != nil {
		t.Fatalf("exactly %d entries refused: %v", fixtureEntries, err)
	}
	s, _ := catalogSession(t)
	fillEntries(t, filepath.Join(s.repoPath, "src"), fixtureEntries+1)
	s.verification = &VerificationPlan{Limits: withEntries(8 * fixtureEntries)}
	if err := reconcileEditors(t.Context(), s); err != nil {
		t.Fatalf("editor step ignored the session's raised bound: %v", err)
	}
	cause := errors.New("verification discovery exceeds 200000 entries")
	if got := entriesBound.exceeded(VerificationEntriesCeiling, cause).Error(); !strings.Contains(got, "max_entries is already at its ceiling 200000") || strings.Contains(got, "--verification") {
		t.Fatalf("at-ceiling remedy = %q", got)
	}
	if got := entriesBound.exceeded(VerificationEntriesCeiling-1, cause).Error(); !strings.Contains(got, "--verification-max-entries") {
		t.Fatalf("below-ceiling remedy = %q", got)
	}
}

// TestDiscoveryDefaults_Boundary_UnsetBoundSelectsTheSharedDefault: an unset entry bound resolves
// to util.DefaultDiscoveryEntries on both walks the adopt path runs, so the explicit bounds the
// tests above set stand in for it. Positive: nil limits, and a session without a resolved plan or
// without plan limits, select the default, and the default admits a tree past the earlier
// 4096-entry default, the size this repository's own checkout reached once its documentation
// site was built. Negative: a plan's own bound wins over the default. Boundary: the default sits
// within the ceiling the flag is held to.
func TestDiscoveryDefaults_Boundary_UnsetBoundSelectsTheSharedDefault(t *testing.T) {
	normalized, err := NormalizeVerificationLimits(nil)
	if err != nil || normalized.MaxEntries != util.DefaultDiscoveryEntries {
		t.Fatalf("nil limits = %+v, %v; want max_entries %d", normalized, err, util.DefaultDiscoveryEntries)
	}
	for name, plan := range map[string]*VerificationPlan{"no plan": nil, "plan without limits": {}} {
		if got := (&adoptSession{verification: plan}).discoveryEntries(); got != util.DefaultDiscoveryEntries {
			t.Errorf("%s: editor bound %d; want the default %d", name, got, util.DefaultDiscoveryEntries)
		}
	}
	past := largeGoRepo(t, t.TempDir(), 4096)
	if got, _, err := RepositoryHISSFacts(t.Context(), past, nil); err != nil || got.Languages != hisscatalog.LanguageGo {
		t.Fatalf("default-bound detection of a 4098-entry tree = %+v, %v; want Go", got, err)
	}
	planned := &adoptSession{verification: &VerificationPlan{Limits: withEntries(fixtureEntries)}}
	if got := planned.discoveryEntries(); got != fixtureEntries {
		t.Fatalf("plan bound %d gave the editor %d", fixtureEntries, got)
	}
	if util.DefaultDiscoveryEntries > VerificationEntriesCeiling {
		t.Fatalf("default %d exceeds the ceiling %d", util.DefaultDiscoveryEntries, VerificationEntriesCeiling)
	}
}
