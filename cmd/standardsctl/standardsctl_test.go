package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cordanaLLM/praetor/internal/builder"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/lockdown"
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
	for _, fw := range []string{framework, ""} {
		out, err := captureStdout(t, func() error {
			return dispatchCommand("needs", []string{"report", "--path=" + repo, "--framework=" + fw})
		})
		if err != nil {
			t.Fatalf("needs report (framework=%s) failed: %v", fw, err)
		}
		mustContain(t, out, "=== Golusoris Migration Report:", "Framework: github.com/golusoris/golusoris", "Library relationships and migration candidates:", "github.com/spf13/cobra")
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

// prFixtureHeadSHA is the commit the fixture receipt certifies.
const prFixtureHeadSHA = "2c4574832f8b40626598d457b509acf0056a72b7"

// writeSignedPRFixture writes a manifest pinning a fresh receipt key and a PR body that
// carries a genuine Ed25519 Exit-0 receipt for that key.
func writeSignedPRFixture(t *testing.T, dir string) (manifestPath, prPath string) {
	t.Helper()
	pub, priv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed generating receipt keypair: %v", err)
	}
	manifestPath = filepath.Join(dir, "standards.yaml")
	manifest := fmt.Sprintf("version: 1\nreceipt:\n  public_key: \"%s\"\n", hex.EncodeToString(pub))
	if err := os.WriteFile(manifestPath, []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}

	receipt, err := lockdown.CreateReceipt(gating.ReceiptCommand, 0, []byte("gates passed"),
		prFixtureHeadSHA, "acme/widget", priv)
	if err != nil {
		t.Fatalf("failed creating receipt: %v", err)
	}
	receiptJSON, err := json.MarshalIndent(
		lockdown.ReceiptFile{ExecutionReceipt: *receipt, GateOutput: "gates passed"}, "", "  ")
	if err != nil {
		t.Fatalf("failed encoding receipt: %v", err)
	}

	prPath = filepath.Join(dir, "compliant-pr.md")
	prContent := "## Summary\nTest PR\n\n- [x] HISS-16 standards verified\n" +
		"- [x] 3D tests (positive, negative, boundary) added\n\n" +
		"```receipt\n" + string(receiptJSON) + "\n```\n"
	if err := os.WriteFile(prPath, []byte(prContent), 0o600); err != nil {
		t.Fatal(err)
	}
	return manifestPath, prPath
}

