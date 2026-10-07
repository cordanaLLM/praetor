package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestServerAuditEffectiveDeploymentPolicy(t *testing.T) {
	srv, root := newFixtureServer(t)
	before := callTool(t, srv, "standards_audit", nil)
	expectText(t, "default audit", before, "max_func_loc=60")
	expectText(t, "audited repository identity", before, "audit: fixture/repo governance.")
	args := map[string]any{}
	for _, layer := range []struct{ name, limit string }{
		{"fleet", "55"}, {"organization", "50"}, {"deployment", "8"}, {"workstation", "45"},
	} {
		name := layer.name + ".yaml"
		writeFixtureFile(t, root, name, "complexity:\n  max_func_loc: "+layer.limit+"\n")
		args[layer.name+"_config_path"] = name
	}
	result := callTool(t, srv, "standards_audit", args)
	expectError(t, "deployment restriction", result, "limit of 8 LOC")
	for _, want := range []string{"max_func_loc=8", "contributors=deployment", "source=deployment sha256:"} {
		if !strings.Contains(result.Content[0].Text, want) {
			t.Fatalf("report lacks %q: %s", want, result.Content[0].Text)
		}
	}
	// Policy files remain inactive unless selected by this request.
	after := callTool(t, srv, "standards_audit", nil)
	expectText(t, "default remains", after, "max_func_loc=60")
	if policyEvidenceLine(t, after.Content[0].Text) != policyEvidenceLine(t, before.Content[0].Text) {
		t.Fatal("unselected policy files changed the default policy identity")
	}
}

func TestServerAuditOptionalPolicyPathsRetainConfinement(t *testing.T) {
	srv, root := newFixtureServer(t)
	outside := t.TempDir()
	writeFixtureFile(t, outside, "deployment.yaml", "complexity:\n  max_func_loc: 45\n")
	path := filepath.Join(outside, "deployment.yaml")
	if err := os.Symlink(path, filepath.Join(root, "outside.yaml")); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"catalog_root", "fleet_config_path", "organization_config_path", "deployment_config_path", "workstation_config_path"} {
		t.Run(key, func(t *testing.T) {
			for _, value := range []any{path, "outside.yaml"} {
				result := callTool(t, srv, "standards_audit", map[string]any{key: value})
				expectError(t, "outside policy", result, ErrOutsideRoot.Error())
			}
			result := callTool(t, srv, "standards_audit", map[string]any{key: 12})
			expectError(t, "wrong type", result, "must be a string")
		})
	}
	open, err := NewServerWithOptions(ServerOptions{RootDir: root, Version: "v", AllowOutsideRoot: true})
	if err != nil {
		t.Fatal(err)
	}
	result := callTool(t, open, "standards_audit", map[string]any{"deployment_config_path": path})
	expectText(t, "explicitly allowed outside policy", result, "max_func_loc=45")
}

