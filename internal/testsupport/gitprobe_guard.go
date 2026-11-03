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
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// readOnlyGitProbeSubcommands lists the explicit allow-list of read-only git subcommands
// permitted in util.RunGitProbe (fleet rule: a reader that mirrors another parser uses an
// allow-list, never a list of unsafe cases; order engine-review-threads-first-20261101).
// util.RunGitProbe carries a 5 s read-inspection timeout (GitProbeTimeout) that flakes on
// loaded runners when executing mutations (order engine-agenthook-windows-flake-20261028).
var readOnlyGitProbeSubcommands = []string{
	"cat-file",
	"check-attr",
	"check-ignore",
	"describe",
	"diff",
	"hash-object",
	"log",
	"ls-files",
	"ls-tree",
	"merge-base",
	"remote",
	"rev-list",
	"rev-parse",
	"show",
	"status",
	"symbolic-ref",
}

var globalGitOptionsWithOperand = []string{
	"-C",
	"-c",
	"--attr-source",
	"--config-env",
	"--git-dir",
	"--namespace",
	"--super-prefix",
	"--work-tree",
}

var globalGitFlags = []string{
	"-P",
	"-h",
	"-p",
	"-v",
	"--bare",
	"--exec-path",
	"--glob-pathspecs",
	"--help",
	"--html-path",
	"--icase-pathspecs",
	"--info-path",
	"--literal-pathspecs",
	"--man-path",
	"--no-advice",
	"--no-lazy-fetch",
	"--no-optional-locks",
	"--no-pager",
	"--no-replace-objects",
	"--noglob-pathspecs",
	"--paginate",
	"--version",
}

var hashObjectReadOnlyFlags = []string{
	"--end-of-options",
	"--filters",
	"--literally",
	"--no-filters",
	"--no-literally",
	"--no-path",
	"--no-stdin",
	"--no-stdin-paths",
	"--stdin",
	"--stdin-paths",
}

var remoteGetURLFlags = []string{
	"--all",
	"--no-all",
	"--no-push",
	"--push",
}

var symbolicRefReadOnlyFlags = []string{
	"-q",
	"--no-quiet",
	"--no-recurse",
	"--no-short",
	"--quiet",
	"--recurse",
	"--short",
}

const (
	maxTrackedTestFiles    = 8192
	maxListingBytes        = 2 << 20
	maxTestFileBytes       = 2 << 20
	maxCallArgs            = 64
	maxGuardFindings       = 256
	maxReadOnlySubcommands = 64
	maxDeclSpecs           = 256
	maxConstDepth          = 8
)

// GitProbeFinding records an invalid mutating call to util.RunGitProbe in test fixtures.
type GitProbeFinding struct {
	File       string
	Line       int
	Subcommand string
}

// String formats the finding as a file, line, and reason summary.
func (f GitProbeFinding) String() string {
	return fmt.Sprintf("%s:%d: util.RunGitProbe called with disallowed subcommand %q; use testsupport.RunFixtureGit instead",
		f.File, f.Line, f.Subcommand)
}

// isReadOnlySubcommand reports whether subcmd is in the allow-list of read-only git subcommands.
func isReadOnlySubcommand(subcmd string) bool {
	for i, allowed := range readOnlyGitProbeSubcommands {
		if i >= maxReadOnlySubcommands {
			break
		}
		if subcmd == allowed {
			return true
		}
	}
	return false
}

// utilPackageNames collects package identifiers importing internal util within a parsed AST.
func utilPackageNames(f *ast.File) map[string]bool {
	names := map[string]bool{"util": true}
	for _, spec := range util.GoImportSpecs(f) {
		if isUtilImportPath(spec.Path) && spec.Name != "" && spec.Name != "_" && spec.Name != "." {
			names[spec.Name] = true
		}
	}
	return names
}

// isUtilImportPath reports whether importPath refers to the praetor internal util package.
func isUtilImportPath(importPath string) bool {
	return importPath == "github.com/cordanaLLM/praetor/internal/util" ||
		strings.HasSuffix(importPath, "/internal/util")
}

