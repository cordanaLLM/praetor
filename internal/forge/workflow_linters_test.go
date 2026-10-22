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
			got := commandRunsTool(ctx, cmd, tc.tool, nil, nil)
			if got != tc.wantMatch {
				t.Fatalf("commandRunsTool(%q, %q) = %v; want %v", tc.line, tc.tool, got, tc.wantMatch)
			}
		})
	}
}

func TestScriptPathMatchesTool_3D_Positive(t *testing.T) {
	tests := []struct {
		path string
		tool string
	}{
		{"run-clang-tidy.py", "clang-tidy"},
		{"clang_tidy_run.sh", "clang-tidy"},
		{"scripts/ci/clang-tidy/run.py", "clang-tidy"},
		{"scripts/ci/clang_tidy/run.py", "clang-tidy"},
		{"tools/run-golangci-lint.sh", "golangci-lint"},
	}
	for _, tc := range tests {
		if !scriptPathMatchesTool(tc.path, tc.tool) {
			t.Errorf("scriptPathMatchesTool(%q, %q) = false; want true", tc.path, tc.tool)
		}
	}
}

func TestScriptPathMatchesTool_3D_Negative(t *testing.T) {
	tests := []struct {
		path string
		tool string
	}{
		{"clang-tidyx.py", "clang-tidy"},
		{"xclang-tidy.py", "clang-tidy"},
		{"scripts/notsemgrepish.py", "semgrep"},
		{"scripts/ci/other.py", "clang-tidy"},
	}
	for _, tc := range tests {
		if scriptPathMatchesTool(tc.path, tc.tool) {
			t.Errorf("scriptPathMatchesTool(%q, %q) = true; want false", tc.path, tc.tool)
		}
	}
}

func TestScriptPathMatchesTool_3D_Boundary(t *testing.T) {
	tests := []struct {
		path      string
		tool      string
		wantMatch bool
	}{
		{"clang-tidy", "clang-tidy", true},
		{"clang-tidy.py", "clang-tidy", true},
		{"", "clang-tidy", false},
		{"run-clang-tidy.py", "", false},
		{"test_semgrep_run.py", "semgrep", true},
	}
	for _, tc := range tests {
		got := scriptPathMatchesTool(tc.path, tc.tool)
		if got != tc.wantMatch {
			t.Errorf("scriptPathMatchesTool(%q, %q) = %v; want %v", tc.path, tc.tool, got, tc.wantMatch)
		}
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
			got := commandRunsTool(ctx, cmd, tc.tool, nil, nil)
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

func TestCommandRunsTool_OptionNamedAfterTool(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		line      string
		tool      string
		wantMatch bool
	}{
		{
			name:      "python3 script with --gitleaks flag",
			line:      `python3 -I tools/ci/secret_scan.py --gitleaks "$gitleaks"`,
			tool:      "gitleaks",
			wantMatch: true,
		},
		{
			name:      "python3 script with --gitleaks= value flag",
			line:      `python3 tools/ci/secret_scan.py --gitleaks=/usr/bin/gitleaks`,
			tool:      "gitleaks",
			wantMatch: true,
		},
		{
			name:      "direct repo script with --gitleaks",
			line:      `./tools/ci/secret_scan.py --gitleaks "$gitleaks"`,
			tool:      "gitleaks",
			wantMatch: true,
		},
		{
			name:      "near-miss --not-gitleaks must not count",
			line:      `python3 -I tools/ci/secret_scan.py --not-gitleaks "$gitleaks"`,
			tool:      "gitleaks",
			wantMatch: false,
		},
		{
			name:      "echo --gitleaks is not run by an interpreter or repo path",
			line:      `echo --gitleaks`,
			tool:      "gitleaks",
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

func TestCommandRunsTool_RunnerSpecs(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		line      string
		tool      string
		wantMatch bool
	}{
		{
			name:      "pipx run with quoted version pinned spec",
			line:      `pipx run "yamllint==${YAMLLINT_VERSION}" .`,
			tool:      "yamllint",
			wantMatch: true,
		},
		{
			name:      "pipx run with >= version spec",
			line:      `pipx run 'yamllint>=1.2.0' .`,
			tool:      "yamllint",
			wantMatch: true,
		},
		{
			name:      "pipx run with ~= version spec",
			line:      `pipx run "yamllint~=1.2.0" .`,
			tool:      "yamllint",
			wantMatch: true,
		},
		{
			name:      "npx with quoted @version spec and flags",
			line:      `npx --yes "markdownlint-cli2@${MARKDOWNLINT_VERSION}"`,
			tool:      "markdownlint",
			wantMatch: true,
		},
		{
			name:      "go run with version spec and env var",
			line:      `go run "github.com/golangci/golangci-lint/cmd/golangci-lint@${VERSION}" run`,
			tool:      "golangci-lint",
			wantMatch: true,
		},
		{
			name:      "docker run with image tag and env var",
			line:      `docker run --rm ghcr.io/aquasecurity/trivy:${TRIVY_VERSION} fs .`,
			tool:      "trivy",
			wantMatch: true,
		},
		{
			name:      "near-miss pipx run yamllint-extra must not count",
			line:      `pipx run yamllint-extra`,
			tool:      "yamllint",
			wantMatch: false,
		},
		{
			name:      "near-miss pipx run with versioned yamllint-extra must not count",
			line:      `pipx run "yamllint-extra==1.0"`,
			tool:      "yamllint",
			wantMatch: false,
		},
		{
			name:      "near-miss uvx yamllint-extra must not count",
			line:      `uvx yamllint-extra`,
			tool:      "yamllint",
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

func TestCommandRunsTool_WrapperPrograms(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		line      string
		tool      string
		wantMatch bool
	}{
		{
			name:      "xargs -r shellcheck -x",
			line:      `xargs -r shellcheck -x`,
			tool:      "shellcheck",
			wantMatch: true,
		},
		{
			name:      "xargs with -I placeholder flag",
			line:      `xargs -I {} shellcheck {}`,
			tool:      "shellcheck",
			wantMatch: true,
		},
		{
			name:      "xargs with multiple value flags",
			line:      `xargs -n 1 -P 4 shellcheck`,
			tool:      "shellcheck",
			wantMatch: true,
		},
		{
			name:      "find -exec shellcheck",
			line:      `find . -name '*.sh' -exec shellcheck {} +`,
			tool:      "shellcheck",
			wantMatch: true,
		},
		{
			name:      "env with VAR=x assignments",
			line:      `env VAR=x shellcheck -x`,
			tool:      "shellcheck",
			wantMatch: true,
		},
		{
			name:      "sudo shellcheck",
			line:      `sudo shellcheck -x`,
			tool:      "shellcheck",
			wantMatch: true,
		},
		{
			name:      "time shellcheck",
			line:      `time shellcheck -x`,
			tool:      "shellcheck",
			wantMatch: true,
		},
		{
			name:      "nice -n 10 shellcheck",
			line:      `nice -n 10 shellcheck -x`,
			tool:      "shellcheck",
			wantMatch: true,
		},
		{
			name:      "near-miss xargs echo shellcheck must not count",
			line:      `xargs echo shellcheck`,
			tool:      "shellcheck",
			wantMatch: false,
		},
		{
			name:      "near-miss find -exec echo shellcheck must not count",
			line:      `find . -exec echo shellcheck {} +`,
			tool:      "shellcheck",
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
