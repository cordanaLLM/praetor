// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

func writeTestPackageJSON(t *testing.T, dir, relPath, content string) {
	t.Helper()
	fullPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(fullPath), err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", fullPath, err)
	}
}

// Positive, negative, and near-miss tests for exec forms:
// pnpm exec <tool>, npm exec <tool>, npm exec -- <tool>, yarn exec <tool>,
// bun x <tool>, and yarn <tool> when the word is not a yarn built-in command.
func TestCommandRunsTool_PackageManagersExec(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		line      string
		tool      string
		wantMatch bool
	}{
		// Evidence 1 from issue 1298: pnpm exec eslint scripts/
		{
			name:      "pnpm exec eslint scripts/ (issue 1298 evidence)",
			line:      "pnpm exec eslint scripts/",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "pnpm exec eslint without extra args",
			line:      "pnpm exec eslint",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "pnpm exec with double dash",
			line:      "pnpm exec -- eslint src/",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "npm exec eslint",
			line:      "npm exec eslint",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "npm exec -- eslint",
			line:      "npm exec -- eslint src/",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "npm exec with package option and double dash",
			line:      "npm exec --package=eslint -- eslint src/",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "yarn exec eslint",
			line:      "yarn exec eslint scripts/",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "bun x eslint",
			line:      "bun x eslint src/",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "bun x -- eslint",
			line:      "bun x -- eslint src/",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "yarn <tool> when not a builtin command",
			line:      "yarn eslint src/",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "yarn prettier --check .",
			line:      "yarn prettier --check .",
			tool:      "prettier",
			wantMatch: true,
		},
		// Evidence 2 from issue 1298: node_modules/.bin/eslint scripts/
		{
			name:      "node_modules/.bin/eslint scripts/ (issue 1298 evidence)",
			line:      "node_modules/.bin/eslint scripts/",
			tool:      "eslint",
			wantMatch: true,
		},
		// Near misses: tool name passed as argument to another command
		{
			name:      "near-miss pnpm exec echo eslint must not count",
			line:      "pnpm exec echo eslint",
			tool:      "eslint",
			wantMatch: false,
		},
		{
			name:      "near-miss npm exec echo eslint must not count",
			line:      "npm exec echo eslint",
			tool:      "eslint",
			wantMatch: false,
		},
		{
			name:      "near-miss npm exec -- echo eslint must not count",
			line:      "npm exec -- echo eslint",
			tool:      "eslint",
			wantMatch: false,
		},
		{
			name:      "near-miss yarn exec echo eslint must not count",
			line:      "yarn exec echo eslint",
			tool:      "eslint",
			wantMatch: false,
		},
		{
			name:      "near-miss bun x echo eslint must not count",
			line:      "bun x echo eslint",
			tool:      "eslint",
			wantMatch: false,
		},
		// Built-in yarn commands must not count as tools
		{
			name:      "yarn install is a built-in command",
			line:      "yarn install --frozen-lockfile",
			tool:      "install",
			wantMatch: false,
		},
		{
			name:      "yarn test is a built-in command when not in package scripts",
			line:      "yarn test",
			tool:      "test",
			wantMatch: false,
		},
		{
			name:      "yarn run with no script",
			line:      "yarn run",
			tool:      "run",
			wantMatch: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := cmdFromLine(t, tc.line)
			got := commandRunsTool(ctx, cmd, tc.tool, nil, nil)
			if got != tc.wantMatch {
				t.Fatalf("commandRunsTool(%q, %q) = %v; want %v", tc.line, tc.tool, got, tc.wantMatch)
			}
		})
	}
}