func TestDispatchCommand_ForgeSubcommands(t *testing.T) {
	tmpDir := t.TempDir()

	// Positive: Forge help
	if err := dispatchCommand("forge", []string{}); err != nil {
		t.Fatalf("forge with no args failed: %v", err)
	}

	// Positive: PR validation with a compliant body carrying a real signed receipt
	manifestPath, prFile := writeSignedPRFixture(t, tmpDir)
	if err := dispatchCommand("forge", []string{
		"validate-pr", "--config=" + manifestPath, "--head-sha=" + prFixtureHeadSHA, prFile,
	}); err != nil {
		t.Fatalf("forge validate-pr failed: %v", err)
	}

	// Negative: the same checklist without a signed receipt must be rejected
	unsignedFile := filepath.Join(tmpDir, "unsigned-pr.md")
	unsigned := "## Summary\nTest PR\n\n- [x] HISS-16 standards verified\n" +
		"- [x] 3D tests (positive, negative, boundary) added\n" +
		"- [x] Ed25519 Exit-0 Receipt verified: `receipt:ed25519:abcdef0123456789`\n"
	if err := os.WriteFile(unsignedFile, []byte(unsigned), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dispatchCommand("forge", []string{"validate-pr", "--config=" + manifestPath, unsignedFile}); err == nil {
		t.Fatal("expected validate-pr to reject a PR body without a signed Ed25519 receipt")
	}

	// Negative: Non-compliant PR body
	badPRFile := filepath.Join(tmpDir, "bad-pr.md")
	if err := os.WriteFile(badPRFile, []byte("Just random text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := dispatchCommand("forge", []string{"validate-pr", "--config=" + manifestPath, badPRFile}); err == nil {
		t.Fatal("expected error on non-compliant PR validation")
	}
}

func TestDispatchCommand_ForgePinnedReceiptArguments(t *testing.T) {
	manifest, body := writeSignedPRFixture(t, t.TempDir())
	// Flags after the positional body must still enforce the requested trust policy.
	if err := dispatchCommand("forge", []string{"validate-pr", body, "--config", manifest, "--head-sha", prFixtureHeadSHA}); err != nil {
		t.Fatalf("interspersed receipt arguments: %v", err)
	}
	foreignManifest, _ := writeSignedPRFixture(t, t.TempDir())
	cases := [][]string{
		{"validate-pr", body, "--config", manifest, "--head-sha", strings.Repeat("a", 40)},
		{"validate-pr", body, "--config", foreignManifest, "--head-sha", prFixtureHeadSHA},
		{"validate-pr", body, "--config", filepath.Join(t.TempDir(), "missing.yaml")},
		{"validate-pr", body, "--config", manifest, "extra"},
	}
	for _, args := range cases {
		if err := dispatchCommand("forge", args); err == nil {
			t.Fatalf("invalid receipt policy or arguments accepted: %v", args)
		}
	}
}

// TestDispatchCommand_RepositoryDogfood audits this repository's own committed state. It
// is read-only; the hermetic audit behaviour is covered by audit_cmd_test.go.
func TestDispatchCommand_RepositoryDogfood(t *testing.T) {
	t.Setenv("CI", "true")
	const manifestPath = "../../.standards.yaml"
	manifest, err := config.LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("load repository identity: %v", err)
	}
	out, err := captureStdout(t, func() error {
		return dispatchCommand("audit", []string{"--config=" + manifestPath})
	})
	if err != nil {
		t.Fatalf("repository audit failed: %v\n%s", err, out)
	}
	expectedHeader := fmt.Sprintf("=== %s/%s Governance Audit ===", manifest.Repository.Owner, manifest.Repository.Name)
	mustContain(t, out, expectedHeader, "Audit Summary: configured governance gates passed")

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

func TestEditorsGenerateReportsCreatedMergedAndPresent(t *testing.T) {
	root := t.TempDir()
	out, err := captureStdout(t, func() error { return runEditors([]string{"generate", "--path=" + root}) })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "created", "0 merged")

	settingsPath := filepath.Join(root, ".vscode", "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"editor.fontSize":42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = captureStdout(t, func() error { return runEditors([]string{"generate", "--path=" + root}) })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "1 merged")
	settings, err := os.ReadFile(settingsPath)
	if err != nil || !strings.Contains(string(settings), `"editor.fontSize": 42`) {
		t.Fatalf("merge report lost the existing setting: %s %v", settings, err)
	}

	out, err = captureStdout(t, func() error { return runEditors([]string{"generate", "--path=" + root}) })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "0 merged", "already present")
}

func TestEditorsVerifyReportsPreservedNonJSONAsUnverified(t *testing.T) {
	root := t.TempDir()
	if _, err := captureStdout(t, func() error { return runEditors([]string{"generate", "--path=" + root}) }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".editorconfig"), []byte("root = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return runEditors([]string{"verify", "--path=" + root}) })
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Managed requirements verified", "[UNVERIFIED]", ".editorconfig")
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

	// Negative: fleet accepts no flags, and unknown subcommands fail.
	if err := dispatchCommand("harvest", []string{"fleet", "--dev-dir=" + t.TempDir()}); err == nil {
		t.Fatal("expected error for unexpected harvest fleet argument")
	}
	if err := dispatchCommand("harvest", []string{"not-a-subcommand"}); err == nil {
		t.Fatal("expected error for unknown harvest subcommand")
	}
}

func TestDispatchCommand_AdoptPlanSyncInitHelp(t *testing.T) {
	for _, cmd := range []string{"adopt", "plan", "sync", "init"} {
		if err := dispatchCommand(cmd, []string{"-h"}); err != nil && !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("%s -h failed: %v", cmd, err)
		}
	}
}

func TestDispatchCommand_PaperclipAndAdopt(t *testing.T) {
	tmpDir := newAuditFixture(t).dir

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

	// Preserve the equals-form dry-run contract as well as reordered arguments.
	if err := dispatchCommand("adopt", []string{"--dry-run", "--path=" + tmpDir, "--profile=framework"}); err != nil {
		t.Fatalf("adopt dry-run with equals-form flags failed: %v", err)
	}

	// Adopt dry-run, in the space-separated flag form that exercises the argument
	// reordering (the =-form never reaches the lookahead branch).
	if err := dispatchCommand("adopt", []string{"--dry-run", "--path", tmpDir, "--profile", "framework"}); err != nil {
		t.Fatalf("adopt dry-run failed: %v", err)
	}

	// Boundary: the bare positional form must target the same repository.
	if err := dispatchCommand("adopt", []string{"--dry-run", tmpDir}); err != nil {
		t.Fatalf("adopt dry-run with a positional path failed: %v", err)
	}

	// Negative: an unknown adopt flag is rejected.
	if err := dispatchCommand("adopt", []string{"--not-a-flag"}); err == nil {
		t.Fatal("expected an error for an unknown adopt flag")
	}
}

func TestDispatchCommand_AdoptDryRunBaselineRequiresPins(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, dir, ".git/HEAD", "ref: refs/heads/main\n")
	_, err := captureStdout(t, func() error {
		return dispatchCommand("adopt", []string{"--dry-run", "--path=" + dir, "--profile=framework"})
	})
	mustErrContain(t, err, "new lock pins require an explicit verified lock source root")
}

