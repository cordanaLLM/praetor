package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// An adopter repository checked out on Windows gets every text file with CRLF: under
// "* text=auto" git writes core.eol, whose default there is CRLF, even with core.autocrlf=false.
// The archetype digests, compile-context --verify and the effective-policy digest then failed
// or moved on an unmodified repository (#781). These tests check a committed adopter fixture
// out again with core.eol set in the fixture repository, so real git does the conversion on
// every platform; they skip where git is not installed (testsupport.InitGitRepoWithOrigin).

const checkoutManifest = "version: 1\nrepository:\n  owner: example\n  name: demo\nprofiles: [framework]\nfacets: [security:high]\n"

// checkoutFixture commits an adopter fixture (pinned archetypes, a manifest, AGENTS.md, one
// persona and the compiled context) and writes its working tree again under core.eol=eol.
func checkoutFixture(t *testing.T, eol string) *lockFixture {
	t.Helper()
	f := newLockFixture(t)
	testsupport.InitGitRepoWithOrigin(t, f.dir, "")
	writeFixtureFile(t, f.dir, ".gitattributes", "* text=auto\n")
	writeFixtureFile(t, f.dir, ".standards.yaml", checkoutManifest)
	writeFixtureFile(t, f.dir, "AGENTS.md", fixtureAgentsMD)
	writeFixtureFile(t, f.dir, ".agents/agents/praetor-gatekeeper.md", fixturePersona)
	if out, err := runCompileContextCmd(t, f.dir); err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	f.writeLock(t, f.digestOf(t, ".config/archetypes/framework.yaml"), f.digestOf(t, ".config/archetypes/facets/security-high.yaml"), "")
	testsupport.RunFixtureGit(t, f.dir, []string{"add", "-A"}, []string{"commit", "--quiet", "-m", "fixture"},
		[]string{"config", "core.autocrlf", "false"}, []string{"config", "core.eol", eol})
	entries, err := os.ReadDir(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != ".git" {
			if err := os.RemoveAll(filepath.Join(f.dir, entry.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	testsupport.RunFixtureGit(t, f.dir, []string{"checkout", "--", "."})
	if converted := strings.Contains(readFixtureFile(t, f.dir, "AGENTS.md"), "\r\n"); converted != (eol == "crlf") {
		t.Fatalf("fixture: core.eol=%s wrote AGENTS.md with CRLF=%v", eol, converted)
	}
	return f
}

// checkoutPolicy resolves the fixture's effective policy, as the audit's policy gate does.
func checkoutPolicy(t *testing.T, f *lockFixture) (*config.EffectivePolicy, error) {
	t.Helper()
	return config.LoadEffectivePolicyContext(t.Context(), config.EffectiveOptions{Root: f.dir})
}

// Positive: a CRLF checkout of an unmodified adopter passes the archetype digests and
// compile-context --verify, and reports the effective-policy digest and source lines of its LF
// clone. Boundary: the LF clone passes as it did before.
func TestCheckoutLineEndings_Positive_CRLFCheckoutAuditsAsLFClone(t *testing.T) {
	evidence := map[string]string{}
	for _, eol := range []string{"lf", "crlf"} {
		f := checkoutFixture(t, eol)
		if out, err := captureStdout(t, func() error { return auditLockDigests(f.manifest, f.dir) }); err != nil {
			t.Fatalf("core.eol=%s: archetype digests: %v\n%s", eol, err, out)
		}
		if out, err := runCompileContextCmd(t, f.dir, "--verify"); err != nil {
			t.Fatalf("core.eol=%s: compile-context --verify: %v\n%s", eol, err, out)
		}
		policy, err := checkoutPolicy(t, f)
		if err != nil {
			t.Fatalf("core.eol=%s: effective policy: %v", eol, err)
		}
		evidence[eol] = policy.Evidence()
	}
	if evidence["crlf"] != evidence["lf"] {
		t.Fatalf("a CRLF checkout reports another policy:\n%s\nLF clone:\n%s", evidence["crlf"], evidence["lf"])
	}
}

// Negative: a real content change in the CRLF checkout still fails the archetype digest, the
// effective policy and compile-context --verify.
func TestCheckoutLineEndings_Negative_CRLFContentEditStillFails(t *testing.T) {
	f := checkoutFixture(t, "crlf")
	writeFixtureFile(t, f.dir, ".config/archetypes/framework.yaml", "id: \"framework\"\r\nname: \"Edited\"\r\n")
	if _, err := captureStdout(t, func() error { return auditLockDigests(f.manifest, f.dir) }); !errors.Is(err, config.ErrLockDigestMismatch) {
		t.Fatalf("an edited CRLF archetype: %v", err)
	}
	if _, err := checkoutPolicy(t, f); !errors.Is(err, config.ErrLockDigestMismatch) {
		t.Fatalf("effective policy of an edited CRLF archetype: %v", err)
	}
	writeFixtureFile(t, f.dir, "CLAUDE.md", strings.Replace(readFixtureFile(t, f.dir, "CLAUDE.md"), "Keep functions small.", "Keep functions large.", 1))
	_, err := runCompileContextCmd(t, f.dir, "--verify")
	mustErrContain(t, err, "target CLAUDE.md is out of sync")
}

// Boundary: an archetype and a vendor file with mixed line endings are compared byte for byte,
// so they fail on a checkout that is otherwise CRLF, and each report says why.
func TestCheckoutLineEndings_Boundary_MixedEndingsStayByteExact(t *testing.T) {
	f := checkoutFixture(t, "crlf")
	writeFixtureFile(t, f.dir, ".config/archetypes/framework.yaml", "id: \"framework\"\r\nname: \"Framework\"\n")
	_, err := captureStdout(t, func() error { return auditLockDigests(f.manifest, f.dir) })
	mustErrContain(t, err, "compared byte for byte")
	writeFixtureFile(t, f.dir, "CLAUDE.md", strings.Replace(readFixtureFile(t, f.dir, "CLAUDE.md"), "\r\n", "\n", 1))
	_, err = runCompileContextCmd(t, f.dir, "--verify")
	mustErrContain(t, err, "target CLAUDE.md is out of sync with")
	mustErrContain(t, err, "compared byte for byte")
}
