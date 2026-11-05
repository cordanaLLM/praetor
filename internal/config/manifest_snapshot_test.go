// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Planning uses an immutable snapshot across all context-aware manifest readers.
func TestManifestSnapshotReadersAndIsolation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ManifestFileName)
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := []byte("version: 1\nrepository: {owner: acme, name: snapshot, forge: forgejo}\nprofiles: [framework]\nskills: {}\n")
	ctx := WithManifestSnapshot(t.Context(), path, data)
	data[0] = '!'
	assertSnapshotManifests(t, ctx, root)
	assertSnapshotPolicyAndIdentity(t, ctx, root)
	manifest, err := LoadRepositoryManifest(t.Context(), root)
	if err != nil || manifest == nil || len(manifest.Profiles) != 0 {
		t.Fatalf("snapshot leaked into disk: %+v %v", manifest, err)
	}
	if other, err := LoadRepositoryManifest(ctx, t.TempDir()); err != nil || other != nil {
		t.Fatalf("snapshot leaked into another root: %+v %v", other, err)
	}
	if skills, err := LoadSkillsPolicyContext(t.Context(), t.TempDir()); err != nil || skills != nil {
		t.Fatalf("absent skills policy: %+v %v", skills, err)
	}
}

func assertSnapshotManifests(t *testing.T, ctx context.Context, root string) {
	t.Helper()
	manifest, err := LoadRepositoryManifest(ctx, root)
	if err != nil || manifest == nil || len(manifest.Profiles) != 1 {
		t.Fatalf("planned repository manifest: %+v %v", manifest, err)
	}
	authority, err := LoadRegisterAuthority(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = authority.Manifest()
	if err != nil || manifest == nil || len(manifest.Profiles) != 1 {
		t.Fatalf("planned register manifest: %+v %v", manifest, err)
	}
}

func assertSnapshotPolicyAndIdentity(t *testing.T, ctx context.Context, root string) {
	t.Helper()
	if skills, err := LoadSkillsPolicyContext(ctx, root); err != nil || skills == nil {
		t.Fatalf("planned skills: %+v %v", skills, err)
	}
	if owner, name, err := ResolveRepositoryIdentity(ctx, root, "", ""); err != nil || owner != "acme" || name != "snapshot" {
		t.Fatalf("planned identity: %s/%s %v", owner, name, err)
	}
	if forge, err := ResolveRepositoryForge(ctx, root); err != nil || forge != ForgeForgejo {
		t.Fatalf("planned forge: %s %v", forge, err)
	}
}

func TestManifestSnapshotStillValidates(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ManifestFileName)
	ctx := WithManifestSnapshot(t.Context(), path, []byte("version: 1\nunknown: true\n"))
	if _, err := LoadRepositoryManifest(ctx, root); err == nil {
		t.Fatal("repository reader accepted invalid snapshot")
	}
	if _, err := LoadRegisterAuthority(ctx, root); err == nil {
		t.Fatal("register reader accepted invalid snapshot")
	}
	if _, err := LoadSkillsPolicyContext(ctx, root); err == nil {
		t.Fatal("skills reader accepted invalid snapshot")
	}
	ctx, cancel := context.WithCancel(WithManifestSnapshot(t.Context(), path, []byte("version: 1\n")))
	cancel()
	if _, err := LoadRepositoryManifest(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("repository cancellation lost: %v", err)
	}
	if _, err := LoadRegisterAuthority(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("register cancellation lost: %v", err)
	}
	if _, err := LoadSkillsPolicyContext(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("skills cancellation lost: %v", err)
	}
}
