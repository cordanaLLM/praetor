package adopt

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// auditSupplyChainAt runs the gate over root under policy with no exceptions entry, today.
func auditSupplyChainAt(ctx context.Context, root string, policy *config.ResolvedPolicy) (string, error) {
	return AuditSupplyChain(ctx, SupplyChainOptions{Root: root, Policy: policy, Today: time.Now()})
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
			line, err := auditSupplyChainAt(context.Background(), supplyChainRepo(t, tc.files), tc.policy)
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
			_, err := auditSupplyChainAt(context.Background(), supplyChainRepo(t, tc.files), tc.policy)
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
	if line, err := auditSupplyChainAt(context.Background(), supplyChainRepo(t, bare), supplyChainPolicy(0, false, false)); err != nil ||
		!strings.Contains(line, "declares SLSA Build Level 0, no cosign signing and no SBOM") {
		t.Fatalf("Level 0 without a workflow = %q, %v; want a pass", line, err)
	}
	if _, err := auditSupplyChainAt(context.Background(), supplyChainRepo(t, bare), supplyChainPolicy(1, false, false)); err == nil ||
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
			_, err := auditSupplyChainAt(context.Background(), root, supplyChainPolicy(1, false, false))
			if err == nil || !strings.Contains(err.Error(), "cannot measure the workflows: workflow release.yml") {
				t.Fatalf("AuditSupplyChain(%s workflow) = %v; want a closed failure naming the workflow", name, err)
			}
			if _, err := auditSupplyChainAt(context.Background(), root, supplyChainPolicy(0, false, false)); err != nil {
				t.Fatalf("a policy declaring nothing read the %s workflow: %v", name, err)
			}
		})
	}
	//nolint:staticcheck // SA1012: a nil context is the refused input under test.
	if _, err := auditSupplyChainAt(nil, t.TempDir(), supplyChainPolicy(0, false, false)); err == nil {
		t.Fatal("AuditSupplyChain(nil context) passed; want a failure")
	}
	if _, err := auditSupplyChainAt(context.Background(), t.TempDir(), nil); err == nil {
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
			_, err = auditSupplyChainAt(context.Background(), root, supplyChainPolicy(3, true, false))
			if (err != nil) != wantFail {
				t.Errorf("%s/%s: AuditSupplyChain error = %v; want failure %t", bucket, entries[i].Name(), err, wantFail)
			}
		}
	}
}

// supplyChainToday is the day the exception cases judge expiry against.
var supplyChainToday = time.Date(2026, time.October, 6, 12, 0, 0, 0, time.UTC)

// supplyChainEntry is a HISS-11 exceptions entry for the workflow path, expiring on expires.
func supplyChainEntry(path, expires string) config.Exception {
	return config.Exception{Rule: config.ExceptionRuleSupplyChain, Path: path,
		Reason: "release provenance not yet built by an isolated Level 3 builder", Expires: expires}
}

// auditWithEntries runs the gate over files under policy with the exceptions list entries.
func auditWithEntries(t *testing.T, files map[string]string, policy *config.ResolvedPolicy, entries ...config.Exception) (string, error) {
	t.Helper()
	return AuditSupplyChain(context.Background(), SupplyChainOptions{
		Root: supplyChainRepo(t, files), Policy: policy, Exceptions: entries, Today: supplyChainToday,
	})
}

