// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apicompat

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// gateRunTimeout bounds one gate run in a test (HISS-02).
const gateRunTimeout = 3 * time.Minute

// stubCheckerSource stands in for go-apidiff. Like go-apidiff it refuses a dirty tree at
// --repo-path. It records every module run as "<module dir> <old> <new>", the directory relative
// to --repo-path, followed by " vendor" when the new commit has a root vendor entry, and answers
// from the module it runs in: the gate's canary module is incompatible, as a working checker
// reports it, unless STUB_BLIND is set; any other module answers from its stub-verdict file in
// the new commit.
const stubCheckerSource = `package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func git(root string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	return string(out), err
}

func main() {
	gomod, _ := os.ReadFile("go.mod")
	if strings.Contains(string(gomod), "apicompat.invalid/canary") {
		if os.Getenv("STUB_BLIND") != "" {
			os.Exit(0)
		}
		os.Exit(1)
	}
	wd, _ := os.Getwd()
	root := strings.TrimPrefix(os.Args[len(os.Args)-1], "--repo-path=")
	rel, err := filepath.Rel(root, wd)
	if err != nil || len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "stub: unexpected invocation", os.Args, err)
		os.Exit(3)
	}
	if status, err := git(root, "status", "--porcelain"); err != nil || status != "" {
		fmt.Fprintln(os.Stderr, "stub: current git tree is dirty", status, err)
		os.Exit(2)
	}
	rel = filepath.ToSlash(rel)
	record := rel + " " + os.Args[1] + " " + os.Args[2]
	if _, err := git(root, "cat-file", "-e", os.Args[2]+":vendor"); err == nil {
		record += " vendor"
	}
	log, err := os.OpenFile(os.Getenv("STUB_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(3)
	}
	fmt.Fprintln(log, record)
	log.Close()
	verdictPath := "stub-verdict"
	if rel != "." {
		verdictPath = rel + "/stub-verdict"
	}
	verdict, _ := git(root, "show", os.Args[2]+":"+verdictPath)
	switch strings.TrimSpace(verdict) {
	case "incompatible":
		fmt.Println("- Connect: removed")
		os.Exit(1)
	case "error":
		fmt.Fprintln(os.Stderr, "failed to load packages")
		os.Exit(2)
	case "usage":
		fmt.Fprintln(os.Stderr, "Error: accepts 1 or 2 args, received 3")
	}
}
`

// gateHarness is the embedded gate and the stub checker, built once per test.
type gateHarness struct {
	gate    string
	checker string
	log     string
	env     []string
}

func newGateHarness(t *testing.T) gateHarness {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	source, err := Read(GateFile)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	h := gateHarness{
		gate:    testsupport.BuildExecutable(t, bin, "gate", string(source)),
		checker: testsupport.BuildExecutable(t, bin, "checker", stubCheckerSource),
		log:     filepath.Join(t.TempDir(), "checker.log"),
	}
	h.env = append(testsupport.HermeticGitEnv(t), "STUB_LOG="+h.log, "GITHUB_ACTIONS=", "STUB_BLIND=")
	return h
}

// gateRun is one gate run's exit status and output.
type gateRun struct {
	status         int
	stdout, stderr string
}

// run runs the gate in dir with the stub checker and extra environment entries.
func (h gateHarness) run(t *testing.T, dir string, env []string, args ...string) gateRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), gateRunTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, h.gate, append([]string{"-checker=" + h.checker}, args...)...)
	command.Dir = dir
	command.Env = append(slices.Clone(h.env), env...)
	var stdout, stderr strings.Builder
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	run := gateRun{stdout: stdout.String(), stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exit):
		run.status = exit.ExitCode()
	default:
		t.Fatalf("run the gate: %v", err)
	}
	return run
}