func TestDispatchCommand_IssueReconcile(t *testing.T) {
	// A local server exercises real forge reads; dry-run must perform no writes.
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	var reads, writes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
			http.Error(w, "dry-run write forbidden", http.StatusMethodNotAllowed)
			return
		}
		reads.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte("[]")); err != nil {
			t.Errorf("write fixture response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	if err := dispatchCommand("issue", []string{}); err != nil {
		t.Fatalf("issue with no args failed: %v", err)
	}
	if err := dispatchCommand("issue", []string{"-h"}); err != nil {
		t.Fatalf("issue -h failed: %v", err)
	}
	out, err := captureStdout(t, func() error {
		return dispatchCommand("issue", []string{"reconcile", "--owner=cordanaLLM", "--repos=praetor", "--dry-run", "--token=test-fixture", "--endpoint=" + srv.URL})
	})
	if err != nil {
		t.Fatalf("issue reconcile failed: %v", err)
	}
	if strings.Contains(out, "[WARN]") {
		t.Fatalf("fixture-backed reconcile must not warn about remote failures:\n%s", out)
	}
	if reads.Load() != 1 || writes.Load() != 0 {
		t.Fatalf("dry-run performed %d reads and %d writes", reads.Load(), writes.Load())
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
	if err := dispatchCommand("build", []string{"--config=" + buildCfgPath, "--target=cli"}); !errors.Is(err, builder.ErrBackendUnavailable) {
		t.Fatalf("unimplemented backend must reject even an existing entrypoint: %v", err)
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
	if err := dispatchCommand("dedupe", []string{"cadence", "--threshold=20", tmpDir}); err == nil {
		t.Fatal("dedupe cadence must report that its commit count is unavailable outside Git")
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

	// Hermetic forge isolation: without this the command paths resolve a real token from
	// the developer's environment or `gh` session and mutate a live board.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

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

func TestDispatchCommand_MilestoneDirectoryFlags(t *testing.T) {
	dir := t.TempDir()
	if err := dispatchCommand("milestone", []string{"create", "--title=Flag fixture", "--dir=" + dir}); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error {
		return dispatchCommand("milestone", []string{"status", "--dir", dir})
	})
	if err != nil {
		t.Fatalf("status with directory flag: %v", err)
	}
	mustContain(t, out, "Total:       1", "Open:        1")
	if err := dispatchCommand("milestone", []string{"close", "1", "--dir", dir}); err != nil {
		t.Fatalf("close with flag after selector: %v", err)
	}
	out, err = captureStdout(t, func() error {
		return dispatchCommand("milestone", []string{"status", "--dir=" + dir})
	})
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Closed:      1")
}

func TestDispatchCommand_ProjectCachedStatusDoesNotResolveAuth(t *testing.T) {
	dir, bin := t.TempDir(), t.TempDir()
	t.Setenv("PATH", bin)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	out, err := captureStdout(t, func() error {
		return dispatchCommand("project", []string{"add", "1", "https://github.com/acme/widgets/issues/1", "--dir", dir})
	})
	if err != nil {
		t.Fatalf("local project fixture: %v", err)
	}
	mustContain(t, out, "local cache only", "board was not updated")
	if strings.Contains(out, "[PASS]") {
		t.Fatalf("local cache write claimed remote success: %s", out)
	}
	before := readFixtureFile(t, dir, ".workingdir/project.json")
	marker := filepath.Join(bin, "called")
	t.Setenv("PRAETOR_TEST_GH_MARKER", marker)
	script := "#!/bin/sh\nprintf called > \"$PRAETOR_TEST_GH_MARKER\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := dispatchCommand("project", []string{"status", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("cached status resolved credentials: %v", err)
	}
	if after := readFixtureFile(t, dir, ".workingdir/project.json"); before != after {
		t.Fatal("cached status rewrote the cache")
	}
}

func TestDispatchCommand_MilestoneProjectRejectExtraArguments(t *testing.T) {
	cases := []struct {
		command string
		args    []string
	}{
		{"milestone", []string{"close"}},
		{"milestone", []string{"close", "1", "--unknown"}},
		{"milestone", []string{"close", "1", "a", "b"}},
		{"milestone", []string{"status", "a", "b"}},
		{"milestone", []string{"status", "--unknown"}},
		{"project", []string{"list", "extra"}},
		{"project", []string{"status", "a", "b"}},
		{"project", []string{"add", "1", "https://example.invalid/issues/1", "extra"}},
	}
	for _, tc := range cases {
		if err := dispatchCommand(tc.command, tc.args); err == nil {
			t.Fatalf("accepted extra or invalid args: %s %v", tc.command, tc.args)
		}
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

// =========================================================================
// Argument reordering (HISS-15: positive, negative, boundary)
// =========================================================================

func TestReorderArgs_Table(t *testing.T) {
	fs := flag.NewFlagSet("fixture", flag.ContinueOnError)
	fs.String("path", ".", "string flag")
	fs.String("log", "", "string flag")
	fs.Bool("dry-run", false, "bool flag")
	boolFlags := boolFlagNames(fs)

	if !boolFlags["dry-run"] || boolFlags["path"] {
		t.Fatalf("boolFlagNames misclassified flags: %v", boolFlags)
	}

	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"equals form keeps order", []string{"--path=/x", "target"}, []string{"--path=/x", "target"}},
		{"space form keeps flag value attached", []string{"--path", "/x", "target"}, []string{"--path", "/x", "target"}},
		{"positional before flag is hoisted", []string{"target", "--log=msg"}, []string{"--log=msg", "target"}},
		{"bool flag does not swallow positional", []string{"--dry-run", "target"}, []string{"--dry-run", "target"}},
		{"bool flag before value flag", []string{"dir", "--dry-run", "--path", "/x"}, []string{"--dry-run", "--path", "/x", "dir"}},
		{"trailing flag without value", []string{"dir", "--path"}, []string{"--path", "dir"}},
		{"bare positional only", []string{"dir"}, []string{"dir"}},
		{"empty input", []string{}, []string{}},
		{"lone dash stays positional", []string{"-"}, []string{"-"}},
		{"double dash terminates flags", []string{"--dry-run", "--", "--path", "x"}, []string{"--dry-run", "--path", "x"}},
	}

	for _, tc := range cases {
		got := reorderArgs(tc.in, boolFlags)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
				break
			}
		}
	}
}

