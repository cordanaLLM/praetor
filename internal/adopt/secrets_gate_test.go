// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/toolpins"
	"github.com/cordanaLLM/praetor/internal/util"
	secretsassets "github.com/cordanaLLM/praetor/tools/secrets"
)

func TestSecretsGate_AssetsEmitted(t *testing.T) {
	root := newTestRepo(t, "secrets-gate-assets")
	trackGoModule(t, root, "go.mod")

	s := familySession(t, false)
	s.repoPath = root

	if err := reconcileSecretsGate(t.Context(), s); err != nil {
		t.Fatalf("reconcileSecretsGate failed: %v", err)
	}

	for _, name := range secretsassets.Names() {
		rel := filepath.Join(secretsassets.Directory, name)
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("asset %s not emitted: %v", rel, err)
		}
		canonical, err := secretsassets.Read(name)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != string(canonical) {
			t.Fatalf("asset %s differs from canonical embedded bytes", rel)
		}
	}
}

const plainSecurityWorkflow = `name: Security Scan
jobs:
  vulncheck:
    runs-on: ubuntu-26.04
    steps:
      - name: Run Secrets Scan (gitleaks)
        run: go run -tags=secretsgate ./tools/secrets/gate
`

const hiss12ExceptionManifest = `version: 1
exceptions:
  - rule: HISS-12
    path: creds.py
    tool: generic-api-key
    reason: planted test waiver
    expires: "2026-11-01"
`

func setupSecretsGateWorkflowFixture(t *testing.T) (*adoptSession, string, string) {
	t.Helper()
	root := newTestRepo(t, "secrets-gate-workflow")
	trackGoModule(t, root, "go.mod")

	s := familySession(t, false)
	s.repoPath = root

	wfDir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	secFile := filepath.Join(wfDir, "security.yml")
	mustWrite(t, secFile, plainSecurityWorkflow)
	mustWrite(t, filepath.Join(root, ".standards.yaml"), hiss12ExceptionManifest)
	return s, root, secFile
}

func TestSecretsGate_RenderWorkflow_3D(t *testing.T) {
	s, root, secFile := setupSecretsGateWorkflowFixture(t)

	// 1. First run renders SECRETS_EXCEPTIONS
	if err := reconcileSecretsGate(t.Context(), s); err != nil {
		t.Fatalf("reconcileSecretsGate: %v", err)
	}
	rendered := mustRead(t, secFile)
	if !strings.Contains(rendered, "SECRETS_EXCEPTIONS:") {
		t.Fatalf("workflow lacks SECRETS_EXCEPTIONS:\n%s", rendered)
	}
	if !strings.Contains(rendered, "planted test waiver") {
		t.Fatalf("workflow lacks planted reason:\n%s", rendered)
	}

	// 2. Second run is a no-op / identical
	if err := reconcileSecretsGate(t.Context(), s); err != nil {
		t.Fatalf("reconcileSecretsGate 2nd run: %v", err)
	}
	if got := mustRead(t, secFile); got != rendered {
		t.Fatalf("workflow changed on second run")
	}

	// 3. Removing exception strips SECRETS_EXCEPTIONS back to plain
	mustWrite(t, filepath.Join(root, ".standards.yaml"), "version: 1\n")
	if err := reconcileSecretsGate(t.Context(), s); err != nil {
		t.Fatalf("reconcileSecretsGate stripped run: %v", err)
	}
	stripped := mustRead(t, secFile)
	if strings.Contains(stripped, "SECRETS_EXCEPTIONS:") {
		t.Fatalf("workflow still contains SECRETS_EXCEPTIONS after manifest removal:\n%s", stripped)
	}
	if stripped != plainSecurityWorkflow {
		t.Fatalf("workflow did not restore to plain:\n%s", stripped)
	}
}

func TestSecretsGate_NonGoRepo_Skips(t *testing.T) {
	root := newTestRepo(t, "secrets-gate-non-go")
	// No go.mod tracked

	s := familySession(t, false)
	s.repoPath = root

	if err := reconcileSecretsGate(t.Context(), s); err != nil {
		t.Fatalf("reconcileSecretsGate non-go failed: %v", err)
	}

	gateMain := filepath.Join(root, "tools", "secrets", "gate", "main.go")
	if _, err := os.Stat(gateMain); !os.IsNotExist(err) {
		t.Fatalf("expected gate not to be emitted for non-Go repo, but found: %s", gateMain)
	}
}

