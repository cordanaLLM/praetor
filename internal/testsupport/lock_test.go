// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

const fixtureProfileSource = "id: framework\nname: fixture archetype\n"

// validateFixtureLock validates repo's lock for a manifest selecting the framework profile.
func validateFixtureLock(t *testing.T, repo string) (*config.LockValidation, error) {
	t.Helper()
	manifest, err := config.DecodeManifest([]byte("version: 1\nprofiles: [framework]\n"))
	if err != nil {
		t.Fatal(err)
	}
	return config.ValidateLockfile(context.Background(), repo, manifest)
}

// writeFixtureCatalog materializes body as the framework profile source in repo.
func writeFixtureCatalog(t *testing.T, repo, body string) {
	t.Helper()
	dir := filepath.Join(repo, ".config", "archetypes")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "framework.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Positive: the lock is valid and unverifiable without a catalog, and verified once the
// pinned profile source is materialized.
func TestWritePinnedLock_Positive_ValidThenVerified(t *testing.T) {
	repo := t.TempDir()
	WritePinnedLock(t, repo, "framework", fixtureProfileSource)
	lock, err := validateFixtureLock(t, repo)
	if err != nil || lock.Status != config.LockStatusUnverifiable {
		t.Fatalf("source-less lock = %+v, %v; want valid and unverifiable", lock, err)
	}
	writeFixtureCatalog(t, repo, fixtureProfileSource)
	lock, err = validateFixtureLock(t, repo)
	if err != nil || !lock.Verified() {
		t.Fatalf("lock with its catalog = %+v, %v; want verified", lock, err)
	}
}

// Negative: a repository directory that does not exist fails the test with the reason.
func TestWritePinnedLock_Negative_MissingRepositoryFails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent")
	message := runRecorded(t, func(tb testing.TB) { WritePinnedLock(tb, missing, "framework", fixtureProfileSource) })
	if !strings.Contains(message, "write fixture lock") {
		t.Fatalf("a failed lock write was not reported: %q", message)
	}
}

// Boundary: the pin is the digest of profileSource exactly, so a catalog one byte away from it
// fails verification.
func TestWritePinnedLock_Boundary_PinsTheExactSource(t *testing.T) {
	repo := t.TempDir()
	WritePinnedLock(t, repo, "framework", fixtureProfileSource)
	writeFixtureCatalog(t, repo, fixtureProfileSource+" ")
	if lock, err := validateFixtureLock(t, repo); err == nil {
		t.Fatalf("a catalog that differs from the pinned source verified: %+v", lock)
	}
}
