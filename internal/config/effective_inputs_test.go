package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readPolicyFixture(t *testing.T, root, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestLoadEffectivePolicyInputsMatchesDiskWithoutScaffolding(t *testing.T) {
	catalog := policyFixture(t, "complexity: {max_func_loc: 35}\n", "", "")
	manifest := readPolicyFixture(t, catalog, ".standards.yaml")
	lock := readPolicyFixture(t, catalog, ".standards.lock")
	onDisk, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: catalog, Audit: true})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "not-created")
	preview, err := LoadEffectivePolicyInputsContext(t.Context(), EffectiveOptions{Root: target, CatalogRoot: catalog, Audit: true}, manifest, lock)
	if err != nil {
		t.Fatal(err)
	}
	if preview.SHA256 != onDisk.SHA256 || preview.Policy.Complexity.MaxFuncLOC != 35 {
		t.Fatalf("preview differs from applied inputs: %+v vs %+v", preview, onDisk)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("input loader created target: %v", err)
	}
	manifest[0], lock[0] = 'x', 'x'
	if preview.Manifest.Version != 1 || preview.SHA256 != onDisk.SHA256 {
		t.Fatal("result aliases planned input bytes")
	}
}

func TestLoadEffectivePolicyInputsValidatesOnlySelectedOverrides(t *testing.T) {
	catalog := policyFixture(t, "", "", "")
	manifest := readPolicyFixture(t, catalog, ".standards.yaml")
	lock := readPolicyFixture(t, catalog, ".standards.lock")
	target := t.TempDir()
	writePolicyFile(t, target, ".standards.yaml", "invalid on disk\n")
	writePolicyFile(t, target, ".standards.lock", "invalid on disk\n")
	opts := EffectiveOptions{Root: target, CatalogRoot: catalog}
	if _, err := LoadEffectivePolicyInputsContext(t.Context(), opts, manifest, lock); err != nil {
		t.Fatalf("planned bytes were not used: %v", err)
	}
	// Selecting the same physical path as an external source must still read its
	// actual bytes; only the named manifest and lock roles receive planned inputs.
	opts.FleetPath = filepath.Join(target, ".standards.yaml")
	if _, err := LoadEffectivePolicyInputsContext(t.Context(), opts, manifest, lock); err == nil {
		t.Fatal("planned manifest leaked into an ordinary external source read")
	}
}

func TestLoadEffectivePolicyInputsNegativeAndByteBoundary(t *testing.T) {
	catalog := policyFixture(t, "", "", "")
	manifest := readPolicyFixture(t, catalog, ".standards.yaml")
	lock := readPolicyFixture(t, catalog, ".standards.lock")
	opts := EffectiveOptions{Root: filepath.Join(t.TempDir(), "preview"), CatalogRoot: catalog}
	for _, planned := range []struct{ manifest, lock []byte }{
		{nil, lock}, {manifest, nil}, {[]byte{0xff}, lock},
		{[]byte(strings.Repeat("x", (1<<20)+1)), lock},
		{manifest, []byte(strings.Repeat("x", (1<<20)+1))},
	} {
		if _, err := LoadEffectivePolicyInputsContext(t.Context(), opts, planned.manifest, planned.lock); err == nil {
			t.Fatal("invalid planned input accepted")
		}
	}
	manifest = append(manifest, []byte("#"+strings.Repeat("x", (1<<20)-len(manifest)-1))...)
	lock = append(lock, []byte("#"+strings.Repeat("x", (1<<20)-len(lock)-1))...)
	if _, err := LoadEffectivePolicyInputsContext(t.Context(), opts, manifest, lock); err != nil {
		t.Fatalf("exact 1MiB inputs rejected: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := LoadEffectivePolicyInputsContext(ctx, opts, manifest, lock); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation cause lost: %v", err)
	}
	var absent context.Context
	if _, err := LoadEffectivePolicyInputsContext(absent, opts, manifest, lock); err == nil {
		t.Fatal("nil context accepted")
	}
}

func TestEffectivePolicyAcceptsExplicitRelativeCatalog(t *testing.T) {
	catalog := policyFixture(t, "", "", "")
	t.Chdir(filepath.Dir(catalog))
	if _, err := LoadEffectivePolicyContext(t.Context(), EffectiveOptions{Root: catalog, CatalogRoot: filepath.Base(catalog)}); err != nil {
		t.Fatalf("explicit relative catalog rejected: %v", err)
	}
}
