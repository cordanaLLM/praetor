package adopt

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
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

// setLocalHooksPath appends core.hooksPath to the checkout's own configuration, keeping what it
// holds, such as the origin remote. Git reads a backslash in a configuration value as an escape,
// so the value is written with forward slashes, which git for Windows accepts too.
func setLocalHooksPath(t *testing.T, root, value string) {
	t.Helper()
	path := filepath.Join(root, ".git", "config")
	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read %s: %v", path, err)
	}
	mustWrite(t, path, string(existing)+"[core]\n\thooksPath = "+filepath.ToSlash(value)+"\n")
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
	if err != nil || len(settings) != maxHooksPathSettings ||
		settings[0] != (hooksPathSetting{scope: "local", origin: "file:.git/config", value: ".githooks"}) {
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

// writeIncludedHooksPath writes a file that sets core.hooksPath to value, and a local configuration
// that includes it with section, include or includeIf "onbranch:main" (the test checkout's HEAD).
// It returns the included file.
func writeIncludedHooksPath(t *testing.T, root, section, value string) string {
	t.Helper()
	included := filepath.Join(t.TempDir(), "hooks.inc")
	mustWrite(t, included, "[core]\n\thooksPath = "+filepath.ToSlash(value)+"\n")
	mustWrite(t, filepath.Join(root, ".git", "config"), section+"\n\tpath = "+filepath.ToSlash(included)+"\n")
	return included
}

// TestAuditInstalledGitHook_Negative_HooksPathFromAnIncludedFile (#61): a value an include.path or
// includeIf file sets fails like any other, and the failure names that file and the git config
// --file command that removes the value from it. git config --unset-all --local, the hint for a
// value of the local file itself, edits .git/config only and fails on an included value. Running
// the printed command makes the audit pass.
func TestAuditInstalledGitHook_Negative_HooksPathFromAnIncludedFile(t *testing.T) {
	for name, section := range map[string]string{
		"include.path":            "[include]",
		"includeIf onbranch:main": `[includeIf "onbranch:main"]`,
	} {
		t.Run(name, func(t *testing.T) {
			root := hooksPathRepo(t)
			included := writeIncludedHooksPath(t, root, section, os.DevNull)
			_, err := AuditInstalledGitHook(t.Context(), root)
			if err == nil {
				t.Fatal("an included core.hooksPath passed")
			}
			fix := `git config --file "` + filepath.Clean(included) + `" --unset-all core.hooksPath`
			for _, want := range []string{`set in "` + filepath.Clean(included) + `"`, "a file git config --local does not edit", fix} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("err %v; want %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "--unset-all --local") || strings.Contains(err.Error(), "--reset-hooks-path") {
				t.Fatalf("an included value got a hint that cannot remove it: %v", err)
			}
			if _, err := util.RunGit(t.Context(), root, "config", "--local", "--unset-all", hooksPathKey); err == nil {
				t.Fatal("git config --unset-all --local removed an included value")
			}
			if _, err := util.RunGit(t.Context(), root, "config", "--file", included, "--unset-all", hooksPathKey); err != nil {
				t.Fatalf("the printed fix: %v", err)
			}
			if line, err := AuditInstalledGitHook(t.Context(), root); err != nil {
				t.Fatalf("after the printed fix: %q, %v", line, err)
			}
		})
	}
}

// TestAuditHooksPath_Boundary_LefthookResetHint (#61): where lefthook runs the hooks (lefthook.yml),
// the finding names lefthook install --reset-hooks-path, which unsets a local or a global value
// from the scope's own file (lefthook 2.1.14 unsetHooksPathConfig). It is not named for a value
// that command does not remove, an included or a command-line one, nor without lefthook.yml.
func TestAuditHooksPath_Boundary_LefthookResetHint(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(t *testing.T, root string)
		want bool
	}{
		{"local", func(t *testing.T, root string) { setLocalHooksPath(t, root, ".githooks") }, true},
		{"global", func(t *testing.T, _ string) { setGlobalHooksPath(t, os.DevNull) }, true},
		{"included", func(t *testing.T, root string) { writeIncludedHooksPath(t, root, "[include]", os.DevNull) }, false},
		{"command", func(t *testing.T, _ string) {
			t.Setenv("GIT_CONFIG_COUNT", "1")
			t.Setenv("GIT_CONFIG_KEY_0", hooksPathKey)
			t.Setenv("GIT_CONFIG_VALUE_0", os.DevNull)
		}, false},
		{"local without lefthook.yml", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, lefthookFile)); err != nil {
				t.Fatal(err)
			}
			setLocalHooksPath(t, root, ".githooks")
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := hooksPathRepo(t)
			tc.set(t, root)
			err := auditHooksPath(t.Context(), root)
			if err == nil {
				t.Fatal("core.hooksPath outside the managed directory passed")
			}
			if got := strings.Contains(err.Error(), "'lefthook install --reset-hooks-path'"); got != tc.want {
				t.Fatalf("reset hint named = %v, want %v: %v", got, tc.want, err)
			}
		})
	}
}