// Positive, negative, and near-miss tests for script forms:
// pnpm <script>, pnpm run <script>, npm run <script>, npm run-script <script>,
// npm test / npm run test, yarn <script>, yarn run <script>, bun run <script>.
func TestCommandRunsTool_PackageScripts(t *testing.T) {
	ctx := context.Background()

	scripts := map[string]string{
		"lint":         "eslint scripts/",
		"format:check": "prettier --check .",
		"test":         "eslint src/",
		"echo-fake":    "echo not-really-eslint",
		"bad-eslint":   "echo eslint",
	}

	tests := []struct {
		name      string
		line      string
		tool      string
		wantMatch bool
	}{
		// Evidence 3 from issue 1298: pnpm lint / pnpm format:check
		{
			name:      "pnpm lint calling eslint (issue 1298 evidence)",
			line:      "pnpm lint",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "pnpm format:check calling prettier (issue 1298 evidence)",
			line:      "pnpm format:check",
			tool:      "prettier",
			wantMatch: true,
		},
		{
			name:      "pnpm run lint",
			line:      "pnpm run lint",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "npm run lint",
			line:      "npm run lint",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "npm run-script lint",
			line:      "npm run-script lint",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "npm test calling eslint",
			line:      "npm test",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "npm run test calling eslint",
			line:      "npm run test",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "yarn lint calling eslint",
			line:      "yarn lint",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "yarn run lint calling eslint",
			line:      "yarn run lint",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "bun run lint calling eslint",
			line:      "bun run lint",
			tool:      "eslint",
			wantMatch: true,
		},
		{
			name:      "yarn test calling eslint via test script",
			line:      "yarn test",
			tool:      "eslint",
			wantMatch: true,
		},
		// Near misses: script named like the tool whose command runs something else
		{
			name:      "near-miss npm run bad-eslint runs echo eslint",
			line:      "npm run bad-eslint",
			tool:      "eslint",
			wantMatch: false,
		},
		{
			name:      "near-miss pnpm echo-fake must not count",
			line:      "pnpm echo-fake",
			tool:      "eslint",
			wantMatch: false,
		},
		{
			name:      "near-miss bun run echo-fake must not count",
			line:      "bun run echo-fake",
			tool:      "eslint",
			wantMatch: false,
		},
		// Near misses: script missing from package.json
		{
			name:      "near-miss missing script in pnpm run",
			line:      "pnpm run non-existent",
			tool:      "eslint",
			wantMatch: false,
		},
		{
			name:      "near-miss missing script in npm run",
			line:      "npm run non-existent",
			tool:      "eslint",
			wantMatch: false,
		},
		{
			name:      "near-miss missing script in bun run",
			line:      "bun run non-existent",
			tool:      "eslint",
			wantMatch: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := cmdFromLine(t, tc.line)
			got := commandRunsTool(ctx, cmd, tc.tool, nil, scripts)
			if got != tc.wantMatch {
				t.Fatalf("commandRunsTool(%q, %q) = %v; want %v", tc.line, tc.tool, got, tc.wantMatch)
			}
		})
	}
}

// Near miss: script named like the tool whose command runs something else.
func TestCommandRunsTool_NearMiss_ScriptNamedLikeToolRunsSomethingElse(t *testing.T) {
	ctx := context.Background()

	scripts := map[string]string{
		"eslint":   "echo not-the-real-eslint",
		"prettier": "echo not-prettier",
	}

	tests := []struct {
		name string
		line string
		tool string
	}{
		{"pnpm eslint runs echo", "pnpm eslint", "eslint"},
		{"pnpm run eslint runs echo", "pnpm run eslint", "eslint"},
		{"npm run eslint runs echo", "npm run eslint", "eslint"},
		{"npm run-script eslint runs echo", "npm run-script eslint", "eslint"},
		{"yarn eslint runs echo", "yarn eslint", "eslint"},
		{"yarn run eslint runs echo", "yarn run eslint", "eslint"},
		{"bun run eslint runs echo", "bun run eslint", "eslint"},
		{"pnpm prettier runs echo", "pnpm prettier", "prettier"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := cmdFromLine(t, tc.line)
			got := commandRunsTool(ctx, cmd, tc.tool, nil, scripts)
			if got {
				t.Fatalf("commandRunsTool(%q, %q) = true; want false (near-miss script)", tc.line, tc.tool)
			}
		})
	}
}

// Chained scripts expansion and depth boundary tests.
func TestCommandRunsTool_ChainedScripts(t *testing.T) {
	ctx := context.Background()

	t.Run("two hop script chain", func(t *testing.T) {
		scripts := map[string]string{
			"lint":    "pnpm run lint:js",
			"lint:js": "eslint src/",
		}
		cmd := cmdFromLine(t, "pnpm lint")
		if !commandRunsTool(ctx, cmd, "eslint", nil, scripts) {
			t.Fatal("expected chained script pnpm lint -> lint:js -> eslint to match")
		}
	})

	t.Run("multi command chained script", func(t *testing.T) {
		scripts := map[string]string{
			"check":     "pnpm run check:js && pnpm run check:fmt",
			"check:js":  "eslint src/",
			"check:fmt": "prettier --check src/",
		}
		cmd := cmdFromLine(t, "pnpm check")
		if !commandRunsTool(ctx, cmd, "eslint", nil, scripts) {
			t.Fatal("expected pnpm check to match eslint")
		}
		if !commandRunsTool(ctx, cmd, "prettier", nil, scripts) {
			t.Fatal("expected pnpm check to match prettier")
		}
	})

	t.Run("circular chained scripts terminate without error", func(t *testing.T) {
		scripts := map[string]string{
			"loop-a": "npm run loop-b",
			"loop-b": "npm run loop-a",
		}
		cmd := cmdFromLine(t, "npm run loop-a")
		if commandRunsTool(ctx, cmd, "eslint", nil, scripts) {
			t.Fatal("expected circular chain to return false")
		}
	})

	t.Run("depth bound 8 is followed", func(t *testing.T) {
		scripts := make(map[string]string)
		for i := 1; i < 8; i++ {
			scripts[fmt.Sprintf("step%d", i)] = fmt.Sprintf("pnpm run step%d", i+1)
		}
		scripts["step8"] = "eslint src/"

		cmd := cmdFromLine(t, "pnpm step1")
		if !commandRunsTool(ctx, cmd, "eslint", nil, scripts) {
			t.Fatal("expected chain of depth 8 to be followed")
		}
	})

	t.Run("depth bound exceeding 8 is cut off", func(t *testing.T) {
		scripts := make(map[string]string)
		for i := 1; i < 9; i++ {
			scripts[fmt.Sprintf("step%d", i)] = fmt.Sprintf("pnpm run step%d", i+1)
		}
		scripts["step9"] = "eslint src/"

		cmd := cmdFromLine(t, "pnpm step1")
		if commandRunsTool(ctx, cmd, "eslint", nil, scripts) {
			t.Fatal("expected chain of depth 9 to exceed depth bound and return false")
		}
	})
}

