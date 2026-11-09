// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeWorkflowFile writes one workflow file under .github/workflows of root for a test.
func writeWorkflowFile(t *testing.T, dir, filename, content string) string {
	t.Helper()
	wfDir := filepath.Join(dir, ".github", "workflows")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a directory the test writes files into needs the owner execute bit; 0o700 is least privilege
	if err := os.MkdirAll(wfDir, 0o700); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	path := filepath.Join(wfDir, filename)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write workflow file: %v", err)
	}
	return dir
}

type publisherLimitCase struct {
	name       string
	filename   string
	content    string
	wantPub    string
	wantErrSub string
}

var measureReleasePublishersLimitsCases = []publisherLimitCase{
	{
		name:     "goreleaser args exceed bound",
		filename: "goreleaser.yml",
		content: `name: release
on: push
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: goreleaser/goreleaser-action@v6
        with:
          args: ` + strings.Repeat("opt ", maxRunScriptFields+1) + `
`,
		wantErrSub: "4096",
	},
	{
		name:     "run script exceeds bound",
		filename: "script.yml",
		content: `name: release
on: push
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - run: |
          ` + strings.ReplaceAll(strings.Repeat("echo safe\n", maxRunScriptFields)+"gh release create v1\n", "\n", "\n          ") + `
`,
		wantErrSub: "4096",
	},
	{
		name:     "boundary exactly maxRunScriptFields args reads last position",
		filename: "goreleaser.yml",
		content: `name: release
on: push
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: goreleaser/goreleaser-action@v6
        with:
          args: ` + strings.Repeat("--skip=announce ", maxRunScriptFields-1) + "release" + `
`,
		wantPub: ".github/workflows/goreleaser.yml",
	},
	{
		name:     "boundary script reads publisher at last position",
		filename: "script.yml",
		content: `name: release
on: push
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - run: ` + strings.Repeat("true;", 2046) + "gh release create v1" + `
`,
		wantPub: ".github/workflows/script.yml",
	},
}

// checkPublisherLimitFail verifies that MeasureReleasePublishers returns an error containing wantErrSub.
func checkPublisherLimitFail(t *testing.T, err error, wantErrSub string, publishers []string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error naming bound %d, got nil (publishers: %v)", maxRunScriptFields, publishers)
	}
	if !strings.Contains(err.Error(), wantErrSub) {
		t.Fatalf("expected error naming bound %s, got %v", wantErrSub, err)
	}
}

// checkPublisherLimitPass verifies that MeasureReleasePublishers succeeds and returns the expected publisher.
func checkPublisherLimitPass(t *testing.T, err error, publishers []string, wantPub string) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(publishers) != 1 || publishers[0] != wantPub {
		t.Fatalf("expected [%s], got %v", wantPub, publishers)
	}
}

// runPublisherLimitCase executes one limit boundary test case.
func runPublisherLimitCase(t *testing.T, tc publisherLimitCase) {
	t.Helper()
	dir := t.TempDir()
	writeWorkflowFile(t, dir, tc.filename, tc.content)
	publishers, err := MeasureReleasePublishers(context.Background(), dir)
	if tc.wantErrSub != "" {
		checkPublisherLimitFail(t, err, tc.wantErrSub, publishers)
		return
	}
	checkPublisherLimitPass(t, err, publishers, tc.wantPub)
}

// TestMeasureReleasePublishers_Limits covers the file, job, step and argument bounds of the publisher walk.
func TestMeasureReleasePublishers_Limits(t *testing.T) {
	for i := 0; i < len(measureReleasePublishersLimitsCases); i++ {
		tc := measureReleasePublishersLimitsCases[i]
		t.Run(tc.name, func(t *testing.T) {
			runPublisherLimitCase(t, tc)
		})
	}
}

type measureReleasePublishersCase struct {
	name       string
	workflows  map[string]string
	wantPubs   []string
	wantErrSub string
}