// TestMarkOutsideScopeFile_Boundary: settings the reads of the scopes' own files lack are marked,
// equal ones matched one for one, so a value both set directly and included is marked once.
func TestMarkOutsideScopeFile_Boundary(t *testing.T) {
	direct := hooksPathSetting{scope: "local", origin: "file:.git/config", value: ".husky"}
	other := hooksPathSetting{scope: "local", origin: "file:.git/hooks.inc", value: os.DevNull}
	for name, tc := range map[string]struct {
		direct []hooksPathSetting
		want   []bool
	}{
		"none direct":        {nil, []bool{true, true, true}},
		"one of two equal":   {[]hooksPathSetting{direct}, []bool{false, true, true}},
		"every value direct": {[]hooksPathSetting{direct, direct, other}, []bool{false, false, false}},
	} {
		settings := []hooksPathSetting{direct, direct, other}
		markOutsideScopeFile(settings, tc.direct)
		for i, setting := range settings {
			if setting.outsideScopeFile != tc.want[i] {
				t.Fatalf("%s: setting %d outsideScopeFile = %v, want %v", name, i, setting.outsideScopeFile, tc.want[i])
			}
		}
	}
}

// symlinkedRoot returns root reached through a symlink to its parent directory, the way macOS
// reaches its TMPDIR through /var -> /private/var, so Linux reproduces that case. Git resolves the
// symlink in the common directory it reports for the returned spelling; the test is skipped where
// the platform refuses to create a symlink.
func symlinkedRoot(t *testing.T, root string) string {
	t.Helper()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Dir(root), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	linked := filepath.Join(link, filepath.Base(root))
	managed, err := managedHooksDir(t.Context(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if managed == filepath.Join(linked, ".git", "hooks") {
		t.Fatalf("git reported the managed hooks directory %s through the symlink; the test would prove nothing", managed)
	}
	return linked
}

// TestAuditHooksPath_Boundary_SymlinkedRoot (#61): under a root reached through a symlink, a value
// naming the managed hooks directory passes whether or not the directory exists yet, relative or
// absolute through the symlink. A value naming another directory under that root fails, existing
// or not, and so does a .. value that a cleaned spelling puts in the managed directory while the
// symlink before the .. sends git elsewhere. A .. value the operating system resolves to the
// managed directory passes.
func TestAuditHooksPath_Boundary_SymlinkedRoot(t *testing.T) {
	for _, tc := range []struct {
		name    string
		created bool
		value   func(t *testing.T, root, linked string) string
		pass    bool
	}{
		{"relative, managed directory exists", true, func(*testing.T, string, string) string { return ".git/hooks" }, true},
		{"relative, managed directory not yet created", false, func(*testing.T, string, string) string { return ".git/hooks" }, true},
		{"absolute through the symlink, not yet created", false, func(_ *testing.T, _, linked string) string {
			return filepath.Join(linked, ".git", "hooks")
		}, true},
		{"relative, another directory not yet created", false, func(*testing.T, string, string) string { return ".githooks" }, false},
		{"relative, another existing directory", true, func(t *testing.T, root, _ string) string {
			plantKnownRunnerHook(t, filepath.Join(root, ".git", "custom-hooks"))
			return ".git/custom-hooks"
		}, false},
		{".. after a symlink that leaves the root", true, func(t *testing.T, root, _ string) string {
			elsewhere := t.TempDir()
			plantKnownRunnerHook(t, filepath.Join(elsewhere, ".git", "hooks"))
			if err := os.Symlink(filepath.Join(elsewhere, ".git"), filepath.Join(root, "escape")); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			return "escape/../.git/hooks"
		}, false},
		{".. inside the root, managed directory exists", true, func(t *testing.T, root, _ string) string {
			if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
				t.Fatal(err)
			}
			return "sub/../.git/hooks"
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := hooksPathRepo(t)
			linked := symlinkedRoot(t, root)
			if !tc.created {
				if err := os.RemoveAll(filepath.Join(root, ".git", "hooks")); err != nil {
					t.Fatal(err)
				}
			}
			value := tc.value(t, root, linked)
			setLocalHooksPath(t, root, value)
			err := auditHooksPath(t.Context(), linked)
			var finding *hooksPathFindingError
			switch {
			case tc.pass && err != nil:
				t.Fatalf("core.hooksPath %q under a symlinked root refused: %v", value, err)
			case !tc.pass && !errors.As(err, &finding):
				t.Fatalf("core.hooksPath %q under a symlinked root: want a finding, got %v", value, err)
			}
		})
	}
}

// xdgGlobalHooksPath sets core.hooksPath to value in $XDG_CONFIG_HOME/git/config of a fresh home,
// with a ~/.gitconfig beside it when gitconfig is true, and unsets GIT_CONFIG_GLOBAL so git reads
// both files. It returns the XDG file.
func xdgGlobalHooksPath(t *testing.T, value string, gitconfig bool) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	if err := os.Unsetenv("GIT_CONFIG_GLOBAL"); err != nil {
		t.Fatal(err)
	}
	xdg := filepath.Join(home, ".config", "git", "config")
	mustWrite(t, xdg, "[core]\n\thooksPath = "+filepath.ToSlash(value)+"\n")
	if gitconfig {
		mustWrite(t, filepath.Join(home, ".gitconfig"), "[user]\n\tname = praetor test\n")
	}
	return xdg
}

