// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

func TestSecretsGate_RenderWorkflow_3D(t *testing.T) {
	root := newTestRepo(t, "secrets-gate-workflow")
	trackGoModule(t, root, "go.mod")

	s := familySession(t, false)
	s.repoPath = root

	// Write plain security workflow
	wfDir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	plainWorkflow := `name: Security Scan
jobs:
  vulncheck:
    runs-on: ubuntu-26.04
    steps:
      - name: Run Secrets Scan (gitleaks)
        run: go run -tags=secretsgate ./tools/secrets/gate
`
	secFile := filepath.Join(wfDir, "security.yml")
	mustWrite(t, secFile, plainWorkflow)

	// Declare HISS-12 exception in .standards.yaml
	manifest := `version: 1
exceptions:
  - rule: HISS-12
    path: creds.py
    tool: generic-api-key
    reason: planted test waiver
    expires: "2026-11-01"
`
	mustWrite(t, filepath.Join(root, ".standards.yaml"), manifest)

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
	if stripped != plainWorkflow {
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
