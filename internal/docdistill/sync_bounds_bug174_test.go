// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package docdistill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// BUG-174: SyncRepositoryDocs ranged over every declared dependency with no scalar
// bound and no deadline it enforced itself (HISS-02). These tests exercise the new
// opts.MaxPackages / opts.Timeout bounds and the ErrSyncTruncated report they produce.

// syncFixture seeds a Go module repo declaring n dependencies, each backed by a local
// module-cache README so HarvestDocumentation resolves it offline without a network
// call. Mirrors TestSyncRepositoryDocs_UsesSharedWriter's fixture, generalized to n.
func syncFixture(t *testing.T, n int) (repo string) {
	t.Helper()
	repo = t.TempDir()
	goPath := t.TempDir()
	t.Setenv("GOPATH", goPath)
	t.Setenv("GOMODCACHE", "")

	requires := ""
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("github.com/example/dep%d", i)
		version := "v1.0.0"
		readmeDir := filepath.Join(goPath, "pkg", "mod", filepath.FromSlash(name)+"@"+version)
		if err := os.MkdirAll(readmeDir, 0o700); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf("# %s\nLocal documentation fixture with a real source.", name)
		if err := os.WriteFile(filepath.Join(readmeDir, "README.md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		requires += "\t" + name + " " + version + "\n"
	}
	modBody := "module example.com/x\n\nrequire (\n" + requires + ")\n"
	if n == 0 {
		modBody = "module example.com/x\n"
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte(modBody), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestSyncRepositoryDocs_Positive_NRefsWriteCatalogAndEveryDoc(t *testing.T) {
	const n = 3
	repo := syncFixture(t, n)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	cat, err := SyncRepositoryDocs(ctx, repo, DistillOptions{OfflineOnly: true})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(cat.Packages) != n {
		t.Fatalf("catalog has %d packages, want %d", len(cat.Packages), n)
	}
	for _, doc := range cat.Packages {
		path := filepath.Join(repo, DistilledDirRel, sanitizeDocFilename(doc.PackageName, doc.Version))
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("distilled markdown missing at %s: %v", path, statErr)
		}
	}
	assertCatalogParseable(t, repo, n)
}

func TestSyncRepositoryDocs_Negative_CancelledContextLeavesPreviousCatalogParseable(t *testing.T) {
	repo := syncFixture(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	if _, err := SyncRepositoryDocs(ctx, repo, DistillOptions{OfflineOnly: true}); err != nil {
		cancel()
		t.Fatalf("seed sync: %v", err)
	}
	cancel()
	before, err := os.ReadFile(filepath.Join(repo, CatalogFileRel)) // #nosec G304 -- test-local path from t.TempDir
	if err != nil {
		t.Fatalf("read seeded catalog: %v", err)
	}

	cancelledCtx, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if _, err := SyncRepositoryDocs(cancelledCtx, repo, DistillOptions{OfflineOnly: true, ForceRefresh: true}); err == nil {
		t.Fatal("expected an error syncing with an already-cancelled context")
	}

	after, err := os.ReadFile(filepath.Join(repo, CatalogFileRel)) // #nosec G304 -- test-local path from t.TempDir
	if err != nil {
		t.Fatalf("previous catalog must remain readable: %v", err)
	}
	var parsed DocCatalog
	if jsonErr := json.Unmarshal(after, &parsed); jsonErr != nil {
		t.Fatalf("previous catalog must stay parseable JSON, got %v; content: %s", jsonErr, after)
	}
	if string(before) != string(after) {
		t.Errorf("catalog changed after a sync that never got past the cancelled context")
	}
}

func TestSyncRepositoryDocs_Boundary_AtBoundSucceedsBoundPlusOneTruncates(t *testing.T) {
	repo := syncFixture(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	// At the bound: both refs are synced, no truncation reported.
	cat, err := SyncRepositoryDocs(ctx, repo, DistillOptions{OfflineOnly: true, MaxPackages: 2})
	if err != nil {
		t.Fatalf("sync at bound: %v", err)
	}
	if len(cat.Packages) != 2 {
		t.Fatalf("catalog has %d packages, want 2", len(cat.Packages))
	}

	// Over the bound (2 refs, bound 1): only one is synced, and the caller is told.
	repo2 := syncFixture(t, 2)
	cat2, err := SyncRepositoryDocs(ctx, repo2, DistillOptions{OfflineOnly: true, MaxPackages: 1})
	if err == nil {
		t.Fatal("expected ErrSyncTruncated when refs exceed MaxPackages")
	}
	if !errors.Is(err, ErrSyncTruncated) {
		t.Fatalf("expected ErrSyncTruncated, got %v", err)
	}
	if cat2 == nil || len(cat2.Packages) != 1 {
		t.Fatalf("expected exactly 1 synced package, got %v", cat2)
	}
	assertCatalogParseable(t, repo2, 1)
}

func TestSyncRepositoryDocs_Boundary_ZeroRefsWritesEmptyCatalog(t *testing.T) {
	repo := syncFixture(t, 0)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	cat, err := SyncRepositoryDocs(ctx, repo, DistillOptions{OfflineOnly: true})
	if err != nil {
		t.Fatalf("sync with zero refs: %v", err)
	}
	if len(cat.Packages) != 0 {
		t.Fatalf("expected an empty catalog, got %d packages", len(cat.Packages))
	}
	assertCatalogParseable(t, repo, 0)
}

func TestSyncRepositoryDocs_Boundary_OverallTimeoutTruncates(t *testing.T) {
	repo := syncFixture(t, 2)
	ctx := context.Background()

	// A positive but effectively-elapsed timeout: scan and load (local filesystem work)
	// still complete, but the derived sync deadline has already passed by the time the
	// harvest loop starts, so the loop must stop and report truncation rather than
	// silently skipping every ref.
	cat, err := SyncRepositoryDocs(ctx, repo, DistillOptions{OfflineOnly: true, Timeout: time.Nanosecond})
	if err == nil {
		t.Fatal("expected ErrSyncTruncated when the sync deadline elapses mid-loop")
	}
	if !errors.Is(err, ErrSyncTruncated) {
		t.Fatalf("expected ErrSyncTruncated, got %v", err)
	}
	if cat == nil {
		t.Fatal("expected a non-nil (possibly empty) catalog alongside the truncation error")
	}
}

// assertCatalogParseable reads catalog.json back and requires exactly want packages.
func assertCatalogParseable(t *testing.T, repo string, want int) {
	t.Helper()
	cat, err := LoadCatalog(repo)
	if err != nil {
		t.Fatalf("catalog must remain parseable: %v", err)
	}
	if len(cat.Packages) != want {
		t.Fatalf("catalog has %d packages, want %d", len(cat.Packages), want)
	}
}

// stoppedByBound decides whether a failed harvest saves a partial catalog and reports
// ErrSyncTruncated or surfaces the error as-is, so it carries the whole contract for a
// deadline that lands inside a package rather than between two of them. The harvest that
// trips it cannot be timed deterministically across platforms; the classifier can.
func TestStoppedByBound_PositiveNegativeBoundary(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// Positive: the two ways the sync context ends, bare and wrapped the way
		// syncOnePackage wraps them.
		{"deadline", context.DeadlineExceeded, true},
		{"canceled", context.Canceled, true},
		{"wrapped deadline", fmt.Errorf("harvest example.com/dep@v1.0.0: %w", context.DeadlineExceeded), true},
		{"wrapped cancel", fmt.Errorf("harvest example.com/dep@v1.0.0: %w", context.Canceled), true},
		// Negative: a real harvest failure must never be reported as truncation.
		{"harvest failure", errors.New("harvest example.com/dep@v1.0.0: connection refused"), false},
		{"deadline text only", errors.New("context deadline exceeded"), false},
		// Boundary: no error at all.
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := stoppedByBound(tc.err); got != tc.want {
			t.Fatalf("%s: stoppedByBound(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}
