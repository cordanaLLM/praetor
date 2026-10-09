package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// initSyntheticSource builds a minimal synthetic Praetor source checkout with only the files
// needed for devcontainer bootstrap and archetype pinning, avoiding timeout flakiness from
// capturing the entire repository tree.
func initSyntheticSource(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                   "module github.com/cordanaLLM/praetor\n\ngo 1.27\n",
		"go.sum":                   "",
		"LICENSE":                  "Synthetic test license\n",
		"cmd/standardsctl/main.go": "package main\nfunc main() {}\n",
	}
	for name, content := range files {
		writeFixtureFile(t, root, name, content)
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".standards.yaml", ".standards.lock"} {
		content, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, root, rel, string(content))
	}
	copySyntheticArchetypes(t, repoRoot, root)
	if _, err := util.RunCommandBytes(t.Context(), root, "git", 4096, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	return root
}

func copySyntheticArchetypes(t *testing.T, repoRoot, targetRoot string) {
	t.Helper()
	archetypesDir := filepath.Join(repoRoot, ".config", "archetypes")
	err := filepath.Walk(archetypesDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return walkErr
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		writeFixtureFile(t, targetRoot, rel, string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
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
	source := initSyntheticSource(t)
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
	// The audit stops at its first failing gate. A fresh init passes manifest, lockfile, digests,
	// DevContainer, agent context projection and caveman register gates. It records no HISS-11
	// supply-chain exception (adoption does), so the supply chain gate fails: the test asserts
	// the exact list of passing gates and the remaining gap.
	audited, err := captureStdout(t, func() error { return dispatchCommand("audit", []string{"--offline", "--config=" + manifest}) })
	report := audited
	if err != nil {
		report += err.Error()
	}
	mustContain(t, report,
		"[PASS] Manifest verified",
		"[PASS] Repository identity verified",
		"[PASS] SemVer lockfile",
		"[PASS] Lockfile digests verified",
		"[PASS] HISS invariant scan verified",
		"[PASS] Cross-agent context targets verified in sync",
		"[PASS] Agent context caveman lint passed",
		"[PASS] DevContainer configuration verified",
		"[PASS] Branch protection & merge ruleset",
		"[PASS] Repository label taxonomy",
		"Supply chain (HISS-11)",
	)
	if err == nil {
		t.Fatalf("an init-only repository must not pass the whole audit (docs/guides/onboarding.md states the gaps):\n%s", report)
	}
}

// Negative: a pin that cannot be built fails before init writes any file, so the same command
// can be rerun once the source or profile is fixed.
func TestInitLock_Negative_PinFailureWritesNothing(t *testing.T) {
	source := initSyntheticSource(t)
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
// archetype that repeats a pinned ID fails init before writing any files.
func TestInitLock_Negative_DuplicateArchetypeIDRefused(t *testing.T) {
	source := initSyntheticSource(t)
	dir, manifest := initContextRepo(t, fixtureAgentsMD, "")
	writeFixtureFile(t, dir, ".config/archetypes/local-framework.yaml", "id: framework\n")
	out, err := runInitCmd(t, "--output="+manifest, "--lock-source-root="+source, "--profile=framework")
	mustErrContain(t, err, "duplicate archetype ID")
	if strings.Contains(out, "(pinned catalog)") {
		t.Fatalf("init reported the catalog written:\n%s", out)
	}
	for _, rel := range []string{".standards.yaml", ".standards-baseline.json", ".standards.lock"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Fatalf("a duplicate archetype ID left %s behind", rel)
		}
	}
}

// Negative: without a lock source init cannot compute digests; it writes the placeholder and
// says so, and devcontainer generate refuses the placeholder. A source that is not a directory
// fails before any file is written.
func TestInitLock_Negative_PlaceholderIsNamedAndRefused(t *testing.T) {
	source := initSyntheticSource(t)
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
	for _, rel := range []string{".standards.yaml", ".standards-baseline.json", ".standards.lock"} {
		if _, err := os.Stat(filepath.Join(other, rel)); err == nil {
			t.Fatalf("a refused lock source left %s behind", rel)
		}
	}
}

// Boundary: when .standards.lock already exists, init with --lock-source-root preserves the
// existing lockfile and prints a warning naming the skipped pin.
func TestInitLock_Boundary_ExistingLockfileWarnsSkippedPin(t *testing.T) {
	source := initSyntheticSource(t)
	dir, manifest := initContextRepo(t, fixtureAgentsMD, "")
	existingLock := "# Existing custom lockfile\nversion: 1\n"
	writeFixtureFile(t, dir, ".standards.lock", existingLock)
	out, err := runInitCmd(t, "--output="+manifest, "--lock-source-root="+source, "--profile=template-seed")
	if err != nil {
		t.Fatalf("init with existing lockfile: %v\n%s", err, out)
	}
	mustContain(t, out, "[WARN]", ".standards.lock already exists; keeping existing lock and skipping pins from")
	if got := readFixtureFile(t, dir, ".standards.lock"); got != existingLock {
		t.Fatalf("existing lockfile was overwritten:\n%s", got)
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
