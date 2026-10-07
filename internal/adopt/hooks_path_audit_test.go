package adopt

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hooksPathRepo returns a hermetic checkout (hookRunnerRepo) with lefthook.yml and lefthook's hook
// installed in the managed hooks directory, so the core.hooksPath rule is the only thing a test
// can fail on.
func hooksPathRepo(t *testing.T) string {
	t.Helper()
	root, hooks := hookRunnerRepo(t, true, "")
	installTestHook(t, hooks, lefthookHookExcerpt, 0o700)
	return root
}

// setLocalHooksPath writes core.hooksPath into the checkout's own configuration. Git reads a
// backslash in a configuration value as an escape, so the value is written with forward slashes,
// which git for Windows accepts too.
func setLocalHooksPath(t *testing.T, root, value string) {
	t.Helper()
	mustWrite(t, filepath.Join(root, ".git", "config"), "[core]\n\thooksPath = "+filepath.ToSlash(value)+"\n")
}

// setGlobalHooksPath points git's global configuration at a file that sets core.hooksPath.
func setGlobalHooksPath(t *testing.T, value string) {
	t.Helper()
	global := filepath.Join(t.TempDir(), "gitconfig")
	mustWrite(t, global, "[core]\n\thooksPath = "+filepath.ToSlash(value)+"\n")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
}

// plantKnownRunnerHook installs lefthook's hook in dir and returns dir.
func plantKnownRunnerHook(t *testing.T, dir string) string {
	t.Helper()
	installTestHook(t, dir, lefthookHookExcerpt, 0o700)
	return dir
}

// TestAuditInstalledGitHook_Negative_HooksPathLeavesTheManagedDirectory (#61): core.hooksPath set
// to /dev/null, to a directory outside the repository, inside the working tree or inside the git
// directory beside the managed one fails, even where that directory holds a known runner's hook
// and the managed directory holds one too. Each failure names the value, its scope and origin, and
// how to unset it.
func TestAuditInstalledGitHook_Negative_HooksPathLeavesTheManagedDirectory(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value func(t *testing.T, root string) string
	}{
		{"dev null", func(*testing.T, string) string { return os.DevNull }},
		{"outside the repository with a known runner's hook", func(t *testing.T, _ string) string {
			return filepath.ToSlash(plantKnownRunnerHook(t, t.TempDir()))
		}},
		{"inside the working tree with a known runner's hook", func(t *testing.T, root string) string {
			plantKnownRunnerHook(t, filepath.Join(root, ".githooks"))
			return ".githooks"
		}},
		{"inside the git directory beside the managed one", func(t *testing.T, root string) string {
			plantKnownRunnerHook(t, filepath.Join(root, ".git", "custom-hooks"))
			return ".git/custom-hooks"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := hooksPathRepo(t)
			value := tc.value(t, root)
			setLocalHooksPath(t, root, value)
			line, err := AuditInstalledGitHook(t.Context(), root)
			if err == nil {
				t.Fatalf("core.hooksPath %q passed: %q", value, line)
			}
			for _, want := range []string{
				`[FAIL] core.hooksPath is set to "` + filepath.ToSlash(value) + `" (local scope, file:`,
				"git config --unset-all --local core.hooksPath",
				"instead of the managed hooks directory",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("err %v; want %q", err, want)
				}
			}
		})
	}
}

// TestAuditInstalledGitHook_Negative_HooksPathAtEveryScope (#61): the rule reads every scope git
// does, so a global value and one passed through the environment, as git -c passes it, fail too
// and are named by their scope.
func TestAuditInstalledGitHook_Negative_HooksPathAtEveryScope(t *testing.T) {
	t.Run("global", func(t *testing.T) {
		root := hooksPathRepo(t)
		outside := filepath.ToSlash(plantKnownRunnerHook(t, t.TempDir()))
		setGlobalHooksPath(t, outside)
		_, err := AuditInstalledGitHook(t.Context(), root)
		if err == nil || !strings.Contains(err.Error(), `"`+outside+`" (global scope, file:`) ||
			!strings.Contains(err.Error(), "git config --unset-all --global core.hooksPath") {
			t.Fatalf("global core.hooksPath: %v", err)
		}
	})
	t.Run("command", func(t *testing.T) {
		root := hooksPathRepo(t)
		t.Setenv("GIT_CONFIG_COUNT", "1")
		t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
		t.Setenv("GIT_CONFIG_VALUE_0", os.DevNull)
		_, err := AuditInstalledGitHook(t.Context(), root)
		if err == nil || !strings.Contains(err.Error(), `"`+os.DevNull+`" (command scope`) ||
			!strings.Contains(err.Error(), "GIT_CONFIG_PARAMETERS") {
			t.Fatalf("core.hooksPath passed through the environment: %v", err)
		}
	})
}

