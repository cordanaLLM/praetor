package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/needs"
)

// unsetAvailability is how every CLI row scored against no framework renders its mapping
// availability (ADR-0014 §4, needs.MappingAvailability).
const unsetAvailability = "n/a (no target framework configured)"

// runNeedsCapture runs one needs subcommand and returns what it printed.
func runNeedsCapture(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error { return dispatchCommand("needs", args) })
}

// mustNotRenderPercent fails when output renders a percentage.
func mustNotRenderPercent(t *testing.T, what, output string) {
	t.Helper()
	if strings.Contains(output, "%") {
		t.Fatalf("%s rendered a percentage without a framework:\n%s", what, output)
	}
}

// needs scan prints n/a for a repository scored against no framework, never 0% or the
// empty-denominator 100%.
func TestNeedsScanUnconfiguredAvailability_3D(t *testing.T) {
	repo := newNeedsRepo(t)
	// Positive: the unconfigured scan classifies and says n/a.
	out, err := runNeedsCapture(t, "scan", "--path="+repo)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Target Framework: not configured", "Mapping availability: "+unsetAvailability+" (0 covered, 1 gaps, 1 total third-party)",
		"Coverage basis: not-configured")
	mustNotRenderPercent(t, "needs scan", out)
	// Negative: a configured target is scored as a percentage.
	t.Setenv(config.WorkstationConfigEnv, acmeWorkstation(t))
	out, err = runNeedsCapture(t, "scan", "--path="+repo)
	if err != nil || !strings.Contains(out, "Mapping availability: 0.0% (0 covered, 1 gaps") || strings.Contains(out, unsetAvailability) {
		t.Fatalf("configured scan = %v\n%s", err, out)
	}
	// Boundary: a repository without dependencies is n/a when unconfigured, not 100%.
	t.Setenv(config.WorkstationConfigEnv, "")
	bare := t.TempDir()
	writeFixtureFile(t, bare, ".git/HEAD", "ref: refs/heads/main\n")
	writeFixtureFile(t, bare, "go.mod", "module example.com/bare\n\ngo 1.27\n")
	out, err = runNeedsCapture(t, "scan", "--path="+bare)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Mapping availability: "+unsetAvailability+" (0 covered, 0 gaps, 0 total third-party)")
	mustNotRenderPercent(t, "dependency-free needs scan", out)
}

// needs migrate prints n/a and the nothing-to-rewrite blocker without a framework, and
// --apply refuses with needs.ErrFrameworkNotConfigured.
func TestNeedsMigrateUnconfigured_3D(t *testing.T) {
	repo := newNeedsRepo(t)
	// Positive: the dry run shows the blocker and n/a.
	out, err := runNeedsCapture(t, "migrate", "--path="+repo)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "-> not configured ===", "Mapping availability: "+unsetAvailability+" | Coverage basis: not-configured",
		"Blocker: nothing to rewrite: no target framework configured")
	mustNotRenderPercent(t, "needs migrate", out)
	// Negative: --apply refuses before anything is rewritten.
	if _, err := runNeedsCapture(t, "migrate", "--path="+repo, "--apply"); !errors.Is(err, needs.ErrFrameworkNotConfigured) {
		t.Fatalf("unconfigured migrate --apply = %v; want ErrFrameworkNotConfigured", err)
	}
	// Boundary: a configured target is a percentage and no longer the unconfigured blocker.
	t.Setenv(config.WorkstationConfigEnv, acmeWorkstation(t))
	out, err = runNeedsCapture(t, "migrate", "--path="+repo)
	if err != nil || !strings.Contains(out, "Mapping availability: 0.0% | Coverage basis: catalog-declared") ||
		strings.Contains(out, "nothing to rewrite") {
		t.Fatalf("configured migrate = %v\n%s", err, out)
	}
}

