// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

const validEvidenceCommit = "0123456789abcdef0123456789abcdef01234567"

// validEvidenceYAML formats a valid gate.declared_verify_evidence YAML document for measured.
func validEvidenceYAML(measured string) string {
	return fmt.Sprintf("command: make verify-all\nmeasured_duration: %s\ndate: \"2026-11-18\"\ncommit: %s\n",
		measured, validEvidenceCommit)
}

// initEvidenceRepo creates a committed git repository holding docs/verify-evidence.yaml and sub/readme.txt.
func initEvidenceRepo(t *testing.T, evidenceRel, evidenceBody string, commitEvidence bool) string {
	t.Helper()
	repo := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, repo, "https://github.com/example/fixture.git")
	writeEvidenceRepoFile(t, repo, "README.md", "# fixture\n")
	writeEvidenceRepoFile(t, repo, "sub/readme.txt", "sub\n")
	args := [][]string{{"add", "--", "README.md", "sub/readme.txt"}}
	if evidenceRel != "" && evidenceBody != "" {
		writeEvidenceRepoFile(t, repo, evidenceRel, evidenceBody)
		if commitEvidence {
			args = append(args, []string{"add", "--", evidenceRel})
		}
	}
	args = append(args, []string{"commit", "-q", "-m", "seed evidence fixture"})
	testsupport.RunFixtureGit(t, repo, args...)
	return repo
}