// isRunGitProbeSelector reports whether sel refers to RunGitProbe on a known util import.
func isRunGitProbeSelector(sel *ast.SelectorExpr, utilNames map[string]bool) bool {
	ident, ok := sel.X.(*ast.Ident)
	return ok && utilNames[ident.Name] && sel.Sel.Name == "RunGitProbe"
}

// isRunGitProbeCall reports whether call invokes RunGitProbe from a known util import.
func isRunGitProbeCall(call *ast.CallExpr, utilNames map[string]bool) bool {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return isRunGitProbeSelector(fn, utilNames)
	case *ast.Ident:
		return fn.Name == "RunGitProbe"
	default:
		return false
	}
}

// collectDirectRunGitProbeSelectors returns the set of RunGitProbe selectors used directly as call targets.
func collectDirectRunGitProbeSelectors(f *ast.File, utilNames map[string]bool) map[*ast.SelectorExpr]bool {
	called := make(map[*ast.SelectorExpr]bool)
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && isRunGitProbeSelector(sel, utilNames) {
			called[sel] = true
		}
		return true
	})
	return called
}

// probeGuard verifies that test fixture git invocations through util.RunGitProbe are strictly read-only.
type probeGuard struct {
	utilNames       map[string]bool
	directSelectors map[*ast.SelectorExpr]bool
	consts          map[string]ast.Expr
}

// probeNodeFinding inspects an AST node and returns a disallowed subcommand finding when present.
func (g *probeGuard) probeNodeFinding(n ast.Node) (token.Pos, string, bool) {
	switch node := n.(type) {
	case *ast.CallExpr:
		if !isRunGitProbeCall(node, g.utilNames) {
			return token.NoPos, "", false
		}
		subcmd, found := g.findDisallowedSubcommandArg(node)
		return node.Pos(), subcmd, found
	case *ast.SelectorExpr:
		if isRunGitProbeSelector(node, g.utilNames) && !g.directSelectors[node] {
			return node.Pos(), "<non-literal>", true
		}
		return token.NoPos, "", false
	default:
		return token.NoPos, "", false
	}
}

// extractLiteralString extracts a literal string value from an AST expression or constant identifier.
func (g *probeGuard) extractLiteralString(expr ast.Expr) (string, bool) {
	if ident, ok := expr.(*ast.Ident); ok {
		expr = g.resolveConstIdent(ident)
	}
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	val, err := strconv.Unquote(lit.Value)
	if err != nil {
		val = strings.Trim(lit.Value, "\"`")
	}
	return strings.TrimSpace(val), true
}

// resolveConstIdent resolves a constant identifier to its defining expression using collected file constants.
func (g *probeGuard) resolveConstIdent(ident *ast.Ident) ast.Expr {
	curr := ast.Expr(ident)
	for depth := 0; depth < maxConstDepth; depth++ {
		id, ok := curr.(*ast.Ident)
		if !ok {
			break
		}
		val, found := g.consts[id.Name]
		if !found {
			break
		}
		curr = val
	}
	return curr
}

// collectFileConstants extracts constant identifier definitions from file and local declarations.
func collectFileConstants(f *ast.File) map[string]ast.Expr {
	consts := make(map[string]ast.Expr)
	ambiguous := make(map[string]bool)
	ast.Inspect(f, func(n ast.Node) bool {
		genDecl, ok := n.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			return true
		}
		collectGenDeclConstants(genDecl, consts, ambiguous)
		return true
	})
	bound := 0
	for name := range ambiguous {
		if bound >= maxDeclSpecs {
			break
		}
		delete(consts, name)
		bound++
	}
	return consts
}

