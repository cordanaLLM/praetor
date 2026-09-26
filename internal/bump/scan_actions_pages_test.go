// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package bump

import (
	"os"
	"path/filepath"
	"testing"
)

// writePagesWorkflow drops a single workflow file into repo/.github/workflows.
func writePagesWorkflow(t *testing.T, body string) string {
	t.Helper()
	repo := t.TempDir()
	wfDir := filepath.Join(repo, ".github", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatalf("mkdir workflows: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wfDir, "pages.yml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	return repo
}

// findPagesAction returns the candidate for an action name, or fails the test.
func findPagesAction(t *testing.T, got []ActionCandidate, name string) ActionCandidate {
	t.Helper()
	for _, cand := range got {
		if cand.Action == name {
			return cand
		}
	}
	t.Fatalf("action %s not detected in %+v", name, got)
	return ActionCandidate{}
}

// TestScanWorkflowActionsPagesPublishPositive: the pre-v5 Pages publish pair is
// reported as behind v5, and deploy-pages@v4 carries the Node 20 deprecation.
func TestScanWorkflowActionsPagesPublishPositive(t *testing.T) {
	repo := writePagesWorkflow(t, `name: Pages
jobs:
  publish:
    steps:
      - uses: actions/upload-pages-artifact@v3
      - uses: actions/deploy-pages@v4
`)
	got, deps, err := ScanWorkflowActions(t.Context(), repo)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	upload := findPagesAction(t, got, "actions/upload-pages-artifact")
	if upload.LatestVersion != "v5" || upload.CurrentVersion != "v3" {
		t.Errorf("upload-pages-artifact: want v3 -> v5, got %+v", upload)
	}
	deploy := findPagesAction(t, got, "actions/deploy-pages")
	if deploy.LatestVersion != "v5" || !deploy.Deprecated {
		t.Errorf("deploy-pages@v4: want latest v5 and deprecated, got %+v", deploy)
	}
	if len(deps) != 1 || deps[0].Component != "actions/deploy-pages@v4" {
		t.Errorf("want one deploy-pages@v4 deprecation, got %+v", deps)
	}
}

// TestScanWorkflowActionsPagesPublishNegative: the v5 pair is current, so nothing
// is flagged as deprecated and no deprecation warning is emitted.
func TestScanWorkflowActionsPagesPublishNegative(t *testing.T) {
	repo := writePagesWorkflow(t, `name: Pages
jobs:
  publish:
    steps:
      - uses: actions/upload-pages-artifact@v5
      - uses: actions/deploy-pages@v5
`)
	got, deps, err := ScanWorkflowActions(t.Context(), repo)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(deps) != 0 {
		t.Errorf("v5 pair must not warn, got %+v", deps)
	}
	for _, name := range []string{"actions/upload-pages-artifact", "actions/deploy-pages"} {
		cand := findPagesAction(t, got, name)
		if cand.Deprecated || cand.CurrentVersion != cand.LatestVersion {
			t.Errorf("%s: want current == latest and not deprecated, got %+v", name, cand)
		}
	}
}

// TestScanWorkflowActionsPagesPublishBoundary: a version outside the deprecation
// table is reported against the known latest without a warning, and an unmapped
// Pages action falls back to its own pin.
func TestScanWorkflowActionsPagesPublishBoundary(t *testing.T) {
	repo := writePagesWorkflow(t, `name: Pages
jobs:
  publish:
    steps:
      - uses: actions/deploy-pages@v5.0.1
      - uses: example/pages-publisher@v0.1.0
`)
	got, deps, err := ScanWorkflowActions(t.Context(), repo)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(deps) != 0 {
		t.Errorf("unlisted versions must not warn, got %+v", deps)
	}
	deploy := findPagesAction(t, got, "actions/deploy-pages")
	if deploy.Deprecated || deploy.LatestVersion != "v5" {
		t.Errorf("deploy-pages@v5.0.1: want latest v5 and not deprecated, got %+v", deploy)
	}
	unmapped := findPagesAction(t, got, "example/pages-publisher")
	if unmapped.LatestVersion != "v0.1.0" {
		t.Errorf("unmapped action must report its own pin, got %+v", unmapped)
	}
}
