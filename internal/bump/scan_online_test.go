// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package bump

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// standInDeadline bounds every scan, update and canary a test below drives through a
// stand-in toolchain (HISS-02). A stand-in answers at once; the bound only turns a hang
// into a failure instead of the test binary's timeout.
const standInDeadline = 2 * time.Minute

// testDeadline returns the test's context bounded by standInDeadline.
func testDeadline(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), standInDeadline)
	t.Cleanup(cancel)
	return ctx
}

// standInReply is how the stand-in toolchain answers one call.
type standInReply struct {
	// Out is printed on standard output, Code is the exit code.
	Out  string
	Code int
	// Edit, when set, replaces Edit[0] with Edit[1] in the working directory's go.mod
	// before answering, the way a successful `go get` moves a requirement.
	Edit [2]string
	// Show names a file in the working directory printed before Out, so a test command
	// can prove which manifest it ran against.
	Show string
}

// standInCall is one recorded invocation: the working directory and the call, the tool
// name followed by its arguments.
type standInCall struct {
	Dir  string
	Call string
}

// standInToolchainSource is a main package that records every invocation to LOGPATH and
// answers from REPLIES, keyed by the tool name and its arguments. An unexpected call
// exits 64, so code under test cannot run a command the test did not anticipate and
// still pass.
const standInToolchainSource = `package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type reply struct {
	out       string
	code      int
	old, repl string
	show      string
}

var replies = map[string]reply{
REPLIES}

func main() {
	tool := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	call := strings.TrimSpace(tool + " " + strings.Join(os.Args[1:], " "))
	dir, err := os.Getwd()
	if err != nil {
		os.Exit(70)
	}
	log, err := os.OpenFile(LOGPATH, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(70)
	}
	fmt.Fprintf(log, "%s\t%s\n", dir, call)
	if log.Close() != nil {
		os.Exit(70)
	}
	answer, ok := replies[call]
	if !ok {
		fmt.Fprintln(os.Stderr, "stand-in: unexpected call:", call)
		os.Exit(64)
	}
	if answer.old != "" && !edit(answer.old, answer.repl) {
		os.Exit(65)
	}
	if answer.show != "" {
		data, err := os.ReadFile(answer.show)
		if err != nil {
			os.Exit(66)
		}
		fmt.Print(string(data))
	}
	fmt.Print(answer.out)
	os.Exit(answer.code)
}

func edit(old, repl string) bool {
	data, err := os.ReadFile("go.mod")
	if err != nil || !strings.Contains(string(data), old) {
		return false
	}
	return os.WriteFile("go.mod", []byte(strings.Replace(string(data), old, repl, 1)), 0o600) == nil
}
`