// collectGenDeclConstants records constant identifier bindings from a const declaration group.
func collectGenDeclConstants(genDecl *ast.GenDecl, consts map[string]ast.Expr, ambiguous map[string]bool) {
	var lastValues []ast.Expr
	for i := 0; i < len(genDecl.Specs) && i < maxDeclSpecs; i++ {
		valSpec, ok := genDecl.Specs[i].(*ast.ValueSpec)
		if !ok {
			continue
		}
		values := valSpec.Values
		if len(values) == 0 {
			values = lastValues
		} else {
			lastValues = values
		}
		recordValueSpecConstants(valSpec.Names, values, consts, ambiguous)
	}
}

// recordValueSpecConstants maps value specification names to corresponding expressions.
func recordValueSpecConstants(names []*ast.Ident, values []ast.Expr, consts map[string]ast.Expr, ambiguous map[string]bool) {
	for j := 0; j < len(names) && j < len(values) && j < maxCallArgs; j++ {
		name := names[j].Name
		if _, exists := consts[name]; exists {
			ambiguous[name] = true
			continue
		}
		consts[name] = values[j]
	}
}

// findDisallowedSubcommandArg inspects call arguments to locate any disallowed git subcommand invocation.
func (g *probeGuard) findDisallowedSubcommandArg(call *ast.CallExpr) (string, bool) {
	if len(call.Args) <= 3 {
		return "", false
	}
	callArgs := call.Args[3:]
	hasEllipsis := call.Ellipsis.IsValid()
	for i := 0; i < len(callArgs) && i < maxCallArgs; i++ {
		val, ok := g.extractLiteralString(callArgs[i])
		if !ok {
			return "<non-literal>", true
		}
		if !strings.HasPrefix(val, "-") {
			return g.checkSubcommandInvocation(val, callArgs[i+1:], hasEllipsis)
		}
		next, valid := consumeGlobalGitOption(val, i, callArgs, hasEllipsis)
		if !valid {
			return val, true
		}
		i = next
	}
	if len(callArgs) >= maxCallArgs {
		return "<argument bound exceeded>", true
	}
	return "<missing subcommand>", true
}

// consumeGlobalGitOption advances past a global git flag or option with its operand in call arguments.
func consumeGlobalGitOption(val string, i int, callArgs []ast.Expr, hasEllipsis bool) (int, bool) {
	advance, valid := classifyGlobalGitOption(val)
	if !valid {
		return i, false
	}
	if advance == 0 {
		return i, true
	}
	next := i + 1
	if next >= len(callArgs) {
		return i, false
	}
	if next == len(callArgs)-1 && hasEllipsis {
		return i, false
	}
	return next, true
}

// classifyGlobalGitOption reports the argument count consumed by a recognized global git option.
func classifyGlobalGitOption(arg string) (int, bool) {
	if slices.Contains(globalGitOptionsWithOperand, arg) {
		return 1, true
	}
	if slices.Contains(globalGitFlags, arg) {
		return 0, true
	}
	if isAttachedShortGlobalOption(arg) {
		return 0, true
	}
	if opt, _, ok := strings.Cut(arg, "="); ok {
		return 0, isAttachedLongGlobalOption(opt)
	}
	return 0, false
}

// isAttachedShortGlobalOption reports whether an argument is a short git option with an attached value.
func isAttachedShortGlobalOption(arg string) bool {
	return len(arg) > 2 && (strings.HasPrefix(arg, "-C") || strings.HasPrefix(arg, "-c"))
}

// isAttachedLongGlobalOption reports whether an argument is a long git option with an attached operand.
func isAttachedLongGlobalOption(opt string) bool {
	return slices.Contains(globalGitOptionsWithOperand, opt) || opt == "--exec-path" || opt == "--list-cmds"
}

// formatDisallowedConfig formats the config subcommand diagnostic string based on following arguments.
func (g *probeGuard) formatDisallowedConfig(args []ast.Expr) string {
	if len(args) > 0 {
		if nextVal, ok := g.extractLiteralString(args[0]); ok && nextVal == "--add" {
			return "config --add"
		}
	}
	return "config"
}