func TestServerAuditPolicyFailureAndCeiling(t *testing.T) {
	srv, root := newFixtureServer(t)
	result := callTool(t, srv, "standards_audit", map[string]any{"deployment_config_path": "missing.yaml"})
	expectError(t, "missing policy", result, "read policy source")
	writeFixtureFile(t, root, "deployment.yaml", "complexity:\n  max_func_loc: 1000\n")
	source := "package main\n\nfunc count() int {\n value := 0\n" + strings.Repeat(" value++\n", 58) + " return value\n}\n"
	writeFixtureFile(t, root, "count.go", source)
	result = callTool(t, srv, "standards_audit", map[string]any{"deployment_config_path": "deployment.yaml"})
	expectError(t, "audit ceiling", result, "limit of 60 LOC")
	if !strings.Contains(result.Content[0].Text, "max_func_loc=60") {
		t.Fatal("report omitted effective compatibility ceiling")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	paths, err := srv.resolveAuditPaths(nil)
	if err != nil {
		t.Fatal(err)
	}
	expectError(t, "cancelled policy audit", srv.runAuditGates(ctx, paths), "context canceled")
}

func TestServerAuditExternalManifestPreservesExplicitAuthorization(t *testing.T) {
	srv, root := newFixtureServer(t)
	data, err := os.ReadFile(filepath.Join(root, ".standards.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	writeFixtureFile(t, outside, "manifest.yaml", string(data))
	args := map[string]any{"config_path": filepath.Join(outside, "manifest.yaml")}
	expectError(t, "confined external manifest", callTool(t, srv, "standards_audit", args), ErrOutsideRoot.Error())
	open, err := NewServerWithOptions(ServerOptions{RootDir: root, Version: "v", AllowOutsideRoot: true})
	if err != nil {
		t.Fatal(err)
	}
	expectText(t, "authorized external manifest", callTool(t, open, "standards_audit", args), "passed: 10/10")
}

func TestServerAuditUsesSelectedCatalog(t *testing.T) {
	srv, root := newFixtureServer(t)
	before := callTool(t, srv, "standards_audit", nil)
	expectText(t, "original catalog", before, "max_func_loc=60")
	relocateCatalog(t, root, "catalog")
	expectError(t, "missing default catalog", callTool(t, srv, "standards_audit", nil), "materialized profile")
	after := callTool(t, srv, "standards_audit", map[string]any{"catalog_root": "catalog"})
	expectText(t, "selected catalog", after, "passed: 10/10")
	// Mounting identical catalog bytes at another path retains policy identity.
	beforeLine := strings.Split(before.Content[0].Text, "\n")[2]
	afterLine := strings.Split(after.Content[0].Text, "\n")[2]
	if beforeLine != afterLine {
		t.Fatalf("moving catalog changed policy identity: %s != %s", beforeLine, afterLine)
	}
}

// relocateCatalog moves root's pinned .config/archetypes under root/rel, so the lock stays
// valid while the default catalog is no longer materialized.
func relocateCatalog(t *testing.T, root, rel string) {
	t.Helper()
	catalog := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Join(catalog, ".config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, ".config", "archetypes"), filepath.Join(catalog, ".config", "archetypes")); err != nil {
		t.Fatal(err)
	}
}

func policyEvidenceLine(t *testing.T, text string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "Effective complexity policy sha256:") {
			return line
		}
	}
	t.Fatal("audit omitted effective policy evidence")
	return ""
}

// TestServerAuditRunsTheSupplyChainGate (#330): standards_audit runs the HISS-11 gate the CLI
// audit runs. Negative: an override declaring SLSA Build Level 2 fails while no workflow writes
// provenance. Positive: a release job that attests its build meets it, and the gate's pass line
// names the measurement.
func TestServerAuditRunsTheSupplyChainGate(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeFixtureFile(t, root, ".standards.yaml", "version: 1\nrepository:\n  owner: fixture\n  name: repo\nprofiles: [framework]\n"+
		"facets: []\noverrides:\n  supply_chain:\n    slsa_level: 2\n")
	expectError(t, "declared level above the workflows", callTool(t, srv, "standards_audit", nil),
		"[FAIL] Supply chain (HISS-11): policy declares SLSA Build Level 2 but the workflows reach Level 0")
	writeFixtureFile(t, root, ".github/workflows/release.yml", "on:\n  push:\n    tags: ['v*']\njobs:\n  release:\n"+
		"    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/attest-build-provenance@v4\n")
	audit := callTool(t, srv, "standards_audit", nil)
	expectText(t, "declared level met", audit, "[PASS] Supply chain (HISS-11): SLSA Build Level 2 declared, Level 2 measured from release.yml.")
	expectText(t, "declared level met", audit, "passed: 10/10")
}

// TestServerAuditRunsTheWorkflowTriggerCheck (#817): standards_audit runs the HISS-18 workflow
// trigger check the CLI audit runs. Positive: a push without a branch filter is reported as a
// [WARN] line naming the file and line, and the audit still passes every gate. Boundary: a live
// HISS-18 exceptions entry for the workflow prints the finding under a [PASS] line instead.
func TestServerAuditRunsTheWorkflowTriggerCheck(t *testing.T) {
	srv, root := newFixtureServer(t)
	writeFixtureFile(t, root, ".github/workflows/everywhere.yml", "on: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n"+
		"    steps:\n      - run: make\n")
	audit := callTool(t, srv, "standards_audit", nil)
	expectText(t, "unfiltered push", audit,
		"[WARN] Workflow triggers (HISS-18): .github/workflows/everywhere.yml:1: push runs on every branch and tag")
	expectText(t, "unfiltered push", audit, "passed: 10/10")
	expires := config.ExceptionDay(time.Now()).AddDate(0, 0, 30).Format(config.ExceptionDateLayout)
	writeFixtureFile(t, root, ".standards.yaml", "version: 1\nrepository:\n  owner: fixture\n  name: repo\nprofiles: [framework]\n"+
		"facets: []\nexceptions:\n  - rule: \"HISS-18\"\n    path: \".github/workflows/everywhere.yml\"\n"+
		"    reason: \"builds every branch push by design\"\n    expires: \""+expires+"\"\n")
	excepted := callTool(t, srv, "standards_audit", nil)
	expectText(t, "excepted push", excepted, "[PASS] Workflow triggers (HISS-18): .github/workflows/everywhere.yml excepted until "+
		expires+" by the exceptions entry (rule HISS-18, .github/workflows/everywhere.yml): builds every branch push by design")
	expectText(t, "excepted push", excepted, "passed: 10/10")
}
