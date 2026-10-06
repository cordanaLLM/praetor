// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/govuln"
)

// govulnScanner returns a runner that answers govulncheck with the recorded stream named (or with
// scanErr), go list with dir and every other command with nothing. The streams are govulncheck
// v1.8.0's own output under internal/govuln/testdata/scan.
func govulnScanner(t *testing.T, dir, stream string, scanErr error) commandRunner {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "govuln", "testdata", "scan", stream+".stream"))
	if err != nil {
		t.Fatal(err)
	}
	return func(_ context.Context, _, name string, args ...string) (string, error) {
		switch {
		case name == govuln.DefaultScanner:
			return string(data), scanErr
		case name == "go" && slices.Contains(args, "list"):
			return dir + "\n", nil
		}
		return "", nil
	}
}

// securityModule is a Go module with the pinned gosec configuration and, when vex is not empty,
// the OpenVEX document at the default path.
func securityModule(t *testing.T, vex string) string {
	t.Helper()
	dir := newGoModuleDir(t)
	writeFile(t, filepath.Join(dir, GosecConfigFile), "{\"global\":{}}\n")
	if vex != "" {
		writeFile(t, filepath.Join(dir, filepath.FromSlash("security/vex/go.openvex.json")), vex)
	}
	return dir
}

// moduleStatement is an OpenVEX document whose one not_affected statement, reviewed yesterday,
// covers GO-2022-1059 at module level.
func moduleStatement() string {
	yesterday := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	return `{"@context": "https://openvex.dev/ns/v0.2.0", "@id": "https://example.com/vex", "author": "Example", ` +
		`"timestamp": "` + yesterday + `", "version": 1, "statements": [{"vulnerability": {"name": "GO-2022-1059"}, ` +
		`"status": "not_affected", "justification": "vulnerable_code_not_present", "impact_statement": "No package of it is built."}]}`
}

// Negative (planted defect): the security stage fails on a called vulnerable symbol, naming the
// call, and on a scan that gave no verdict, never reading either as a pass.
func TestRunSecurityStage_Negative_GovulnGateFailsTheStage(t *testing.T) {
	cases := map[string]struct {
		stream  string
		scanErr error
		want    string
	}{
		"called symbol":   {"symbol", nil, "govulncheck found vulnerabilities: GO-2022-1059: golang.org/x/text/language.ParseAcceptLanguage is called"},
		"uncovered":       {"module", nil, "security/vex/go.openvex.json does not exist"},
		"incomplete scan": {"incomplete", errors.New("exit status 1"), "go vulnerability gate: the govulncheck scan did not complete"},
	}
	for name, tc := range cases {
		dir := securityModule(t, "")
		cfg, _ := newTestConfig(t, dir, false)
		cfg.run = govulnScanner(t, dir, tc.stream, tc.scanErr)
		if _, err := runSecurityStage(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}

// Positive: an advisory present but not called passes the stage under a current not_affected
// statement, and the stage message records the waiver, so the signed stage output names it.
func TestRunSecurityStage_Positive_CoveredAdvisoryIsRecorded(t *testing.T) {
	dir := securityModule(t, moduleStatement())
	cfg, _ := newTestConfig(t, dir, false)
	cfg.run = govulnScanner(t, dir, "module", nil)
	msg, err := runSecurityStage(t.Context(), cfg)
	if err != nil {
		t.Fatalf("security stage: %v", err)
	}
	for _, want := range []string{"1 advisories present, 0 failing, 1 covered", "GO-2022-1059: module golang.org/x/text@v0.3.7 is required but not called"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not name %q", msg, want)
		}
	}
}

// Boundary: the OpenVEX document is a subtractive input wherever the manifest puts it, and a
// manifest that does not load names no document while the baseline and gosec configuration stay.
func TestSubtractiveInputs_Boundary_FollowTheDeclaredDocument(t *testing.T) {
	dir := t.TempDir()
	if got := subtractiveInputs(dir); !slices.Equal(got, []string{BaselineFile, GosecConfigFile, "security/vex/go.openvex.json"}) {
		t.Fatalf("default inputs = %q", got)
	}
	writeFile(t, filepath.Join(dir, ".standards.yaml"), "version: 1\nsecurity:\n  go_vex: \"vex/go.json\"\n")
	if got := subtractiveInputs(dir); !slices.Contains(got, "vex/go.json") {
		t.Fatalf("declared inputs = %q", got)
	}
	writeFile(t, filepath.Join(dir, ".standards.yaml"), "version: 1\nsecurity:\n  go_vex: \"../vex.json\"\n")
	if got := subtractiveInputs(dir); !slices.Equal(got, []string{BaselineFile, GosecConfigFile}) {
		t.Fatalf("inputs of an invalid manifest = %q", got)
	}
}
