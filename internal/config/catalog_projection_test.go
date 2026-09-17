package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func projectionArtifact(name, id string) PolicyArtifact {
	data := []byte("id: " + id + "\ncomplexity: {max_func_loc: 50}\n")
	return PolicyArtifact{RelativePath: name, Content: data, SHA256: policyDigest(data)}
}

func TestCatalogProjectionFindsAlternateFilenameCollisionWithoutWrites(t *testing.T) {
	root := t.TempDir()
	artifact := projectionArtifact(".config/archetypes/framework.yaml", "framework")
	other := writePolicyFile(t, root, ".config/archetypes/other.yaml", "id: framework\n")
	err := ValidateCatalogProjectionContext(t.Context(), root, []PolicyArtifact{artifact})
	if err == nil || !strings.Contains(err.Error(), "duplicate archetype ID") {
		t.Fatalf("duplicate identity accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, artifact.RelativePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("projection wrote planned artifact")
	}
	if got := string(readPolicyFixture(t, filepath.Dir(other), filepath.Base(other))); got != "id: framework\n" {
		t.Fatal("projection changed existing input")
	}
}

func TestCatalogProjectionReplacesBadBytesButValidatesOtherEntries(t *testing.T) {
	root := t.TempDir()
	artifact := projectionArtifact(".config/archetypes/framework.yaml", "framework")
	writePolicyFile(t, root, artifact.RelativePath, "invalid: [\n")
	if err := ValidateCatalogProjectionContext(t.Context(), root, []PolicyArtifact{artifact}); err != nil {
		t.Fatalf("explicit replacement parsed obsolete bytes: %v", err)
	}
	writePolicyFile(t, root, ".config/archetypes/unselected.yaml", "invalid: [\n")
	if err := ValidateCatalogProjectionContext(t.Context(), root, []PolicyArtifact{artifact}); err == nil {
		t.Fatal("invalid unchanged YAML accepted")
	}
	if got := string(readPolicyFixture(t, root, artifact.RelativePath)); got != "invalid: [\n" {
		t.Fatal("read-only projection replaced target")
	}
}

func TestCatalogProjectionRejectsUnsafeArtifactsAndFilesystemEntries(t *testing.T) {
	root := t.TempDir()
	good := projectionArtifact(".config/archetypes/framework.yaml", "framework")
	for _, path := range []string{"../outside.yaml", ".config/archetypes/nested/x.yaml", ".config/x.yaml", ".config/archetypes/x\n.yaml"} {
		bad := good
		bad.RelativePath = path
		if err := ValidateCatalogProjectionContext(t.Context(), root, []PolicyArtifact{bad}); err == nil {
			t.Fatalf("unsafe artifact path accepted: %q", path)
		}
	}
	bad := good
	bad.SHA256 = strings.Repeat("0", 64)
	if err := ValidateCatalogProjectionContext(t.Context(), root, []PolicyArtifact{bad}); err == nil {
		t.Fatal("changed artifact hash accepted")
	}
	if err := ValidateCatalogProjectionContext(t.Context(), root, []PolicyArtifact{good, good}); err == nil {
		t.Fatal("duplicate path accepted")
	}
	if err := os.MkdirAll(filepath.Join(root, ".config/archetypes"), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := writePolicyFile(t, t.TempDir(), "source.yaml", "id: framework\n")
	if err := os.Symlink(outside, filepath.Join(root, good.RelativePath)); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCatalogProjectionContext(t.Context(), root, []PolicyArtifact{good}); err == nil {
		t.Fatal("replacement through symlink accepted")
	}
}

func TestCatalogProjectionBoundaryAndCancellation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "uncreated")
	artifacts := make([]PolicyArtifact, 0, 2*maxLockEntries)
	// path.Join, not filepath.Join: a catalog path is a slash identity, and the
	// literals elsewhere in this file spell it that way. filepath.Join is the same
	// thing on Linux and a backslash path on Windows, so this loop built inputs the
	// validator rightly refuses -- while passing on the platform it was written on.
	for _, dir := range []string{archetypeDirName, path.Join(archetypeDirName, facetDirName)} {
		for i := 0; i < maxLockEntries; i++ {
			id := fmt.Sprintf("item%d", i)
			artifacts = append(artifacts, projectionArtifact(path.Join(dir, id+".yaml"), id))
		}
	}
	if err := ValidateCatalogProjectionContext(t.Context(), root, artifacts); err != nil {
		t.Fatalf("exact artifact bound rejected: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("projection created root")
	}
	tooMany := append(append([]PolicyArtifact(nil), artifacts...), projectionArtifact(".config/archetypes/overflow.yaml", "overflow"))
	if err := ValidateCatalogProjectionContext(t.Context(), root, tooMany); err == nil {
		t.Fatal("excess artifacts accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ValidateCatalogProjectionContext(ctx, root, artifacts); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation cause lost: %v", err)
	}
	var absent context.Context
	if err := ValidateCatalogProjectionContext(absent, root, nil); err == nil {
		t.Fatal("nil context accepted")
	}
}

func TestCatalogProjectionCountsAllProspectiveDirectoryEntries(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < maxArchetypeDirectoryEntries-1; i++ {
		writePolicyFile(t, root, fmt.Sprintf(".config/archetypes/note%d.txt", i), "unrelated\n")
	}
	one := projectionArtifact(".config/archetypes/one.yaml", "one")
	two := projectionArtifact(".config/archetypes/two.yaml", "two")
	if err := ValidateCatalogProjectionContext(t.Context(), root, []PolicyArtifact{one}); err != nil {
		t.Fatalf("exact prospective entry bound rejected: %v", err)
	}
	if err := ValidateCatalogProjectionContext(t.Context(), root, []PolicyArtifact{one, two}); err == nil {
		t.Fatal("new YAML entries were omitted from raw directory bound")
	}
	// A replacement counts once, even when the existing file has invalid bytes.
	writePolicyFile(t, root, one.RelativePath, "invalid: [\n")
	if err := ValidateCatalogProjectionContext(t.Context(), root, []PolicyArtifact{one}); err != nil {
		t.Fatalf("replacement counted twice: %v", err)
	}
	facet := projectionArtifact(".config/archetypes/facets/facet.yaml", "facet")
	if err := ValidateCatalogProjectionContext(t.Context(), root, []PolicyArtifact{one, facet}); err == nil {
		t.Fatal("new facets directory omitted from parent entry bound")
	}
	if _, err := os.Stat(filepath.Join(root, two.RelativePath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed preflight published an artifact")
	}
	if _, err := os.Stat(filepath.Join(root, ".config/archetypes/facets")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed preflight created facets directory")
	}
}
