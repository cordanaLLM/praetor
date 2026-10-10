// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// The engine launcher decides which binary judges a repository (#906). These tests run the real
// script against stub binaries that print their identity, in a PATH holding only the stubs and the
// utilities the script uses (hookToolbox).

const (
	enginePin      = "492a00f930e1"
	engineLauncher = "sh " + engineLauncherFile + " audit --offline"
)

// engineFixture is one repository directory with its stubs and engine cache.
type engineFixture struct {
	t     *testing.T
	work  string
	stubs string
	cache string
	log   string
}

func newEngineFixture(t *testing.T) *engineFixture {
	t.Helper()
	work := t.TempDir()
	return &engineFixture{t: t, work: work, stubs: t.TempDir(), cache: filepath.Join(t.TempDir(), "cache"), log: filepath.Join(work, "calls.log")}
}

// declare writes a workflow file declaring text.
func (f *engineFixture) declare(name, text string) {
	f.t.Helper()
	mustWrite(f.t, filepath.Join(f.work, ".github", "workflows", name), text)
}

// pathEngine installs the stubs a newer binary on PATH would be.
func (f *engineFixture) pathEngine() {
	f.t.Helper()
	writeStub(f.t, f.stubs, util.PraetorCLI, "echo \"path-engine $*\"\n")
}

// cached installs the engine of pin into the cache, as an earlier install left it.
func (f *engineFixture) cached(pin string) string {
	f.t.Helper()
	directory := filepath.Join(f.cache, "praetor", "engine", pin)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		f.t.Fatal(err)
	}
	writeStub(f.t, directory, "standardsctl", "echo \"pinned-engine $*\"\n")
	return directory
}

// goStub installs a go stub running body, which logs its call first.
func (f *engineFixture) goStub(body string) {
	f.t.Helper()
	writeStub(f.t, f.stubs, "go", "echo \"go $*\" >> '"+f.log+"'\n"+body)
}

// run starts the launcher with the cache and extra environment.
func (f *engineFixture) run(extra ...string) (string, int) {
	f.t.Helper()
	return f.runLine(engineLauncher, extra...)
}

// runLine is run with another command line.
func (f *engineFixture) runLine(line string, extra ...string) (string, int) {
	f.t.Helper()
	if os.PathSeparator == '\\' {
		f.t.Skip("POSIX shell stubs required: the toolbox links MSYS tools, which do not start from a link, and the stubs are shell scripts")
	}
	shellPath, err := lookShell()
	if err != nil {
		f.t.Skip(err.Error())
	}
	return runHookLineEnv(f.t, shellPath, line, f.stubs, f.work, append([]string{"XDG_CACHE_HOME=" + f.cache}, extra...))
}

func lookShell() (string, error) {
	return exec.LookPath("sh")
}

const installingGo = "mkdir -p \"$GOBIN\"\nprintf '#!/bin/sh\\necho \"installed-engine $*\"\\n' > \"$GOBIN/standardsctl\"\nchmod +x \"$GOBIN/standardsctl\"\n"

// Positive: a repository that pins an engine runs the pinned engine even when a different
// praetorctl is first on PATH, and the output names neither.
func TestEngineLauncher_Positive_PinnedEngineWinsOverPath(t *testing.T) {
	f := newEngineFixture(t)
	f.pathEngine()
	f.cached(enginePin)
	f.declare("standards-gate.yml", "jobs:\n  gate:\n    env:\n      PRAETOR_REF: "+enginePin+"\n")
	out, code := f.run()
	if code != 0 || strings.TrimSpace(out) != "pinned-engine audit --offline" {
		t.Fatalf("exit %d, output %q, want the pinned engine and nothing from PATH", code, out)
	}
}

