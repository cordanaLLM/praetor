// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/govuln"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// govulnStream reads one of govulncheck v1.8.0's recorded streams (internal/govuln/testdata/scan).
func govulnStream(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "govuln", "testdata", "scan", name+".stream"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// govulnCLI runs `security govuln args` with a runner answering out and runErr, and returns its
// output streams, the command lines the runner saw and its error.
func govulnCLI(t *testing.T, out string, runErr error, args ...string) (string, string, []string, error) {
	t.Helper()
	seedTrackedGovulnModule(t, securityTestPath(args...))
	var commands []string
	run := func(_ context.Context, _, name string, args ...string) (string, error) {
		commands = append(commands, strings.Join(append([]string{name}, args...), " "))
		if name == "go" && len(args) == 5 && args[0] == "list" && args[1] == "-find" &&
			args[2] == "-f" && args[3] == "{{.ImportPath}}" && args[4] == "./..." {
			return "example.test/security\n", nil
		}
		return out, runErr
	}
	var stdout, stderr bytes.Buffer
	err := runSecurityGovuln(args, run, &stdout, &stderr)
	return stdout.String(), stderr.String(), commands, err
}

func securityTestPath(args ...string) string {
	for index := 0; index < len(args); index++ {
		if strings.HasPrefix(args[index], "--path=") {
			return strings.TrimPrefix(args[index], "--path=")
		}
		if args[index] == "--path" && index+1 < len(args) {
			return args[index+1]
		}
	}
	return "."
}

func seedTrackedGovulnModule(t *testing.T, dir string) {
	t.Helper()
	if dir == "." {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/security\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := testsupport.HermeticGitEnv(t)
	for _, args := range [][]string{{"init", "-q"}, {"add", "--", "go.mod"}} {
		if out, err := runFixtureGit(t, dir, env, args...); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
}

// exitCode is the status main would exit with for err: 0 for nil, else what commandExitCode
// gives it.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	return commandExitCode(&bytes.Buffer{}, err)
}

// Negative: a called vulnerable symbol exits 1 and names the call on stderr, and the scanner
// command after "--" runs with the symbol-scan arguments appended.
func TestSecurityGovuln_Negative_CalledSymbolExitsOne(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, commands, err := govulnCLI(t, govulnStream(t, "symbol"), nil,
		"--path", dir, "--", "go", "tool", "-modfile=tools/go/go.mod", "govulncheck")
	if exitCode(err) != govulnFindingExit || !strings.Contains(stderr, "golang.org/x/text/language.ParseAcceptLanguage is called") ||
		!strings.Contains(stderr, "govuln: FAIL: govulncheck v1.8.0, symbol scan: 1 advisories present, 1 failing") {
		t.Fatalf("exit %d, stdout %q, stderr %q", exitCode(err), stdout, stderr)
	}
	want := "go tool -modfile=" + filepath.Join(dir, "tools", "go", "go.mod") + " govulncheck " + strings.Join(govuln.ScanArgs(), " ")
	if !slices.Equal(commands, []string{"go list -find -f {{.ImportPath}} ./...", want}) {
		t.Fatalf("commands = %q, want %q", commands, want)
	}
}

// Positive: a module-level advisory with a current not_affected statement passes, the covered
// advisory reported on stdout, and govulncheck from PATH runs when no scanner is named. The
// command judges against the real clock, so the document is issued a day before it runs: a fixed
// date would expire after govuln.MaxStatementAge and fail the test with no code change.
func TestSecurityGovuln_Positive_CoveredAdvisoryPasses(t *testing.T) {
	dir := t.TempDir()
	yesterday := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	vex := `{"@context": "https://openvex.dev/ns/v0.2.0", "@id": "https://example.com/vex", "author": "Example", ` +
		`"timestamp": "` + yesterday + `", "version": 1, "statements": [{"vulnerability": {"name": "GO-2022-1059"}, ` +
		`"status": "not_affected", "justification": "vulnerable_code_not_present", "impact_statement": "No package of it is built."}]}`
	path := filepath.Join(dir, "security", "vex", "go.openvex.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(vex), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, commands, err := govulnCLI(t, govulnStream(t, "module"), nil, "--path="+dir)
	if err != nil || !strings.Contains(stdout, "not called; not_affected (vulnerable_code_not_present)") ||
		!strings.Contains(stdout, "govuln: PASS:") || stderr != "" {
		t.Fatalf("err %v, stdout %q, stderr %q", err, stdout, stderr)
	}
	if len(commands) != 2 || commands[0] != "go list -find -f {{.ImportPath}} ./..." ||
		!strings.HasPrefix(commands[1], govuln.DefaultScanner+" -scan symbol") {
		t.Fatalf("commands = %q", commands)
	}
}

// Positive: a tracked go.mod with no Go packages prints the stated skip and exits successfully;
// no scanner command is allowed to turn the empty package set into a false failure.
func TestSecurityGovuln_Positive_EmptyModuleSkips(t *testing.T) {
	dir := t.TempDir()
	seedTrackedGovulnModule(t, dir)
	var commands []string
	run := func(_ context.Context, _, name string, args ...string) (string, error) {
		commands = append(commands, strings.Join(append([]string{name}, args...), " "))
		if name == "go" && len(args) == 5 && args[0] == "list" && args[1] == "-find" {
			return "", nil
		}
		return "", nil
	}
	var stdout, stderr bytes.Buffer
	err := runSecurityGovuln([]string{"--path=" + dir}, run, &stdout, &stderr)
	if err != nil || !strings.Contains(stdout.String(), "govuln: SKIP: "+gating.NoPackagesNote) || stderr.Len() != 0 {
		t.Fatalf("err %v, stdout %q, stderr %q", err, stdout.String(), stderr.String())
	}
	if !slices.Equal(commands, []string{"go list -find -f {{.ImportPath}} ./..."}) {
		t.Fatalf("commands = %q", commands)
	}
}

func seedTrackedMultiModule(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/root\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "go.mod"), []byte("module example.test/root/sub\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	toolsDir := filepath.Join(dir, "tools", "go")
	if err := os.MkdirAll(toolsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolsDir, "go.mod"), []byte("module example.test/tools\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := testsupport.HermeticGitEnv(t)
	for _, args := range [][]string{{"init", "-q"}, {"add", "--", "go.mod", "sub/go.mod"}} {
		if out, err := runFixtureGit(t, dir, env, args...); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
}

func seedVEXDocument(t *testing.T, dir string) {
	t.Helper()
	yesterday := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	vex := `{"@context": "https://openvex.dev/ns/v0.2.0", "@id": "https://example.com/vex", "author": "Example", ` +
		`"timestamp": "` + yesterday + `", "version": 1, "statements": [{"vulnerability": {"name": "GO-2022-1059"}, ` +
		`"status": "not_affected", "justification": "vulnerable_code_not_present", "impact_statement": "No package built."}]}`
	for _, sub := range []string{"", "sub"} {
		path := filepath.Join(dir, sub, "security", "vex", "go.openvex.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(vex), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func extractScannerModfile(args []string) string {
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-modfile=") || strings.HasPrefix(args[i], "--modfile=") {
			return strings.TrimPrefix(strings.TrimPrefix(args[i], "--modfile="), "-modfile=")
		}
	}
	return ""
}

func multiModuleStubRunner(t *testing.T) govuln.Runner {
	return func(_ context.Context, runDir, name string, args ...string) (string, error) {
		if name == "go" && len(args) == 5 && args[0] == "list" && args[1] == "-find" {
			return "example.test/pkg\n", nil
		}
		target := extractScannerModfile(args)
		if !filepath.IsAbs(target) {
			target = filepath.Join(runDir, target)
		}
		if _, err := os.Stat(target); err != nil {
			return "", fmt.Errorf("modfile %s does not exist: %w", target, err)
		}
		return govulnStream(t, "module"), nil
	}
}

// Positive: in a multi-module repository, scanner -modfile resolves against the repository root
// so both the root module and submodules get a verdict.
func TestSecurityGovuln_Positive_MultiModuleModfileRoot(t *testing.T) {
	dir := t.TempDir()
	seedTrackedMultiModule(t, dir)
	seedVEXDocument(t, dir)
	var stdout, stderr bytes.Buffer
	args := []string{"--path=" + dir, "--", "go", "tool", "-modfile=tools/go/go.mod", "govulncheck"}
	err := runSecurityGovuln(args, multiModuleStubRunner(t), &stdout, &stderr)
	if err != nil || stderr.Len() != 0 {
		t.Fatalf("err %v, stdout %q, stderr %q", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "module: go.mod") || !strings.Contains(stdout.String(), "module: sub/go.mod") {
		t.Fatalf("stdout = %q, want both modules reported", stdout.String())
	}
}

// Boundary: a scan that failed exits 2, not 1 and never 0.
func TestSecurityGovuln_Boundary_NoVerdictExitsTwo(t *testing.T) {
	_, stderr, _, err := govulnCLI(t, govulnStream(t, "incomplete"), errors.New("exit status 1"), "--path", t.TempDir())
	if exitCode(err) != govulnIncompleteExit || !strings.Contains(stderr, "govuln: go.mod: no verdict: the govulncheck scan did not complete") {
		t.Fatalf("exit %d, stderr %q", exitCode(err), stderr)
	}
}

// Negative (planted defect): a command line the gate cannot run -- a misspelled flag, a missing or
// unknown subcommand -- exits 2 with the synopsis and runs no scan; exit 1 would read as a failing
// advisory. --help is no error: main exits 0 on flag.ErrHelp.
func TestSecurityGovuln_Negative_UsageErrorExitsTwo(t *testing.T) {
	cases := map[string][]string{
		"misspelled flag":    {"govuln", "--pth=."},
		"missing subcommand": nil,
		"unknown subcommand": {"gosec"},
	}
	for name, args := range cases {
		var scans int
		run := func(context.Context, string, string, ...string) (string, error) { scans++; return "", nil }
		var stdout, stderr bytes.Buffer
		err := dispatchSecurity(args, run, &stdout, &stderr)
		if exitCode(err) != govulnIncompleteExit || !strings.Contains(stderr.String(), securityUsage) || scans != 0 {
			t.Errorf("%s: exit %d, %d scans, stderr %q; want exit 2 with the usage and no scan", name, exitCode(err), scans, stderr.String())
		}
	}
	var stderr bytes.Buffer
	if err := dispatchSecurity([]string{"govuln", "--help"}, nil, &bytes.Buffer{}, &stderr); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("--help: err %v, want flag.ErrHelp", err)
	}
}

// The lines this repository's own entry points run the gate with: the security workflow's step
// and the make vuln recipe, which hands it the scanner tools/go/go.mod pins.
const (
	workflowGovulnStep = "        run: go run ./cmd/standardsctl security govuln\n"
	makeVulnRecipe     = "\tgo run ./cmd/standardsctl security govuln -- $(GO_SECURITY_TOOL) govulncheck\n"
)

// runsGovulnGate reports whether text runs the gate through line and no longer runs govulncheck
// directly over the module.
func runsGovulnGate(text, line string) bool {
	return strings.Contains(text, line) && !strings.Contains(text, "govulncheck ./...")
}

// Negative: the security workflow and make vuln run the Go vulnerability gate; the text each ran
// before, plain govulncheck ./..., is refused, so neither can go back to it unnoticed.
func TestSecurityGovuln_Negative_RepositoryEntryPointsRunTheGate(t *testing.T) {
	for path, line := range map[string]string{
		filepath.Join("..", "..", ".github", "workflows", "security.yml"): workflowGovulnStep,
		filepath.Join("..", "..", "Makefile"):                             makeVulnRecipe,
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !runsGovulnGate(strings.ReplaceAll(string(data), "\r\n", "\n"), line) {
			t.Errorf("%s does not run the gate with %q", path, line)
		}
	}
	for _, prior := range []string{"        run: govulncheck ./...\n", "\t$(GO_SECURITY_TOOL) govulncheck ./...\n"} {
		if runsGovulnGate(workflowGovulnStep+makeVulnRecipe+prior, workflowGovulnStep) {
			t.Errorf("the prior entry point %q passed", prior)
		}
	}
}

// gateCommands are the run: texts that start the Go vulnerability gate in a workflow job: the
// command itself, make vuln, and make verify-all, which runs make vuln.
var gateCommands = []string{"standardsctl security govuln", "make vuln", "make verify-all"}

// runsGovulnGateStep reports whether one workflow step starts the gate.
func runsGovulnGateStep(step ghworkflow.Step) bool {
	return slices.ContainsFunc(gateCommands, func(command string) bool { return strings.Contains(step.Run, command) })
}

// setsGoUpFromGoMod reports whether a setup-go step takes its version from go.mod alone.
// setup-go reads go-version first when both inputs are set, so a step that also sets
// go-version resolves that range through the manifest again.
func setsGoUpFromGoMod(step ghworkflow.Step) bool {
	_, ranged := step.With["go-version"]
	return step.With["go-version-file"] == "go.mod" && !ranged
}

// cachedGoGateJobs parses one workflow and returns how many of its jobs run the gate and which of
// those set Go up without go-version-file: go.mod. Such a job scans with a Go patch release
// resolved from a range, and the actions/go-versions manifest behind that range lags a release
// (or stops updating), so its standard-library advisories fail the gate although a fixed Go
// release exists. go.mod's toolchain directive is the one source of the scanning version.
func cachedGoGateJobs(t *testing.T, data []byte) (int, []string) {
	t.Helper()
	spec, err := ghworkflow.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	gateJobs, cached := 0, []string{}
	for _, id := range ghworkflow.SortedJobIDs(spec.Jobs) {
		steps := spec.Jobs[id].Steps
		if !slices.ContainsFunc(steps, runsGovulnGateStep) {
			continue
		}
		gateJobs++
		if slices.ContainsFunc(steps, func(step ghworkflow.Step) bool {
			return strings.HasPrefix(step.Uses, "actions/setup-go@") && !setsGoUpFromGoMod(step)
		}) {
			cached = append(cached, id)
		}
	}
	return gateJobs, cached
}

// Negative: every job of the CI and security workflows that runs the gate resolves the newest Go
// patch release from go.mod's toolchain directive (setup-go go-version-file: go.mod). A job that
// sets Go up from a version range, with or without check-latest, is reported.
func TestSecurityGovuln_Negative_GateJobsResolveGoFromGoMod(t *testing.T) {
	for _, name := range []string{"ci.yml", "security.yml"} {
		data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		if gateJobs, cached := cachedGoGateJobs(t, data); gateJobs == 0 || len(cached) != 0 {
			t.Errorf("%s: %d jobs run the gate; these set Go up without go-version-file: go.mod: %q", name, gateJobs, cached)
		}
	}
	planted := "jobs:\n  scan:\n    runs-on: ubuntu-26.04\n    steps:\n" +
		"      - uses: actions/setup-go@v7\n        with:\n          go-version: '1.27'\n          cache: false\n" +
		"      - run: go run ./cmd/standardsctl security govuln\n"
	if gateJobs, cached := cachedGoGateJobs(t, []byte(planted)); gateJobs != 1 || !slices.Equal(cached, []string{"scan"}) {
		t.Fatalf("planted job: %d gate jobs, cached %q; want the scan job reported", gateJobs, cached)
	}
	plantedLatest := strings.Replace(planted, "cache: false\n", "cache: false\n          check-latest: true\n", 1)
	if _, cached := cachedGoGateJobs(t, []byte(plantedLatest)); !slices.Equal(cached, []string{"scan"}) {
		t.Fatalf("check-latest job: cached %q; a version range with check-latest still resolves through the manifest", cached)
	}
	fixed := strings.Replace(planted, "go-version: '1.27'", "go-version-file: go.mod", 1)
	if _, cached := cachedGoGateJobs(t, []byte(fixed)); len(cached) != 0 {
		t.Fatalf("go-version-file job reported: %q", cached)
	}
	// boundary: both inputs set; setup-go uses go-version, so the range comes back
	both := strings.Replace(planted, "go-version: '1.27'", "go-version: '1.27'\n          go-version-file: go.mod", 1)
	if _, cached := cachedGoGateJobs(t, []byte(both)); !slices.Equal(cached, []string{"scan"}) {
		t.Fatalf("job with go-version and go-version-file: cached %q; setup-go reads go-version first", cached)
	}
}

// goReleaseSteps returns the ids of a job's steps that resolve the Go release from go.mod: a
// setup-go step reading go-version-file: go.mod alone, or a run step reading go.mod's
// toolchain directive (a job that keeps no Go cache runs no setup-go step of its own).
func goReleaseSteps(steps []ghworkflow.Step) map[string]bool {
	ids := map[string]bool{}
	for _, step := range steps {
		fromSetup := strings.HasPrefix(step.Uses, "actions/setup-go@") && setsGoUpFromGoMod(step)
		fromRun := strings.Contains(step.Run, "go.mod") && strings.Contains(step.Run, "toolchain go")
		if step.ID != "" && (fromSetup || fromRun) {
			ids[step.ID] = true
		}
	}
	return ids
}

// passesResolvedGo reports whether a praetor-adopt call's go-version input is the go-version
// output of one of the job's go.mod-resolving steps.
func passesResolvedGo(step ghworkflow.Step, resolved map[string]bool) bool {
	input, ok := step.With["go-version"].(string)
	if !ok {
		return false
	}
	id, ok := strings.CutPrefix(input, "${{ steps.")
	if !ok {
		return false
	}
	id, ok = strings.CutSuffix(id, ".outputs.go-version }}")
	return ok && resolved[id]
}

// adoptCallsWithoutResolvedGo parses one workflow and returns how many steps call the
// praetor-adopt action and which of them ("job/step name") do not pass a go.mod-resolved Go
// release. The action runs its own setup-go with check-latest; without the exact release it
// resolves the default range through the lagging manifest and overrides the job's toolchain.
func adoptCallsWithoutResolvedGo(t *testing.T, data []byte) (int, []string) {
	t.Helper()
	spec, err := ghworkflow.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	calls, missing := 0, []string{}
	for _, id := range ghworkflow.SortedJobIDs(spec.Jobs) {
		steps := spec.Jobs[id].Steps
		resolved := goReleaseSteps(steps)
		for _, step := range steps {
			if step.Uses != "./.github/actions/praetor-adopt" {
				continue
			}
			calls++
			if !passesResolvedGo(step, resolved) {
				missing = append(missing, id+"/"+step.Name)
			}
		}
	}
	return calls, missing
}

// Negative: every praetor-adopt call in adopt.yml passes a go.mod-resolved Go release. A copy
// of adopt.yml with the input dropped from its last call reports exactly that call.
func TestAdoptWorkflow_Negative_PassesResolvedGoVersionToAction(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "adopt.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	calls, missing := adoptCallsWithoutResolvedGo(t, []byte(text))
	if calls < 3 || len(missing) != 0 {
		t.Fatalf("adopt.yml: %d praetor-adopt calls, want at least 3; missing a go.mod-resolved Go release: %q", calls, missing)
	}
	last := strings.LastIndex(text, "\n          go-version: ${{ steps.")
	if last < 0 {
		t.Fatal("adopt.yml carries no resolved Go version input")
	}
	end := strings.IndexByte(text[last+1:], '\n')
	if end < 0 {
		t.Fatal("adopt.yml ends inside the resolved Go version input")
	}
	mutant := text[:last] + text[last+1+end:]
	if _, missing := adoptCallsWithoutResolvedGo(t, []byte(mutant)); len(missing) != 1 {
		t.Fatalf("mutant with one input dropped: missing %q; want exactly one call reported", missing)
	}
}

func TestSecuritySecrets_Negative_FindingExitsThree(t *testing.T) {
	dir := t.TempDir()
	reportJSON := `[{"RuleID":"generic-api-key","File":"creds.py","StartLine":1,"Commit":"1122334455667788990011223344556677889900","Fingerprint":"fp1"}]`
	run := func(_ context.Context, _ string, _ string, args ...string) (string, error) {
		for i := 0; i < len(args); i++ {
			if args[i] == "--report-path" && i+1 < len(args) {
				if writeErr := os.WriteFile(args[i+1], []byte(reportJSON), 0o600); writeErr != nil {
					return "", writeErr
				}
			}
		}
		return "", errors.New("exit status 1")
	}
	var stdout, stderr bytes.Buffer
	err := dispatchSecurity([]string{"secrets", "--path=" + dir}, run, &stdout, &stderr)
	if exitCode(err) != secretsFindingExit {
		t.Fatalf("exit = %d, want %d (secretsFindingExit)\nstderr: %s", exitCode(err), secretsFindingExit, stderr.String())
	}
	if !strings.Contains(stderr.String(), "1122334455667788990011223344556677889900") || !strings.Contains(stderr.String(), "generic-api-key") {
		t.Errorf("stderr does not report finding: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "secrets: FAIL: 1 blocking secret finding(s)") {
		t.Errorf("stderr does not report FAIL summary: %s", stderr.String())
	}
}

func TestSecuritySecrets_Positive_CleanPasses(t *testing.T) {
	dir := t.TempDir()
	run := func(_ context.Context, _ string, _ string, args ...string) (string, error) {
		for i := 0; i < len(args); i++ {
			if args[i] == "--report-path" && i+1 < len(args) {
				if writeErr := os.WriteFile(args[i+1], []byte("[]"), 0o600); writeErr != nil {
					return "", writeErr
				}
			}
		}
		return "", nil
	}
	var stdout, stderr bytes.Buffer
	err := dispatchSecurity([]string{"secrets", "--path=" + dir}, run, &stdout, &stderr)
	if err != nil || exitCode(err) != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", exitCode(err), stderr.String())
	}
	if !strings.Contains(stdout.String(), "secrets: PASS: zero secret findings in history") {
		t.Errorf("stdout does not report PASS summary: %s", stdout.String())
	}
}

func TestSecuritySecrets_Negative_UsageErrorExitsTwo(t *testing.T) {
	cases := map[string][]string{
		"unknown flag": {"secrets", "--bogus-flag"},
	}
	for name, args := range cases {
		var stdout, stderr bytes.Buffer
		err := dispatchSecurity(args, nil, &stdout, &stderr)
		if exitCode(err) != secretsIncompleteExit || !strings.Contains(stderr.String(), "secrets: no verdict") {
			t.Errorf("%s: exit %d, want %d, stderr: %s", name, exitCode(err), secretsIncompleteExit, stderr.String())
		}
	}
}
