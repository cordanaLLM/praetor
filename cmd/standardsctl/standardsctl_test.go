package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDispatchCommand_HelpAndVersion(t *testing.T) {
	// Boundary: Help flags
	if err := dispatchCommand("help", []string{}); err != nil {
		t.Fatalf("help failed: %v", err)
	}
	if err := dispatchCommand("--help", []string{}); err != nil {
		t.Fatalf("--help failed: %v", err)
	}
	if err := dispatchCommand("-h", []string{}); err != nil {
		t.Fatalf("-h failed: %v", err)
	}

	// Positive: Version
	out, err := captureStdout(t, func() error { return dispatchCommand("version", []string{}) })
	if err != nil {
		t.Fatalf("version failed: %v", err)
	}
	mustContain(t, out, version)

	// Negative: Unknown command
	if err := dispatchCommand("unknown-cmd", []string{}); err == nil {
		t.Fatal("expected error for unknown command")
	}
}

// TestCommandTable_CoversUsage keeps the dispatch table and the usage text in step: every
// command listed by printUsage must dispatch, and every alias must resolve.
func TestCommandTable_CoversUsage(t *testing.T) {
	usage, err := captureStdout(t, func() error { printUsage(); return nil })
	if err != nil {
		t.Fatal(err)
	}
	table := commandTable()
	listed := 0
	for _, line := range strings.Split(usage, "\n") {
		if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "  praetorctl") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		listed++
		if _, ok := table[fields[0]]; !ok {
			t.Errorf("usage lists %q but the command table does not dispatch it", fields[0])
		}
	}
	if listed < 30 {
		t.Fatalf("expected the usage text to list the commands, parsed only %d", listed)
	}
	for _, alias := range []string{"conform", "bootstrap", "help", "-h", "--help"} {
		if _, ok := table[alias]; !ok {
			t.Errorf("alias %q missing from the command table", alias)
		}
	}
}

// newNeedsRepo builds a leaf git repository with a go.mod so the needs commands accept it.
func newNeedsRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixtureFile(t, dir, ".git/HEAD", "ref: refs/heads/main\n")
	writeFixtureFile(t, dir, "go.mod", "module example.com/fixture\n\ngo 1.27\n\nrequire github.com/spf13/cobra v1.8.0\n")
	writeFixtureFile(t, dir, "main.go", "package main\n\nimport \"github.com/spf13/cobra\"\n\nfunc main() { _ = cobra.Command{} }\n")
	return dir
}

