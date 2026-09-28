package needs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// maxMigrationFileBytes bounds how much of one Go source or go.mod the migration reads
// (HISS-02). It is the framework observer's source ceiling (maxFrameworkSourceBytes), so a
// file the observer could read whole is never too large to plan or rewrite.
const maxMigrationFileBytes = maxFrameworkSourceBytes

// migrationGuideName is the walkthrough a rewrite leaves at the repository root.
const migrationGuideName = "MIGRATION.md"

// defaultMigrationBranch is the branch an admitted migration creates when
// framework.migration_branch is unset.
const defaultMigrationBranch = "refactor/framework-adoption"

// MigrationBranch returns the branch an admitted migration creates: the configured
// framework.migration_branch, or the built-in refactor/framework-adoption when none is
// configured. An operator with an open branch under the former built-in name configures that
// name, since the migration refuses to reset an existing branch (ErrBranchExists).
func MigrationBranch(configured string) string {
	if configured == "" {
		return defaultMigrationBranch
	}
	return configured
}

var (
	// ErrNilMigrationPlan is returned when no plan was supplied.
	ErrNilMigrationPlan = errors.New("needs: migration plan cannot be nil")
	// ErrNotGitRepo is returned when the migration target is not a git work tree.
	ErrNotGitRepo = errors.New("needs: migration target is not a git work tree")
	// ErrBranchExists is returned when the adoption branch already exists. Resetting it
	// with `git checkout -B` would orphan every commit previously made on it.
	ErrBranchExists = errors.New("needs: adoption branch already exists")
)

// CommandRunner executes name with args inside dir and returns combined output.
// It supports isolated testing of rewrite primitives; it cannot admit a candidate.
type CommandRunner func(ctx context.Context, dir, name string, args ...string) (string, error)

// MigrationOptions configures ApplyMigrationWithOptions.
type MigrationOptions struct {
	// Runner is retained for source compatibility; admission happens before commands.
	Runner CommandRunner
	// SkipTidy is retained for source compatibility and cannot bypass admission.
	SkipTidy bool
}

// PlanMigration analyzes selected framework evidence and builds a blocked candidate.
// registry supplies the analyzers and framework targets; nil selects DefaultRegistry.
func PlanMigration(ctx context.Context, repoPath string, framework FrameworkSource, registry *AnalyzerRegistry) (*MigrationPlan, error) {
	analysis, err := analyzeMigration(ctx, repoPath, framework, registry)
	if err != nil {
		return nil, err
	}
	return planMigrationFromAnalysis(ctx, repoPath, analysis)
}

func planMigrationFromAnalysis(ctx context.Context, repoPath string, analysis *migrationAnalysis) (*MigrationPlan, error) {
	repoNeeds := analysis.report
	plan := &MigrationPlan{
		Repository:          repoNeeds.Repository,
		Framework:           analysis.framework.Name,
		FrameworkVersion:    "unverified",
		Status:              "candidate",
		CoverageBasis:       repoNeeds.Readiness.Basis,
		MappingAvailability: repoNeeds.Readiness.Score,
		Blockers:            migrationBlockers(analysis),
	}

	// Only Go modules may be dropped from go.mod or rewritten in Go import blocks. A
	// polyglot scan also yields npm/PyPI/cargo/system names such as "redis" or "click",
	// and treating those as Go module paths deletes unrelated go.mod lines.
	importReplacements := make(map[string]string)
	for _, dep := range repoNeeds.Dependencies {
		if dep.Relationship != nil || dep.Status != StatusCovered || dep.FrameworkReplacement == "" || dep.Ecosystem != "go" {
			continue
		}
		plan.DroppedRequires = append(plan.DroppedRequires, dep.Package)
		importReplacements[dep.Package] = dep.FrameworkReplacement
	}
	sort.Strings(plan.DroppedRequires)

	actions, err := findFileImportReplacements(ctx, repoPath, importReplacements)
	if err != nil {
		return nil, fmt.Errorf("failed to scan file import replacements: %w", err)
	}
	plan.Replacements = actions
	plan.GuideMarkdown = generateMigrationGuide(plan)

	return plan, nil
}