// Positive: the pin is read whatever its spelling: quoted, with a trailing comment, in CRLF
// lines, as a shell assignment, or declared twice with one value.
func TestEngineLauncher_Positive_PinSpellings(t *testing.T) {
	for name, text := range map[string]string{
		"double quoted": "  PRAETOR_REF: \"" + enginePin + "\"\n",
		"single quoted": "  PRAETOR_REF: '" + enginePin + "'\n",
		"comment":       "  PRAETOR_REF: " + enginePin + "  # engine\n",
		"crlf":          "  PRAETOR_REF: " + enginePin + "\r\n",
		"assignment":    "        PRAETOR_REF=" + enginePin + "\n",
		"twice":         "  PRAETOR_REF: " + enginePin + "\n  PRAETOR_REF: " + enginePin + "\n",
	} {
		f := newEngineFixture(t)
		f.pathEngine()
		f.cached(enginePin)
		f.declare("gate.yml", "env:\n"+text)
		if out, code := f.run(); code != 0 || !strings.HasPrefix(out, "pinned-engine") {
			t.Errorf("%s: exit %d, output %q", name, code, out)
		}
	}
	f := newEngineFixture(t)
	f.cached("v1.2.3")
	f.declare("gate.yaml", "env:\n  PRAETOR_REF: v1.2.3\n")
	if out, code := f.run(); code != 0 || !strings.HasPrefix(out, "pinned-engine") {
		t.Errorf("release tag in a .yaml workflow: exit %d, output %q", code, out)
	}
}

// Positive: an engine that is not cached is installed once per pin with go install into a
// cache keyed by the pin, and later runs do not install again.
func TestEngineLauncher_Positive_InstallsOncePerPin(t *testing.T) {
	f := newEngineFixture(t)
	f.pathEngine()
	f.goStub(installingGo)
	f.declare("gate.yml", "env:\n  PRAETOR_REF: "+enginePin+"\n")
	for i := range 2 {
		if out, code := f.run(); code != 0 || !strings.Contains(out, "installed-engine audit --offline") || strings.Contains(out, "path-engine") {
			t.Fatalf("run %d: exit %d, output %q", i, code, out)
		}
	}
	want := "go install github.com/cordanaLLM/praetor/cmd/standardsctl@" + enginePin + "\n"
	if got := mustRead(t, f.log); got != want {
		t.Fatalf("go calls = %q, want exactly one: %q", got, want)
	}
	if !fileExists(filepath.Join(f.cache, "praetor", "engine", enginePin, "standardsctl")) {
		t.Fatal("the engine is not cached under its pin")
	}
	f.declare("gate.yml", "env:\n  PRAETOR_REF: 1111111aaaaaaa\n")
	f.run()
	if got := mustRead(t, f.log); strings.Count(got, "go install") != 2 || !strings.Contains(got, "@1111111aaaaaaa") {
		t.Fatalf("a new pin did not install its own engine: %q", got)
	}
}

// Boundary: jobs that start together on a cold cache (parallel pre-commit jobs, worktrees sharing
// the cache) all succeed, and the pin directory holds the engine and nothing else.
func TestEngineLauncher_Boundary_ConcurrentColdInstalls(t *testing.T) {
	f := newEngineFixture(t)
	f.pathEngine()
	f.goStub("sleep 1\n" + installingGo)
	f.declare("gate.yml", "env:\n  PRAETOR_REF: "+enginePin+"\n")
	job := "sh " + engineLauncherFile + " audit"
	line := "for n in 1 2 3 4; do (" + job + " >\"out$n\" 2>&1; echo $? >\"code$n\") & done; wait"
	if out, code := f.runLine(line); code != 0 {
		t.Fatalf("exit %d, output %q", code, out)
	}
	for _, n := range []string{"1", "2", "3", "4"} {
		out, code := mustRead(t, filepath.Join(f.work, "out"+n)), mustRead(t, filepath.Join(f.work, "code"+n))
		if strings.TrimSpace(code) != "0" || !strings.Contains(out, "installed-engine audit") {
			t.Errorf("job %s: exit %q, output %q", n, strings.TrimSpace(code), out)
		}
	}
	entries, err := os.ReadDir(filepath.Join(f.cache, "praetor", "engine", enginePin))
	if err != nil || len(entries) != 1 || entries[0].Name() != "standardsctl" {
		t.Fatalf("pin directory = %v (%v), want only standardsctl", entries, err)
	}
	left, err := filepath.Glob(filepath.Join(f.cache, "praetor", "engine", "*.tmp.*"))
	if err != nil || len(left) != 0 {
		t.Fatalf("scratch left behind: %v (%v)", left, err)
	}
}

