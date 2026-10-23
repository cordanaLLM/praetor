package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// TestAudit_AdoptedDotGitHub_PassesWithoutScaffoldFiles_Planted verifies that adopting
// an organisation .github repository writes none of the community health files and passes
// audit without them. Red on base.
func TestAudit_AdoptedDotGitHub_PassesWithoutScaffoldFiles_Planted(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), ".github")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	env := testsupport.HermeticGitEnv(t)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"remote", "add", "origin", "https://github.com/acme/.github.git"},
	} {
		if out, err := runFixtureGit(t, root, env, args...); err != nil {
			t.Skipf("git %v failed in sandbox: %v (%s)", args, err, out)
		}
	}

	adoptArgs := []string{
		"--path=" + root,
		"--lock-source-root=" + source,
		"--profile=org-health",
	}
	if err := dispatchCommand("adopt", adoptArgs); err != nil {
		t.Fatalf("adopt failed: %v", err)
	}
	if _, err := runFixtureGit(t, root, env, "add", "-A"); err != nil {
		t.Fatalf("git add -A after adopt: %v", err)
	}

	for _, rel := range []string{"CONTRIBUTING.md", ".github/pull_request_template.md", "SECURITY.md"} {
		if _, err := os.Stat(filepath.Join(root, rel)); !os.IsNotExist(err) {
			t.Fatalf("adopted .github repository must not carry scaffold file %s", rel)
		}
	}

	auditOut, err := captureStdout(t, func() error {
		return dispatchCommand("audit", []string{"--config=" + filepath.Join(root, ".standards.yaml"), "--offline"})
	})
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	mustContain(t, auditOut, "Audit Summary: configured governance gates passed")
}