// newFrameworkFixture builds a framework checkout with two domain directories.
func newFrameworkFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, domain := range []string{"config", "clikit"} {
		if err := os.MkdirAll(filepath.Join(dir, domain), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDispatchCommand_NeedsSubcommands(t *testing.T) {
	// Boundary: No arguments prints usage
	if err := dispatchCommand("needs", []string{}); err != nil {
		t.Fatalf("needs with no args failed: %v", err)
	}
	if err := dispatchCommand("needs", []string{"-h"}); err != nil {
		t.Fatalf("needs -h failed: %v", err)
	}

	repo := newNeedsRepo(t)
	framework := newFrameworkFixture(t)

	// Positive: scan a fixture repository
	out, err := captureStdout(t, func() error { return dispatchCommand("needs", []string{"scan", "--path=" + repo}) })
	if err != nil {
		t.Fatalf("needs scan failed: %v", err)
	}
	mustContain(t, out, "=== Framework Needs Scan:", "github.com/spf13/cobra")

	// Positive: report against a real framework checkout and against the built-in index,
	// so both InspectFramework branches are exercised deterministically.
	for _, fw := range []string{framework, filepath.Join(t.TempDir(), "absent")} {
		out, err := captureStdout(t, func() error {
			return dispatchCommand("needs", []string{"report", "--path=" + repo, "--framework=" + fw})
		})
		if err != nil {
			t.Fatalf("needs report (framework=%s) failed: %v", fw, err)
		}
		mustContain(t, out, "=== Golusoris Migration Report:", "Framework: github.com/golusoris/golusoris", "Drop-In Replacement Matrix:", "github.com/spf13/cobra")
	}

	// Negative: a directory that is not a repository, and an unknown subcommand
	if err := dispatchCommand("needs", []string{"report", "--path=" + t.TempDir(), "--framework=" + framework}); err == nil {
		t.Fatal("expected error for a non-repository target")
	}
	if err := dispatchCommand("needs", []string{"invalid-sub"}); err == nil {
		t.Fatal("expected error for unknown needs subcommand")
	}
}

func TestDispatchCommand_CoreGovernance(t *testing.T) {
	// Positive: Sentinel check
	if err := dispatchCommand("sentinel", []string{}); err != nil {
		t.Fatalf("sentinel failed: %v", err)
	}

	// Positive: GC check
	if err := dispatchCommand("gc", []string{"--dry-run", "--path=../.."}); err != nil {
		t.Fatalf("gc failed: %v", err)
	}

	// Positive: Worktree list
	if err := dispatchCommand("worktree", []string{"list", "--path=../.."}); err != nil {
		t.Fatalf("worktree list failed: %v", err)
	}

	// Negative: Unknown worktree subcommand
	if err := dispatchCommand("worktree", []string{"bogus"}); err == nil {
		t.Fatal("expected error for unknown worktree subcommand")
	}
}

func TestDispatchCommand_BumpAndChangelog(t *testing.T) {
	// Boundary: Bump help
	if err := dispatchCommand("bump", []string{}); err != nil {
		t.Fatalf("bump with no args failed: %v", err)
	}
	if err := dispatchCommand("bump", []string{"-h"}); err != nil {
		t.Fatalf("bump -h failed: %v", err)
	}

	// Boundary: Changelog help
	if err := dispatchCommand("changelog", []string{}); err != nil {
		t.Fatalf("changelog with no args failed: %v", err)
	}
	if err := dispatchCommand("changelog", []string{"-h"}); err != nil {
		t.Fatalf("changelog -h failed: %v", err)
	}

	// Negative: Release without required version flag
	if err := dispatchCommand("release", []string{}); err == nil {
		t.Fatal("expected error when release version is missing")
	}
}

func TestDispatchCommand_ForgeSubcommands(t *testing.T) {
	tmpDir := t.TempDir()

	// Positive: Forge help
	if err := dispatchCommand("forge", []string{}); err != nil {
		t.Fatalf("forge with no args failed: %v", err)
	}

	// Positive: PR validation with compliant body
	prFile := filepath.Join(tmpDir, "compliant-pr.md")
	prContent := "## Summary\nTest PR\n\n- [x] HISS-16 standards verified\n- [x] 3D tests (positive, negative, boundary) added\n- [x] Ed25519 Exit-0 Receipt verified: `receipt:ed25519:abcdef0123456789`\n"
	if err := os.WriteFile(prFile, []byte(prContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dispatchCommand("forge", []string{"validate-pr", prFile}); err != nil {
		t.Fatalf("forge validate-pr failed: %v", err)
	}

	// Negative: Non-compliant PR body
	badPRFile := filepath.Join(tmpDir, "bad-pr.md")
	if err := os.WriteFile(badPRFile, []byte("Just random text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dispatchCommand("forge", []string{"validate-pr", badPRFile}); err == nil {
		t.Fatal("expected error on non-compliant PR validation")
	}
}

// TestDispatchCommand_RepositoryDogfood audits this repository's own committed state. It
// is read-only; the hermetic audit behaviour is covered by audit_cmd_test.go.
func TestDispatchCommand_RepositoryDogfood(t *testing.T) {
	t.Setenv("CI", "true")
	out, err := captureStdout(t, func() error {
		return dispatchCommand("audit", []string{"--config=../../.standards.yaml"})
	})
	if err != nil {
		t.Fatalf("repository audit failed: %v\n%s", err, out)
	}
	mustContain(t, out, "=== cordanaLLM/praetor Governance Audit ===", "Audit Summary: 100% Compliance")

	if err := dispatchCommand("compile-context", []string{"--verify", "--source=../../AGENTS.md", "--target-dir=../.."}); err != nil {
		t.Fatalf("compile-context verify failed: %v", err)
	}
}

func TestDispatchCommand_ContextAndDevcontainer(t *testing.T) {
	// Devcontainer verify
	if err := dispatchCommand("devcontainer", []string{"--verify", "--config=../../.standards.yaml", "--output=../../.devcontainer/devcontainer.json"}); err != nil {
		t.Fatalf("devcontainer verify failed: %v", err)
	}

	// Devcontainer help
	if err := dispatchCommand("devcontainer", []string{"-h"}); err != nil && !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("devcontainer -h failed: %v", err)
	}
}

func TestDispatchCommand_EditorsAndFlavors(t *testing.T) {
	tmpDir := t.TempDir()

	// Editors synthesize in temp dir
	if err := dispatchCommand("editors", []string{"generate", "--path=" + tmpDir}); err != nil {
		t.Fatalf("editors failed: %v", err)
	}

	// Flavors help and the real "plan" action (the reconciler knows plan and sync only).
	if err := dispatchCommand("flavors", []string{"-h"}); err != nil && !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("flavors -h failed: %v", err)
	}
	out, err := captureStdout(t, func() error {
		return dispatchCommand("flavors", []string{"--config=../../.config/flavors.yaml", "plan"})
	})
	if err != nil {
		t.Fatalf("flavors plan failed: %v", err)
	}
	mustContain(t, out, "Release Flavor Reconciler", "flavors sync")
}

func TestDispatchCommand_ModelsAndHarvest(t *testing.T) {
	// Models list with existing routing config
	if err := dispatchCommand("models", []string{"--config=../../.config/models/routing.yaml", "list"}); err != nil {
		t.Fatalf("models list failed: %v", err)
	}

	// Harvest help and the static fleet topology (fleet takes no flags).
	if err := dispatchCommand("harvest", []string{"-h"}); err != nil && !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("harvest -h failed: %v", err)
	}
	out, err := captureStdout(t, func() error { return dispatchCommand("harvest", []string{"fleet"}) })
	if err != nil {
		t.Fatalf("harvest fleet failed: %v", err)
	}
	mustContain(t, out, "Fleet Topology")
}

func TestDispatchCommand_AdoptPlanSyncInitHelp(t *testing.T) {
	for _, cmd := range []string{"adopt", "plan", "sync", "init"} {
		if err := dispatchCommand(cmd, []string{"-h"}); err != nil && !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("%s -h failed: %v", cmd, err)
		}
	}
}

func TestDispatchCommand_PaperclipAndAdopt(t *testing.T) {
	tmpDir := t.TempDir()

	// Paperclip help
	if err := dispatchCommand("paperclip", []string{"-h"}); err != nil && !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("paperclip -h failed: %v", err)
	}

	// Paperclip harness synthesis
	if err := dispatchCommand("paperclip", []string{"harness", "--path=" + tmpDir}); err != nil {
		t.Fatalf("paperclip harness failed: %v", err)
	}

	// Paperclip disposition
	dispArgs := []string{
		"disposition",
		"--issue=ISS-42",
		"--status=in_review",
		"--output=" + filepath.Join(tmpDir, "disposition.json"),
		"--note=Completed task",
		"--proof=receipt:ed25519:abcdef1234567890",
	}
	if err := dispatchCommand("paperclip", dispArgs); err != nil {
		t.Fatalf("paperclip disposition failed: %v", err)
	}

	// Initialize leaf git repository for adoption validation; a fixture failure must be
	// reported as such, never as an adoption failure.
	writeFixtureFile(t, tmpDir, ".git/HEAD", "ref: refs/heads/main\n")

	// Adopt dry-run
	if err := dispatchCommand("adopt", []string{"--dry-run", "--path=" + tmpDir, "--profile=framework"}); err != nil {
		t.Fatalf("adopt dry-run failed: %v", err)
	}
}