// standInToolchain builds one recording executable answering replies, installs it under
// every name in tools in a new directory, and returns that directory with the call log.
// It must run before PATH is narrowed: building needs the real go command.
func standInToolchain(t *testing.T, replies map[string]standInReply, tools ...string) (bin, log string) {
	t.Helper()
	log = filepath.Join(t.TempDir(), "calls.log")
	var table strings.Builder
	for _, call := range slices.Sorted(maps.Keys(replies)) {
		r := replies[call]
		fields := []string{strconv.Quote(r.Out), strconv.Itoa(r.Code), strconv.Quote(r.Edit[0]),
			strconv.Quote(r.Edit[1]), strconv.Quote(r.Show)}
		table.WriteString("\t" + strconv.Quote(call) + ": {" + strings.Join(fields, ", ") + "},\n")
	}
	source := strings.NewReplacer("REPLIES", table.String(), "LOGPATH", strconv.Quote(log)).Replace(standInToolchainSource)
	built := testsupport.BuildExecutable(t, t.TempDir(), "standin", source)
	data, err := os.ReadFile(built)
	if err != nil {
		t.Fatal(err)
	}
	bin = t.TempDir()
	for _, tool := range tools {
		if err := os.WriteFile(filepath.Join(bin, testsupport.ExecutableName(tool)), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin, log
}

// standInCalls returns the calls recorded in log, in order; no log means no call.
func standInCalls(t *testing.T, log string) []standInCall {
	t.Helper()
	data, err := os.ReadFile(log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var calls []standInCall
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		dir, call, _ := strings.Cut(line, "\t")
		calls = append(calls, standInCall{Dir: dir, Call: call})
	}
	return calls
}

// callNames returns only the call column of calls.
func callNames(calls []standInCall) []string {
	names := make([]string, 0, len(calls))
	for _, c := range calls {
		names = append(names, c.Call)
	}
	return names
}

// samePath reports whether a and b name the same existing directory. Temporary
// directories are reached through a symlink on macOS, so a child's working directory
// is compared after resolving both sides.
func samePath(a, b string) bool {
	resolvedA, errA := filepath.EvalSymlinks(a)
	resolvedB, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && resolvedA == resolvedB
}

const (
	appGoMod    = "module example.com/app\n\ngo 1.21\n\nrequire example.com/pkg v1.0.0\n"
	pkgGetCall  = "go get example.com/pkg@v1.2.0"
	goTidyCall  = "go mod tidy"
	goTestCall  = "go test ./..."
	goListCall  = "go list -m -u -json all"
	pnpmTest    = "pnpm test"
	pnpmOutdate = "pnpm outdated --json"
)

// goGetMoves is the reply of a `go get` that resolved the target and moved the requirement.
var goGetMoves = standInReply{Edit: [2]string{"example.com/pkg v1.0.0", "example.com/pkg v1.2.0"}}

// Positive: a `go get` that succeeds is followed by go mod tidy in the candidate's
// module, and the go.mod fallback edit never runs. No test drove this path before: the
// offline fixtures always made go get fail.
func TestApplyUpdate_Positive_GoGetSuccessTidiesWithoutFallback(t *testing.T) {
	repo := t.TempDir()
	writeGoMod(t, repo, appGoMod)
	bin, log := standInToolchain(t, map[string]standInReply{pkgGetCall: goGetMoves, goTidyCall: {}}, "go")
	t.Setenv("PATH", bin)

	if err := ApplyUpdate(testDeadline(t), repo, fallbackCandidate); err != nil {
		t.Fatalf("successful go get reported as failure: %v", err)
	}
	calls := standInCalls(t, log)
	if got := strings.Join(callNames(calls), "; "); got != pkgGetCall+"; "+goTidyCall {
		t.Fatalf("calls = %q, want go get then go mod tidy", got)
	}
	for _, c := range calls {
		if !samePath(c.Dir, repo) {
			t.Fatalf("%q ran in %s, not the module %s", c.Call, c.Dir, repo)
		}
	}
	if !strings.Contains(readGoMod(t, repo), "example.com/pkg v1.2.0\n") {
		t.Fatalf("go.mod is not the one go get wrote: %q", readGoMod(t, repo))
	}
}

// Negative: go get succeeds but go mod tidy fails. The tidy failure is the update's
// error, and it is not mistaken for a fallback edit: go.mod is what go get left.
func TestApplyUpdate_Negative_TidyFailureAfterGoGetIsReported(t *testing.T) {
	repo := t.TempDir()
	writeGoMod(t, repo, appGoMod)
	bin, log := standInToolchain(t, map[string]standInReply{
		pkgGetCall: goGetMoves,
		goTidyCall: {Code: 1},
	}, "go")
	t.Setenv("PATH", bin)

	err := ApplyUpdate(testDeadline(t), repo, fallbackCandidate)
	if err == nil || !strings.Contains(err.Error(), "go mod tidy in "+repo) {
		t.Fatalf("tidy failure lost: %v", err)
	}
	if errors.Is(err, errGoModFallbackEdit) || errors.Is(err, errFallbackRefused) {
		t.Fatalf("a successful go get fell back to the text edit: %v", err)
	}
	if got := callNames(standInCalls(t, log)); len(got) != 2 {
		t.Fatalf("calls = %q, want exactly go get and go mod tidy", got)
	}
}

// Boundary: UpdateAll defers tidy to one pass per module. Three successful updates in
// two modules run three go gets, each in its own module, and exactly two tidies.
func TestUpdateAll_Boundary_OneTidyPerModule(t *testing.T) {
	repo := t.TempDir()
	writeGoMod(t, repo, appGoMod)
	svc := filepath.Join(repo, "svc")
	if err := os.Mkdir(svc, 0o750); err != nil {
		t.Fatal(err)
	}
	writeGoMod(t, svc, "module example.com/svc\n\ngo 1.21\n\nrequire (\n\texample.com/a v1.0.0\n\texample.com/b v1.0.0\n)\n")
	bin, log := standInToolchain(t, map[string]standInReply{
		pkgGetCall: {}, "go get example.com/a@v1.1.0": {}, "go get example.com/b@v1.1.0": {}, goTidyCall: {},
	}, "go")
	t.Setenv("PATH", bin)
	inSvc := func(pkg string) UpgradeCandidate {
		return UpgradeCandidate{Package: pkg, CurrentVersion: "v1.0.0", TargetVersion: "v1.1.0", ManifestType: "go.mod", ModuleDir: "svc"}
	}

	applied, err := UpdateAll(testDeadline(t), repo, []UpgradeCandidate{fallbackCandidate, inSvc("example.com/a"), inSvc("example.com/b")})
	if err != nil || applied != 3 {
		t.Fatalf("UpdateAll = %d, %v; want 3 applied", applied, err)
	}
	calls := standInCalls(t, log)
	labelled := make([]string, 0, len(calls))
	for _, c := range calls {
		label := "elsewhere"
		if samePath(c.Dir, repo) {
			label = "root"
		} else if samePath(c.Dir, svc) {
			label = "svc"
		}
		labelled = append(labelled, label+": "+c.Call)
	}
	want := []string{"root: " + pkgGetCall, "root: " + goTidyCall,
		"svc: go get example.com/a@v1.1.0", "svc: go get example.com/b@v1.1.0", "svc: " + goTidyCall}
	if got := slices.Sorted(slices.Values(labelled)); !slices.Equal(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	if callNames(calls[3:])[0] != goTidyCall || callNames(calls[3:])[1] != goTidyCall {
		t.Fatalf("tidy ran before every go get finished: %q", callNames(calls))
	}
}