func TestReorderArgs_ParsesFlagAfterPositional(t *testing.T) {
	fs := flag.NewFlagSet("fixture", flag.ContinueOnError)
	logMsg := fs.String("log", "", "log message")
	if err := fs.Parse(reorderArgs([]string{"/tmp/x", "--log=recorded"}, boolFlagNames(fs))); err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if *logMsg != "recorded" {
		t.Errorf("expected the flag after the positional to be parsed, got %q", *logMsg)
	}
	if fs.NArg() != 1 || fs.Arg(0) != "/tmp/x" {
		t.Errorf("expected the positional to survive, got %v", fs.Args())
	}
}

// =========================================================================
// state: sync logs, status is read-only, --dir is a flag everywhere
// =========================================================================

func TestDispatchCommand_StateSyncRecordsLogAfterPositional(t *testing.T) {
	tmpDir := t.TempDir()
	if err := dispatchCommand("state", []string{"init", tmpDir}); err != nil {
		t.Fatalf("state init failed: %v", err)
	}

	// The documented order places the directory before the flag.
	if err := dispatchCommand("state", []string{"sync", tmpDir, "--log=finished PR 12"}); err != nil {
		t.Fatalf("state sync failed: %v", err)
	}

	stateMD := filepath.Join(tmpDir, ".workingdir", "STATE.md")
	content, err := os.ReadFile(stateMD)
	if err != nil {
		t.Fatalf("failed reading STATE.md: %v", err)
	}
	if !strings.Contains(string(content), "finished PR 12") {
		t.Errorf("expected the --log message in STATE.md, got:\n%s", content)
	}
}

