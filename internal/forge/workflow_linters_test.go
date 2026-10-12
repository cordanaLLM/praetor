// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"testing"
)

func cmdFromLine(t *testing.T, line string) shellCommand {
	t.Helper()
	fields, err := scriptFields(line)
	if err != nil {
		t.Fatalf("scriptFields(%q): %v", line, err)
	}
	cmds := scriptCommands(fields)
	if len(cmds) == 0 {
		t.Fatalf("no commands in %q", line)
	}
	return parseShellCommand(cmds[0])
}

// Step 1: Script named after the tool.
// Count: node tools/markdownlint/verify.mjs (T=markdownlint),
// python3 -B scripts/test_hiss_semgrep.py (T=semgrep), ./scripts/run-gitleaks.sh (T=gitleaks).
// Must not count: python3 scripts/notsemgrepish.py, echo semgrep.
func TestCommandRunsTool_ScriptTools(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		line      string
		tool      string
		wantMatch bool
	}{
		{
			name:      "node markdownlint script in directory",
			line:      "node tools/markdownlint/verify.mjs",
			tool:      "markdownlint",
			wantMatch: true,
		},
		{
			name:      "python3 semgrep script with -B flag and underscore words",
			line:      "python3 -B scripts/test_hiss_semgrep.py",
			tool:      "semgrep",
			wantMatch: true,
		},
		{
			name:      "direct executable script named run-gitleaks.sh",
			line:      "./scripts/run-gitleaks.sh",
			tool:      "gitleaks",
			wantMatch: true,
		},
		{
			name:      "notsemgrepish is not a word match for semgrep",
			line:      "python3 scripts/notsemgrepish.py",
			tool:      "semgrep",
			wantMatch: false,
		},
		{
			name:      "echo semgrep is not a script execution",
			line:      "echo semgrep",
			tool:      "semgrep",
			wantMatch: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := cmdFromLine(t, tc.line)
			got := commandRunsTool(ctx, cmd, tc.tool, nil)
			if got != tc.wantMatch {
				t.Fatalf("commandRunsTool(%q, %q) = %v; want %v", tc.line, tc.tool, got, tc.wantMatch)
			}
		})
	}
}

// Step 2: go tool and go run runners.
// `go tool [flags] T` and `go run [flags] <path ending /T or /T@version>` count for T.
func TestCommandRunsTool_GoRunners(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		line      string
		tool      string
		wantMatch bool
	}{
		{
			name:      "go tool with -modfile flag",
			line:      "go tool -modfile=tools/go/go.mod gitleaks detect",
			tool:      "gitleaks",
			wantMatch: true,
		},
		{
			name:      "go tool without flags",
			line:      "go tool gitleaks detect",
			tool:      "gitleaks",
			wantMatch: true,
		},
		{
			name:      "go run with versioned import path",
			line:      "go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.60.0 run",
			tool:      "golangci-lint",
			wantMatch: true,
		},
		{
			name:      "go run with local path",
			line:      "go run ./cmd/gitleaks detect",
			tool:      "gitleaks",
			wantMatch: true,
		},
		{
			name:      "go run unrelated command",
			line:      "go run ./cmd/other",
			tool:      "gitleaks",
			wantMatch: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := cmdFromLine(t, tc.line)
			got := commandRunsTool(ctx, cmd, tc.tool, nil)
			if got != tc.wantMatch {
				t.Fatalf("commandRunsTool(%q, %q) = %v; want %v", tc.line, tc.tool, got, tc.wantMatch)
			}
		})
	}
}

// Step 3: Makefile variables expansion.
// Before reading a recipe line expand $(VAR) and ${VAR} from simple assignments
// in the root Makefile (=, :=, ::=, ?=), one level, at most 64 variables;
// an unknown variable stays as written.
func TestMakeVariableExpansion(t *testing.T) {
	lines := []string{
		"GO_SEC := go tool -modfile=tools/go/go.mod",
		"LINTER = golangci-lint",
		"FLAGS ::= --verbose",
		"EXTRA ?= check",
	}

	vars := collectMakeVariables(lines)
	if len(vars) != 4 {
		t.Fatalf("expected 4 collected variables, got %d: %+v", len(vars), vars)
	}

	tests := []struct {
		input string
		want  string
	}{
		{
			input: "$(GO_SEC) gitleaks detect",
			want:  "go tool -modfile=tools/go/go.mod gitleaks detect",
		},
		{
			input: "${LINTER} run ${FLAGS}",
			want:  "golangci-lint run --verbose",
		},
		{
			input: "$(UNKNOWN_VAR) gitleaks detect",
			want:  "$(UNKNOWN_VAR) gitleaks detect",
		},
		{
			input: "${UNDEFINED_VAR} ${EXTRA}",
			want:  "${UNDEFINED_VAR} check",
		},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := expandMakeVariables(tc.input, vars)
			if got != tc.want {
				t.Fatalf("expandMakeVariables(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}