// Tests for reading package.json on disk via Step and MeasureWorkflowLinters.
func TestMeasureWorkflowLinters_PackageJSONIntegration(t *testing.T) {
	ctx := context.Background()

	t.Run("root package.json scripts with pnpm", func(t *testing.T) {
		root := t.TempDir()
		writeTestPackageJSON(t, root, "package.json", `{
			"name": "test-pkg",
			"scripts": {
				"lint": "eslint scripts/",
				"format:check": "prettier --check ."
			}
		}`)
		workflow := `name: CI
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: pnpm lint
      - run: pnpm format:check
`
		writeTestPackageJSON(t, root, ".github/workflows/ci.yml", workflow)

		present, err := MeasureWorkflowLinters(ctx, root, []string{"eslint", "prettier", "semgrep"})
		if err != nil {
			t.Fatalf("MeasureWorkflowLinters failed: %v", err)
		}
		if !present["eslint"] {
			t.Errorf("expected eslint to be present via pnpm lint")
		}
		if !present["prettier"] {
			t.Errorf("expected prettier to be present via pnpm format:check")
		}
		if present["semgrep"] {
			t.Errorf("expected semgrep to be absent")
		}
	})

	t.Run("step with working-directory key", func(t *testing.T) {
		root := t.TempDir()
		writeTestPackageJSON(t, root, "frontend/package.json", `{
			"name": "frontend",
			"scripts": {
				"lint": "eslint src/"
			}
		}`)
		workflow := `name: CI
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: pnpm lint
        working-directory: frontend
`
		writeTestPackageJSON(t, root, ".github/workflows/ci.yml", workflow)

		present, err := MeasureWorkflowLinters(ctx, root, []string{"eslint"})
		if err != nil {
			t.Fatalf("MeasureWorkflowLinters failed: %v", err)
		}
		if !present["eslint"] {
			t.Errorf("expected eslint to be present via frontend/package.json")
		}
	})

	t.Run("missing package.json does not fail audit", func(t *testing.T) {
		root := t.TempDir()
		workflow := `name: CI
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: pnpm lint
`
		writeTestPackageJSON(t, root, ".github/workflows/ci.yml", workflow)

		present, err := MeasureWorkflowLinters(ctx, root, []string{"eslint"})
		if err != nil {
			t.Fatalf("expected no error for missing package.json, got: %v", err)
		}
		if present["eslint"] {
			t.Errorf("expected eslint to be absent when package.json is missing")
		}
	})

	t.Run("unreadable malformed package.json does not fail audit", func(t *testing.T) {
		root := t.TempDir()
		writeTestPackageJSON(t, root, "package.json", `{ not valid json `)
		workflow := `name: CI
on: push
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - run: pnpm lint
`
		writeTestPackageJSON(t, root, ".github/workflows/ci.yml", workflow)

		present, err := MeasureWorkflowLinters(ctx, root, []string{"eslint"})
		if err != nil {
			t.Fatalf("expected no error for malformed package.json, got: %v", err)
		}
		if present["eslint"] {
			t.Errorf("expected eslint to be absent when package.json is malformed")
		}
	})

	t.Run("stepRunsTool with direct step", func(t *testing.T) {
		step := ghworkflow.Step{
			Run: "pnpm exec eslint scripts/",
		}
		if !stepRunsTool(ctx, step, "eslint", nil, nil) {
			t.Errorf("expected stepRunsTool to recognise pnpm exec eslint scripts/")
		}
	})
}
