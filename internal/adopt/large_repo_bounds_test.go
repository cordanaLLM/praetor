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
// walk saw --verification-max-entries (issue #535). The fixtures below sit just past the default.

// entriesHint is the remedy an exceeded default entry bound must name.
const entriesHint = "raise max_entries with --verification-max-entries, up to 200000"

// largeGoRepo writes go.mod and fill files under src/ into root: fill+2 walk entries in all.
func largeGoRepo(t *testing.T, root string, fill int) string {
	t.Helper()
	mustWrite(t, filepath.Join(root, "go.mod"), "module example.com/widget\n\ngo 1.27\n")
	fillEntries(t, filepath.Join(root, "src"), fill)
	return root
}

// raisedEntries raises only the entry bound, as `--verification-max-entries=n` does.
func raisedEntries(n int) *VerificationLimits {
	limits := DefaultVerificationLimits()
	limits.MaxEntries = n
	return &limits
}

// Positive: a raised entry bound reaches the harness language detection and the whole adoption
// run, including the editor step that used to stop at its own bound.
func TestLargeRepository_Positive_RaisedEntryBoundReachesEveryWalk(t *testing.T) {
	root := largeGoRepo(t, t.TempDir(), util.DefaultDiscoveryEntries)
	if got, _, err := RepositoryHISSFacts(t.Context(), root, raisedEntries(util.DefaultDiscoveryEntries+2)); err != nil || got.Languages != hisscatalog.LanguageGo {
		t.Fatalf("RepositoryHISSFacts under a raised bound = %+v, %v; want Go", got, err)
	}
	repo := largeGoRepo(t, newTestRepo(t, "large-widget"), util.DefaultDiscoveryEntries)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repo, DryRun: true,
		VerificationLimits: raisedEntries(2 * util.DefaultDiscoveryEntries)})
	if err != nil {
		t.Fatalf("adoption under a raised entry bound failed: %v", err)
	}
	if !contains(rep.CreatedFiles, ".vscode/settings.json") {
		t.Fatalf("the editor step did not run under the raised bound: created %v", rep.CreatedFiles)
	}
}

// Negative: at the default bound each walk still stops, and names the flag that raises it; a
// value past the ceiling is refused, naming the flag and its range, before any walk starts.
func TestLargeRepository_Negative_ExceededBoundNamesItsFlag(t *testing.T) {
	root := largeGoRepo(t, t.TempDir(), util.DefaultDiscoveryEntries)
	if _, _, err := RepositoryHISSFacts(t.Context(), root, nil); err == nil || !strings.Contains(err.Error(), entriesHint) {
		t.Fatalf("default-bound harness detection = %v; want it to name %q", err, entriesHint)
	}
	s, _ := catalogSession(t)
	fillEntries(t, filepath.Join(s.repoPath, "src"), util.DefaultDiscoveryEntries+1)
	err := reconcileEditors(t.Context(), s)
	if !errors.Is(err, editor.ErrWorkspaceScanBound) || !strings.Contains(err.Error(), entriesHint) {
		t.Fatalf("default-bound editor step = %v; want ErrWorkspaceScanBound naming %q", err, entriesHint)
	}
	_, _, err = RepositoryHISSFacts(t.Context(), root, raisedEntries(VerificationEntriesCeiling+1))
	if err == nil || !strings.Contains(err.Error(), "max_entries (--verification-max-entries) must be 1..200000") {
		t.Fatalf("over-ceiling bound = %v; want a refusal naming the flag and its range", err)
	}
}

// Boundary: exactly the default number of entries passes; the editor step obeys the bound the
// session's verification plan resolved, admitting one file more once that bound is one higher;
// a bound already at its ceiling says so instead of advising a flag that cannot raise it.
func TestLargeRepository_Boundary_BoundsAreExact(t *testing.T) {
	root := largeGoRepo(t, t.TempDir(), util.DefaultDiscoveryEntries-2)
	if _, _, err := RepositoryHISSFacts(t.Context(), root, nil); err != nil {
		t.Fatalf("exactly %d entries refused: %v", util.DefaultDiscoveryEntries, err)
	}
	s, _ := catalogSession(t)
	fillEntries(t, filepath.Join(s.repoPath, "src"), util.DefaultDiscoveryEntries+1)
	s.verification = &VerificationPlan{Limits: raisedEntries(2 * util.DefaultDiscoveryEntries)}
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
