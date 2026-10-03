// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// WritePinnedLock writes repo/.standards.lock as a consumer repository holds it without its
// profile catalog: one profile, profileID, pinned at v1.2.3 to the SHA-256 of profileSource,
// with the aggregate digest over that pin. config.ValidateLockfile accepts it as valid and
// unverifiable; writing profileSource to repo/.config/archetypes/<profileID>.yaml makes it
// verified. Onboarding refuses to finish without such a lock, so a test that needs a
// successful onboarding run starts from it.
func WritePinnedLock(t testing.TB, repo, profileID, profileSource string) {
	t.Helper()
	digest := sha256.Sum256([]byte(profileSource))
	pin := "sha256:" + hex.EncodeToString(digest[:])
	aggregate := sha256.Sum256([]byte("profile:" + profileID + "=" + pin + "\n"))
	lock := map[string]any{"version": 1, "pinned_version": "v1.2.3", "digest": "sha256:" + hex.EncodeToString(aggregate[:]),
		"profiles": []map[string]string{{"id": profileID, "version": "v1.2.3", "digest": pin}}}
	data, err := json.Marshal(lock)
	if err != nil {
		t.Fatalf("encode fixture lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".standards.lock"), data, 0o600); err != nil {
		t.Fatalf("write fixture lock: %v", err)
	}
}
