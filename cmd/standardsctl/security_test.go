// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/govuln"
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
	var commands []string
	run := func(_ context.Context, _, name string, args ...string) (string, error) {
		commands = append(commands, strings.Join(append([]string{name}, args...), " "))
		return out, runErr
	}
	var stdout, stderr bytes.Buffer
	err := runSecurityGovuln(args, run, &stdout, &stderr)
	return stdout.String(), stderr.String(), commands, err
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
	stdout, stderr, commands, err := govulnCLI(t, govulnStream(t, "symbol"), nil,
		"--path", t.TempDir(), "--", "go", "tool", "-modfile=tools/go/go.mod", "govulncheck")
	if exitCode(err) != govulnFindingExit || !strings.Contains(stderr, "golang.org/x/text/language.ParseAcceptLanguage is called") ||
		!strings.Contains(stderr, "govuln: FAIL: govulncheck v1.8.0, symbol scan: 1 advisories present, 1 failing") {
		t.Fatalf("exit %d, stdout %q, stderr %q", exitCode(err), stdout, stderr)
	}
	want := "go tool -modfile=tools/go/go.mod govulncheck " + strings.Join(govuln.ScanArgs(), " ")
	if !slices.Equal(commands, []string{want}) {
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
	if len(commands) != 1 || !strings.HasPrefix(commands[0], govuln.DefaultScanner+" -scan symbol") {
		t.Fatalf("commands = %q", commands)
	}
}

// Boundary: a scan that failed exits 2, not 1 and never 0.
func TestSecurityGovuln_Boundary_NoVerdictExitsTwo(t *testing.T) {
	_, stderr, _, err := govulnCLI(t, govulnStream(t, "incomplete"), errors.New("exit status 1"), "--path", t.TempDir())
	if exitCode(err) != govulnIncompleteExit || !strings.Contains(stderr, "govuln: no verdict: the govulncheck scan did not complete") {
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
			return strings.HasPrefix(step.Uses, "actions/setup-go@") && step.With["go-version-file"] != "go.mod"
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
}