func TestDispatchCommand_StateStatusDoesNotMutateLedger(t *testing.T) {
	tmpDir := t.TempDir()
	if err := dispatchCommand("state", []string{"init", tmpDir}); err != nil {
		t.Fatalf("state init failed: %v", err)
	}
	stateMD := filepath.Join(tmpDir, ".workingdir", "STATE.md")
	before, err := os.ReadFile(stateMD)
	if err != nil {
		t.Fatalf("failed reading STATE.md: %v", err)
	}

	out, statusErr := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"status", "--dir=" + tmpDir})
	})
	if statusErr != nil {
		t.Fatalf("state status failed: %v", statusErr)
	}
	if !strings.Contains(out, "Praetor Session State") || !strings.Contains(out, "Open Bugs") {
		t.Errorf("unexpected state status output:\n%s", out)
	}

	after, err := os.ReadFile(stateMD)
	if err != nil {
		t.Fatalf("failed re-reading STATE.md: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("state status mutated STATE.md:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestDispatchCommand_StateStatusRequiresWorkingDir(t *testing.T) {
	tmpDir := t.TempDir()
	if err := dispatchCommand("state", []string{"status", tmpDir}); err == nil {
		t.Fatal("expected state status to fail when .workingdir is absent")
	}
	if _, err := os.Stat(filepath.Join(tmpDir, ".workingdir")); !os.IsNotExist(err) {
		t.Errorf("state status scaffolded .workingdir instead of reporting the error")
	}
}

func TestDispatchCommand_StateTaskLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	if err := dispatchCommand("state", []string{"init", "--dir=" + tmpDir}); err != nil {
		t.Fatalf("state init failed: %v", err)
	}

	if err := dispatchCommand("state", []string{"task", "add", "wire the gate", "--dir=" + tmpDir}); err != nil {
		t.Fatalf("state task add failed: %v", err)
	}
	if err := dispatchCommand("state", []string{"task", "add", "second task", tmpDir}); err != nil {
		t.Fatalf("state task add (positional dir) failed: %v", err)
	}

	listed, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"task", "list", "--dir=" + tmpDir})
	})
	if err != nil {
		t.Fatalf("state task list failed: %v", err)
	}
	if !strings.Contains(listed, "wire the gate") || !strings.Contains(listed, "second task") {
		t.Errorf("expected both tasks in the listing, got:\n%s", listed)
	}

	// Select by text: the seeded ledger already owns index 1.
	if err := dispatchCommand("state", []string{"task", "complete", "wire the gate", "--dir=" + tmpDir}); err != nil {
		t.Fatalf("state task complete failed: %v", err)
	}
	if err := dispatchCommand("state", []string{"task", "archive", "--dir=" + tmpDir}); err != nil {
		t.Fatalf("state task archive failed: %v", err)
	}

	backlog, err := os.ReadFile(filepath.Join(tmpDir, ".workingdir", "BACKLOG.md"))
	if err != nil {
		t.Fatalf("failed reading BACKLOG.md: %v", err)
	}
	if !strings.Contains(string(backlog), "wire the gate") {
		t.Errorf("expected the completed task in BACKLOG.md, got:\n%s", backlog)
	}

	// Negative: task add without a description
	if err := dispatchCommand("state", []string{"task", "add", "--dir=" + tmpDir}); err == nil {
		t.Fatal("expected an error when the task description is missing")
	}
	// Negative: unknown task action
	if err := dispatchCommand("state", []string{"task", "bogus"}); err == nil {
		t.Fatal("expected an error for an unknown task action")
	}
}

func TestDispatchCommand_StateDirFlagIsNotADirectoryName(t *testing.T) {
	tmpDir := t.TempDir()
	if err := dispatchCommand("state", []string{"init", tmpDir}); err != nil {
		t.Fatalf("state init failed: %v", err)
	}
	if err := dispatchCommand("state", []string{"bug", "add", "--title=Ledger bug", "--dir=" + tmpDir}); err != nil {
		t.Fatalf("state bug add failed: %v", err)
	}

	// The documented form places --dir after the positional arguments.
	if err := dispatchCommand("state", []string{"bug", "resolve", "BUG-001", "fixed upstream", "--dir=" + tmpDir}); err != nil {
		t.Fatalf("state bug resolve failed: %v", err)
	}

	bugs, err := os.ReadFile(filepath.Join(tmpDir, ".workingdir", "BUGS.md"))
	if err != nil {
		t.Fatalf("failed reading BUGS.md: %v", err)
	}
	if !strings.Contains(string(bugs), "fixed upstream") {
		t.Errorf("expected the resolution in the target ledger, got:\n%s", bugs)
	}
	// The literal flag must never become a directory name in the working directory.
	if _, statErr := os.Stat("--dir=" + tmpDir); statErr == nil {
		t.Errorf("a stray '--dir=...' directory was created")
	}

	// Negative: resolve without a resolution argument
	if err := dispatchCommand("state", []string{"bug", "resolve", "BUG-001"}); err == nil {
		t.Fatal("expected an error when the resolution argument is missing")
	}
}