// Negative: a pin that cannot be installed fails the hook with the pin, the cache path and the
// command that fixes it, and never runs the binary on PATH.
func TestEngineLauncher_Negative_UninstallablePinFailsClosed(t *testing.T) {
	f := newEngineFixture(t)
	f.pathEngine()
	f.goStub("exit 3\n")
	f.declare("gate.yml", "env:\n  PRAETOR_REF: "+enginePin+"\n")
	out, code := f.run()
	directory := filepath.Join(f.cache, "praetor", "engine", enginePin)
	for _, want := range []string{"PRAETOR_REF=" + enginePin, directory, "GOBIN=" + directory + " go install github.com/cordanaLLM/praetor/cmd/standardsctl@" + enginePin} {
		if code != 1 || !strings.Contains(out, want) {
			t.Fatalf("exit %d, output %q, want it to name %q", code, out, want)
		}
	}
	if strings.Contains(out, "path-engine") {
		t.Fatalf("the hook fell back to the binary on PATH: %q", out)
	}
	if fileExists(directory) {
		t.Fatalf("a failed install left %s behind", directory)
	}
}

// Negative: without a Go toolchain a pinned engine that is not cached cannot be installed, and
// the hook says so instead of running the binary on PATH.
func TestEngineLauncher_Negative_NoGoToolchainFailsClosed(t *testing.T) {
	f := newEngineFixture(t)
	f.pathEngine()
	f.declare("gate.yml", "env:\n  PRAETOR_REF: "+enginePin+"\n")
	out, code := f.run()
	if code != 1 || !strings.Contains(out, "Go toolchain is not on PATH") || !strings.Contains(out, enginePin) || strings.Contains(out, "path-engine") {
		t.Fatalf("exit %d, output %q", code, out)
	}
}

// Boundary: an install that outlives the timeout is abandoned and fails the hook.
func TestEngineLauncher_Boundary_InstallTimeout(t *testing.T) {
	f := newEngineFixture(t)
	f.goStub("sleep 30\n")
	f.declare("gate.yml", "env:\n  PRAETOR_REF: "+enginePin+"\n")
	start := time.Now()
	out, code := f.run("PRAETOR_ENGINE_INSTALL_TIMEOUT=1")
	if code != 1 || !strings.Contains(out, "timeout 1 seconds") || !strings.Contains(out, enginePin) {
		t.Fatalf("exit %d, output %q", code, out)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Second {
		t.Fatalf("the install ran %s past a 1 second timeout", elapsed)
	}
}

// Negative: a declaration that is no commit id or release tag is no pin. A branch name moves, a
// path walks out of the cache, an expression is not a value; each fails closed naming the file.
func TestEngineLauncher_Negative_UnusablePinsFailClosed(t *testing.T) {
	for _, bad := range []string{"main", "../escape", "${{ vars.REF }}", "abc123", strings.Repeat("a", 41), "", "-flag", "v1/../x", "v1", "v1.2"} {
		f := newEngineFixture(t)
		f.pathEngine()
		f.declare("gate.yml", "env:\n  PRAETOR_REF: "+bad+"\n")
		out, code := f.run()
		if code != 1 || !strings.Contains(out, "no usable pin") || !strings.Contains(out, "gate.yml") || strings.Contains(out, "path-engine") {
			t.Errorf("pin %q: exit %d, output %q", bad, code, out)
		}
	}
}

// Negative: two workflows pinning different engines pin none; the hook refuses to choose.
func TestEngineLauncher_Negative_ConflictingPinsFailClosed(t *testing.T) {
	f := newEngineFixture(t)
	f.pathEngine()
	f.declare("a.yml", "env:\n  PRAETOR_REF: "+enginePin+"\n")
	f.declare("b.yml", "env:\n  PRAETOR_REF: 1111111aaaaaaa\n")
	out, code := f.run()
	if code != 1 || !strings.Contains(out, "several PRAETOR_REF values") || !strings.Contains(out, "a.yml") || !strings.Contains(out, "b.yml") || strings.Contains(out, "path-engine") {
		t.Fatalf("exit %d, output %q", code, out)
	}
}

// Boundary: no declaration keeps today's behaviour, praetorctl before standardsctl on PATH, and
// states that it ran unpinned. A text that only mentions PRAETOR_REF declares nothing.
func TestEngineLauncher_Boundary_NoPinKeepsPathBehaviourAndSaysSo(t *testing.T) {
	f := newEngineFixture(t)
	writeStub(t, f.stubs, util.LegacyCLI, "echo \"legacy-engine $*\"\n")
	f.declare("gate.yml", "steps:\n  - run: echo \"PRAETOR_REF is not declared here\" # PRAETOR_REF: "+enginePin+"\n")
	out, code := f.run()
	if code != 0 || !strings.Contains(out, "legacy-engine audit --offline") || !strings.Contains(out, "no PRAETOR_REF pin") {
		t.Fatalf("legacy only: exit %d, output %q", code, out)
	}
	f.pathEngine()
	if out, code := f.run(); code != 0 || !strings.Contains(out, "path-engine audit --offline") || strings.Contains(out, "legacy-engine") {
		t.Fatalf("both names: exit %d, output %q", code, out)
	}
}

// Positive: uses refs of praetor-adopt and reusable workflows pin the engine too (#906).
func TestEngineLauncher_Positive_UsesRefPinsEngine(t *testing.T) {
	for _, tc := range []struct {
		name, content string
	}{
		{"action", "jobs:\n  gate:\n    steps:\n      - uses: cordanaLLM/praetor/.github/actions/praetor-adopt@" + enginePin + "\n"},
		{"workflow", "jobs:\n  call:\n    uses: cordanaLLM/praetor/.github/workflows/standards-gate.yml@" + enginePin + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newEngineFixture(t)
			f.pathEngine()
			f.cached(enginePin)
			f.declare("gate.yml", tc.content)
			out, code := f.run()
			if code != 0 || strings.TrimSpace(out) != "pinned-engine audit --offline" {
				t.Fatalf("exit %d, output %q, want the pinned engine", code, out)
			}
		})
	}
}