// checked returns the module runs the stub recorded, in order.
func (h gateHarness) checked(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(h.log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n")), "\n")
}

// fixture is a git repository a test commits Go modules into.
type fixture struct {
	t   *testing.T
	dir string
	env []string
}

func newFixture(t *testing.T, h gateHarness) *fixture {
	t.Helper()
	f := &fixture{t: t, dir: t.TempDir(), env: h.env}
	f.git("init", "--quiet")
	return f
}

func (f *fixture) git(args ...string) string {
	f.t.Helper()
	ctx, err := util.WithCommandEnvironment(f.t.Context(), f.env)
	if err != nil {
		f.t.Fatal(err)
	}
	out, err := util.RunGit(ctx, f.dir, args...)
	if err != nil {
		f.t.Fatalf("git %v: %v", args, err)
	}
	return out
}

// commit writes files (an empty body removes the file) and commits the whole tree.
func (f *fixture) commit(message string, files map[string]string) string {
	f.t.Helper()
	for name, body := range files {
		path := filepath.Join(f.dir, filepath.FromSlash(name))
		if body == "" {
			if err := os.Remove(path); err != nil {
				f.t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			f.t.Fatal(err)
		}
	}
	f.git("add", "--all")
	f.git("commit", "--quiet", "--allow-empty", "--message", message)
	return f.git("rev-parse", "HEAD")
}

// goModule returns the go.mod and one source file of a module that builds.
func goModule(dir, modulePath, pkg string) map[string]string {
	prefix := ""
	if dir != "." {
		prefix = dir + "/"
	}
	return map[string]string{
		prefix + "go.mod":    "module " + modulePath + "\n\ngo 1.21\n",
		prefix + pkg + ".go": "package " + pkg + "\n\n// Connect is part of the published API.\nfunc Connect() {}\n",
	}
}

// widget commits a root module and a nested lib module, whose go.mod quotes its path and
// carries a comment, and returns the commit.
func (f *fixture) widget() string {
	f.t.Helper()
	files := goModule(".", "example.com/widget", "widget")
	for name, body := range goModule("lib", "example.com/widget/lib", "lib") {
		files[name] = body
	}
	files["lib/go.mod"] = "module \"example.com/widget/lib\" // nested module\n\ngo 1.21\n"
	return f.commit("widget", files)
}

func requireContains(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("output lacks %q:\n%s", want, text)
		}
	}
}

// Positive: from v1 on, an incompatible change in a nested module fails the gate, and every
// module ran once, from its own directory, against the release commit and HEAD.
func TestGate_Positive_RejectsAnIncompatibleNestedModuleFromV1(t *testing.T) {
	t.Parallel()
	h := newGateHarness(t)
	f := newFixture(t, h)
	base := f.widget()
	f.git("tag", "v1.0.0")
	head := f.commit("break lib", map[string]string{"lib/stub-verdict": "incompatible\n"})
	run := h.run(t, f.dir, nil)
	if run.status != 1 {
		t.Fatalf("status %d, want 1:\n%s\n%s", run.status, run.stdout, run.stderr)
	}
	requireContains(t, run.stdout, "incompatible lib (example.com/widget/lib)", "compatible   . (example.com/widget)")
	requireContains(t, run.stderr, "rejected (auto: newest root release tag v1.0.0 is v1 or later)")
	want := []string{". " + base + " " + head, "lib " + base + " " + head}
	if got := h.checked(t); !slices.Equal(got, want) {
		t.Fatalf("checker runs %q, want %q", got, want)
	}
}

// Positive: before v1 the same change is reported as a warning and does not fail the gate.
func TestGate_Positive_WarnsBeforeV1(t *testing.T) {
	t.Parallel()
	h := newGateHarness(t)
	f := newFixture(t, h)
	f.widget()
	f.git("tag", "v0.4.0")
	f.commit("break lib", map[string]string{"lib/stub-verdict": "incompatible\n"})
	run := h.run(t, f.dir, nil)
	if run.status != 0 {
		t.Fatalf("status %d, want 0:\n%s\n%s", run.status, run.stdout, run.stderr)
	}
	requireContains(t, run.stdout, "warning: 1 incompatible module change(s) from v0.4.0",
		"reported, not rejected (auto: newest root release tag v0.4.0 is pre-v1)")
	reject := h.run(t, f.dir, nil, "-policy=reject")
	if reject.status != 1 {
		t.Fatalf("-policy=reject status %d, want 1:\n%s", reject.status, reject.stderr)
	}
}