// =========================================================================
// topology: no hard-coded dev root, flags honoured after a positional
// =========================================================================

func TestDispatchCommand_TopologyAuditReportsStrayFiles(t *testing.T) {
	tmpDir := t.TempDir()
	stray := filepath.Join(tmpDir, "CLAUDE.md")
	if err := os.WriteFile(stray, []byte("# stray governance file\n"), 0o600); err != nil {
		t.Fatalf("failed writing stray file: %v", err)
	}

	if err := dispatchCommand("topology", []string{"audit", "--dev-root=" + tmpDir}); err == nil {
		t.Fatal("expected topology audit to fail when stray governance files exist")
	}

	// R1-56: the flag must still be honoured when it follows the positional dev root.
	if err := dispatchCommand("topology", []string{"clean", tmpDir, "--dry-run=false"}); err != nil {
		t.Fatalf("topology clean failed: %v", err)
	}
	if _, statErr := os.Stat(stray); !os.IsNotExist(statErr) {
		t.Errorf("--dry-run=false after the positional dev root was ignored; %s still exists", stray)
	}

	// Positive: a clean tree audits successfully.
	if err := dispatchCommand("topology", []string{"audit", tmpDir}); err != nil {
		t.Fatalf("topology audit on a clean tree failed: %v", err)
	}
}

func TestDefaultDevRoot_NeverReturnsAForeignPath(t *testing.T) {
	// Boundary: no home directory at all must be an error, never a hard-coded path.
	t.Setenv("HOME", "")
	root, err := defaultDevRoot()
	if err == nil {
		t.Fatalf("expected an error without a usable home directory, got %q", root)
	}
	if strings.Contains(err.Error(), "/home/kilian") {
		t.Errorf("error still references a workstation-specific path: %v", err)
	}

	// Positive: an explicit value always wins over the default.
	explicit, err := resolveDevRoot("/srv/dev", nil)
	if err != nil || explicit != "/srv/dev" {
		t.Errorf("expected the explicit --dev-root to win, got %q (%v)", explicit, err)
	}
	// Boundary: the positional argument is the second choice.
	positional, err := resolveDevRoot("", []string{"/srv/other"})
	if err != nil || positional != "/srv/other" {
		t.Errorf("expected the positional dev root, got %q (%v)", positional, err)
	}
}

// =========================================================================
// needs: --apply semantics and publishing safety
// =========================================================================

func newGitFixtureRepo(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	writeFixtureFile(t, tmpDir, ".git/HEAD", "ref: refs/heads/main\n")
	return tmpDir
}

func TestDispatchCommand_NeedsMigrateApplyFlagSemantics(t *testing.T) {
	repo := newGitFixtureRepo(t)
	writeFixtureFile(t, repo, "go.mod", "module example.com/needs-fixture\n\ngo 1.24\n")

	// Negative: --apply together with an explicit --dry-run=true is a contradiction.
	if err := dispatchCommand("needs", []string{"migrate", "--path=" + repo, "--apply", "--dry-run=true"}); err == nil {
		t.Fatal("expected --apply with --dry-run=true to be rejected")
	}

	// Positive: without --apply the command stays a dry run and says so.
	out, err := captureStdout(t, func() error {
		return dispatchCommand("needs", []string{"migrate", "--path=" + repo, "--framework="})
	})
	if err != nil {
		t.Fatalf("needs migrate dry run failed: %v", err)
	}
	if !strings.Contains(out, "Dry-run complete") {
		t.Errorf("expected a dry-run notice, got:\n%s", out)
	}
	if strings.Contains(out, "--dry-run=false") {
		t.Errorf("dry-run notice still advertises the removed --dry-run=false requirement:\n%s", out)
	}
}

func TestDispatchCommand_NeedsEpicPublishSafety(t *testing.T) {
	repo := newGitFixtureRepo(t)
	writeFixtureFile(t, repo, "go.mod", "module example.com/needs-fixture\n\ngo 1.24\n")
	// Keep the token lookup hermetic: no gh CLI invocation, no network.
	t.Setenv("GITHUB_TOKEN", "test-token-not-used")
	t.Setenv("GH_TOKEN", "")

	// Negative: fleet mode cannot resolve per-repository forge coordinates.
	err := dispatchCommand("needs", []string{"epic", "--dev-dir=" + repo, "--publish"})
	if err == nil {
		t.Fatal("expected fleet publishing to be refused")
	}
	if !strings.Contains(err.Error(), "--dev-dir") {
		t.Errorf("expected the refusal to name --dev-dir, got: %v", err)
	}

	// Negative: single-repo publishing requires explicit confirmation.
	err = dispatchCommand("needs", []string{"epic", "--path=" + repo, "--framework=", "--publish", "--owner=acme", "--repo=widget"})
	if err == nil {
		t.Fatal("expected publishing without --yes to be refused")
	}
	if !strings.Contains(err.Error(), "acme/widget") {
		t.Errorf("expected the refusal to print the resolved target, got: %v", err)
	}

	// Negative: --owner without --repo is incomplete.
	err = dispatchCommand("needs", []string{"epic", "--path=" + repo, "--framework=", "--publish", "--owner=acme", "--yes"})
	if err == nil {
		t.Fatal("expected --owner without --repo to be refused")
	}
}