// writeEvidenceRepoFile writes rel under repo with owner-only permissions using confined helpers.
func writeEvidenceRepoFile(t *testing.T, repo, rel, body string) string {
	t.Helper()
	dirRel := filepath.Dir(filepath.FromSlash(rel))
	if dirRel != "." && dirRel != "" {
		if err := util.MkdirConfined(repo, dirRel, util.SecureDirPerm); err != nil {
			t.Fatalf("mkdir %s: %v", dirRel, err)
		}
	}
	full, err := util.ConfinePath(repo, filepath.FromSlash(rel))
	if err != nil {
		t.Fatalf("confine %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return full
}

// loadRepoGateManifest writes .standards.yaml in repo and loads it via LoadRepositoryManifest.
func loadRepoGateManifest(t *testing.T, repo, timeout, evidence string) (*Manifest, error) {
	t.Helper()
	var b strings.Builder
	b.WriteString("version: 1\ngate:\n")
	if timeout != "" {
		b.WriteString("  declared_verify_timeout: " + timeout + "\n")
	}
	if evidence != "" {
		b.WriteString("  declared_verify_evidence: \"" + evidence + "\"\n")
	}
	writeEvidenceRepoFile(t, repo, ManifestFileName, b.String())
	return LoadRepositoryManifest(t.Context(), repo)
}

// TestGatePolicy_Constants_DeclaredVerifyBounds verifies the declared verification timeout and evidence constants.
func TestGatePolicy_Constants_DeclaredVerifyBounds(t *testing.T) {
	if MaxDeclaredVerifyTimeout != 30*time.Minute {
		t.Fatalf("MaxDeclaredVerifyTimeout = %s, want 30m", MaxDeclaredVerifyTimeout)
	}
	if MaxDeclaredVerifyTimeoutWithEvidence != 60*time.Minute {
		t.Fatalf("MaxDeclaredVerifyTimeoutWithEvidence = %s, want 60m", MaxDeclaredVerifyTimeoutWithEvidence)
	}
	if MinDeclaredVerifyEvidenceDuration != 20*time.Minute {
		t.Fatalf("MinDeclaredVerifyEvidenceDuration = %s, want 20m", MinDeclaredVerifyEvidenceDuration)
	}
}

// TestValidateGate_Positive_RaisedBoundWithCommittedEvidence verifies 45m and 60m with valid committed evidence.
func TestValidateGate_Positive_RaisedBoundWithCommittedEvidence(t *testing.T) {
	cases := []struct {
		name     string
		timeout  string
		measured string
		wantDur  time.Duration
		wantMeas time.Duration
	}{
		{"45m with valid committed evidence", "45m", "38m", 45 * time.Minute, 38 * time.Minute},
		{"60m boundary with valid committed evidence", "60m", "52m", 60 * time.Minute, 52 * time.Minute},
		{"30m without evidence", "30m", "", 30 * time.Minute, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evPath := ""
			if tc.measured != "" {
				evPath = "docs/verify-evidence.yaml"
			}
			repo := initEvidenceRepo(t, evPath, validEvidenceYAML(tc.measured), true)
			m, err := loadRepoGateManifest(t, repo, tc.timeout, evPath)
			if err != nil {
				t.Fatalf("expected manifest acceptance for %s, got: %v", tc.name, err)
			}
			if got := m.Gate.DeclaredVerifyTimeoutDuration(); got != tc.wantDur {
				t.Errorf("DeclaredVerifyTimeoutDuration() = %s, want %s", got, tc.wantDur)
			}
			if got := m.Gate.DeclaredVerifyEvidencePath(); got != evPath {
				t.Errorf("DeclaredVerifyEvidencePath() = %q, want %q", got, evPath)
			}
			if got := m.Gate.DeclaredVerifyMeasuredDuration(); got != tc.wantMeas {
				t.Errorf("DeclaredVerifyMeasuredDuration() = %s, want %s", got, tc.wantMeas)
			}
		})
	}
}

// TestValidateGate_Negative_EvidenceAndCapRefusals verifies refusals for missing, untracked, above-cap, and unneeded evidence.
func TestValidateGate_Negative_EvidenceAndCapRefusals(t *testing.T) {
	cases := []struct {
		name      string
		timeout   string
		evPath    string
		evBody    string
		committed bool
		wantSubs  []string
	}{
		{"45m without evidence", "45m", "", "", false, []string{"gate.declared_verify_timeout", "45m", "30m0s", "gate.declared_verify_evidence"}},
		{"45m with untracked evidence", "45m", "docs/verify-evidence.yaml", validEvidenceYAML("38m"), false, []string{"gate.declared_verify_evidence", "docs/verify-evidence.yaml", "not tracked at HEAD"}},
		{"60m1s with evidence refused", "60m1s", "docs/verify-evidence.yaml", validEvidenceYAML("58m"), true, []string{"gate.declared_verify_timeout", "60m1s", "1h0m0s", "hard cap"}},
		{"90m with evidence refused", "90m", "docs/verify-evidence.yaml", validEvidenceYAML("58m"), true, []string{"gate.declared_verify_timeout", "90m", "1h0m0s", "hard cap"}},
		{"90m without evidence refused", "90m", "", "", false, []string{"gate.declared_verify_timeout", "90m", "1h0m0s", "hard cap"}},
		{"30m with unnecessary evidence refused", "30m", "docs/verify-evidence.yaml", validEvidenceYAML("25m"), true, []string{"gate.declared_verify_evidence", "docs/verify-evidence.yaml", "30m0s"}},
		{"omitted timeout with evidence refused", "", "docs/verify-evidence.yaml", validEvidenceYAML("25m"), true, []string{"gate.declared_verify_evidence", "docs/verify-evidence.yaml", "30m0s"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := initEvidenceRepo(t, tc.evPath, tc.evBody, tc.committed)
			_, err := loadRepoGateManifest(t, repo, tc.timeout, tc.evPath)
			assertGateErrorContains(t, tc.name, err, tc.wantSubs)
		})
	}
}

// TestValidateGate_Negative_PathContainmentRefusals verifies refusals for absolute, parent-step, and symlink-escaping evidence paths.
func TestValidateGate_Negative_PathContainmentRefusals(t *testing.T) {
	repo := initEvidenceRepo(t, "evidence.yaml", validEvidenceYAML("38m"), true)
	outside := filepath.Join(t.TempDir(), "outside-evidence.yaml")
	if err := os.WriteFile(outside, []byte(validEvidenceYAML("38m")), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Run("absolute path", func(t *testing.T) {
		_, err := loadRepoGateManifest(t, repo, "45m", outside)
		assertGateErrorContains(t, "absolute path", err, []string{"gate.declared_verify_evidence", outside, "not absolute"})
	})
	t.Run("parent step", func(t *testing.T) {
		for _, parentPath := range []string{"sub/../evidence.yaml", "../outside-evidence.yaml"} {
			_, err := loadRepoGateManifest(t, repo, "45m", parentPath)
			assertGateErrorContains(t, parentPath, err, []string{"gate.declared_verify_evidence", parentPath, ".."})
		}
	})
	t.Run("escaping symlink", func(t *testing.T) {
		linkPath := filepath.Join(repo, "docs", "escape.yaml")
		if err := util.MkdirConfined(repo, "docs", util.SecureDirPerm); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, linkPath); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		_, err := loadRepoGateManifest(t, repo, "45m", "docs/escape.yaml")
		assertGateErrorContains(t, "escaping symlink", err, []string{"gate.declared_verify_evidence", "docs/escape.yaml", "escapes"})
	})
}

// TestValidateGate_Negative_MalformedEvidenceRefused verifies that malformed or incomplete evidence YAML is refused.
func TestValidateGate_Negative_MalformedEvidenceRefused(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantSubs []string
	}{
		{"invalid YAML syntax", "command: [unclosed\n", []string{"gate.declared_verify_evidence", "docs/ev.yaml", "malformed evidence YAML"}},
		{"unknown YAML key", validEvidenceYAML("38m") + "extra: true\n", []string{"gate.declared_verify_evidence", "docs/ev.yaml", "extra"}},
		{"wrong command", "command: echo hi\nmeasured_duration: 38m\ndate: \"2026-11-18\"\ncommit: " + validEvidenceCommit + "\n", []string{"gate.declared_verify_evidence", "docs/ev.yaml", "make verify-all"}},
		{"invalid date", "command: make verify-all\nmeasured_duration: 38m\ndate: \"18-11-2026\"\ncommit: " + validEvidenceCommit + "\n", []string{"gate.declared_verify_evidence", "docs/ev.yaml", "YYYY-MM-DD"}},
		{"invalid commit", "command: make verify-all\nmeasured_duration: 38m\ndate: \"2026-11-18\"\ncommit: not-hex\n", []string{"gate.declared_verify_evidence", "docs/ev.yaml", "commit"}},
		{"non-positive measured_duration", "command: make verify-all\nmeasured_duration: 0s\ndate: \"2026-11-18\"\ncommit: " + validEvidenceCommit + "\n", []string{"gate.declared_verify_evidence", "docs/ev.yaml", "not positive"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := initEvidenceRepo(t, "docs/ev.yaml", tc.body, true)
			_, err := loadRepoGateManifest(t, repo, "45m", "docs/ev.yaml")
			assertGateErrorContains(t, tc.name, err, tc.wantSubs)
		})
	}
}

