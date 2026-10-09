package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// initPraetorSource is the Praetor checkout the tests pin a fresh repository to: this one.
func initPraetorSource(t *testing.T) string {
	t.Helper()
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return source
}

// generateDevContainer runs devcontainer generate for the repository whose manifest is manifest.
func generateDevContainer(t *testing.T, dir, manifest, source string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error {
		return dispatchCommand("devcontainer", []string{"generate", "--config=" + manifest, "--source-root=" + source,
			"--output=" + filepath.Join(dir, ".devcontainer", "devcontainer.json")})
	})
}

// Positive (#955): init with --lock-source-root writes the pinned lock adoption writes, so a
// freshly initialised repository passes devcontainer generate and audit --offline.
func TestInitLock_Positive_PinnedLockPassesGenerateAndAudit(t *testing.T) {
	source := initPraetorSource(t)
	dir, manifest := initContextRepo(t, fixtureAgentsMD, "")
	out, err := runInitCmd(t, "--output="+manifest, "--lock-source-root="+source, "--profile=template-seed", "--facets=agent:sandboxed")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if lock := readFixtureFile(t, dir, ".standards.lock"); !strings.Contains(lock, "digest: sha256:") {
		t.Fatalf("lock carries no digest:\n%s", lock)
	}
	if generated, err := generateDevContainer(t, dir, manifest, source); err != nil {
		t.Fatalf("devcontainer generate on a fresh repository: %v\n%s", err, generated)
	}
	// init scaffolds no branch ruleset; the audit names `praetorctl sync` for it, which is local
	// without --remote.
	if synced, err := runSyncCmd(t, "--config="+manifest); err != nil {
		t.Fatalf("sync on a fresh repository: %v\n%s", err, synced)
	}
	// The audit stops at its first failing gate. Init records no HISS-11 supply-chain exception
	// (adoption does), so that gate fails and the gates after it (Paperclip harness, agent
	// definitions, git hooks) never run: the test claims only the gates before it.
	audited, err := captureStdout(t, func() error { return dispatchCommand("audit", []string{"--offline", "--config=" + manifest}) })
	report := audited
	if err != nil {
		report += err.Error()
	}
	mustContain(t, report, "[PASS] SemVer lockfile", "[PASS] Lockfile digests verified", "[PASS] DevContainer configuration verified",
		"Supply chain (HISS-11)")
	if err == nil {
		t.Fatalf("an init-only repository must not pass the whole audit (docs/guides/onboarding.md states the gaps):\n%s", report)
	}
}

// Negative: a pin that cannot be built fails before init writes any file, so the same command
// can be rerun once the source or profile is fixed.
func TestInitLock_Negative_PinFailureWritesNothing(t *testing.T) {
	source := initPraetorSource(t)
	for name, args := range map[string][]string{
		"empty source":    {"--lock-source-root=" + t.TempDir()},
		"unknown profile": {"--lock-source-root=" + source, "--profile=no-such-profile"},
	} {
		dir, manifest := initContextRepo(t, fixtureAgentsMD, "")
		if _, err := runInitCmd(t, append([]string{"--output=" + manifest}, args...)...); err == nil {
			t.Fatalf("%s: init succeeded", name)
		}
		for _, rel := range []string{".standards.yaml", ".standards-baseline.json", ".standards.lock"} {
			if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
				t.Fatalf("%s: a failed pin left %s behind", name, rel)
			}
		}
	}
}

// Negative: the catalog init materializes is validated as adoption validates it; a local
// archetype that repeats a pinned ID fails init instead of leaving a catalog the audit rejects.
func TestInitLock_Negative_DuplicateArchetypeIDRefused(t *testing.T) {
	source := initPraetorSource(t)
	dir, manifest := initContextRepo(t, fixtureAgentsMD, "")
	writeFixtureFile(t, dir, ".config/archetypes/local-framework.yaml", "id: framework\n")
	out, err := runInitCmd(t, "--output="+manifest, "--lock-source-root="+source, "--profile=framework")
	mustErrContain(t, err, "duplicate archetype ID")
	if strings.Contains(out, "(pinned catalog)") {
		t.Fatalf("init reported the catalog written:\n%s", out)
	}
}

// Negative: without a lock source init cannot compute digests; it writes the placeholder and
// says so, and devcontainer generate refuses the placeholder. A source that is not a directory
// fails before any file is written.
func TestInitLock_Negative_PlaceholderIsNamedAndRefused(t *testing.T) {
	source := initPraetorSource(t)
	dir, manifest := initContextRepo(t, fixtureAgentsMD, "")
	out, err := runInitCmd(t, "--output="+manifest)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	mustContain(t, out, "carries no content digests", "--lock-source-root")
	if lock := readFixtureFile(t, dir, ".standards.lock"); strings.Contains(lock, "digest:") {
		t.Fatalf("placeholder lock carries a digest:\n%s", lock)
	}
	_, err = generateDevContainer(t, dir, manifest, source)
	mustErrContain(t, err, "digest")

	other, otherManifest := initContextRepo(t, fixtureAgentsMD, "")
	_, err = runInitCmd(t, "--output="+otherManifest, "--lock-source-root="+filepath.Join(other, "AGENTS.md"))
	mustErrContain(t, err, "not a directory")
	if err := ensureManifestAbsent(otherManifest); err != nil {
		t.Fatalf("a refused lock source still wrote the manifest: %v", err)
	}
}

// Boundary (#955): the closing line names the repository the manifest records and no upstream
// name; without an owner and name it names nothing.
func TestInitMessage_Boundary_NamesOnlyTheConfiguredRepository(t *testing.T) {
	_, manifest := initContextRepo(t, fixtureAgentsMD, "")
	out, err := runInitCmd(t, "--output="+manifest)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	mustContain(t, out, "Repository successfully onboarded: acme/kit")
	if strings.Contains(out, "cordanaLLM") {
		t.Fatalf("init printed a hard-coded upstream name:\n%s", out)
	}

	bare := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, bare, "")
	writeFixtureFile(t, bare, "AGENTS.md", fixtureAgentsMD)
	out, err = runInitCmd(t, "--output="+filepath.Join(bare, ".standards.yaml"))
	if err != nil {
		t.Fatalf("init without a remote: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Repository successfully onboarded.\n") || strings.Contains(out, "cordanaLLM") {
		t.Fatalf("an unresolved identity must print the bare line:\n%s", out)
	}
}