// needs requests prints how many requests no builder kit receives (ADR-0014 §4).
func TestNeedsRequestsUnroutedSummary_3D(t *testing.T) {
	isolateDevRootEnv(t)
	root := newDevRootFleet(t)
	// Positive: with no target every request is unrouted.
	out, err := runNeedsCapture(t, "requests", "--dev-dir="+root)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "=== Framework Demand Requests: 1 Synthesized ===\n1 of 1 requests unrouted", "Target Kit: unrouted")
	// Negative: a configured go target routes the request to its first builder kit.
	t.Setenv(config.WorkstationConfigEnv, acmeWorkstation(t))
	out, err = runNeedsCapture(t, "requests", "--dev-dir="+root)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "0 of 1 requests unrouted", "Target Kit: acme/kit")
	// Boundary: a fleet without gaps has no request to route.
	t.Setenv(config.WorkstationConfigEnv, "")
	empty := t.TempDir()
	writeFixtureFile(t, empty, "lib/.git/HEAD", "ref: refs/heads/main\n")
	writeFixtureFile(t, empty, "lib/go.mod", "module example.com/lib\n\ngo 1.27\n")
	out, err = runNeedsCapture(t, "requests", "--dev-dir="+empty)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "=== Framework Demand Requests: 0 Synthesized ===\n0 of 0 requests unrouted")
}

// The fleet epic listing and the needs-miner agent render readiness as n/a without a
// framework, and as a percentage with one.
func TestNeedsFleetEpicAndMinerUnconfigured_3D(t *testing.T) {
	isolateDevRootEnv(t)
	root := newDevRootFleet(t)
	// Positive: the fleet epic preview lists the repository with n/a readiness.
	out, err := runNeedsCapture(t, "epic", "--dev-dir="+root)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "(Readiness: "+unsetAvailability+",")
	// Positive: the needs-miner agent scans its working directory the same way.
	t.Chdir(newNeedsRepo(t))
	out, err = captureStdout(t, func() error { return dispatchAgentTask("praetor-needs-miner", nil) })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Scan complete: Readiness "+unsetAvailability+" (0 covered, 1 gaps)")
	mustNotRenderPercent(t, "needs-miner", out)
	// Negative: with a configured target both render a percentage.
	t.Setenv(config.WorkstationConfigEnv, acmeWorkstation(t))
	out, err = captureStdout(t, func() error { return dispatchAgentTask("praetor-needs-miner", nil) })
	if err != nil || !strings.Contains(out, "Scan complete: Readiness 0.0% (0 covered, 1 gaps)") {
		t.Fatalf("configured needs-miner = %v\n%s", err, out)
	}
	out, err = runNeedsCapture(t, "epic", "--dev-dir="+root)
	if err != nil || !strings.Contains(out, "(Readiness: 0.0%,") {
		t.Fatalf("configured fleet epic = %v\n%s", err, out)
	}
	// Boundary: a fleet with no repository lists no epic and renders no readiness.
	t.Setenv(config.WorkstationConfigEnv, "")
	out, err = runNeedsCapture(t, "epic", "--dev-dir="+t.TempDir())
	if err != nil || strings.Contains(out, "Readiness") {
		t.Fatalf("empty fleet epic = %v\n%s", err, out)
	}
}

// pythonOnlyWorkstation writes a workstation document that configures only the python
// target, declared by the placeholder contract of the needs engine tests.
func pythonOnlyWorkstation(t *testing.T) string {
	t.Helper()
	return pythonContractWorkstation(t, "      module: example.com/acme/py\n")
}