func TestDispatchCommand_IssueReconcile(t *testing.T) {
	// The fixture token selects the forge dry-run driver; no credential is harvested and
	// no request leaves the process.
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	if err := dispatchCommand("issue", []string{}); err != nil {
		t.Fatalf("issue with no args failed: %v", err)
	}
	if err := dispatchCommand("issue", []string{"-h"}); err != nil {
		t.Fatalf("issue -h failed: %v", err)
	}
	out, err := captureStdout(t, func() error {
		return dispatchCommand("issue", []string{"reconcile", "--owner=cordanaLLM", "--dry-run", "--token=test-fixture", "--endpoint=http://127.0.0.1:0"})
	})
	if err != nil {
		t.Fatalf("issue reconcile failed: %v", err)
	}
	if strings.Contains(out, "[WARN]") {
		t.Fatalf("fixture-backed reconcile must not warn about remote failures:\n%s", out)
	}
	if err := dispatchCommand("issue", []string{"invalid"}); err == nil {
		t.Fatal("expected error for invalid issue subcommand")
	}
}

func TestDispatchCommand_NeedsFleet(t *testing.T) {
	// Needs requests and epic against fixture repositories and an explicit framework.
	fleet := t.TempDir()
	repo := filepath.Join(fleet, "acme", "widgets")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, repo, ".git/HEAD", "ref: refs/heads/main\n")
	writeFixtureFile(t, repo, "go.mod", "module example.com/widgets\n\ngo 1.27\n\nrequire github.com/spf13/cobra v1.8.0\n")
	framework := newFrameworkFixture(t)
	out, err := captureStdout(t, func() error {
		return dispatchCommand("needs", []string{"requests", "--dev-dir=" + fleet, "--framework=" + framework})
	})
	if err != nil {
		t.Fatalf("needs requests failed: %v", err)
	}
	mustContain(t, out, "Framework Demand Requests")
	epicOut := filepath.Join(t.TempDir(), "EPIC.md")
	if err := dispatchCommand("needs", []string{"epic", "--path=" + repo, "--framework=" + framework, "--output=" + epicOut}); err != nil {
		t.Fatalf("needs epic failed: %v", err)
	}
	if _, err := os.Stat(epicOut); err != nil {
		t.Fatalf("expected the epic to be written: %v", err)
	}
}

