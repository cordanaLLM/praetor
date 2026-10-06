package adopt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// hiss11Fixtures holds the HISS-11 workflow fixtures .config/hiss/coverage.yaml attributes to
// this gate; TestAuditSupplyChainReplaysTheHISS11Fixtures replays them.
const hiss11Fixtures = "../../.config/hiss/testdata/HISS-11/github-actions"

// supplyChainRepo writes files (slash path to content) into a fresh repository directory.
func supplyChainRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		mustWrite(t, filepath.Join(root, filepath.FromSlash(rel)), content)
	}
	return root
}

// supplyChainPolicy is the default policy with supply_chain replaced.
func supplyChainPolicy(level int, cosign, sbom bool) *config.ResolvedPolicy {
	policy := config.DefaultPolicy()
	policy.SupplyChain = config.SupplyChainPolicy{SLSALevel: level, EnforceCosign: cosign, RequireSBOM: sbom}
	return policy
}

// releaseSteps wraps steps into a release workflow a tag push starts.
func releaseSteps(steps string) string {
	return "on:\n  push:\n    tags: ['v*']\njobs:\n  release:\n    runs-on: ubuntu-latest\n    steps:\n" + steps
}

const (
	directAttestation = "      - run: make dist\n      - uses: actions/attest-build-provenance@v4\n        with:\n          subject-path: dist/*\n"
	cosignSignBlob    = "      - run: cosign sign-blob --yes --bundle c.sigstore.json dist/checksums.txt\n"
	syftSBOM          = "      - run: syft dir:dist -o spdx-json=sbom.spdx.json\n"
	generatorCall     = "  provenance:\n    uses: slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@v2.1.0\n"
)

// Positive: each declaration the workflows meet passes, and the pass line states that the
// level was measured from workflow files and that no published attestation was checked.
func TestAuditSupplyChainPassesWhatTheWorkflowsMeasure(t *testing.T) {
	cases := map[string]struct {
		files  map[string]string
		policy *config.ResolvedPolicy
		want   string
	}{
		"Level 2 with a direct attestation": {
			map[string]string{".github/workflows/release.yml": releaseSteps(directAttestation)},
			supplyChainPolicy(2, false, false), "SLSA Build Level 2 declared, Level 2 measured from release.yml"},
		"Level 3 through the SLSA generator": {
			map[string]string{".github/workflows/release.yml": releaseSteps(cosignSignBlob+syftSBOM) + generatorCall},
			supplyChainPolicy(3, true, true),
			"SLSA Build Level 3 declared, Level 3 measured from release.yml; cosign signing in release.yml; SBOM generated in release.yml"},
		"Level 3 through a reusable workflow of the repository": {
			map[string]string{
				".github/workflows/release.yml": "on:\n  push:\n    tags: ['v*']\njobs:\n  build:\n    uses: ./.github/workflows/build.yml\n",
				".github/workflows/build.yml":   "on: workflow_call\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n" + directAttestation,
			},
			supplyChainPolicy(3, false, false), "Level 3 measured from release.yml"},
		"a measured level above the declared one": {
			map[string]string{".github/workflows/release.yml": releaseSteps(directAttestation)},
			supplyChainPolicy(1, false, false), "SLSA Build Level 1 declared, Level 2 measured"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			line, err := AuditSupplyChain(context.Background(), supplyChainRepo(t, tc.files), tc.policy)
			if err != nil {
				t.Fatalf("AuditSupplyChain: %v", err)
			}
			if !strings.HasPrefix(line, "[PASS] Supply chain (HISS-11): ") || !strings.Contains(line, tc.want) ||
				!strings.Contains(line, "published attestations and signatures were not checked") {
				t.Fatalf("pass line = %q; want %q and the scope of the measurement", line, tc.want)
			}
		})
	}
}