func TestResolveRepoCoordinates_RejectsRepositoryNames(t *testing.T) {
	ctx := context.Background()

	// Negative: a bare repository name is not a path and must not be guessed into
	// forge coordinates.
	if _, _, err := resolveRepoCoordinates(ctx, "vmafx"); err == nil {
		t.Fatal("expected a bare repository name to be rejected")
	}

	// Positive: a manifest in a real directory provides the coordinates.
	repo := t.TempDir()
	manifest := "version: 1\nrepository:\n  owner: acme\n  name: widget\n"
	if err := os.WriteFile(filepath.Join(repo, ".standards.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("failed writing manifest: %v", err)
	}
	owner, name, err := resolveRepoCoordinates(ctx, repo)
	if err != nil {
		t.Fatalf("resolveRepoCoordinates failed: %v", err)
	}
	if owner != "acme" || name != "widget" {
		t.Errorf("expected acme/widget, got %s/%s", owner, name)
	}

	// Boundary: a directory with neither a manifest nor an origin remote nor an
	// <owner>/<repo> shaped path resolves to nothing rather than to a guessed owner.
	unresolvable := filepath.Join(t.TempDir(), "dev", "widget")
	if err := os.MkdirAll(unresolvable, 0o755); err != nil {
		t.Fatalf("failed creating fixture: %v", err)
	}
	if _, _, err := resolveRepoCoordinates(ctx, unresolvable); err == nil {
		t.Error("expected an unresolvable directory to produce an error")
	}
}

// =========================================================================
// worktree, gc and dogfood dispatch
// =========================================================================

func TestDispatchCommand_WorktreeArgumentHandling(t *testing.T) {
	// Negative: `list` implements no positional arguments.
	if err := dispatchCommand("worktree", []string{"list", "bogus"}); err == nil {
		t.Fatal("expected an error for a trailing worktree list argument")
	}
	// Negative: `prune` implements no positional arguments either.
	if err := dispatchCommand("worktree", []string{"prune", "bogus", "--path=" + t.TempDir()}); err == nil {
		t.Fatal("expected an error for a trailing worktree prune argument")
	}
	// Negative: create requires a task id.
	if err := dispatchCommand("worktree", []string{"create", "--path=" + t.TempDir()}); err == nil {
		t.Fatal("expected an error when the task id is missing")
	}
	// Negative: remove requires a task id.
	if err := dispatchCommand("worktree", []string{"remove", "--force", "--path=" + t.TempDir()}); err == nil {
		t.Fatal("expected an error when the task id is missing")
	}
	// Boundary: help prints usage without an error.
	if err := dispatchCommand("worktree", []string{"-h"}); err != nil {
		t.Fatalf("worktree -h failed: %v", err)
	}
}

func TestDispatchCommand_GCDefaultsToDryRun(t *testing.T) {
	tmpDir := t.TempDir()
	out, err := captureStdout(t, func() error {
		return dispatchCommand("gc", []string{"--path=" + tmpDir})
	})
	if err != nil {
		t.Fatalf("gc failed: %v", err)
	}
	if !strings.Contains(out, "DRY-RUN MODE") {
		t.Errorf("expected gc to default to a dry run, got:\n%s", out)
	}

	// Negative: an unknown flag is rejected.
	if err := dispatchCommand("gc", []string{"--not-a-flag"}); err == nil {
		t.Fatal("expected an error for an unknown gc flag")
	}
}

func TestDispatchCommand_Dogfood(t *testing.T) {
	// Boundary: help exits through flag.ErrHelp without running the suite.
	if err := dispatchCommand("dogfood", []string{"-h"}); err != nil && !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("dogfood -h failed: %v", err)
	}
	// Negative: an unknown flag is rejected before any repository is touched.
	if err := dispatchCommand("dogfood", []string{"--not-a-flag"}); err == nil {
		t.Fatal("expected an error for an unknown dogfood flag")
	}
}