// Positive: on GitHub Actions the verdict is a workflow command, which renders an annotation.
func TestGate_Positive_AnnotatesOnGitHubActions(t *testing.T) {
	t.Parallel()
	h := newGateHarness(t)
	f := newFixture(t, h)
	f.widget()
	f.git("tag", "v2.1.0")
	f.commit("break lib", map[string]string{"lib/stub-verdict": "incompatible\n"})
	run := h.run(t, f.dir, []string{"GITHUB_ACTIONS=true"})
	if run.status != 1 {
		t.Fatalf("status %d, want 1:\n%s", run.status, run.stderr)
	}
	requireContains(t, run.stderr, "::error::1 incompatible module change(s) from v2.1.0")
}

// Negative: compatible modules pass.
func TestGate_Negative_PassesCompatibleModules(t *testing.T) {
	t.Parallel()
	h := newGateHarness(t)
	f := newFixture(t, h)
	f.widget()
	f.git("tag", "v1.2.3")
	f.commit("document", map[string]string{"README.md": "widget\n"})
	run := h.run(t, f.dir, nil)
	if run.status != 0 {
		t.Fatalf("status %d, want 0:\n%s\n%s", run.status, run.stdout, run.stderr)
	}
	requireContains(t, run.stdout, "Every compared module is compatible")
	if got := h.checked(t); len(got) != 2 {
		t.Fatalf("checker runs %q, want two", got)
	}
}

// Boundary: a module added at HEAD is reported and not compared; a module removed from HEAD is
// an incompatible change under the policy, and only a warning under -policy=warn.
func TestGate_Boundary_ModuleAddedAndRemoved(t *testing.T) {
	t.Parallel()
	h := newGateHarness(t)
	f := newFixture(t, h)
	files := goModule(".", "example.com/widget", "widget")
	for name, body := range goModule("legacy", "example.com/widget/legacy", "legacy") {
		files[name] = body
	}
	f.commit("widget", files)
	f.git("tag", "v1.0.0")
	changed := goModule("extra", "example.com/widget/extra", "extra")
	changed["legacy/go.mod"], changed["legacy/legacy.go"] = "", ""
	f.commit("replace legacy", changed)
	run := h.run(t, f.dir, nil)
	if run.status != 1 {
		t.Fatalf("status %d, want 1:\n%s\n%s", run.status, run.stdout, run.stderr)
	}
	requireContains(t, run.stdout, "added        extra (example.com/widget/extra)",
		"removed      legacy (example.com/widget/legacy): every package of the module is gone")
	if got := h.checked(t); len(got) != 1 || !strings.HasPrefix(got[0], ". ") {
		t.Fatalf("checker runs %q, want the root module only", got)
	}
	if warned := h.run(t, f.dir, nil, "-policy=warn"); warned.status != 0 {
		t.Fatalf("-policy=warn status %d, want 0:\n%s", warned.status, warned.stderr)
	}
}

// Boundary: the base is the newest root release tag, never a nested module's tag nor a
// pre-release; with only those there is no published API and the checker never runs.
func TestGate_Boundary_SelectsOnlyRootReleaseTags(t *testing.T) {
	t.Parallel()
	h := newGateHarness(t)
	f := newFixture(t, h)
	f.widget()
	f.git("tag", "lib/v1.0.0")
	f.git("tag", "v2.0.0-rc.1")
	none := h.run(t, f.dir, nil)
	if none.status != 0 || !strings.Contains(none.stdout, "no published API to compare") || h.checked(t) != nil {
		t.Fatalf("without a root release tag: status %d, runs %q:\n%s", none.status, h.checked(t), none.stdout)
	}
	root := f.commit("release", map[string]string{"README.md": "widget\n"})
	f.git("tag", "v0.2.0")
	f.commit("nested release", map[string]string{"lib/README.md": "lib\n"})
	f.git("tag", "lib/v3.0.0")
	f.commit("change", map[string]string{"CHANGELOG.md": "change\n"})
	run := h.run(t, f.dir, nil)
	if run.status != 0 {
		t.Fatalf("status %d, want 0:\n%s\n%s", run.status, run.stdout, run.stderr)
	}
	requireContains(t, run.stdout, "from v0.2.0 ("+root[:12]+")", "policy auto: newest root release tag v0.2.0 is pre-v1")
}

