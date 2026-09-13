package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

func TestAudit_EffectivePolicyEnforcesExplicitDeployment(t *testing.T) {
	f := newAuditFixture(t)
	source := "package fixture\n\nfunc count() int {\n value := 0\n" + strings.Repeat(" value++\n", 10) + " return value\n}\n"
	writeFixtureFile(t, f.dir, "count.go", source)
	before, err := f.audit(t)
	if err != nil {
		t.Fatalf("default audit failed: %v\n%s", err, before)
	}
	mustContain(t, before, "max_func_loc=60", "sha256:")
	flags := []string{}
	for _, layer := range []struct{ name, limit string }{
		{"fleet", "55"}, {"organization", "50"}, {"deployment", "8"}, {"workstation", "45"},
	} {
		path := writeFixtureFile(t, f.dir, layer.name+".yaml", "complexity:\n  max_func_loc: "+layer.limit+"\n")
		flags = append(flags, "--"+layer.name+"-config="+path)
	}
	out, err := f.audit(t, flags...)
	mustErrContain(t, err, "limit of 8 LOC")
	mustContain(t, out, "max_func_loc=8", "contributors=deployment")
	// Merely placing policy files in the repository cannot activate private layers.
	// Their additional files do change observed scan coverage, so compare the
	// manifest/policy/lock evidence preceding that separate diagnostic.
	after, err := f.audit(t)
	mustContain(t, after, "[INFO] HISS file scope:")
	if err != nil || strings.SplitN(after, "[INFO] HISS file scope:", 2)[0] != strings.SplitN(before, "[INFO] HISS file scope:", 2)[0] {
		t.Fatalf("unselected policy affected audit: %v\nbefore:\n%s\nafter:\n%s", err, before, after)
	}
}

func TestAuditPolicyRejectsUnsafeManifestAndLockSnapshots(t *testing.T) {
	for _, input := range []struct{ name, diagnostic string }{
		{".standards.yaml", "Manifest audit failed"},
		{".standards.lock", ".standards.lock is missing or unreadable"},
	} {
		for _, kind := range []string{"symlink", "directory", "oversize"} {
			t.Run(input.name+"/"+kind, func(t *testing.T) {
				f := newAuditFixture(t)
				path := filepath.Join(f.dir, input.name)
				original := readFixtureFile(t, f.dir, input.name)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "symlink":
					outside := writeFixtureFile(t, t.TempDir(), "source", original)
					if err := os.Symlink(outside, path); err != nil {
						t.Fatal(err)
					}
				case "directory":
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				case "oversize":
					writeFixtureFile(t, f.dir, input.name, strings.Repeat("x", contextopt.MaxSourceBytes+1))
				}
				out, err := f.audit(t)
				mustErrContain(t, err, input.diagnostic)
				if strings.Contains(out, "[PASS]") {
					t.Fatalf("unsafe policy input produced success evidence: %s", out)
				}
			})
		}
	}
}

func TestAuditPolicyReadPreservesCancellation(t *testing.T) {
	f := newAuditFixture(t)
	opts, err := parseAuditOptions([]string{"--config=" + f.manifestPath})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = auditManifestAndLockfile(ctx, opts)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled audit lost its cause: %v", err)
	}
}

func TestAudit_EffectivePolicyRequiresExplicitSourcesAndSupportsCatalog(t *testing.T) {
	f := newAuditFixture(t)
	_, err := f.audit(t, "--deployment-config="+filepath.Join(f.dir, "missing.yaml"))
	mustErrContain(t, err, "read policy source")
	catalog := t.TempDir()
	if err := os.Mkdir(filepath.Join(catalog, ".config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(f.dir, ".config", "archetypes"), filepath.Join(catalog, ".config", "archetypes")); err != nil {
		t.Fatal(err)
	}
	_, err = f.audit(t)
	mustErrContain(t, err, "materialized profile")
	out, err := f.audit(t, "--catalog-root="+catalog)
	if err != nil {
		t.Fatalf("selected catalog failed: %v\n%s", err, out)
	}
	mustContain(t, out, "max_func_loc=60", "configured governance gates passed")
}

func TestAudit_EffectivePolicyCannotLoosenCompatibilityCeiling(t *testing.T) {
	f := newAuditFixture(t)
	source := "package fixture\n\nfunc count() int {\n value := 0\n" + strings.Repeat(" value++\n", 58) + " return value\n}\n"
	writeFixtureFile(t, f.dir, "count.go", source)
	path := writeFixtureFile(t, f.dir, "deployment.yaml", "complexity:\n  max_func_loc: 1000\n")
	out, err := f.audit(t, "--deployment-config="+path)
	mustErrContain(t, err, "limit of 60 LOC")
	mustContain(t, out, "max_func_loc=60", "contributors=builtin:audit-compat-v1")
}