// TestAuditInstalledGitHook_Positive_HooksPathUnsetOrManaged (#61): an unset core.hooksPath passes,
// and so does one that names the managed hooks directory, absolute or relative to the working
// tree, at the local or the global scope.
func TestAuditInstalledGitHook_Positive_HooksPathUnsetOrManaged(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(t *testing.T, root string)
	}{
		{"unset", func(*testing.T, string) {}},
		{"local, absolute", func(t *testing.T, root string) {
			setLocalHooksPath(t, root, filepath.Join(root, ".git", "hooks"))
		}},
		{"local, relative", func(t *testing.T, root string) { setLocalHooksPath(t, root, ".git/hooks") }},
		{"global, absolute", func(t *testing.T, root string) { setGlobalHooksPath(t, filepath.Join(root, ".git", "hooks")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := hooksPathRepo(t)
			tc.set(t, root)
			line, err := AuditInstalledGitHook(t.Context(), root)
			if err != nil || !strings.Contains(line, "via lefthook, lefthook.yml) verified active.") {
				t.Fatalf("line %q, err %v", line, err)
			}
		})
	}
}

// TestAuditInstalledGitHook_Boundary_HooksPathEveryValueCounts (#61): a managed local value does
// not excuse a global one that leaves the repository, though git applies the local one: the
// failure names the global value alone. An empty value names no directory and fails.
func TestAuditInstalledGitHook_Boundary_HooksPathEveryValueCounts(t *testing.T) {
	root := hooksPathRepo(t)
	setLocalHooksPath(t, root, ".git/hooks")
	setGlobalHooksPath(t, os.DevNull)
	_, err := AuditInstalledGitHook(t.Context(), root)
	if err == nil || !strings.Contains(err.Error(), `"`+filepath.ToSlash(os.DevNull)+`" (global scope`) ||
		strings.Contains(err.Error(), "local scope") {
		t.Fatalf("managed local value beside an outside global one: %v", err)
	}

	empty := hooksPathRepo(t)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	mustWrite(t, filepath.Join(empty, ".git", "config"), "[core]\n\thooksPath =\n")
	if _, err := AuditInstalledGitHook(t.Context(), empty); err == nil || !strings.Contains(err.Error(), "core.hooksPath") {
		t.Fatalf("empty core.hooksPath: %v", err)
	}
}

// TestParseHooksPathSettings_Boundary covers the framing the reader refuses: output that does not
// end in NUL, a partial triple, and more values than the bound; nothing and exactly the bound read.
func TestParseHooksPathSettings_Boundary(t *testing.T) {
	triple := []byte("local\x00file:.git/config\x00.githooks\x00")
	if settings, err := parseHooksPathSettings(nil); err != nil || settings != nil {
		t.Fatalf("empty output: %v, %v", settings, err)
	}
	settings, err := parseHooksPathSettings(bytes.Repeat(triple, maxHooksPathSettings))
	if err != nil || len(settings) != maxHooksPathSettings || settings[0] != (hooksPathSetting{"local", "file:.git/config", ".githooks"}) {
		t.Fatalf("bound: %d settings, %v", len(settings), err)
	}
	for name, out := range map[string][]byte{
		"not NUL-terminated": bytes.TrimSuffix(triple, []byte{0}),
		"partial triple":     []byte("local\x00file:.git/config\x00"),
		"past the bound":     bytes.Repeat(triple, maxHooksPathSettings+1),
	} {
		if _, err := parseHooksPathSettings(out); err == nil {
			t.Fatalf("%s: parsed", name)
		}
	}
}
