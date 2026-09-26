package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func lockBuildSource(t *testing.T) (string, *Manifest) {
	t.Helper()
	root, manifest := writeConfigLockFixture(t, lockTestDocument())
	for rel, body := range map[string]string{
		".standards.yaml":                      "version: 1\nprofiles: [framework]\n",
		".config/archetypes/framework.yaml":    lockTestSource,
		".config/archetypes/facets/extra.yaml": "id: test:extra\nname: Extra\n",
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, manifest
}

func TestBuildLockfileRealContentAndDeterministicReplay(t *testing.T) {
	root, manifest := lockBuildSource(t)
	manifest.Facets = []string{"test:extra"}
	first, err := BuildLockfile(context.Background(), root, manifest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildLockfile(context.Background(), root, manifest)
	if err != nil || string(first) != string(second) {
		t.Fatalf("non-deterministic generation: %v", err)
	}
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, ".standards.lock"), first, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := ValidateLockfile(context.Background(), target, manifest)
	if err != nil || result.Profiles != 1 || result.Facets != 1 {
		t.Fatalf("generated pins invalid: %+v, %v", result, err)
	}
	if !strings.Contains(string(first), lockTestDigest("id: test:extra\nname: Extra\n")) {
		t.Fatal("facet pin does not hash actual source")
	}
	if strings.Contains(string(first), "generated_at") {
		t.Fatalf("deterministic lock must not declare an always-empty generated_at:\n%s", first)
	}
}

func TestBuildLockfileRequiresVerifiableSourceBundle(t *testing.T) {
	root, manifest := lockBuildSource(t)
	if err := os.RemoveAll(filepath.Join(root, ".config")); err != nil {
		t.Fatal(err)
	}
	if data, err := BuildLockfile(context.Background(), root, manifest); !errors.Is(err, ErrLockUnverifiable) || data != nil {
		t.Fatalf("source bundle without a catalog returned pins: %q / %v", data, err)
	}
}

func TestBuildLockfileRefusesUnverifiableSources(t *testing.T) {
	for _, kind := range []string{"missing", "corrupt", "changed", "duplicate", "outside"} {
		t.Run(kind, func(t *testing.T) {
			root, manifest := lockBuildSource(t)
			switch kind {
			case "missing":
				manifest.Facets = []string{"unknown"}
			case "corrupt":
				if err := os.WriteFile(filepath.Join(root, ".standards.lock"), []byte("version: 1\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "changed":
				if err := os.WriteFile(filepath.Join(root, ".config/archetypes/framework.yaml"), []byte(lockTestSource+"changed: true\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				manifest.Profiles = []string{"framework", "framework"}
			case "outside":
				manifest.Facets = []string{"outside"}
				if err := os.Symlink(filepath.Join(t.TempDir(), "outside.yaml"), filepath.Join(root, ".config/archetypes/facets/outside.yaml")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if data, err := BuildLockfile(context.Background(), root, manifest); err == nil || data != nil {
				t.Fatalf("unverifiable %s source returned pins: %q / %v", kind, data, err)
			}
		})
	}
}

func TestBuildLockfileBoundsAndCancellation(t *testing.T) {
	root, manifest := lockBuildSource(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := BuildLockfile(ctx, root, manifest); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	manifest.Profiles = make([]string, maxLockEntries+1)
	if data, err := BuildLockfile(context.Background(), root, manifest); err == nil || data != nil {
		t.Fatalf("oversized selection returned pins: %q / %v", data, err)
	}
	for _, tc := range []struct {
		root     string
		manifest *Manifest
	}{
		{"", &Manifest{Version: 1}}, {root, nil}, {root, &Manifest{Version: 2}},
	} {
		if _, err := BuildLockfile(context.Background(), tc.root, tc.manifest); err == nil {
			t.Fatal("missing or invalid inputs accepted")
		}
	}
}