var measureReleasePublishersTableCases = []measureReleasePublishersCase{
	{
		name:      "no workflows directory",
		workflows: map[string]string{},
		wantPubs:  nil,
	},
	{
		name: "non-publishing workflow",
		workflows: map[string]string{
			"test.yml": `name: test
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: go test ./...
`,
		},
		wantPubs: nil,
	},
	{
		name: "action publisher",
		workflows: map[string]string{
			"publish.yml": `name: publish
on: push
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: softprops/action-gh-release@v2
`,
		},
		wantPubs: []string{".github/workflows/publish.yml"},
	},
	{
		name: "script publisher",
		workflows: map[string]string{
			"deploy.yml": `name: deploy
on: push
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - run: gh release create v1.0.0
`,
		},
		wantPubs: []string{".github/workflows/deploy.yml"},
	},
	{
		name: "never-run job skipped",
		workflows: map[string]string{
			"disabled.yml": `name: disabled
on: push
jobs:
  skip:
    if: false
    runs-on: ubuntu-latest
    steps:
      - run: gh release create v1.0.0
`,
		},
		wantPubs: nil,
	},
	{
		name: "never-run step skipped",
		workflows: map[string]string{
			"disabled_step.yml": `name: disabled
on: push
jobs:
  run:
    runs-on: ubuntu-latest
    steps:
      - if: ${{ false }}
        run: gh release create v1.0.0
`,
		},
		wantPubs: nil,
	},
	{
		name: "unparseable workflow yaml",
		workflows: map[string]string{
			"broken.yml": `name: broken
on: [
`,
		},
		wantErrSub: "read release publisher",
	},
}

// checkMeasureReleasePublishersError asserts that MeasureReleasePublishers failed with the expected substring.
func checkMeasureReleasePublishersError(t *testing.T, err error, wantErrSub string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), wantErrSub) {
		t.Fatalf("expected error containing %q, got: %v", wantErrSub, err)
	}
}

// checkMeasureReleasePublishersResults asserts that MeasureReleasePublishers returned the expected list of publishers.
func checkMeasureReleasePublishersResults(t *testing.T, err error, pubs, wantPubs []string) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pubs) != len(wantPubs) {
		t.Fatalf("MeasureReleasePublishers = %v, want %v", pubs, wantPubs)
	}
	for j := 0; j < len(pubs); j++ {
		if pubs[j] != wantPubs[j] {
			t.Errorf("publisher[%d] = %q, want %q", j, pubs[j], wantPubs[j])
		}
	}
}

// runMeasureReleasePublishersCase executes one workflow directory publisher test case.
func runMeasureReleasePublishersCase(t *testing.T, tc measureReleasePublishersCase) {
	t.Helper()
	dir := t.TempDir()
	for fname, content := range tc.workflows {
		writeWorkflowFile(t, dir, fname, content)
	}
	pubs, err := MeasureReleasePublishers(context.Background(), dir)
	if tc.wantErrSub != "" {
		checkMeasureReleasePublishersError(t, err, tc.wantErrSub)
		return
	}
	checkMeasureReleasePublishersResults(t, err, pubs, tc.wantPubs)
}

// TestMeasureReleasePublishers_Table covers publishing and non-publishing workflows end to end.
func TestMeasureReleasePublishers_Table(t *testing.T) {
	for i := 0; i < len(measureReleasePublishersTableCases); i++ {
		tc := measureReleasePublishersTableCases[i]
		t.Run(tc.name, func(t *testing.T) {
			runMeasureReleasePublishersCase(t, tc)
		})
	}
}

// TestMeasureReleasePublishers_NilContext checks that a nil context is refused.
func TestMeasureReleasePublishers_NilContext(t *testing.T) {
	var nilContext context.Context
	_, err := MeasureReleasePublishers(nilContext, ".")
	if err == nil {
		t.Fatal("expected error with nil context, got nil")
	}
}