// pythonContractWorkstation writes a workstation document whose only target is python,
// configured by moduleLine (empty for none) and the placeholder python contract.
func pythonContractWorkstation(t *testing.T, moduleLine string) string {
	t.Helper()
	contract, err := os.ReadFile(filepath.Join("..", "..", "internal", "needs", "testdata", "contracts", "py.capabilities.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFixtureFile(t, dir, "py.capabilities.yaml", string(contract))
	writeFixtureFile(t, dir, "workstation.yaml", "framework:\n  targets:\n    python:\n"+moduleLine+
		"      builder_kits: [acme/py]\n      contract: py.capabilities.yaml\n")
	return filepath.Join(dir, "workstation.yaml")
}

// needs scan on a host whose python target is configured by its contract alone names the
// framework that contract declares, never "not configured" (BUG-1017).
func TestNeedsScanContractOnlyTarget_3D(t *testing.T) {
	_, repo := newPythonFleet(t)
	// Negative: without settings the python scan is not configured.
	out, err := runNeedsCapture(t, "scan", "--path="+repo)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Target Framework: not configured", "Mapping availability: "+unsetAvailability)
	// Positive: the contract-only target names its contract's framework and scores 66.7%.
	t.Setenv(config.WorkstationConfigEnv, pythonContractWorkstation(t, ""))
	out, err = runNeedsCapture(t, "scan", "--path="+repo)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Target Framework: example.com/acme/py", "Mapping availability: 66.7% (2 covered, 1 gaps",
		"Coverage basis: catalog-declared")
	if strings.Contains(out, unsetAvailability) {
		t.Fatalf("contract-only scan rendered not configured:\n%s", out)
	}
	// Boundary: a go repository on the same host has no target and stays not configured.
	goRepo := newNeedsRepo(t)
	out, err = runNeedsCapture(t, "scan", "--path="+goRepo)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Target Framework: not configured", "Coverage basis: not-configured")
}

// newPythonFleet builds a dev root holding one python repository whose fastapi and requests
// the python contract replaces and whose weird-lib nothing declares.
func newPythonFleet(t *testing.T) (root, repo string) {
	t.Helper()
	root = t.TempDir()
	repo = filepath.Join(root, "acme", "pyapp")
	writeFixtureFile(t, repo, ".git/HEAD", "ref: refs/heads/main\n")
	writeFixtureFile(t, repo, "requirements.txt", "fastapi==0.110\nrequests==2.31\nweird-lib==1.0\n")
	return root, repo
}

// A host that configures only a python target renders its python repository at 66.7% in
// every needs command, as needs scan does, never as not configured.
func TestNeedsPythonOnlyHost_3D(t *testing.T) {
	isolateDevRootEnv(t)
	root, repo := newPythonFleet(t)
	t.Setenv(config.WorkstationConfigEnv, pythonOnlyWorkstation(t))
	// Positive: aggregate, report, migrate and the fleet epic listing name the python target.
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"aggregate", "--dev-dir=" + root}, []string{"**Target Framework**: `example.com/acme/py`",
			"**Overall Fleet Target Framework Coverage**: 66.7%", "| 66.7% | 2 | 1 |"}},
		{[]string{"report", "--path=" + repo}, []string{"Framework: example.com/acme/py (declared) | Mapping availability: 66.7%"}},
		{[]string{"migrate", "--path=" + repo}, []string{"-> example.com/acme/py ===", "Mapping availability: 66.7% | Coverage basis: catalog-declared"}},
		{[]string{"epic", "--dev-dir=" + root}, []string{"(Readiness: 66.7%,"}},
	} {
		out, err := runNeedsCapture(t, tc.args...)
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		mustContain(t, out, tc.want...)
		if strings.Contains(out, unsetAvailability) || strings.Contains(out, "nothing to rewrite") {
			t.Fatalf("%v rendered not configured on a python-only host:\n%s", tc.args, out)
		}
	}
	// Negative: --apply is refused for want of evidence, not for want of a framework.
	if _, err := runNeedsCapture(t, "migrate", "--path="+repo, "--apply"); err == nil || errors.Is(err, needs.ErrFrameworkNotConfigured) {
		t.Fatalf("python-only migrate --apply = %v; want the evidence refusal", err)
	}
	// Boundary: a go repository no target configures is still n/a beside the python one.
	writeFixtureFile(t, filepath.Join(root, "acme", "goapp"), ".git/HEAD", "ref: refs/heads/main\n")
	writeFixtureFile(t, filepath.Join(root, "acme", "goapp"), "go.mod", "module example.com/goapp\n\ngo 1.27\n\nrequire github.com/spf13/cobra v1.8.0\n")
	out, err := runNeedsCapture(t, "aggregate", "--dev-dir="+root)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "**Target Framework**: `example.com/acme/py`", "| 66.7% | 2 | 1 |", "| "+unsetAvailability+" | 0 | 1 |")
}