// Negative: a declaration above the measurement fails, naming the declared level, the
// measured one and the workflow read (#330).
func TestAuditSupplyChainFailsDeclarationsAboveTheMeasurement(t *testing.T) {
	cases := map[string]struct {
		files  map[string]string
		policy *config.ResolvedPolicy
		want   []string
	}{
		"Level 3 declared over a direct attestation": {
			map[string]string{".github/workflows/release.yml": releaseSteps(directAttestation)},
			supplyChainPolicy(3, false, false),
			[]string{"declares SLSA Build Level 3 but the workflows reach Level 2 (.github/workflows/release.yml)", "isolated reusable workflow"}},
		"Level 3 declared over another repository's reusable workflow": {
			map[string]string{".github/workflows/release.yml": "on: push\njobs:\n  build:\n    uses: acme/shared/.github/workflows/build.yml@v1\n"},
			supplyChainPolicy(3, false, false),
			[]string{"reach Level 0", "Not credited: release.yml: acme/shared/.github/workflows/build.yml@v1: a reusable workflow in another repository is not read"}},
		"enforce_cosign without a cosign signing step": {
			map[string]string{".github/workflows/release.yml": releaseSteps(directAttestation + "      - uses: sigstore/cosign-installer@v4.1.2\n")},
			supplyChainPolicy(2, true, false),
			[]string{"policy sets enforce_cosign but no workflow signs with cosign"}},
		"require_sbom without an SBOM step": {
			map[string]string{".github/workflows/release.yml": releaseSteps(directAttestation)},
			supplyChainPolicy(2, false, true),
			[]string{"policy sets require_sbom but no workflow generates an SBOM"}},
		"every shortfall at once": {
			map[string]string{".github/workflows/release.yml": releaseSteps("      - run: make dist\n")},
			supplyChainPolicy(2, true, true),
			[]string{"declares SLSA Build Level 2 but the workflows reach Level 0. Level 2 needs", "enforce_cosign", "require_sbom"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := AuditSupplyChain(context.Background(), supplyChainRepo(t, tc.files), tc.policy)
			if err == nil {
				t.Fatal("AuditSupplyChain passed; want a failure")
			}
			for i := 0; i < len(tc.want); i++ {
				if !strings.HasPrefix(err.Error(), "[FAIL] Supply chain (HISS-11): ") || !strings.Contains(err.Error(), tc.want[i]) {
					t.Fatalf("failure = %q; want it to contain %q", err, tc.want[i])
				}
			}
		})
	}
}

// Boundary: without a release workflow, Level 0 passes and Level 1 or more fails; an empty or
// malformed workflow fails closed; a policy that declares nothing reads no workflow at all.
func TestAuditSupplyChainBoundaries(t *testing.T) {
	bare := map[string]string{"README.md": "x\n"}
	if line, err := AuditSupplyChain(context.Background(), supplyChainRepo(t, bare), supplyChainPolicy(0, false, false)); err != nil ||
		!strings.Contains(line, "declares SLSA Build Level 0, no cosign signing and no SBOM") {
		t.Fatalf("Level 0 without a workflow = %q, %v; want a pass", line, err)
	}
	if _, err := AuditSupplyChain(context.Background(), supplyChainRepo(t, bare), supplyChainPolicy(1, false, false)); err == nil ||
		!strings.Contains(err.Error(), "declares SLSA Build Level 1 but the workflows reach Level 0. Level 1 needs") {
		t.Fatalf("Level 1 without a workflow = %v; want a failure", err)
	}
	malformed := map[string]string{
		"empty":     "",
		"malformed": "jobs: [unclosed\n",
	}
	for name, content := range malformed {
		t.Run(name, func(t *testing.T) {
			root := supplyChainRepo(t, map[string]string{".github/workflows/release.yml": content})
			_, err := AuditSupplyChain(context.Background(), root, supplyChainPolicy(1, false, false))
			if err == nil || !strings.Contains(err.Error(), "cannot measure the workflows: workflow release.yml") {
				t.Fatalf("AuditSupplyChain(%s workflow) = %v; want a closed failure naming the workflow", name, err)
			}
			if _, err := AuditSupplyChain(context.Background(), root, supplyChainPolicy(0, false, false)); err != nil {
				t.Fatalf("a policy declaring nothing read the %s workflow: %v", name, err)
			}
		})
	}
	//nolint:staticcheck // SA1012: a nil context is the refused input under test.
	if _, err := AuditSupplyChain(nil, t.TempDir(), supplyChainPolicy(0, false, false)); err == nil {
		t.Fatal("AuditSupplyChain(nil context) passed; want a failure")
	}
	if _, err := AuditSupplyChain(context.Background(), t.TempDir(), nil); err == nil {
		t.Fatal("AuditSupplyChain(nil policy) passed; want a failure")
	}
}

// HISS-20: the HISS-11 workflow fixtures .config/hiss/coverage.yaml attributes to this gate
// replay in both directions under a policy declaring Level 3 and cosign signing. Positive
// fixtures fail it, negative ones pass, and gap fixtures pass although they release nothing
// attested: the gap the catalog records.
func TestAuditSupplyChainReplaysTheHISS11Fixtures(t *testing.T) {
	buckets := map[string]bool{"positive": true, "negative": false, "gap": false}
	for bucket, wantFail := range buckets {
		entries, err := os.ReadDir(filepath.Join(hiss11Fixtures, bucket))
		if err != nil || len(entries) == 0 {
			t.Fatalf("read %s fixtures: %d entries, %v", bucket, len(entries), err)
		}
		for i := 0; i < len(entries); i++ {
			data, err := os.ReadFile(filepath.Join(hiss11Fixtures, bucket, entries[i].Name()))
			if err != nil {
				t.Fatal(err)
			}
			root := supplyChainRepo(t, map[string]string{".github/workflows/release.yml": string(data)})
			_, err = AuditSupplyChain(context.Background(), root, supplyChainPolicy(3, true, false))
			if (err != nil) != wantFail {
				t.Errorf("%s/%s: AuditSupplyChain error = %v; want failure %t", bucket, entries[i].Name(), err, wantFail)
			}
		}
	}
}
