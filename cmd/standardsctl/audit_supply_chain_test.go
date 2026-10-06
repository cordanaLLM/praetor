package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
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

// adoptedGoRepository adopts a fresh Go repository with an origin remote, under the default
// profile and facets unless flags choose others, stages what adoption wrote so the audit reads it as tracked, and returns its root and
// the adoption output.
func adoptedGoRepository(t *testing.T, flags ...string) (string, string) {
	t.Helper()
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeFixtureFile(t, root, "go.mod", "module example.com/widgets\n\ngo 1.24\n")
	writeFixtureFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	env := testsupport.HermeticGitEnv(t)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"remote", "add", "origin", "https://github.com/acme/widgets.git"},
		{"add", "-A"}, {"commit", "-q", "-m", "fixture"},
	} {
		if out, err := runFixtureGit(t, root, env, args...); err != nil {
			t.Skipf("git %v failed in sandbox: %v (%s)", args, err, out)
		}
	}
	code, out := praetorctl(t, append([]string{"adopt", "--path", root, "--lock-source-root", source}, flags...)...)
	if code != 0 {
		t.Fatalf("adopt: exit %d\n%s", code, out)
	}
	if staged, err := runFixtureGit(t, root, env, "add", "-A"); err != nil {
		t.Fatalf("git add: %v\n%s", err, staged)
	}
	return root, out
}

// End to end (#330): the default facet security:high declares SLSA Build Level 3, cosign signing
// and an SBOM, and adoption scaffolds no release workflow. Positive: adoption records that gap as
// a HISS-11 exceptions entry expiring config.MaxExceptionDays ahead and says so, and
// audit --offline passes, printing the declared and measured level with the entry's reason and
// expiry. Negative: the same entry expired fails the audit like a missing one, and without the
// entry the audit fails and names the entry it would accept. Boundary: a profile declaring Level
// 0 under a facet that raises no supply-chain control has no gap, so adoption records no entry and the audit still passes.
func TestAdoptThenAuditDeclaresTheSupplyChainGap(t *testing.T) {
	root, adopted := adoptedGoRepository(t)
	expires := config.ExceptionDay(time.Now()).AddDate(0, 0, config.MaxExceptionDays).Format(config.ExceptionDateLayout)
	mustContain(t, adopted, "exceptions: recorded rule HISS-11 for .github/workflows/release.yml until "+expires)
	manifestPath := filepath.Join(root, ".standards.yaml")
	audit := func() (string, error) {
		return captureStdout(t, func() error { return dispatchCommand("audit", []string{"--config=" + manifestPath, "--offline"}) })
	}
	out, err := audit()
	if err != nil {
		t.Fatalf("audit --offline of a fresh adoption failed: %v\n%s", err, out)
	}
	mustContain(t, out, "[PASS] Supply chain (HISS-11): declared gap, excepted until "+expires+
		" by the exceptions entry (rule HISS-11, .github/workflows/release.yml)",
		"SLSA Build Level 3 declared, Level 0 measured", "policy declares SLSA Build Level 3 but the workflows reach Level 0")

	manifest := readFixtureFile(t, root, ".standards.yaml")
	writeFixtureFile(t, root, ".standards.yaml", strings.Replace(manifest, `expires: "`+expires+`"`, `expires: "2020-01-01"`, 1))
	out, err = audit()
	if err == nil {
		t.Fatalf("audit passed with an expired HISS-11 exception:\n%s", out)
	}
	mustErrContain(t, err, "[FAIL] Supply chain (HISS-11): the exceptions entry (rule HISS-11, .github/workflows/release.yml) expired on 2020-01-01")

	cut := strings.Index(manifest, "exceptions:")
	if cut < 0 {
		t.Fatalf("adoption wrote no exceptions list:\n%s", manifest)
	}
	writeFixtureFile(t, root, ".standards.yaml", manifest[:cut])
	out, err = audit()
	if err == nil {
		t.Fatalf("audit passed without the HISS-11 exception:\n%s", out)
	}
	mustErrContain(t, err, "no exceptions entry declares the gap above")
	mustErrContain(t, err, "rule HISS-11, path .github/workflows/release.yml")

	root, adopted = adoptedGoRepository(t, "--profile", "org-health", "--facets", "docs:seo-portal")
	if strings.Contains(adopted, "exceptions: recorded") || strings.Contains(readFixtureFile(t, root, ".standards.yaml"), "exceptions:") {
		t.Fatalf("adoption recorded an exception for a policy declaring no supply-chain control:\n%s", adopted)
	}
	manifestPath = filepath.Join(root, ".standards.yaml")
	if out, err := audit(); err != nil || !strings.Contains(out, "policy declares SLSA Build Level 0, no cosign signing and no SBOM") {
		t.Fatalf("audit --offline of an org-health adoption: %v\n%s", err, out)
	}
}
