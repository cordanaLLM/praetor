package main

import (
	"strings"
	"testing"
)

const pinFixtureManifest = "version: 1\nrepository:\n  owner: fixture\n  name: fixture\nprofiles:\n  - app-service\n"

// Positive (#1103): a Go service under app-service is audited as go-service without --flavor.
// The audit may fail on missing templates; what matters is that it resolved the flavor.
func TestFlavorAudit_Positive_AppServiceResolvesGoService(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, ".standards.yaml", pinFixtureManifest)
	writeFixtureFile(t, dir, "go.mod", "module example.com/svc\n")
	writeFixtureFile(t, dir, "cmd/svc/main.go", "package main\n")
	out, auditErr := captureStdout(t, func() error { return dispatchCommand("flavor", []string{"audit", dir}) })
	t.Logf("audit result: %v", auditErr)
	mustContain(t, out, "(Flavor: go-service)")
}

// Boundary (#1103): two pins scoped to two directories print two reports, each naming its path.
func TestFlavorAudit_Boundary_ScopedPinsPrintOneReportEach(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, ".standards.yaml",
		pinFixtureManifest+"flavors:\n  - name: go-service\n    path: api\n  - name: typescript-node\n    path: web\n")
	writeFixtureFile(t, dir, "api/go.mod", "module example.com/api\n")
	writeFixtureFile(t, dir, "web/package.json", `{"name": "web"}`)
	out, auditErr := captureStdout(t, func() error { return dispatchCommand("flavor", []string{"audit", dir}) })
	t.Logf("audit result: %v", auditErr)
	mustContain(t, out, "(Flavor: go-service, Path: api)", "(Flavor: typescript-node, Path: web)")
}

// Negative (#1103): an unknown pinned flavor is refused, and an explicit --flavor still wins
// over the pin.
func TestFlavorAudit_Negative_UnknownPinRefused(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, ".standards.yaml", pinFixtureManifest+"flavors:\n  - name: no-such-flavor\n")
	_, err := captureStdout(t, func() error { return dispatchCommand("flavor", []string{"audit", dir}) })
	if err == nil || !strings.Contains(err.Error(), "no-such-flavor") {
		t.Fatalf("want a refusal naming the unknown flavor, got %v", err)
	}
	out, auditErr := captureStdout(t, func() error { return dispatchCommand("flavor", []string{"audit", dir, "--flavor=go-library"}) })
	t.Logf("audit result: %v", auditErr)
	mustContain(t, out, "(Flavor: go-library)")
}