// findFileImportReplacements scans source files for import lines matching replaceable packages.
func findFileImportReplacements(ctx context.Context, rootDir string, replacements map[string]string) ([]ReplacementAction, error) {
	var actions []ReplacementAction
	root := filepath.Clean(rootDir)
	keys := sortedReplacementKeys(replacements)

	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if shouldSkipDir(info, path, root) {
			return filepath.SkipDir
		}
		// Symlinked sources are skipped: filepath.Walk reports them as plain files, and
		// rewriting through the link would mutate a file outside the migration target.
		if info == nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
			!strings.HasSuffix(info.Name(), ".go") {
			return nil
		}

		actions = append(actions, scanFileForReplacements(root, path, replacements, keys)...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk %q for import replacements: %w", root, err)
	}

	return actions, nil
}

// sortedReplacementKeys orders replacement keys longest-first so that a module and one of
// its sub-packages always resolve to the same, deterministic replacement regardless of
// Go's randomised map iteration order.
func sortedReplacementKeys(replacements map[string]string) []string {
	keys := make([]string, 0, len(replacements))
	for k := range replacements {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	return keys
}

// scanFileForReplacements inspects a single file's parsed import specs, read through
// util.GoImportPaths. Matching on the parsed import path (rather than on any line containing
// the package name) keeps string literals such as "redis:6379" out of the plan.
func scanFileForReplacements(root, filePath string, replacements map[string]string, keys []string) []ReplacementAction {
	// Unreadable, unparseable or generated sources carry no rewritable imports.
	node := parseImportsOnly(root, filePath)
	if node == nil {
		return nil
	}

	var actions []ReplacementAction
	importPaths := util.GoImportPaths(node)
	seen := make(map[string]struct{}, len(importPaths))
	for _, rawPath := range importPaths {
		if _, dup := seen[rawPath]; dup {
			continue
		}
		newPath, ok := rewriteImportPath(rawPath, replacements, keys)
		if !ok {
			continue
		}
		seen[rawPath] = struct{}{}
		actions = append(actions, ReplacementAction{
			File:        filePath,
			OldImport:   rawPath,
			NewImport:   newPath,
			Description: fmt.Sprintf("Replace %s with target framework package %s", rawPath, newPath),
		})
	}

	return actions
}

// parseImportsOnly parses just the import block of filePath, a Go file below root, returning
// nil when the file cannot be read through readMigrationFile or cannot be parsed. The source
// is read here rather than by go/parser, whose own read is unbounded and blocks on a FIFO.
func parseImportsOnly(root, filePath string) *ast.File {
	data, err := readMigrationFile(root, filePath)
	if err != nil {
		return nil
	}
	node, err := parser.ParseFile(token.NewFileSet(), filePath, data, parser.ImportsOnly)
	if err != nil {
		return nil
	}
	return node
}

// rewriteImportPath maps an import path onto its framework replacement, preserving the
// sub-package suffix. A key owns an import path under util.ModuleImportDir. keys must be
// ordered longest-first.
func rewriteImportPath(importPath string, replacements map[string]string, keys []string) (string, bool) {
	bound := len(keys)
	for i := 0; i < bound; i++ {
		key := keys[i]
		if _, inside := util.ModuleImportDir(importPath, key); !inside {
			continue
		}
		rewritten := replacements[key] + strings.TrimPrefix(importPath, key)
		if rewritten == importPath {
			return "", false
		}
		return rewritten, true
	}
	return "", false
}

// ApplyMigration rejects migration without verified version/API evidence.
func ApplyMigration(ctx context.Context, repoPath string, plan *MigrationPlan) (*MigrationResult, error) {
	return ApplyMigrationWithOptions(ctx, repoPath, plan, MigrationOptions{})
}

// ApplyMigrationWithOptions rejects candidates before commands or mutations: a plan with no
// target framework has nothing to rewrite (ErrFrameworkNotConfigured), and no
// module-version/API compatibility evidence validator is available yet. Options remain
// source-compatible but cannot bypass admission, including SkipTidy/Runner.
func ApplyMigrationWithOptions(ctx context.Context, repoPath string, plan *MigrationPlan, opts MigrationOptions) (*MigrationResult, error) {
	if ctx == nil {
		return nil, errors.New("needs: migration requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, ErrNilMigrationPlan
	}
	if plan.Framework == "" {
		return failedMigration(&MigrationResult{Repository: plan.Repository},
			fmt.Errorf("%s: %w", nothingToRewrite, ErrFrameworkNotConfigured))
	}
	return failedMigration(&MigrationResult{Repository: plan.Repository}, &UnverifiedMigrationError{})
}

func failedMigration(result *MigrationResult, err error) (*MigrationResult, error) {
	result.Error = err.Error()
	result.Warnings = append(result.Warnings, err.Error())
	return result, err
}

// applyPlannedRewrites returns every completed file mutation, including when a
// later step fails. Each path is confined before writing.
func applyPlannedRewrites(ctx context.Context, repoPath string, plan *MigrationPlan, run CommandRunner, skipTidy bool) ([]string, error) {
	changed := make(map[string]struct{})
	for _, act := range plan.Replacements {
		if err := ctx.Err(); err != nil {
			return sortedFileList(changed), err
		}
		if err := applyFileImportReplacement(repoPath, act.File, act.OldImport, act.NewImport); err != nil {
			return sortedFileList(changed), err
		}
		changed[act.File] = struct{}{}
	}
	if err := applyMigrationGoMod(ctx, repoPath, plan, changed, run, skipTidy); err != nil {
		return sortedFileList(changed), err
	}
	if err := ctx.Err(); err != nil {
		return sortedFileList(changed), err
	}
	guidePath, err := confineToRepo(repoPath, filepath.Join(repoPath, migrationGuideName))
	if err != nil {
		return sortedFileList(changed), err
	}
	if err := util.WriteFileConfined(repoPath, migrationGuideName, []byte(plan.GuideMarkdown), manifestFilePerm); err != nil {
		return sortedFileList(changed), fmt.Errorf("write migration guide: %w", err)
	}
	changed[guidePath] = struct{}{}
	return sortedFileList(changed), nil
}

func applyMigrationGoMod(ctx context.Context, repoPath string, plan *MigrationPlan, changed map[string]struct{}, run CommandRunner, skipTidy bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	goModPath, err := confineToRepo(repoPath, filepath.Join(repoPath, "go.mod"))
	if err != nil {
		return err
	}
	if _, err := os.Stat(goModPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect go.mod: %w", err)
	}
	if err := updateGoMod(repoPath, goModPath, plan.AddedRequires, plan.DroppedRequires); err != nil {
		return err
	}
	changed[goModPath] = struct{}{}
	if skipTidy {
		return nil
	}
	if out, err := run(ctx, repoPath, "go", "mod", "tidy"); err != nil {
		return fmt.Errorf("go mod tidy: %w: %s", err, out)
	}
	return nil
}

// sortedFileList flattens the changed-file set into a deterministic slice.
func sortedFileList(files map[string]struct{}) []string {
	return slices.Sorted(maps.Keys(files))
}

// readMigrationFile reads path, a file below root, through the bounded, root-anchored read
// the manifest readers share (util.ReadConfinedLimited): only a regular file is opened, so a
// FIFO named like a Go source or go.mod fails instead of blocking the migration past its
// deadline; a link that resolves outside root is refused; and at most maxMigrationFileBytes
// are read (BUG-857).
func readMigrationFile(root, path string) ([]byte, error) {
	rel, err := migrationRel(root, path)
	if err != nil {
		return nil, err
	}
	data, err := util.ReadConfinedLimited(root, rel, maxMigrationFileBytes)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	return data, nil
}

// migrationRel returns path relative to root, the member path the confined reader and writer
// resolve below root.
func migrationRel(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", fmt.Errorf("failed to relativize %q against %q: %w", path, root, err)
	}
	return rel, nil
}

// confineToRepo verifies that a planned target file really resolves inside repoPath, so
// that a symlinked or relocated entry cannot redirect the rewrite outside the repository.
func confineToRepo(repoPath, filePath string) (string, error) {
	rel, err := filepath.Rel(repoPath, filePath)
	if err != nil {
		return "", fmt.Errorf("failed to relativize %q against %q: %w", filePath, repoPath, err)
	}
	confined, err := util.ConfinePath(repoPath, rel)
	if err != nil {
		return "", fmt.Errorf("migration target %q escapes %q: %w", filePath, repoPath, err)
	}
	return confined, nil
}

// createAdoptionBranch creates the migration branch, refusing to reset an existing one.
func createAdoptionBranch(ctx context.Context, repoPath, branchName string, run CommandRunner) error {
	out, err := run(ctx, repoPath, "git", "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrNotGitRepo, repoPath, err)
	}
	if strings.TrimSpace(out) != "true" {
		return fmt.Errorf("%w: %s", ErrNotGitRepo, repoPath)
	}
	if _, err := run(ctx, repoPath, "git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branchName); err == nil {
		return fmt.Errorf("%w: %s", ErrBranchExists, branchName)
	} else {
		var exitCode interface{ ExitCode() int }
		if !errors.As(err, &exitCode) || exitCode.ExitCode() != 1 {
			return fmt.Errorf("inspect migration branch %q: %w", branchName, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := run(ctx, repoPath, "git", "switch", "-c", branchName); err != nil {
		return fmt.Errorf("create migration branch %q: %w", branchName, err)
	}
	return nil
}

// applyFileImportReplacement updates only parsed import literals, preserving
// aliases, comments, unrelated string values and the file's existing permissions.
func applyFileImportReplacement(repoRoot, filePath, oldImport, newImport string) error {
	target, err := confineToRepo(repoRoot, filePath)
	if err != nil {
		return err
	}
	data, err := readMigrationFile(repoRoot, target)
	if err != nil {
		return err
	}
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, target, data, parser.ImportsOnly)
	if err != nil {
		return fmt.Errorf("parse imports in %q: %w", target, err)
	}
	replaced := string(data)
	for i := len(node.Imports) - 1; i >= 0; i-- {
		imp := node.Imports[i]
		value, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return fmt.Errorf("decode import in %q: %w", target, err)
		}
		if value == oldImport {
			start := fset.Position(imp.Path.Pos()).Offset
			end := fset.Position(imp.Path.End()).Offset
			replaced = replaced[:start] + strconv.Quote(newImport) + replaced[end:]
		}
	}
	if replaced == string(data) {
		return nil
	}
	return writePreservingMode(repoRoot, target, []byte(replaced))
}

// writePreservingMode replaces path, a file below root, through util.WriteFileConfined: every
// directory between root and the file resolves through a pinned handle on root, so a
// symlinked ancestor cannot carry the rewrite outside the repository, and a link at the file
// itself is refused rather than written through (BUG-826). It retains existing permissions
// except for the world-write bit. Missing files use the secure default, while metadata
// errors abort before writing.
func writePreservingMode(root, path string, data []byte) error {
	perm := util.SecureFilePerm
	if info, err := os.Lstat(path); err == nil {
		perm = info.Mode().Perm() &^ 0o002
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect %q before writing: %w", path, err)
	}
	rel, err := migrationRel(root, path)
	if err != nil {
		return err
	}
	if err := util.WriteFileConfined(root, rel, data, perm); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}
	return nil
}

// updateGoMod drops superseded modules from goModPath, the go.mod below repoRoot, and
// appends the target framework's require.
func updateGoMod(repoRoot, goModPath string, added, dropped []string) error {
	dropSet := make(map[string]struct{}, len(dropped))
	for _, d := range dropped {
		dropSet[d] = struct{}{}
	}

	lines, present, err := readGoModLines(repoRoot, goModPath, dropSet)
	if err != nil {
		return err
	}

	for _, add := range added {
		module := strings.Fields(add)
		if len(module) == 0 {
			continue
		}
		if _, exists := present[module[0]]; exists {
			continue
		}
		lines = append(lines, "require "+add)
		present[module[0]] = struct{}{}
	}

	body := []byte(strings.Join(lines, "\n") + "\n")
	return writePreservingMode(repoRoot, goModPath, body)
}

// readGoModLines returns the go.mod lines that survive the drop set plus the set of
// module paths the file still requires. goModPath is read through readMigrationFile. A read
// error aborts before any write: rewriting go.mod from a truncated scan silently deletes
// the rest of the file.
func readGoModLines(repoRoot, goModPath string, dropSet map[string]struct{}) (kept []string, present map[string]struct{}, err error) {
	data, err := readMigrationFile(repoRoot, goModPath)
	if err != nil {
		return nil, nil, err
	}

	present = make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(data))
	inRequire := false
	scanErr := scanBoundedLines(scanner, func(line string) {
		module := requireModulePath(line, &inRequire)
		if module != "" {
			if _, drop := dropSet[module]; drop {
				return
			}
			present[module] = struct{}{}
		}
		kept = append(kept, line)
	})
	if scanErr != nil {
		return nil, nil, fmt.Errorf("failed to read %q: %w", goModPath, scanErr)
	}
	return kept, present, nil
}

