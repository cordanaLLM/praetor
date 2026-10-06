package main

import (
	"strings"
	"testing"
)

// fixtureReleaseWorkflow builds the fixture's archives and attests them in the same job:
// SLSA Build Level 2.
const fixtureReleaseWorkflow = "on:\n  push:\n    tags: ['v*']\njobs:\n  release:\n    runs-on: ubuntu-latest\n    steps:\n" +
	"      - run: make dist\n      - uses: actions/attest-build-provenance@v4\n        with:\n          subject-path: dist/*\n"

// The CLI audit runs the HISS-11 supply-chain gate (#330). Positive: the fixture policy declares
// no supply-chain control, so the gate passes without a workflow. Negative: an override raising
// slsa_level to 2 fails the audit while no workflow writes provenance. Boundary: a release that
// attests in its build job meets Level 2 exactly, and the pass line names the measurement and
// its scope.
func TestAuditSupplyChain_CLI_MeasuresTheDeclaredLevel(t *testing.T) {
	f := newAuditFixture(t)
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit failed: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[PASS] Supply chain (HISS-11): policy declares SLSA Build Level 0, no cosign signing and no SBOM")

	writeFixtureFile(t, f.dir, ".standards.yaml",
		fixtureManifest("acme", "widgets", false)+"overrides:\n  supply_chain:\n    slsa_level: 2\n")
	out, err = f.audit(t)
	if err == nil {
		t.Fatalf("audit passed a declared Level 2 with no provenance workflow:\n%s", out)
	}
	mustErrContain(t, err, "[FAIL] Supply chain (HISS-11): policy declares SLSA Build Level 2 but the workflows reach Level 0")

	writeFixtureFile(t, f.dir, ".github/workflows/release.yml", fixtureReleaseWorkflow)
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("audit failed with a Level 2 release workflow: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[PASS] Supply chain (HISS-11): SLSA Build Level 2 declared, Level 2 measured from release.yml.")
	if !strings.Contains(out, "published attestations and signatures were not checked") {
		t.Fatalf("pass line does not state the scope of the measurement:\n%s", out)
	}
}
