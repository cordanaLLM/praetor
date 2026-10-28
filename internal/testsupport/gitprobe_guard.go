// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// mutatingGitSubcommands lists mutating git subcommands prohibited in util.RunGitProbe.
// util.RunGitProbe carries a 5 s read-inspection timeout (GitProbeTimeout) that flakes on
// loaded runners when executing mutations (order engine-agenthook-windows-flake-20261028).
var mutatingGitSubcommands = []string{"init", "commit", "add", "config"}

const (
	maxTrackedTestFiles = 8192
	maxListingBytes     = 2 << 20
	maxTestFileBytes    = 2 << 20
	maxCallArgs         = 64
	maxGuardFindings    = 256
)

// GitProbeFinding records an invalid mutating call to util.RunGitProbe in test fixtures.
type GitProbeFinding struct {
	File       string
	Line       int
	Subcommand string
}

func (f GitProbeFinding) String() string {
	return fmt.Sprintf("%s:%d: util.RunGitProbe called with mutating subcommand %q; use testsupport.RunFixtureGit instead",
		f.File, f.Line, f.Subcommand)
}

func isMutatingSubcommand(subcmd string) bool {
	for i, mutating := range mutatingGitSubcommands {
		if i >= len(mutatingGitSubcommands) {
			break
		}
		if subcmd == mutating {
			return true
		}
	}
	return false
}

func isRunGitProbeCall(call *ast.CallExpr) bool {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		ident, ok := fn.X.(*ast.Ident)
		return ok && ident.Name == "util" && fn.Sel.Name == "RunGitProbe"
	case *ast.Ident:
		return fn.Name == "RunGitProbe"
	default:
		return false
	}
}

func findMutatingSubcommandArg(args []ast.Expr) (string, bool) {
	if len(args) <= 3 {
		return "", false
	}
	for i, arg := range args[3:] {
		if i >= maxCallArgs {
			break
		}
		lit, ok := arg.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		val, err := strconv.Unquote(lit.Value)
		if err != nil {
			val = strings.Trim(lit.Value, "\"`")
		}
		if isMutatingSubcommand(val) {
			return val, true
		}
	}
	return "", false
}

// ScanProbeMutations inspects the AST of Go test source bytes for invalid mutating calls.
func ScanProbeMutations(filename string, src []byte) ([]GitProbeFinding, error) {
	if len(src) == 0 {
		return nil, nil
	}
	if !bytes.Contains(src, []byte("RunGitProbe")) {
		return nil, nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filename, err)
	}
	var findings []GitProbeFinding
	ast.Inspect(f, func(n ast.Node) bool {
		if len(findings) >= maxGuardFindings {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok || !isRunGitProbeCall(call) {
			return true
		}
		if subcmd, found := findMutatingSubcommandArg(call.Args); found {
			pos := fset.Position(call.Pos())
			findings = append(findings, GitProbeFinding{
				File:       pos.Filename,
				Line:       pos.Line,
				Subcommand: subcmd,
			})
		}
		return true
	})
	return findings, nil
}

// ScanTrackedTestGitProbes scans all tracked *_test.go files in repoRoot for mutating RunGitProbe calls.
// It uses git ls-files with a bounded walk (HISS-02).
func ScanTrackedTestGitProbes(ctx context.Context, repoRoot string) ([]GitProbeFinding, error) {
	if repoRoot == "" {
		out, err := util.RunGitProbe(ctx, ".", 4096, "rev-parse", "--show-toplevel")
		if err != nil {
			return nil, fmt.Errorf("git rev-parse --show-toplevel: %w", err)
		}
		repoRoot = filepath.Clean(strings.TrimSpace(string(out.Stdout)))
	}
	out, err := util.RunGitProbe(ctx, repoRoot, maxListingBytes, "ls-files", "-z", "--", "*_test.go")
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	entries := strings.Split(string(out.Stdout), "\x00")
	var allFindings []GitProbeFinding
	for i, entry := range entries {
		if i >= maxTrackedTestFiles || len(allFindings) >= maxGuardFindings {
			break
		}
		if entry == "" {
			continue
		}
		fullPath := filepath.Join(repoRoot, filepath.FromSlash(entry))
		src, err := util.ReadFileLimited(fullPath, maxTestFileBytes)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry, err)
		}
		findings, err := ScanProbeMutations(entry, src)
		if err != nil {
			return nil, err
		}
		allFindings = append(allFindings, findings...)
	}
	return allFindings, nil
}