// TestValidateGate_Boundary_MeasuredDurationRelation checks both boundaries of MinDeclaredVerifyEvidenceDuration <= measured <= declaredBound.
func TestValidateGate_Boundary_MeasuredDurationRelation(t *testing.T) {
	cases := []struct {
		name     string
		timeout  string
		measured string
		wantErr  bool
		wantSubs []string
	}{
		{"at lower bound 20m accepted", "45m", "20m", false, nil},
		{"below lower bound 19m59s refused", "45m", "19m59s", true, []string{"gate.declared_verify_evidence", "19m59s", "20m0s"}},
		{"at upper bound equal to declared 45m accepted", "45m", "45m", false, nil},
		{"above upper bound 45m1s exceeds declared 45m refused", "45m", "45m1s", true, []string{"gate.declared_verify_evidence", "45m1s", "45m0s"}},
		{"at hard cap 60m with 60m measurement accepted", "60m", "60m", false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := initEvidenceRepo(t, "docs/ev.yaml", validEvidenceYAML(tc.measured), true)
			_, err := loadRepoGateManifest(t, repo, tc.timeout, "docs/ev.yaml")
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("expected %s to be accepted, got: %v", tc.name, err)
				}
				return
			}
			assertGateErrorContains(t, tc.name, err, tc.wantSubs)
		})
	}
}

// TestLoadDeclaredVerifyEvidence_Boundary_NilAndOversize checks nil context, nil policy, and oversized evidence file refusal.
func TestLoadDeclaredVerifyEvidence_Boundary_NilAndOversize(t *testing.T) {
	if err := ValidateGate(t.Context(), ".", nil); err != nil {
		t.Fatalf("ValidateGate(nil) = %v, want nil", err)
	}
	//nolint:staticcheck // nil context is the boundary case under test
	if err := ValidateGate(nil, ".", &GatePolicy{}); err == nil {
		t.Fatal("ValidateGate(nil ctx) must return an error")
	}
	var nilPolicy *GatePolicy
	if nilPolicy.DeclaredVerifyEvidencePath() != "" || nilPolicy.DeclaredVerifyMeasuredDuration() != 0 {
		t.Fatal("nil GatePolicy getters must return zero values")
	}
	oversized := validEvidenceYAML("35m") + "# " + strings.Repeat("x", MaxDeclaredVerifyEvidenceBytes) + "\n"
	repo := initEvidenceRepo(t, "docs/ev.yaml", oversized, true)
	//nolint:staticcheck // nil context is the boundary case under test
	if _, _, err := LoadDeclaredVerifyEvidence(nil, repo, "docs/ev.yaml", 45*time.Minute); err == nil {
		t.Fatal("LoadDeclaredVerifyEvidence(nil ctx) must return an error")
	}
	_, _, err := LoadDeclaredVerifyEvidence(t.Context(), repo, "docs/ev.yaml", 45*time.Minute)
	assertGateErrorContains(t, "oversized evidence", err, []string{"gate.declared_verify_evidence", "docs/ev.yaml"})
}

// assertGateErrorContains fails t when err is nil or misses any expected substring.
func assertGateErrorContains(t *testing.T, label string, err error, wantSubs []string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error, got nil", label)
	}
	msg := err.Error()
	for _, sub := range wantSubs {
		if !strings.Contains(msg, sub) {
			t.Errorf("%s: error %q does not contain %q", label, msg, sub)
		}
	}
}