// Positive: an explicit PRAETOR_REF wins over a uses: ref with another pin value (#906).
func TestEngineLauncher_Positive_ExplicitPraetorRefWinsOverUsesRef(t *testing.T) {
	f := newEngineFixture(t)
	f.pathEngine()
	f.cached(enginePin)
	const usesPin = "7828da5640001111222233334444555566667777"
	f.cached(usesPin)
	f.declare("gate.yml", "env:\n  PRAETOR_REF: "+enginePin+"\njobs:\n  gate:\n    steps:\n      - uses: cordanaLLM/praetor/.github/actions/praetor-adopt@"+usesPin+"\n")
	out, code := f.run()
	if code != 0 || strings.TrimSpace(out) != "pinned-engine audit --offline" {
		t.Fatalf("exit %d, output %q, want explicit PRAETOR_REF to win", code, out)
	}
}

// Negative: conflicting uses: refs when no PRAETOR_REF is declared fail closed (#906).
func TestEngineLauncher_Negative_ConflictingUsesRefsFailClosed(t *testing.T) {
	f := newEngineFixture(t)
	f.pathEngine()
	f.declare("a.yml", "jobs:\n  gate:\n    steps:\n      - uses: cordanaLLM/praetor/.github/actions/praetor-adopt@"+enginePin+"\n")
	f.declare("b.yml", "jobs:\n  gate:\n    steps:\n      - uses: cordanaLLM/praetor/.github/actions/praetor-adopt@1111111aaaaaaa\n")
	out, code := f.run()
	if code != 1 || !strings.Contains(out, "several PRAETOR_REF values") || strings.Contains(out, "path-engine") {
		t.Fatalf("exit %d, output %q, want conflicting uses refs to fail closed", code, out)
	}
}