func TestDispatchCommand_Build(t *testing.T) {
	tmpDir := t.TempDir()
	writeFixtureFile(t, tmpDir, "cmd/app/main.go", "package main\n\nfunc main() {}\n")
	buildCfgPath := filepath.Join(tmpDir, ".framework-build.yaml")
	cfgContent := `
version: 1
project: cli-test
output_dir: ` + filepath.Join(tmpDir, "dist") + `
targets:
  cli:
    runtime: go
    entrypoint: ` + filepath.Join(tmpDir, "cmd", "app") + `
`
	if err := os.WriteFile(buildCfgPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatalf("failed to write build config: %v", err)
	}

	if err := dispatchCommand("build", []string{"-h"}); err != nil && !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("build -h failed: %v", err)
	}
	if err := dispatchCommand("build", []string{"--config=" + buildCfgPath, "--target=cli"}); err != nil {
		t.Fatalf("build failed: %v", err)
	}
}

func TestDispatchCommand_StateSubcommands(t *testing.T) {
	tmpDir := t.TempDir()

	if err := dispatchCommand("state", []string{}); err != nil {
		t.Fatalf("state with no args failed: %v", err)
	}
	if err := dispatchCommand("state", []string{"init", tmpDir}); err != nil {
		t.Fatalf("state init failed: %v", err)
	}
	if err := dispatchCommand("state", []string{"status", tmpDir}); err != nil {
		t.Fatalf("state status failed: %v", err)
	}
	if err := dispatchCommand("state", []string{"bug", "add", "--title=Test bug", "--dir=" + tmpDir}); err != nil {
		t.Fatalf("state bug add failed: %v", err)
	}
	if err := dispatchCommand("state", []string{"bug", "list", tmpDir}); err != nil {
		t.Fatalf("state bug list failed: %v", err)
	}
	if err := dispatchCommand("state", []string{"bug", "resolve", "BUG-001", "Fixed", tmpDir}); err != nil {
		t.Fatalf("state bug resolve failed: %v", err)
	}
	if err := dispatchCommand("state", []string{"question", "add", "--prompt=Test Q", "--options=A,B", "--dir=" + tmpDir}); err != nil {
		t.Fatalf("state question add failed: %v", err)
	}
	if err := dispatchCommand("state", []string{"question", "list", tmpDir}); err != nil {
		t.Fatalf("state question list failed: %v", err)
	}
	if err := dispatchCommand("state", []string{"question", "decide", "Q-001", "A", tmpDir}); err != nil {
		t.Fatalf("state question decide failed: %v", err)
	}
	// Flags precede the positional directory; the log line must land in STATE.md.
	if err := dispatchCommand("state", []string{"sync", "--log=test execution", tmpDir}); err != nil {
		t.Fatalf("state sync failed: %v", err)
	}
	if got := readFixtureFile(t, tmpDir, ".workingdir/STATE.md"); !strings.Contains(got, "test execution") {
		t.Fatalf("expected the --log message in STATE.md, got:\n%s", got)
	}
	if err := dispatchCommand("state", []string{"audit", tmpDir}); err != nil {
		t.Fatalf("state audit failed: %v", err)
	}
	if err := dispatchCommand("state", []string{"invalid"}); err == nil {
		t.Fatal("expected error for invalid state subcommand")
	}
}

func TestDispatchCommand_FlavorSubcommands(t *testing.T) {
	tmpDir := t.TempDir()

	if err := dispatchCommand("flavor", []string{}); err != nil {
		t.Fatalf("flavor with no args failed: %v", err)
	}
	if err := dispatchCommand("flavor", []string{"list"}); err != nil {
		t.Fatalf("flavor list failed: %v", err)
	}
	if err := dispatchCommand("flavor", []string{"inspect", "go-service"}); err != nil {
		t.Fatalf("flavor inspect failed: %v", err)
	}
	if err := dispatchCommand("flavor", []string{"apply", tmpDir, "--flavor=go-service"}); err != nil {
		t.Fatalf("flavor apply failed: %v", err)
	}
	if err := dispatchCommand("flavor", []string{"invalid"}); err == nil {
		t.Fatal("expected error for invalid flavor subcommand")
	}
}

