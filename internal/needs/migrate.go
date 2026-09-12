package needs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// migrationBranch is the branch ApplyMigration creates for the rewrite.
const migrationBranch = "refactor/golusoris-adoption"

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
func PlanMigration(ctx context.Context, repoPath, frameworkPath string) (*MigrationPlan, error) {
	analysis, err := analyzeMigration(ctx, repoPath, frameworkPath)
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
		if dep.Status != StatusCovered || dep.GolusorisReplacement == "" || dep.Ecosystem != "go" {
			continue
		}
		plan.DroppedRequires = append(plan.DroppedRequires, dep.Package)
		importReplacements[dep.Package] = dep.GolusorisReplacement
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

		actions = append(actions, scanFileForReplacements(path, replacements, keys)...)
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

// scanFileForReplacements inspects a single file's parsed import specs. Matching on the
// parsed import path (rather than on any line containing the package name) keeps string
// literals such as "redis:6379" out of the plan.
func scanFileForReplacements(filePath string, replacements map[string]string, keys []string) []ReplacementAction {
	// Unparseable or generated sources carry no rewritable imports.
	node := parseImportsOnly(filePath)
	if node == nil {
		return nil
	}

	var actions []ReplacementAction
	seen := make(map[string]struct{}, len(node.Imports))
	for _, imp := range node.Imports {
		rawPath, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
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
			Description: fmt.Sprintf("Replace %s with Golusoris %s", rawPath, newPath),
		})
	}

	return actions
}

// parseImportsOnly parses just the import block of a Go file, returning nil when the file
// cannot be parsed.
func parseImportsOnly(filePath string) *ast.File {
	node, err := parser.ParseFile(token.NewFileSet(), filePath, nil, parser.ImportsOnly)
	if err != nil {
		return nil
	}
	return node
}

// rewriteImportPath maps an import path onto its Golusoris replacement, preserving the
// sub-package suffix. keys must be ordered longest-first.
func rewriteImportPath(importPath string, replacements map[string]string, keys []string) (string, bool) {
	bound := len(keys)
	for i := 0; i < bound; i++ {
		key := keys[i]
		if importPath != key && !strings.HasPrefix(importPath, key+"/") {
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

// ApplyMigrationWithOptions rejects candidates before commands or mutations.
// No module-version/API compatibility evidence validator is available yet. Options
// remain source-compatible but cannot bypass admission, including SkipTidy/Runner.
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
	guidePath, err := confineToRepo(repoPath, filepath.Join(repoPath, "MIGRATION.md"))
	if err != nil {
		return sortedFileList(changed), err
	}
	if err := util.WriteFileSecure(guidePath, []byte(plan.GuideMarkdown), manifestFilePerm); err != nil {
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
	if err := updateGoMod(goModPath, plan.AddedRequires, plan.DroppedRequires); err != nil {
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
	out := make([]string, 0, len(files))
	for f := range files {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
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
	// #nosec G304 -- target was confined to repoRoot by confineToRepo.
	data, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("read %q: %w", target, err)
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
	return writePreservingMode(target, []byte(replaced))
}

// writePreservingMode retains existing permissions except for the world-write bit.
// Missing files use the secure default, while metadata errors abort before writing.
func writePreservingMode(path string, data []byte) error {
	perm := util.SecureFilePerm
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm() &^ 0o002
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect %q before writing: %w", path, err)
	}
	if err := util.WriteFileSecure(path, data, perm); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}
	return nil
}

// updateGoMod drops superseded modules and appends the Golusoris framework require.
func updateGoMod(goModPath string, added, dropped []string) error {
	dropSet := make(map[string]struct{}, len(dropped))
	for _, d := range dropped {
		dropSet[d] = struct{}{}
	}

	lines, present, err := readGoModLines(goModPath, dropSet)
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
	return writePreservingMode(goModPath, body)
}

// readGoModLines returns the go.mod lines that survive the drop set plus the set of
// module paths the file still requires. A read error aborts before any write: rewriting
// go.mod from a truncated scan silently deletes the rest of the file.
func readGoModLines(goModPath string, dropSet map[string]struct{}) (kept []string, present map[string]struct{}, err error) {
	// #nosec G304 -- goModPath is filepath.Join(repoPath, "go.mod") for the migration
	// target; the filename is a constant, not user input.
	file, openErr := os.Open(goModPath)
	if openErr != nil {
		return nil, nil, fmt.Errorf("failed to open %q: %w", goModPath, openErr)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close %q: %w", goModPath, cerr)
		}
	}()

	present = make(map[string]struct{})
	scanner := bufio.NewScanner(file)
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
	writef(&sb, "# Migration Guide: %s -> %s\n\n", plan.Repository, plan.Framework)
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