// Boundary: when an explicit PRAETOR_REF matches the uses: ref, it succeeds (#906).
func TestEngineLauncher_Boundary_ExplicitPraetorRefSameAsUsesRef(t *testing.T) {
	f := newEngineFixture(t)
	f.pathEngine()
	f.cached(enginePin)
	f.declare("gate.yml", "env:\n  PRAETOR_REF: "+enginePin+"\njobs:\n  gate:\n    steps:\n      - uses: cordanaLLM/praetor/.github/actions/praetor-adopt@"+enginePin+"\n")
	out, code := f.run()
	if code != 0 || strings.TrimSpace(out) != "pinned-engine audit --offline" {
		t.Fatalf("exit %d, output %q, want same pin to succeed", code, out)
	}
}

// Boundary: a non-pin uses ref such as @main is treated as unpinned (falling back to PATH with notice).
func TestEngineLauncher_Boundary_NonPinUsesRefRunsOnPath(t *testing.T) {
	f := newEngineFixture(t)
	f.pathEngine()
	f.declare("gate.yml", "jobs:\n  gate:\n    steps:\n      - uses: cordanaLLM/praetor/.github/actions/praetor-adopt@main\n")
	out, code := f.run()
	if code != 0 || !strings.Contains(out, "path-engine audit --offline") || !strings.Contains(out, "no PRAETOR_REF pin") {
		t.Fatalf("exit %d, output %q, want fallback to PATH engine with stderr notice", code, out)
	}
}

// Negative: an unreadable workflow file fails closed and names the file.
func TestEngineLauncher_Negative_UnreadableWorkflowFailsClosed(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell stubs required: the toolbox links MSYS tools, which do not start from a link, and the stubs are shell scripts")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores read permissions")
	}
	f := newEngineFixture(t)
	f.pathEngine()
	f.declare("unreadable.yml", "env:\n  PRAETOR_REF: "+enginePin+"\n")
	path := filepath.Join(f.work, ".github", "workflows", "unreadable.yml")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if chmodErr := os.Chmod(path, 0o644); chmodErr != nil && !os.IsNotExist(chmodErr) {
			t.Log(chmodErr)
		}
	})
	out, code := f.run()
	if code != 1 || !strings.Contains(out, "unreadable.yml is not readable") {
		t.Fatalf("exit %d, output %q, want refusal naming the unreadable file", code, out)
	}
}

// Positive: --print-path prints only the path of the resolved binary and exits 0.
func TestEngineLauncher_Positive_PrintPath(t *testing.T) {
	f := newEngineFixture(t)
	f.cached(enginePin)
	f.declare("gate.yml", "env:\n  PRAETOR_REF: "+enginePin+"\n")
	wantPinned := filepath.Join(f.cache, "praetor", "engine", enginePin, "standardsctl")
	out, code := f.runLine("sh " + engineLauncherFile + " --print-path")
	if code != 0 || strings.TrimSpace(out) != wantPinned {
		t.Fatalf("pinned: exit %d, output %q, want %q", code, out, wantPinned)
	}

	fUnpinned := newEngineFixture(t)
	fUnpinned.pathEngine()
	wantPath := filepath.Join(fUnpinned.stubs, util.PraetorCLI)
	out, code = fUnpinned.runLine("sh " + engineLauncherFile + " --print-path")
	if code != 0 || strings.TrimSpace(out) != wantPath {
		t.Fatalf("unpinned: exit %d, output %q, want %q", code, out, wantPath)
	}
}

// Negative: --print-path with an unusable pin fails closed with exit 1.
func TestEngineLauncher_Negative_PrintPathFailsClosedOnUnusablePin(t *testing.T) {
	f := newEngineFixture(t)
	f.declare("gate.yml", "env:\n  PRAETOR_REF: main\n")
	out, code := f.runLine("sh " + engineLauncherFile + " --print-path")
	if code != 1 || !strings.Contains(out, "no usable pin") {
		t.Fatalf("exit %d, output %q, want exit 1", code, out)
	}
}

// Boundary: timeout of 0 or invalid integer fails closed.
func TestEngineLauncher_Boundary_InstallTimeoutZeroFailsClosed(t *testing.T) {
	f := newEngineFixture(t)
	f.pathEngine()
	f.declare("gate.yml", "env:\n  PRAETOR_REF: "+enginePin+"\n")
	for _, bad := range []string{"0", "00", "-1", "abc"} {
		out, code := f.run("PRAETOR_ENGINE_INSTALL_TIMEOUT=" + bad)
		if code != 1 || !strings.Contains(out, "must be an integer >= 1") {
			t.Fatalf("timeout %q: exit %d, output %q", bad, code, out)
		}
	}
}