// =========================================================================
// Home-directory resolution: explicit flags, hard errors, never the cwd
// =========================================================================

func TestResolveHomeSubdir_3D(t *testing.T) {
	// Positive: an explicit value is returned untouched.
	explicit, err := resolveHomeSubdir("/srv/skills", "--skills-dir", ".gemini")
	if err != nil || explicit != "/srv/skills" {
		t.Errorf("expected the explicit value, got %q (%v)", explicit, err)
	}

	// Positive: the default is anchored at the home directory.
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := resolveHomeSubdir("", "--dir", "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != filepath.Join(home, "dev") {
		t.Errorf("expected %s, got %s", filepath.Join(home, "dev"), got)
	}

	// Negative/boundary: without a home directory the caller gets an error naming the
	// flag, never a working-directory-relative path such as "dev".
	t.Setenv("HOME", "")
	missing, err := resolveHomeSubdir("", "--dir", "dev")
	if err == nil {
		t.Fatalf("expected an error without a home directory, got %q", missing)
	}
	if !strings.Contains(err.Error(), "--dir") {
		t.Errorf("expected the error to name the flag, got: %v", err)
	}
}

func TestDispatchCommand_AdoptAllMissingNeverFallsBackToCwd(t *testing.T) {
	// Negative: an unset HOME must fail loudly instead of scanning ./dev.
	t.Setenv("HOME", "")
	if err := dispatchCommand("adopt", []string{"--all-missing", "--dry-run"}); err == nil {
		t.Fatal("expected adopt --all-missing to fail without a home directory")
	}

	// Positive: --dev-dir makes the scan root injectable, so nothing escapes the sandbox.
	devDir := t.TempDir()
	out, err := captureStdout(t, func() error {
		return dispatchCommand("adopt", []string{"--all-missing", "--dry-run", "--dev-dir=" + devDir})
	})
	if err != nil {
		t.Fatalf("adopt --all-missing failed: %v", err)
	}
	if !strings.Contains(out, "Batch Repository Adoption") {
		t.Errorf("unexpected batch adoption output:\n%s", out)
	}
	entries, readErr := os.ReadDir(devDir)
	if readErr != nil {
		t.Fatalf("failed reading the scan root: %v", readErr)
	}
	if len(entries) != 0 {
		t.Errorf("expected an empty scan root to stay empty, found %d entries", len(entries))
	}
}

func TestDispatchCommand_HarvestSubcommandsAreInjectable(t *testing.T) {
	tmpDir := t.TempDir()

	// Positive: every home-anchored path has an explicit override flag.
	if err := dispatchCommand("harvest", []string{"workstation", "--dir=" + tmpDir}); err != nil {
		t.Fatalf("harvest workstation failed: %v", err)
	}
	if err := dispatchCommand("harvest", []string{"skills", "--gemini=" + tmpDir}); err != nil {
		t.Fatalf("harvest skills failed: %v", err)
	}
	if err := dispatchCommand("harvest", []string{"memory", "--brain=" + tmpDir}); err != nil {
		t.Fatalf("harvest memory failed: %v", err)
	}
	if err := dispatchCommand("harvest", []string{"ingest", "--bundle=" + tmpDir, "--skills-dir=" + tmpDir}); err == nil {
		t.Fatal("expected ingest of a directory without a manifest to fail")
	}

	// Negative: required flags are still required.
	if err := dispatchCommand("harvest", []string{"ingest"}); err == nil {
		t.Fatal("expected harvest ingest to require --bundle")
	}
	if err := dispatchCommand("harvest", []string{"bundle", "--name=ws"}); err == nil {
		t.Fatal("expected harvest bundle to require --out")
	}
	if err := dispatchCommand("harvest", []string{"onboard"}); err == nil {
		t.Fatal("expected harvest onboard to require --repo or --all-missing")
	}

	// Boundary: onboarding a single empty repository stays in dry-run mode.
	if err := dispatchCommand("harvest", []string{"onboard", "--repo=" + tmpDir}); err != nil {
		t.Fatalf("harvest onboard failed: %v", err)
	}
}

func TestDispatchCommand_HarvestFleetOutput(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return dispatchCommand("harvest", []string{"fleet"})
	})
	if err != nil {
		t.Fatalf("harvest fleet failed: %v", err)
	}
	if !strings.Contains(out, "Fleet Topology") || !strings.Contains(out, "cordanaLLM") {
		t.Errorf("unexpected harvest fleet output:\n%s", out)
	}
}
