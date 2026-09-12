package adopt

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