// Boundary: a successful cold install leaves no orphaned sleep watchdog process behind (#906).
func TestEngineLauncher_Boundary_NoSleepSurvivesInstall(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX shell stubs required: the toolbox links MSYS tools, which do not start from a link, and the stubs are shell scripts")
	}
	f := newEngineFixture(t)
	f.goStub(installingGo)
	f.declare("gate.yml", "env:\n  PRAETOR_REF: "+enginePin+"\n")
	out, code := f.run("PRAETOR_ENGINE_INSTALL_TIMEOUT=299")
	if code != 0 || !strings.Contains(out, "installed-engine audit --offline") {
		t.Fatalf("exit %d, output %q", code, out)
	}
	// Check that no sleep process with timeout 299 survived.
	pgrep, err := exec.LookPath("pgrep")
	if err != nil {
		t.Fatalf("pgrep is required: %v", err)
	}
	pgrepOut, pgrepErr := exec.CommandContext(t.Context(), pgrep, "-g", strconv.Itoa(currentProcessGroup()), "-f", "sleep 299").Output()
	if pgrepErr == nil && len(strings.TrimSpace(string(pgrepOut))) > 0 {
		t.Fatalf("orphaned sleep process survived install: %s", pgrepOut)
	}
}

// Negative: no pin and no binary blocks, naming both names; the exit status of an engine is the
// hook's own.
func TestEngineLauncher_Negative_NothingToRunAndEngineStatus(t *testing.T) {
	f := newEngineFixture(t)
	out, code := f.run()
	if code != 1 || !strings.Contains(out, util.PraetorCLI) || !strings.Contains(out, util.LegacyCLI) {
		t.Fatalf("exit %d, output %q", code, out)
	}
	writeStub(t, f.stubs, util.PraetorCLI, "exit 7\n")
	if _, code := f.run(); code != 7 {
		t.Fatalf("an engine exiting 7 gave the hook status %d", code)
	}
}

// Boundary: a checkout that gave the launcher CRLF line endings (core.autocrlf) still runs it.
func TestEngineLauncher_Boundary_CRLFCheckoutRuns(t *testing.T) {
	f := newEngineFixture(t)
	f.cached(enginePin)
	f.declare("gate.yml", "env:\n  PRAETOR_REF: "+enginePin+"\n")
	mustWrite(t, filepath.Join(f.work, filepath.FromSlash(engineLauncherFile)), strings.ReplaceAll(engineLauncherScript, "\n", "\r\n"))
	if out, code := f.run(); code != 0 || !strings.HasPrefix(out, "pinned-engine") {
		t.Fatalf("exit %d, output %q", code, out)
	}
}

// The launcher names the module and the resolution the rest of Praetor spells once (HISS-19): the
// module path of go.mod, and util.ShellCLIResolution.
func TestEngineLauncher_Positive_AgreesWithTheModuleAndTheResolution(t *testing.T) {
	gomod := mustRead(t, filepath.Join("..", "..", "go.mod"))
	module := regexp.MustCompile(`(?m)^module\s+(\S+)`).FindStringSubmatch(gomod)
	if module == nil || !strings.Contains(engineLauncherScript, "\nmodule="+module[1]+" #\n") {
		t.Fatalf("the launcher does not name the module %v of go.mod", module)
	}
	if !strings.Contains(engineLauncherScript, util.ShellCLIResolution) {
		t.Errorf("the launcher does not resolve a PATH binary as util.ShellCLIResolution does: %s", util.ShellCLIResolution)
	}
	if _, err := os.Stat(filepath.Join("..", "..", "cmd", "standardsctl")); err != nil || !strings.Contains(engineLauncherScript, "package=cmd/standardsctl") {
		t.Errorf("the launcher installs a package that does not exist: %v", err)
	}
	for number, line := range strings.Split(strings.TrimSuffix(engineLauncherScript, "\n"), "\n")[1:] {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") && !strings.HasSuffix(line, " #") {
			t.Errorf("line %d does not end in a comment sign, so a CRLF checkout breaks it: %q", number+2, line)
		}
	}
}

