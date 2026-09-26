package adopt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func lockAdoptSession(t *testing.T) *adoptSession {
	t.Helper()
	return &adoptSession{repoPath: t.TempDir(), arch: "framework", facets: resolveFacets(nil), report: &AdoptReport{}}
}

func TestAdoptLockRequiresExplicitSource(t *testing.T) {
	s := lockAdoptSession(t)
	if err := reconcileLockfile(context.Background(), s); !errors.Is(err, ErrLockSourceRequired) {
		t.Fatalf("missing source must fail, got %v", err)
	}
	s.opts.DryRun = true
	if err := reconcileLockfile(context.Background(), s); err != nil || len(s.report.Warnings) != 1 {
		t.Fatalf("dry-run must expose unresolved lock: %+v / %v", s.report, err)
	}
	if _, err := os.Stat(filepath.Join(s.repoPath, lockFile)); !os.IsNotExist(err) {
		t.Fatalf("missing source created fake lock: %v", err)
	}
}

func TestAdoptLockGeneratesValidPinsAndPreservesThem(t *testing.T) {
	s := lockAdoptSession(t)
	s.opts.LockSourceRoot = newAdoptLockSource(t)
	mustWrite(t, filepath.Join(s.repoPath, manifestFile), "version: 1\nprofiles: [framework]\n")
	if err := reconcileLockfile(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	assertAdoptedLock(t, s.repoPath)
	before := mustRead(t, filepath.Join(s.repoPath, lockFile))
	s.opts.LockSourceRoot = "" // An already valid lock is sufficient for recheck.
	if err := reconcileLockfile(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if after := mustRead(t, filepath.Join(s.repoPath, lockFile)); before != after {
		t.Fatal("existing valid lock changed")
	}
}

func lastLockDetail(t *testing.T, s *adoptSession) string {
	t.Helper()
	for i := len(s.report.ActionDetails) - 1; i >= 0; i-- {
		if s.report.ActionDetails[i].Path == lockFile {
			return s.report.ActionDetails[i].Details
		}
	}
	t.Fatal("no lock action recorded")
	return ""
}

// An existing lock is hashed against the catalog the policy-catalog step resolves; a
// lock with no catalog to hash is recorded as unverifiable, never as verified.
func TestAdoptExistingLockReportsVerificationOutcome(t *testing.T) {
	s := lockAdoptSession(t)
	source := newAdoptLockSource(t)
	s.opts.LockSourceRoot = source
	mustWrite(t, filepath.Join(s.repoPath, manifestFile), "version: 1\nprofiles: [framework]\n")
	if err := reconcileLockfile(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	// Positive: the selected source bundle verifies the existing lock's content.
	if err := reconcileLockfile(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if detail := lastLockDetail(t, s); detail != "Verified existing version pins and content digests" || len(s.report.Warnings) != 0 {
		t.Fatalf("selected catalog must verify content: %q %v", detail, s.report.Warnings)
	}
	// Boundary: no catalog anywhere is unverifiable, stated in the detail and a warning.
	s.opts.LockSourceRoot = ""
	if err := reconcileLockfile(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if detail := lastLockDetail(t, s); !strings.Contains(detail, config.ErrLockUnverifiable.Error()) || len(s.report.Warnings) != 1 {
		t.Fatalf("source-less lock must be reported unverifiable: %q %v", detail, s.report.Warnings)
	}
	// Negative: a repository catalog whose archetype changed its id fails verification.
	mustWrite(t, filepath.Join(s.repoPath, ".config", "archetypes", "framework.yaml"), "id: renamed\nname: Framework\n")
	if err := reconcileLockfile(context.Background(), s); !errors.Is(err, config.ErrLockSourceMissing) {
		t.Fatalf("renamed archetype id must fail existing-lock verification: %v", err)
	}
}

// Rerunning adoption with the same relative --lock-source-root verifies the lock the
// first run generated from it.
func TestAdoptExistingLockRelativeSourceRoot(t *testing.T) {
	s := lockAdoptSession(t)
	source := newAdoptLockSource(t)
	t.Chdir(filepath.Dir(source))
	s.opts.LockSourceRoot = filepath.Join(".", filepath.Base(source))
	mustWrite(t, filepath.Join(s.repoPath, manifestFile), "version: 1\nprofiles: [framework]\n")
	if err := reconcileLockfile(context.Background(), s); err != nil {
		t.Fatalf("first run must generate from a relative source: %v", err)
	}
	// Positive: the second run hashes the same relative source bundle.
	if err := reconcileLockfile(context.Background(), s); err != nil {
		t.Fatalf("rerun with a relative source must verify: %v", err)
	}
	if detail := lastLockDetail(t, s); detail != "Verified existing version pins and content digests" {
		t.Fatalf("relative source must verify content: %q", detail)
	}
	// Negative: tampered content under the relative source is a mismatch.
	mustWrite(t, filepath.Join(source, ".config", "archetypes", "framework.yaml"), "id: framework\nname: Changed\n")
	if err := reconcileLockfile(context.Background(), s); !errors.Is(err, config.ErrLockDigestMismatch) {
		t.Fatalf("tampered relative source must fail: %v", err)
	}
}

func TestAdoptLockRejectsPlaceholderAndExplicitlyRepairsIt(t *testing.T) {
	s := lockAdoptSession(t)
	mustWrite(t, filepath.Join(s.repoPath, manifestFile), "version: 1\nprofiles: [framework]\n")
	placeholder := "version: 1\npinned_version: v1.0.0\n"
	mustWrite(t, filepath.Join(s.repoPath, lockFile), placeholder)
	s.opts.LockSourceRoot = newAdoptLockSource(t)
	if err := reconcileLockfile(context.Background(), s); err == nil || !strings.Contains(err.Error(), "verify existing lock") {
		t.Fatalf("invalid existing lock accepted: %v", err)
	}
	if mustRead(t, filepath.Join(s.repoPath, lockFile)) != placeholder {
		t.Fatal("invalid existing lock overwritten without force")
	}
	s.opts.Force = true
	if err := reconcileLockfile(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	assertAdoptedLock(t, s.repoPath)
}