// Boundary: go.mod files in directories ./... skips are not modules, a module path with an
// internal element is reported as not public, and a base equal to HEAD compares nothing.
func TestGate_Boundary_DiscoveryScope(t *testing.T) {
	t.Parallel()
	h := newGateHarness(t)
	f := newFixture(t, h)
	files := goModule(".", "example.com/widget", "widget")
	for _, dir := range []string{"testdata/fixture", "vendor/dep", ".hidden/tool", "_scratch/tool"} {
		files[dir+"/go.mod"] = "this is not a go.mod\n"
	}
	for name, body := range goModule("internal/engine", "example.com/widget/internal/engine", "engine") {
		files[name] = body
	}
	base := f.commit("widget", files)
	f.commit("document", map[string]string{"README.md": "widget\n"})
	run := h.run(t, f.dir, nil, "-base="+base)
	if run.status != 0 {
		t.Fatalf("status %d, want 0:\n%s\n%s", run.status, run.stdout, run.stderr)
	}
	requireContains(t, run.stdout, "not public   internal/engine (example.com/widget/internal/engine)",
		"policy auto: no root release tag, so the API is pre-v1")
	if got := h.checked(t); len(got) != 1 || !strings.HasPrefix(got[0], ". ") {
		t.Fatalf("checker runs %q, want the root module only", got)
	}
	same := h.run(t, f.dir, nil, "-base=HEAD")
	if same.status != 0 || !strings.Contains(same.stdout, "nothing to compare") {
		t.Fatalf("-base=HEAD: status %d:\n%s", same.status, same.stdout)
	}
}

// Boundary: more modules than one run compares fail the gate rather than compare a part.
func TestGate_Boundary_RefusesMoreModulesThanItsBound(t *testing.T) {
	t.Parallel()
	h := newGateHarness(t)
	f := newFixture(t, h)
	files := map[string]string{}
	for index := 0; index <= 512; index++ {
		files[fmt.Sprintf("m%03d/go.mod", index)] = fmt.Sprintf("module example.com/m%03d\n", index)
	}
	base := f.commit("modules", files)
	f.commit("document", map[string]string{"README.md": "widget\n"})
	run := h.run(t, f.dir, nil, "-base="+base)
	if run.status != 2 || !strings.Contains(run.stderr, "513 Go modules, more than the 512 one run compares") {
		t.Fatalf("status %d, want 2:\n%s", run.status, run.stderr)
	}
}

// Negative: a comparison that did not run fails the gate whatever the policy.
func TestGate_Negative_ExecutionFailuresFailWhateverThePolicy(t *testing.T) {
	t.Parallel()
	h := newGateHarness(t)
	f := newFixture(t, h)
	base := f.widget()
	cases := []struct {
		name  string
		files map[string]string
		args  []string
		env   []string
		want  string
	}{
		{"checker error", map[string]string{"lib/stub-verdict": "error\n"}, nil, nil, "the checker failed"},
		{"checker usage error with status 0", map[string]string{"lib/stub-verdict": "usage\n"}, nil, nil, "printed an error and exited 0"},
		{"blind checker", nil, nil, []string{"STUB_BLIND=1"}, "removed exported function as compatible"},
		{"module that does not build", map[string]string{"lib/lib.go": "package lib\n\nfunc Connect() int { return \"x\" }\n"}, nil, nil, "do not build at HEAD"},
		{"go.mod without a module directive", map[string]string{"lib/go.mod": "go 1.21\n"}, nil, nil, "declares no module directive"},
		{"unknown base", nil, []string{"-base=no-such-revision"}, nil, "does not name a commit"},
		{"unknown policy", nil, []string{"-policy=lenient"}, nil, "-policy must be auto, warn or reject"},
		{"missing checker", nil, []string{"-checker=" + filepath.Join(t.TempDir(), "absent")}, nil, "the checker failed"},
	}
	for _, tc := range cases {
		f.git("reset", "--quiet", "--hard", base)
		f.commit(tc.name, tc.files)
		args := append([]string{"-policy=warn", "-base=" + base}, tc.args...)
		run := h.run(t, f.dir, tc.env, args...)
		if run.status != 2 || !strings.Contains(run.stderr, tc.want) {
			t.Errorf("%s: status %d, want 2 with %q:\n%s\n%s", tc.name, run.status, tc.want, run.stdout, run.stderr)
		}
	}
}