// The generated hooks hold no PATH resolution of their own: every governance job and the fallback
// hook run the launcher.
func TestEngineLauncher_Positive_EveryGeneratedHookRunsIt(t *testing.T) {
	rendering := buildLefthookYAMLFor(lefthookShape{languages: lefthookJobLanguages, reuse: true}, true) + buildFallbackPreCommitScript()
	if strings.Contains(rendering, "praetor_cli") || strings.Contains(rendering, "command -v "+util.PraetorCLI) {
		t.Error("a generated hook still resolves the engine from PATH itself")
	}
	if !strings.Contains(rendering, "sh "+engineLauncherFile+" compile-context --verify") || !strings.Contains(rendering, "sh "+engineLauncherFile+" audit") {
		t.Error("the generated hooks do not run the launcher")
	}
}

// The digest set records the current launcher (HISS-20), so a later change still refreshes it.
func TestPriorEngineLauncherDigests_Boundary_CurrentTextRecorded(t *testing.T) {
	digest, _, err := util.CanonicalTextDigest([]byte(engineLauncherScript))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := priorEngineLauncherDigests[digest]; !ok {
		t.Fatalf("the current launcher (%s) is not recorded in priorEngineLauncherDigests", digest)
	}
}

// adoptEngineLauncher runs adoption over repoPath and returns its report.
func adoptEngineLauncher(t *testing.T, repoPath string, force bool) *AdoptReport {
	t.Helper()
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: force})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	assertNoIssues(t, rep)
	return rep
}

// Positive: adoption installs the launcher beside lefthook.yml and a re-run keeps it.
// Boundary: an unedited earlier text is refreshed without --force, in its own line endings.
func TestAdopt_EngineLauncher_InstalledAndRefreshed(t *testing.T) {
	repoPath := newTestRepo(t, "engine-launcher")
	launcher := filepath.Join(repoPath, filepath.FromSlash(engineLauncherFile))
	if rep := adoptEngineLauncher(t, repoPath, false); !hasAction(rep, engineLauncherFile, actionCreate) || mustRead(t, launcher) != engineLauncherScript {
		t.Fatalf("the launcher was not installed: %+v", rep.ActionDetails)
	}
	if rep := adoptEngineLauncher(t, repoPath, false); hasAction(rep, engineLauncherFile, actionReplace) || mustRead(t, launcher) != engineLauncherScript {
		t.Fatalf("a re-run changed the launcher: %+v", rep.ActionDetails)
	}

	earlier := "#!/bin/sh\necho earlier launcher\n"
	digest, _, err := util.CanonicalTextDigest([]byte(earlier))
	if err != nil {
		t.Fatal(err)
	}
	priorEngineLauncherDigests[digest] = "test earlier text"
	t.Cleanup(func() { delete(priorEngineLauncherDigests, digest) })
	mustWrite(t, launcher, strings.ReplaceAll(earlier, "\n", "\r\n"))
	rep := adoptEngineLauncher(t, repoPath, false)
	if !hasAction(rep, engineLauncherFile, actionReconcile) || mustRead(t, launcher) != strings.ReplaceAll(engineLauncherScript, "\n", "\r\n") {
		t.Fatalf("an earlier CRLF launcher was not refreshed in its own line endings: %+v", rep.ActionDetails)
	}
}

// Negative: a hand-edited launcher is the repository's and is kept, --force included.
func TestAdopt_EngineLauncher_EditedCopyKept(t *testing.T) {
	repoPath := newTestRepo(t, "engine-launcher-edited")
	launcher := filepath.Join(repoPath, filepath.FromSlash(engineLauncherFile))
	edited := engineLauncherScript + "# repository note\n"
	mustWrite(t, launcher, edited)
	for _, force := range []bool{false, true} {
		adoptEngineLauncher(t, repoPath, force)
		if got := mustRead(t, launcher); got != edited {
			t.Fatalf("force=%v replaced a hand-edited launcher", force)
		}
	}
}