// checkSubcommandInvocation validates a git subcommand and its trailing arguments for mutation hazards.
func (g *probeGuard) checkSubcommandInvocation(subcmd string, args []ast.Expr, hasEllipsis bool) (string, bool) {
	if subcmd == "config" {
		return g.formatDisallowedConfig(args), true
	}
	if !isReadOnlySubcommand(subcmd) {
		return subcmd, true
	}
	if len(args) > maxCallArgs {
		return "<argument bound exceeded>", true
	}
	switch subcmd {
	case "hash-object":
		return g.checkHashObjectArgs(args, hasEllipsis)
	case "remote":
		return g.checkRemoteArgs(args, hasEllipsis)
	case "symbolic-ref":
		return g.checkSymbolicRefArgs(args, hasEllipsis)
	default:
		return "", false
	}
}

// checkHashObjectArgs verifies that git hash-object arguments do not include write flags or non-literals.
func (g *probeGuard) checkHashObjectArgs(args []ast.Expr, hasEllipsis bool) (string, bool) {
	if hasEllipsis {
		return "hash-object <non-literal>", true
	}
	for i := 0; i < len(args) && i < maxCallArgs; i++ {
		val, ok := g.extractLiteralString(args[i])
		if !ok {
			return "hash-object <non-literal>", true
		}
		if val == "--" {
			return "", false
		}
		if !strings.HasPrefix(val, "-") {
			continue
		}
		next, allowed := consumeHashObjectOption(val, i, len(args))
		if !allowed {
			return "hash-object " + val, true
		}
		i = next
	}
	return "", false
}

// consumeHashObjectOption validates an option for git hash-object and returns the advanced argument index.
func consumeHashObjectOption(val string, i, total int) (int, bool) {
	if val == "-t" || val == "--path" {
		if i+1 >= total {
			return i, false
		}
		return i + 1, true
	}
	allowed := slices.Contains(hashObjectReadOnlyFlags, val) || strings.HasPrefix(val, "--path=")
	return i, allowed
}

// checkRemoteArgs verifies that git remote arguments only invoke permitted read-only subcommands.
func (g *probeGuard) checkRemoteArgs(args []ast.Expr, hasEllipsis bool) (string, bool) {
	if hasEllipsis {
		return "remote <non-literal>", true
	}
	i, bad, failed := g.skipRemoteVerboseFlags(args)
	if failed {
		return bad, true
	}
	if i >= len(args) {
		return "", false
	}
	sub, ok := g.extractLiteralString(args[i])
	if !ok {
		return "remote <non-literal>", true
	}
	switch sub {
	case "get-url":
		return g.checkAllowedSubcommandFlags("remote get-url", args[i+1:], remoteGetURLFlags)
	case "show":
		return g.checkAllowedSubcommandFlags("remote show", args[i+1:], []string{"-n", "--no-n"})
	default:
		return "remote " + sub, true
	}
}

// skipRemoteVerboseFlags advances past leading verbose flags in git remote argument slices.
func (g *probeGuard) skipRemoteVerboseFlags(args []ast.Expr) (int, string, bool) {
	i := 0
	for ; i < len(args) && i < maxCallArgs; i++ {
		val, ok := g.extractLiteralString(args[i])
		if !ok {
			return i, "remote <non-literal>", true
		}
		if val != "-v" && val != "--verbose" && val != "--no-verbose" {
			break
		}
	}
	return i, "", false
}

// checkAllowedSubcommandFlags verifies that subcommand arguments only use allowed flags.
func (g *probeGuard) checkAllowedSubcommandFlags(prefix string, args []ast.Expr, allowedFlags []string) (string, bool) {
	for i := 0; i < len(args) && i < maxCallArgs; i++ {
		val, ok := g.extractLiteralString(args[i])
		if !ok {
			return prefix + " <non-literal>", true
		}
		if val == "--" {
			return "", false
		}
		if strings.HasPrefix(val, "-") && !slices.Contains(allowedFlags, val) {
			return prefix + " " + val, true
		}
	}
	return "", false
}