// requireModulePath tracks require blocks so entries in replace/exclude blocks
// never become drop keys. Only complete module tokens are compared.
func requireModulePath(line string, inRequire *bool) string {
	trimmed := strings.TrimSpace(line)
	if idx := strings.Index(trimmed, "//"); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ""
	}
	if fields[0] == ")" {
		*inRequire = false
		return ""
	}
	if fields[0] == "require" && len(fields) >= 2 {
		if fields[1] == "(" {
			*inRequire = true
			return ""
		}
		if len(fields) >= 3 {
			return fields[1]
		}
	}
	if *inRequire && len(fields) >= 2 {
		return fields[0]
	}
	return ""
}

// generateMigrationGuide creates a concise markdown walkthrough for the developer.
func generateMigrationGuide(plan *MigrationPlan) string {
	var sb strings.Builder
	writef(&sb, "# Migration Guide: %s -> %s\n\n", plan.Repository, FrameworkDisplay(plan.Framework))
	writeMigrationEvidence(&sb, plan)
	sb.WriteString("## Candidate Dependency Changes\n\n")
	sb.WriteString("**Added Requirements:**\n")
	for _, a := range plan.AddedRequires {
		writef(&sb, "- `%s`\n", a)
	}
	sb.WriteString("\n**Proposed Third-Party Removals:**\n")
	for _, d := range plan.DroppedRequires {
		writef(&sb, "- `%s`\n", d)
	}
	sb.WriteString("\n## File Import Replacements\n\n")
	if len(plan.Replacements) == 0 {
		sb.WriteString("No direct file import replacements identified.\n")
	} else {
		for _, r := range plan.Replacements {
			writef(&sb, "- `%s`: `%s` -> `%s`\n", r.File, r.OldImport, r.NewImport)
		}
	}
	sb.WriteString("\n## Next Steps\n")
	sb.WriteString("1. Resolve an immutable module version matching the inspected framework.\n")
	sb.WriteString("2. Validate replacement APIs and consumer compilation/tests in isolation.\n")
	sb.WriteString("3. Executable migration admission remains unavailable pending a real evidence validator.\n")
	return sb.String()
}
