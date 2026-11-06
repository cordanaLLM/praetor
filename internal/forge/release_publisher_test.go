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
	if err := os.MkdirAll(wfDir, 0755); err != nil {
		t.Fatalf("mkdir workflow dir: %v", err)
	}
	path := filepath.Join(wfDir, filename)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write workflow file: %v", err)
	}
	return dir
}

// TestMeasureReleasePublishers_Limits covers the file, job, step and argument bounds of the publisher walk.
func TestMeasureReleasePublishers_Limits(t *testing.T) {
	t.Run("goreleaser args exceed bound", func(t *testing.T) {
		dir := t.TempDir()
		args := strings.Repeat("opt ", maxRunScriptFields+1)
		content := `name: release
on: push
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: goreleaser/goreleaser-action@v6
        with:
          args: ` + args + `
`
		writeWorkflowFile(t, dir, "goreleaser.yml", content)
		publishers, err := MeasureReleasePublishers(context.Background(), dir)
		if err == nil {
			t.Fatalf("expected error naming bound %d, got nil (publishers: %v)", maxRunScriptFields, publishers)
		}
		if !strings.Contains(err.Error(), "4096") {
			t.Fatalf("expected error naming bound 4096, got %v", err)
		}
	})

	t.Run("run script exceeds bound", func(t *testing.T) {
		dir := t.TempDir()
		script := strings.Repeat("echo safe\n", maxRunScriptFields) + "gh release create v1\n"
		content := `name: release
on: push
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - run: |
          ` + strings.ReplaceAll(script, "\n", "\n          ") + `
`
		writeWorkflowFile(t, dir, "script.yml", content)
		publishers, err := MeasureReleasePublishers(context.Background(), dir)
		if err == nil {
			t.Fatalf("expected error naming bound %d, got nil (publishers: %v)", maxRunScriptFields, publishers)
		}
		if !strings.Contains(err.Error(), "4096") {
			t.Fatalf("expected error naming bound 4096, got %v", err)
		}
	})

	t.Run("boundary exactly maxRunScriptFields args reads last position", func(t *testing.T) {
		dir := t.TempDir()
		args := strings.Repeat("--skip=announce ", maxRunScriptFields-1) + "release"
		content := `name: release
on: push
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - uses: goreleaser/goreleaser-action@v6
        with:
          args: ` + args + `
`
		writeWorkflowFile(t, dir, "goreleaser.yml", content)
		publishers, err := MeasureReleasePublishers(context.Background(), dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(publishers) != 1 || publishers[0] != ".github/workflows/goreleaser.yml" {
			t.Fatalf("expected [.github/workflows/goreleaser.yml], got %v", publishers)
		}
	})

	t.Run("boundary script reads publisher at last position", func(t *testing.T) {
		dir := t.TempDir()
		script := strings.Repeat("true;", 2046) + "gh release create v1"
		content := `name: release
on: push
jobs:
  publish:
    runs-on: ubuntu-latest
    steps:
      - run: ` + script + `
`
		writeWorkflowFile(t, dir, "script.yml", content)
		publishers, err := MeasureReleasePublishers(context.Background(), dir)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(publishers) != 1 || publishers[0] != ".github/workflows/script.yml" {
			t.Fatalf("expected [.github/workflows/script.yml], got %v", publishers)
		}
	})
}

// TestMeasureReleasePublishers_Table covers publishing and non-publishing workflows end to end.
func TestMeasureReleasePublishers_Table(t *testing.T) {
	cases := []struct {
		name       string
		workflows  map[string]string
		wantPubs   []string
		wantErrSub string
	}{
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

	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for fname, content := range tc.workflows {
				writeWorkflowFile(t, dir, fname, content)
			}
			pubs, err := MeasureReleasePublishers(context.Background(), dir)
			if tc.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("expected error containing %q, got: %v", tc.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(pubs) != len(tc.wantPubs) {
				t.Fatalf("MeasureReleasePublishers = %v, want %v", pubs, tc.wantPubs)
			}
			for j := 0; j < len(pubs); j++ {
				if pubs[j] != tc.wantPubs[j] {
					t.Errorf("publisher[%d] = %q, want %q", j, pubs[j], tc.wantPubs[j])
				}
			}
		})
	}
}

// TestMeasureReleasePublishers_NilContext checks that a nil context is refused.
func TestMeasureReleasePublishers_NilContext(t *testing.T) {
	_, err := MeasureReleasePublishers(nil, ".")
	if err == nil {
		t.Fatal("expected error with nil context, got nil")
	}
}