// checkSymbolicRefArgs verifies that git symbolic-ref arguments do not perform mutating ref updates.
func (g *probeGuard) checkSymbolicRefArgs(args []ast.Expr, hasEllipsis bool) (string, bool) {
	if hasEllipsis {
		return "symbolic-ref <non-literal>", true
	}
	var positional []string
	for i := 0; i < len(args) && i < maxCallArgs; i++ {
		val, ok := g.extractLiteralString(args[i])
		if !ok {
			return "symbolic-ref <non-literal>", true
		}
		if val == "--" {
			return g.checkSymbolicRefPositional(positional, args[i+1:])
		}
		if strings.HasPrefix(val, "-") {
			if !slices.Contains(symbolicRefReadOnlyFlags, val) {
				return "symbolic-ref " + val, true
			}
			continue
		}
		positional = append(positional, val)
		if len(positional) > 1 {
			return "symbolic-ref " + positional[0] + " " + positional[1], true
		}
	}
	return "", false
}

// checkSymbolicRefPositional checks positional arguments after a double-dash separator in symbolic-ref calls.
func (g *probeGuard) checkSymbolicRefPositional(positional []string, rest []ast.Expr) (string, bool) {
	for i := 0; i < len(rest) && i < maxCallArgs; i++ {
		val, ok := g.extractLiteralString(rest[i])
		if !ok {
			val = "<non-literal>"
		}
		positional = append(positional, val)
		if len(positional) > 1 {
			return "symbolic-ref " + positional[0] + " " + positional[1], true
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
	f, err := parser.ParseFile(fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filename, err)
	}
	utilNames := utilPackageNames(f)
	guard := &probeGuard{
		utilNames:       utilNames,
		directSelectors: collectDirectRunGitProbeSelectors(f, utilNames),
		consts:          collectFileConstants(f),
	}
	var findings []GitProbeFinding
	ast.Inspect(f, func(n ast.Node) bool {
		if len(findings) >= maxGuardFindings {
			return false
		}
		if pos, subcmd, found := guard.probeNodeFinding(n); found {
			loc := fset.Position(pos)
			findings = append(findings, GitProbeFinding{
				File:       loc.Filename,
				Line:       loc.Line,
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
	return scanTrackedTestListing(repoRoot, out.Stdout)
}

// scanTrackedTestListing parses a null-delimited test file list and scans each file for probe mutations.
func scanTrackedTestListing(repoRoot string, listing []byte) ([]GitProbeFinding, error) {
	trimmed := strings.TrimSuffix(string(listing), "\x00")
	if trimmed == "" {
		return nil, nil
	}
	entries := strings.Split(trimmed, "\x00")
	if len(entries) > maxTrackedTestFiles {
		return nil, fmt.Errorf("git ls-files listed %d tracked test files, exceeding limit %d", len(entries), maxTrackedTestFiles)
	}
	var allFindings []GitProbeFinding
	for i := 0; i < len(entries) && i < maxTrackedTestFiles; i++ {
		if entries[i] == "" {
			continue
		}
		findings, err := scanTrackedTestFile(repoRoot, entries[i])
		if err != nil {
			return nil, err
		}
		allFindings = append(allFindings, findings...)
		if len(allFindings) >= maxGuardFindings {
			return allFindings[:maxGuardFindings], fmt.Errorf("git probe findings reached limit %d", maxGuardFindings)
		}
	}
	return allFindings, nil
}

// scanTrackedTestFile reads a single test file from disk and scans its AST for probe mutations.
func scanTrackedTestFile(repoRoot, entry string) ([]GitProbeFinding, error) {
	fullPath := filepath.Join(repoRoot, filepath.FromSlash(entry))
	src, err := util.ReadFileLimited(fullPath, maxTestFileBytes)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", entry, err)
	}
	findings, err := ScanProbeMutations(entry, src)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", entry, err)
	}
	return findings, nil
}