func TestSecretsGate_AdoptionBaseline_3D(t *testing.T) {
	t.Run("missing gitleaks reports pinned version", func(t *testing.T) {
		root := newTestRepo(t, "secrets-missing-gitleaks")
		s := familySession(t, false)
		s.repoPath = root
		if err := reconcileSecretsGate(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		details := actionDetailsFor(s.report, "secrets")
		if !strings.Contains(details, "gitleaks not found on PATH") || !strings.Contains(details, toolpins.GitleaksVersion) {
			t.Fatalf("details = %q, want missing gitleaks and pinned version %s", details, toolpins.GitleaksVersion)
		}
	})
	t.Run("gitleaks on PATH records HISS-12 baseline once", func(t *testing.T) {
		root := newTestRepo(t, "secrets-with-gitleaks")
		stubDir := t.TempDir()
		script := "while [ $# -gt 0 ]; do\n  if [ \"$1\" = \"--report-path\" ]; then\n    shift\n    printf '%s' '[{\"RuleID\":\"generic-api-key\",\"File\":\"creds.py\",\"StartLine\":5,\"Commit\":\"aaaa\",\"Secret\":\"SYNTH-NEVER-SAVE\",\"Fingerprint\":\"synth-adopt-fp\"}]' > \"$1\"\n    exit 1\n  fi\n  shift\ndone\nexit 0\n"
		writeStub(t, stubDir, "gitleaks", script)
		t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		s := familySession(t, false)
		s.repoPath = root
		if err := reconcileSecretsGate(t.Context(), s); err != nil {
			t.Fatal(err)
		}
		details := actionDetailsFor(s.report, "secrets")
		if !strings.Contains(details, "Recorded 1 HISS-12 secret finding(s)") {
			t.Fatalf("details = %q, want 1 recorded finding", details)
		}
		base, err := baseline.LoadBaseline(filepath.Join(root, baselineFile))
		if err != nil {
			t.Fatal(err)
		}
		if base.Absent || len(base.Infractions) != 1 || base.Infractions[0].RuleID != "HISS-12" || base.Infractions[0].Fingerprint != "synth-adopt-fp" {
			t.Fatalf("baseline = %+v; want 1 HISS-12 entry with synth-adopt-fp", base)
		}
	})
	t.Run("gitleaks as mise shim in isolated environment succeeds", testSecretsGate_MiseShimIsolated)
}

func testSecretsGate_MiseShimIsolated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on windows")
	}
	root := newTestRepo(t, "secrets-with-mise-shim")
	normalHome := t.TempDir()
	isolatedHome := t.TempDir()
	realDir := t.TempDir()
	realGitleaks := testsupport.BuildFakeGitleaks(t, realDir)
	shimDir := t.TempDir()
	miseBin := testsupport.BuildFakeMise(t, shimDir, normalHome, realGitleaks)
	if err := os.Symlink(miseBin, filepath.Join(shimDir, "gitleaks")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", normalHome)
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	isolatedCtx, err := util.WithCommandEnvironment(t.Context(), []string{
		"HOME=" + isolatedHome,
		"PATH=/usr/bin:/bin",
	})
	if err != nil {
		t.Fatal(err)
	}
	s := familySession(t, false)
	s.repoPath = root

	if err := reconcileSecretsGate(isolatedCtx, s); err != nil {
		t.Fatalf("reconcileSecretsGate failed: %v", err)
	}
	details := actionDetailsFor(s.report, "secrets")
	if !strings.Contains(details, "Recorded 0 HISS-12 secret finding(s)") {
		t.Fatalf("details = %q, want 0 recorded findings", details)
	}
}

func actionDetailsFor(rep *AdoptReport, path string) string {
	var parts []string
	for i := 0; i < len(rep.ActionDetails); i++ {
		if rep.ActionDetails[i].Path == path {
			parts = append(parts, rep.ActionDetails[i].Details)
		}
	}
	return strings.Join(parts, "\n")
}