func TestDispatchCommand_DedupeSubcommands(t *testing.T) {
	tmpDir := t.TempDir()

	if err := dispatchCommand("dedupe", []string{}); err != nil {
		t.Fatalf("dedupe with no args failed: %v", err)
	}
	if err := dispatchCommand("dedupe", []string{"scan", tmpDir}); err != nil {
		t.Fatalf("dedupe scan failed: %v", err)
	}
	if err := dispatchCommand("dedupe", []string{"cadence", "--threshold=20", tmpDir}); err != nil {
		t.Fatalf("dedupe cadence failed: %v", err)
	}
	if err := dispatchCommand("dedupe", []string{"invalid"}); err == nil {
		t.Fatal("expected error for invalid dedupe subcommand")
	}
}

func TestDispatchCommand_TopologySubcommands(t *testing.T) {
	tmpDir := t.TempDir()

	if err := dispatchCommand("topology", []string{}); err != nil {
		t.Fatalf("topology with no args failed: %v", err)
	}
	if err := dispatchCommand("topology", []string{"-h"}); err != nil {
		t.Fatalf("topology -h failed: %v", err)
	}
	if err := dispatchCommand("topology", []string{"audit", tmpDir}); err != nil {
		t.Fatalf("topology audit on empty dir failed: %v", err)
	}
	if err := dispatchCommand("topology", []string{"clean", "--dry-run=true", tmpDir}); err != nil {
		t.Fatalf("topology clean dry-run failed: %v", err)
	}
	if err := dispatchCommand("topology", []string{"invalid"}); err == nil {
		t.Fatal("expected error for invalid topology subcommand")
	}
}

func TestDispatchCommand_MilestoneAndProject(t *testing.T) {
	tmpDir := t.TempDir()

	// Milestone subcommands
	if err := dispatchCommand("milestone", []string{}); err != nil {
		t.Fatalf("milestone empty args failed: %v", err)
	}
	if err := dispatchCommand("milestone", []string{"-h"}); err != nil {
		t.Fatalf("milestone -h failed: %v", err)
	}
	if err := dispatchCommand("milestone", []string{"create", "--title=TestMilestone", "--dir=" + tmpDir}); err != nil {
		t.Fatalf("milestone create failed: %v", err)
	}
	if err := dispatchCommand("milestone", []string{"list", "--dir=" + tmpDir}); err != nil {
		t.Fatalf("milestone list failed: %v", err)
	}
	if err := dispatchCommand("milestone", []string{"status", tmpDir}); err != nil {
		t.Fatalf("milestone status failed: %v", err)
	}
	if err := dispatchCommand("milestone", []string{"close", "1", tmpDir}); err != nil {
		t.Fatalf("milestone close failed: %v", err)
	}
	if err := dispatchCommand("milestone", []string{"invalid"}); err == nil {
		t.Fatal("expected error for invalid milestone subcommand")
	}

	// Project subcommands
	if err := dispatchCommand("project", []string{}); err != nil {
		t.Fatalf("project empty args failed: %v", err)
	}
	if err := dispatchCommand("project", []string{"-h"}); err != nil {
		t.Fatalf("project -h failed: %v", err)
	}
	if err := dispatchCommand("project", []string{"status", tmpDir}); err != nil {
		t.Fatalf("project status failed: %v", err)
	}
	if err := dispatchCommand("project", []string{"add", "--dir=" + tmpDir, "1", "https://github.com/cordanaLLM/praetor/issues/1"}); err != nil {
		t.Fatalf("project add failed: %v", err)
	}
	if err := dispatchCommand("project", []string{"list", "--dir=" + tmpDir}); err != nil {
		t.Fatalf("project list failed: %v", err)
	}
	if err := dispatchCommand("project", []string{"invalid"}); err == nil {
		t.Fatal("expected error for invalid project subcommand")
	}
}

func TestDispatchCommand_CISubcommands(t *testing.T) {
	// Boundary: empty args and help
	if err := dispatchCommand("ci", []string{}); err != nil {
		t.Fatalf("ci empty args failed: %v", err)
	}
	if err := dispatchCommand("ci", []string{"-h"}); err != nil {
		t.Fatalf("ci -h failed: %v", err)
	}

	// Negative: invalid subcommand (the filter itself is covered by ci_cmd_test.go)
	if err := dispatchCommand("ci", []string{"unknown-sub"}); err == nil {
		t.Fatal("expected error for invalid ci subcommand")
	}
}