// A HISS-11 entry of the exceptions list declares the gap the measurement finds (#330).
// Positive: a live entry naming the workflow the measurement read passes, printing the entry's
// expiry and reason with the declared and measured level; with no workflow writing provenance,
// an entry for the release workflow still to be added declares the gap, cosign and SBOM
// shortfalls included. Boundary: the entry still holds on its expires day.
func TestAuditSupplyChainPassesADeclaredGap(t *testing.T) {
	release := map[string]string{".github/workflows/release.yml": releaseSteps(directAttestation)}
	cases := map[string]struct {
		files  map[string]string
		policy *config.ResolvedPolicy
		entry  config.Exception
		want   []string
	}{
		"Level 2 measured, Level 3 declared": {release, supplyChainPolicy(3, false, false),
			supplyChainEntry(".github/workflows/release.yml", "2026-12-31"),
			[]string{"declared gap, excepted until 2026-12-31 by the exceptions entry (rule HISS-11, .github/workflows/release.yml): " +
				"release provenance not yet built", "policy declares SLSA Build Level 3 but the workflows reach Level 2 (.github/workflows/release.yml)"}},
		"no provenance workflow yet": {map[string]string{"README.md": "x\n"}, supplyChainPolicy(3, true, true),
			supplyChainEntry(".github/workflows/release.yml", "2026-12-31"),
			[]string{"reach Level 0", "enforce_cosign", "require_sbom"}},
		"an entry on its expires day": {release, supplyChainPolicy(3, false, false),
			supplyChainEntry(".github/workflows/release.yml", "2026-10-06"), []string{"excepted until 2026-10-06"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := auditWithEntries(t, tc.files, tc.policy, tc.entry)
			if err != nil {
				t.Fatalf("AuditSupplyChain: %v", err)
			}
			if !strings.HasPrefix(out, "[PASS] Supply chain (HISS-11): declared gap") || !strings.HasSuffix(out, supplyChainScope) {
				t.Fatalf("output = %q; want the declared-gap pass line and the measurement's scope", out)
			}
			for i := 0; i < len(tc.want); i++ {
				if !strings.Contains(out, tc.want[i]) {
					t.Fatalf("output = %q; want it to contain %q", out, tc.want[i])
				}
			}
		})
	}
}

// Negative: an expired entry fails like a missing one, naming its expiry; an entry naming
// another workflow than the one the measurement read, or one with no gap to excuse, is stale and
// fails; a malformed entry fails the gate before anything is measured.
func TestAuditSupplyChainRefusesExpiredStaleAndMalformedEntries(t *testing.T) {
	release := map[string]string{".github/workflows/release.yml": releaseSteps(directAttestation)}
	cases := map[string]struct {
		policy *config.ResolvedPolicy
		entry  config.Exception
		want   []string
	}{
		"an expired entry": {supplyChainPolicy(3, false, false), supplyChainEntry(".github/workflows/release.yml", "2026-10-05"),
			[]string{"reach Level 2", "the exceptions entry (rule HISS-11, .github/workflows/release.yml) expired on 2026-10-05"}},
		"an entry for another workflow": {supplyChainPolicy(3, false, false), supplyChainEntry(".github/workflows/publish.yml", "2026-12-31"),
			[]string{"for .github/workflows/publish.yml excuse no gap, because the measurement read .github/workflows/release.yml",
				"no exceptions entry declares the gap above"}},
		"an entry with no gap to excuse": {supplyChainPolicy(2, false, false), supplyChainEntry(".github/workflows/release.yml", "2026-12-31"),
			[]string{"excuse no gap, because the workflows meet every declaration; remove them"}},
		"an entry under a policy declaring nothing": {supplyChainPolicy(0, false, false), supplyChainEntry(".github/workflows/release.yml", "2026-12-31"),
			[]string{"because the policy declares no supply-chain control"}},
		"a malformed entry": {supplyChainPolicy(3, false, false), supplyChainEntry("release.yml", "2026-12-31"),
			[]string{"rule HISS-11 must name one workflow file directly in .github/workflows"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := auditWithEntries(t, release, tc.policy, tc.entry)
			if err == nil {
				t.Fatalf("AuditSupplyChain passed:\n%s", out)
			}
			for i := 0; i < len(tc.want); i++ {
				if !strings.Contains(err.Error(), tc.want[i]) {
					t.Fatalf("failure = %q; want it to contain %q", err, tc.want[i])
				}
			}
		})
	}
}