// Boundary: the gate fails closed outside a git repository.
func TestGate_Boundary_FailsOutsideARepository(t *testing.T) {
	t.Parallel()
	h := newGateHarness(t)
	run := h.run(t, t.TempDir(), nil)
	if run.status != 2 || !strings.Contains(run.stderr, "did not run") {
		t.Fatalf("status %d, want 2:\n%s", run.status, run.stderr)
	}
}

// scratchEnv points the gate's temporary directories at a fresh directory and returns both.
func scratchEnv(t *testing.T) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	return dir, []string{"TMPDIR=" + dir, "TMP=" + dir, "TEMP=" + dir}
}

// requireEmptyDir fails unless dir holds nothing.
func requireEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("%s holds %d entries after the run, want none: first %s", dir, len(entries), entries[0].Name())
	}
}

// Positive: a module whose path moves in place to a later major version is a new module, which
// is reported and not compared, so an in-place major version bump passes from v1 on.
func TestGate_Positive_InPlaceMajorVersionIsANewModule(t *testing.T) {
	t.Parallel()
	h := newGateHarness(t)
	f := newFixture(t, h)
	base := f.widget()
	f.git("tag", "v1.0.0")
	head := f.commit("v2", map[string]string{"go.mod": "module example.com/widget/v2\n\ngo 1.21\n", "stub-verdict": "incompatible\n"})
	run := h.run(t, f.dir, nil)
	if run.status != 0 {
		t.Fatalf("status %d, want 0:\n%s\n%s", run.status, run.stdout, run.stderr)
	}
	requireContains(t, run.stdout,
		"new major    . (example.com/widget/v2): a new major version of example.com/widget, nothing to compare",
		"Every compared module is compatible")
	if got, want := h.checked(t), []string{"lib " + base + " " + head}; !slices.Equal(got, want) {
		t.Fatalf("checker runs %q, want %q", got, want)
	}
}

// Boundary: only a higher major version suffix of the same path is a new major version. A lower
// one, a /v1 or zero-padded element, neither of which is a suffix, and another prefix are
// compared.
func TestGate_Boundary_MajorVersionSuffixes(t *testing.T) {
	t.Parallel()
	h := newGateHarness(t)
	f := newFixture(t, h)
	cases := []struct {
		from, to string
		newMajor bool
	}{
		{"example.com/widget/v2", "example.com/widget/v3", true},
		{"gopkg.in/widget.v1", "gopkg.in/widget.v2", true},
		{"example.com/widget/v3", "example.com/widget/v2", false},
		{"example.com/widget", "example.com/widget/v1", false},
		{"example.com/widget", "example.com/widget/v02", false},
		{"example.com/widget", "example.com/gadget/v2", false},
	}
	for _, tc := range cases {
		files := goModule(".", tc.from, "widget")
		files["stub-verdict"] = "incompatible\n"
		base := f.commit("from "+tc.from, files)
		f.commit("to "+tc.to, map[string]string{"go.mod": "module " + tc.to + "\n\ngo 1.21\n"})
		runs := len(h.checked(t))
		run := h.run(t, f.dir, nil, "-base="+base, "-policy=warn")
		compared := len(h.checked(t)) > runs
		newMajor := strings.Contains(run.stdout, "new major    . ("+tc.to+"): a new major version of "+tc.from+",")
		if run.status != 0 || newMajor != tc.newMajor || compared == tc.newMajor {
			t.Errorf("%s -> %s: status %d, new major %t, compared %t, want new major %t:\n%s\n%s",
				tc.from, tc.to, run.status, newMajor, compared, tc.newMajor, run.stdout, run.stderr)
		}
	}
}

