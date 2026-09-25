package stress

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// hostileGlobalConfig writes a global git configuration that breaks committing: signing is
// demanded and the signing program does not exist. It stands in for what developers really
// carry — commit.gpgsign, a core.hooksPath, a template directory — any of which decided
// whether this package's stress tests could run at all.
func hostileGlobalConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gitconfig")
	body := "[commit]\n\tgpgsign = true\n[gpg]\n\tprogram = " +
		filepath.ToSlash(filepath.Join(dir, "no-such-signing-program")) + "\n" +
		"[core]\n\thooksPath = " + filepath.ToSlash(filepath.Join(dir, "no-such-hooks")) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write hostile config: %v", err)
	}
	return path
}

// TestSetupTestGitRepo_Negative_SurvivesAHostileGlobalConfig is the defect: the fixture ran
// git with the inherited environment, so a developer's own global configuration decided
// the result. The first half proves the configuration is genuinely hostile, so the second
// half is evidence rather than an assertion that cannot fail.
func TestSetupTestGitRepo_Negative_SurvivesAHostileGlobalConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", hostileGlobalConfig(t))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	inherited := t.TempDir()
	if out, err := commitWithInheritedEnv(inherited); err == nil {
		t.Fatalf("the fixture config must break an inherited-environment commit, it succeeded: %s", out)
	}

	hermetic := t.TempDir()
	setupTestGitRepo(t, hermetic)
	if _, err := os.Stat(filepath.Join(hermetic, ".git")); err != nil {
		t.Fatalf("the hermetic fixture must exist: %v", err)
	}
}

// commitWithInheritedEnv runs the fixture's own command sequence with whatever environment
// the process carries, which is what setupTestGitRepo used to do.
func commitWithInheritedEnv(dir string) (string, error) {
	for _, args := range [][]string{
		{"init"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@example.com"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return string(out), err
		}
	}
	return "", nil
}
