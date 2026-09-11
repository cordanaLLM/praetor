// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package bump

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestScanWorkflowActions_Positive verifies detection of actions and deprecation flags.
func TestScanWorkflowActions_Positive(t *testing.T) {
	tmpDir := t.TempDir()
	wfDir := filepath.Join(tmpDir, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0755); err != nil {
		t.Fatalf("failed to create wfDir: %v", err)
	}

	workflowContent := `name: CI
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v5
      - uses: fsfe/reuse-action@v5
`
	if err := os.WriteFile(filepath.Join(wfDir, "ci.yml"), []byte(workflowContent), 0644); err != nil {
		t.Fatalf("failed writing test workflow: %v", err)
	}

	actions, deps, err := ScanWorkflowActions(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(actions) != 3 {
		t.Fatalf("expected 3 actions, got %d", len(actions))
	}

	foundCheckout := false
	for _, act := range actions {
		if act.Action == "actions/checkout" {
			foundCheckout = true
			if act.CurrentVersion != "v3" || act.LatestVersion != "v4" || !act.Deprecated {
				t.Errorf("checkout@v3 metadata mismatch: %+v", act)
			}
		}
	}
	if !foundCheckout || len(deps) == 0 {
		t.Errorf("actions/checkout was not detected or deprecations missing")
	}
}

// TestScanWorkflowActions_Negative verifies error handling on invalid contexts.
func TestScanWorkflowActions_Negative(t *testing.T) {
	if _, err := AuditCodebaseVersions(nil, "/tmp", false); err == nil {
		t.Errorf("expected error with nil context")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AuditCodebaseVersions(ctx, "/tmp", false); err == nil {
		t.Errorf("expected error with cancelled context")
	}
}

// TestScanWorkflowActions_Boundary verifies edge cases with empty or missing directories.
func TestScanWorkflowActions_Boundary(t *testing.T) {
	tmpDir := t.TempDir()
	actions, deps, err := ScanWorkflowActions(tmpDir)
	if err != nil || len(actions) != 0 || len(deps) != 0 {
		t.Fatalf("expected 0 actions and 0 deps on missing dir")
	}

	wfDir := filepath.Join(tmpDir, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0755); err != nil {
		t.Fatalf("failed mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "empty.yml"), []byte(""), 0644); err != nil {
		t.Fatalf("failed write: %v", err)
	}

	actions, deps, err = ScanWorkflowActions(tmpDir)
	if err != nil || len(actions) != 0 || len(deps) != 0 {
		t.Errorf("expected 0 results on empty workflow")
	}
}