// Positive: while the root tracks a vendor directory, a nested module is compared in a clone
// whose commits drop it, since the go command never builds a nested module from the root's
// vendor directory. The root module is compared in the repository at the release commit and
// HEAD, and the clone is removed afterwards.
func TestGate_Positive_ComparesNestedModulesWithoutTheRootVendorDirectory(t *testing.T) {
	t.Parallel()
	h := newGateHarness(t)
	f := newFixture(t, h)
	f.widget()
	base := f.commit("vendor", map[string]string{"vendor/README.md": "vendored\n"})
	f.git("tag", "v1.0.0")
	head := f.commit("break lib", map[string]string{"lib/stub-verdict": "incompatible\n"})
	scratch, env := scratchEnv(t)
	run := h.run(t, f.dir, env)
	if run.status != 1 {
		t.Fatalf("status %d, want 1:\n%s\n%s", run.status, run.stdout, run.stderr)
	}
	requireContains(t, run.stdout, "incompatible lib (example.com/widget/lib)", "compatible   . (example.com/widget)")
	got := h.checked(t)
	if len(got) != 2 || got[0] != ". "+base+" "+head+" vendor" {
		t.Fatalf("checker runs %q, want the root module at %s and %s with its vendor directory first", got, base, head)
	}
	if nested := strings.Fields(got[1]); len(nested) != 3 || nested[0] != "lib" || nested[1] == base || nested[2] == head {
		t.Fatalf("nested module run %q, want lib at commits without the root vendor directory", got[1])
	}
	requireEmptyDir(t, scratch)
}

// Boundary: a root vendor entry at the base only still moves nested modules into the clone; a
// nested module's own vendor directory, which the checker reads only for that module, does not.
func TestGate_Boundary_VendorFreeCloneScope(t *testing.T) {
	t.Parallel()
	h := newGateHarness(t)
	f := newFixture(t, h)
	f.widget()
	vendored := f.commit("vendor", map[string]string{"vendor/README.md": "vendored\n"})
	head := f.commit("drop the vendor directory", map[string]string{"vendor/README.md": ""})
	if run := h.run(t, f.dir, nil, "-base="+vendored); run.status != 0 {
		t.Fatalf("status %d, want 0:\n%s\n%s", run.status, run.stdout, run.stderr)
	}
	got := h.checked(t)
	if len(got) != 2 || got[0] != ". "+vendored+" "+head {
		t.Fatalf("checker runs %q, want the root module at %s and %s first", got, vendored, head)
	}
	if nested := strings.Fields(got[1]); len(nested) != 3 || nested[1] == vendored || nested[2] == head {
		t.Fatalf("nested module run %q, want commits without the root vendor directory", got[1])
	}
	g := newFixture(t, h)
	g.widget()
	base := g.commit("vendor lib", map[string]string{"lib/vendor/README.md": "vendored\n"})
	last := g.commit("document", map[string]string{"README.md": "widget\n"})
	if run := h.run(t, g.dir, nil, "-base="+base); run.status != 0 {
		t.Fatalf("nested vendor directory: status %d, want 0:\n%s\n%s", run.status, run.stdout, run.stderr)
	}
	want := []string{". " + base + " " + last, "lib " + base + " " + last}
	if got := h.checked(t)[2:]; !slices.Equal(got, want) {
		t.Fatalf("nested vendor directory: checker runs %q, want %q", got, want)
	}
}