// TestAuditInstalledGitHook_Negative_HooksPathFromXDGBesideGitconfig (#61): git reads a global
// value from $XDG_CONFIG_HOME/git/config while git config --global writes ~/.gitconfig when it
// exists, so git config --unset-all --global, and lefthook install --reset-hooks-path with it,
// leave the value in place. The failure names the XDG file and the git config --file command, and
// running that command makes the audit pass.
func TestAuditInstalledGitHook_Negative_HooksPathFromXDGBesideGitconfig(t *testing.T) {
	root := hooksPathRepo(t)
	xdg := filepath.Clean(xdgGlobalHooksPath(t, os.DevNull, true))
	_, err := AuditInstalledGitHook(t.Context(), root)
	if err == nil {
		t.Fatal("a global core.hooksPath from the XDG file passed")
	}
	fix := `git config --file "` + xdg + `" --unset-all core.hooksPath`
	for _, want := range []string{`(global scope, file:`, `set in "` + xdg + `"`, "a file git config --global does not edit", fix} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err %v; want %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "--unset-all --global") || strings.Contains(err.Error(), "--reset-hooks-path") {
		t.Fatalf("an XDG value beside ~/.gitconfig got a hint that cannot remove it: %v", err)
	}
	if _, err := util.RunGit(t.Context(), root, "config", "--global", "--unset-all", hooksPathKey); err == nil {
		t.Fatal("git config --unset-all --global removed the XDG value beside ~/.gitconfig")
	}
	if _, err := util.RunGit(t.Context(), root, "config", "--file", xdg, "--unset-all", hooksPathKey); err != nil {
		t.Fatalf("the printed fix: %v", err)
	}
	if line, err := AuditInstalledGitHook(t.Context(), root); err != nil {
		t.Fatalf("after the printed fix: %q, %v", line, err)
	}
}

// TestAuditInstalledGitHook_Boundary_HooksPathFromXDGAlone (#61): without ~/.gitconfig, git config
// --global writes the XDG file, so the failure names git config --unset-all --global and lefthook
// install --reset-hooks-path, and the first makes the audit pass.
func TestAuditInstalledGitHook_Boundary_HooksPathFromXDGAlone(t *testing.T) {
	root := hooksPathRepo(t)
	xdgGlobalHooksPath(t, os.DevNull, false)
	_, err := AuditInstalledGitHook(t.Context(), root)
	if err == nil || !strings.Contains(err.Error(), "git config --unset-all --global core.hooksPath") ||
		!strings.Contains(err.Error(), "'lefthook install --reset-hooks-path'") || strings.Contains(err.Error(), "--file") {
		t.Fatalf("XDG value without ~/.gitconfig: %v", err)
	}
	if _, err := util.RunGit(t.Context(), root, "config", "--global", "--unset-all", hooksPathKey); err != nil {
		t.Fatalf("the printed fix: %v", err)
	}
	if line, err := AuditInstalledGitHook(t.Context(), root); err != nil {
		t.Fatalf("after the printed fix: %q, %v", line, err)
	}
}

// TestAuditHooksPath_Negative_ReadFailureIsNoFinding (#61): a value git cannot expand fails the
// read, here a ~user value for a user that does not exist, and the audit returns that failure, not
// a finding that claims a value leaves the managed directory.
func TestAuditHooksPath_Negative_ReadFailureIsNoFinding(t *testing.T) {
	root := hooksPathRepo(t)
	setGlobalHooksPath(t, unexpandableHooksPath)
	err := auditHooksPath(t.Context(), root)
	var finding *hooksPathFindingError
	if err == nil || errors.As(err, &finding) || !strings.Contains(err.Error(), "--get-all core.hooksPath") {
		t.Fatalf("unreadable core.hooksPath: want a read failure, got %v", err)
	}
	setLocalHooksPath(t, root, ".githooks")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	if err := auditHooksPath(t.Context(), root); !errors.As(err, &finding) {
		t.Fatalf("refused core.hooksPath: want a finding, got %v", err)
	}
}

// unexpandableHooksPath is a core.hooksPath value git config --type=path cannot expand: the home
// directory of a user that does not exist.
const unexpandableHooksPath = "~praetor-no-such-user/hooks"
