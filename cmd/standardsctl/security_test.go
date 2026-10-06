// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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

// exitCode is the status main would exit with for err.
func exitCode(err error) int {
	var status exitStatusError
	if errors.As(err, &status) {
		return status.code
	}
	if err != nil {
		return 1
	}
	return 0
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
// advisory reported on stdout, and govulncheck from PATH runs when no scanner is named.
func TestSecurityGovuln_Positive_CoveredAdvisoryPasses(t *testing.T) {
	dir := t.TempDir()
	vex := `{"@context": "https://openvex.dev/ns/v0.2.0", "@id": "https://example.com/vex", "author": "Example", ` +
		`"timestamp": "` + "2026-10-05T12:00:00Z" + `", "version": 1, "statements": [{"vulnerability": {"name": "GO-2022-1059"}, ` +
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

// Boundary: a scan that failed exits 2, not 1 and never 0, and a missing or unknown subcommand is
// a usage error.
func TestSecurityGovuln_Boundary_NoVerdictExitsTwoAndUsage(t *testing.T) {
	_, stderr, _, err := govulnCLI(t, govulnStream(t, "incomplete"), errors.New("exit status 1"), "--path", t.TempDir())
	if exitCode(err) != govulnIncompleteExit || !strings.Contains(stderr, "govuln: no verdict: the govulncheck scan did not complete") {
		t.Fatalf("exit %d, stderr %q", exitCode(err), stderr)
	}
	for _, args := range [][]string{nil, {"gosec"}} {
		if err := runSecurity(args); err == nil || !strings.Contains(err.Error(), securityUsage) {
			t.Errorf("security %q: err %v, want the usage", args, err)
		}
	}
}
