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
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// adoptionBranch is the branch ApplyMigration creates for the rewrite.
const adoptionBranch = "refactor/golusoris-adoption"

var (
	// ErrNotGitRepo is returned when the migration target is not a git work tree.
	ErrNotGitRepo = errors.New("needs: migration target is not a git work tree")
	// ErrBranchExists is returned when the adoption branch already exists. Resetting it
	// with `git checkout -B` would orphan every commit previously made on it.
	ErrBranchExists = errors.New("needs: adoption branch already exists")
)

// PlanMigration analyzes a repository and builds an actionable migration plan.
func PlanMigration(ctx context.Context, repoPath, frameworkPath string) (*MigrationPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	repoNeeds, err := ScanRepo(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to scan repo for migration: %w", err)
	}

	plan := &MigrationPlan{
		Repository:    repoNeeds.Repository,
		Framework:     defaultFrameworkModule + " v0.8.0",
		AddedRequires: []string{defaultFrameworkModule + " v0.8.0"},
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
		rawPath := strings.Trim(imp.Path.Value, `"`)
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

// ApplyMigration applies the planned migration changes to the target repository. Every
// mutating step is a hard failure: a partially applied rewrite reported as a success is
// what turns a migration into a broken build on an unexpected branch.
func ApplyMigration(ctx context.Context, repoPath string, plan *MigrationPlan) (*MigrationResult, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	result := &MigrationResult{Repository: plan.Repository, Branch: adoptionBranch}
	if err := createAdoptionBranch(ctx, repoPath, adoptionBranch); err != nil {
		result.Error = err.Error()
		return result, err
	}

	changed, err := applyPlannedRewrites(ctx, repoPath, plan, result)
	result.FilesChanged = changed
	if err != nil {
		result.Error = err.Error()
		return result, err
	}

	result.Success = true
	return result, nil
}

// applyPlannedRewrites performs the file, go.mod and guide mutations and returns the
// sorted list of files it changed.
func applyPlannedRewrites(ctx context.Context, repoPath string, plan *MigrationPlan, result *MigrationResult) ([]string, error) {
	changedFiles := make(map[string]struct{})

	for _, act := range plan.Replacements {
		target, err := confineToRepo(repoPath, act.File)
		if err != nil {
			return sortedFileList(changedFiles), err
		}
		if err := applyFileImportReplacement(target, act.OldImport, act.NewImport); err != nil {
			return sortedFileList(changedFiles), err
		}
		changedFiles[act.File] = struct{}{}
	}

	goModPath := filepath.Join(repoPath, "go.mod")
	if util.FileExists(goModPath) {
		if err := updateGoMod(goModPath, plan.AddedRequires, plan.DroppedRequires); err != nil {
			return sortedFileList(changedFiles), err
		}
		changedFiles[goModPath] = struct{}{}
		if _, err := util.RunCommand(ctx, repoPath, "go", "mod", "tidy"); err != nil {
			// Tidy needs the module proxy and may legitimately fail offline; record it
			// instead of failing the rewrite that already succeeded.
			result.Warnings = append(result.Warnings, fmt.Sprintf("go mod tidy failed: %v", err))
		}
	}

	guidePath := filepath.Join(repoPath, "MIGRATION.md")
	if err := util.WriteFileSecure(guidePath, []byte(plan.GuideMarkdown), manifestFilePerm); err != nil {
		return sortedFileList(changedFiles), fmt.Errorf("failed to write %q: %w", guidePath, err)
	}
	changedFiles[guidePath] = struct{}{}

	return sortedFileList(changedFiles), nil
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
func createAdoptionBranch(ctx context.Context, repoPath, branchName string) error {
	if _, err := util.RunCommand(ctx, repoPath, "git", "rev-parse", "--is-inside-work-tree"); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrNotGitRepo, repoPath, err)
	}
	if _, err := util.RunCommand(ctx, repoPath, "git", "rev-parse", "--verify", "--quiet",
		"refs/heads/"+branchName); err == nil {
		return fmt.Errorf("%w: %s", ErrBranchExists, branchName)
	}
	if _, err := util.RunCommand(ctx, repoPath, "git", "switch", "-c", branchName); err != nil {
		return fmt.Errorf("failed to create migration branch %q: %w", branchName, err)
	}
	return nil
}

// applyFileImportReplacement updates the import string inside a source file. Only the
// exact quoted import path is replaced; a prefix rewrite would also corrupt unrelated
// string literals that happen to start with the package name.
func applyFileImportReplacement(filePath, oldImport, newImport string) error {
	// #nosec G304 -- filePath was produced by confineToRepo, which resolves symlinks and
	// requires the result to stay inside the migration target.
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read %q: %w", filePath, err)
	}
	replaced := strings.ReplaceAll(string(data), `"`+oldImport+`"`, `"`+newImport+`"`)
	if replaced == string(data) {
		return nil
	}
	if err := util.WriteFileSecure(filePath, []byte(replaced), manifestFilePerm); err != nil {
		return fmt.Errorf("failed to write %q: %w", filePath, err)
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
	if err := util.WriteFileSecure(goModPath, body, manifestFilePerm); err != nil {
		return fmt.Errorf("failed to write %q: %w", goModPath, err)
	}
	return nil
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
	for scanner.Scan() {
		line := scanner.Text()
		module := requireModulePath(line)
		if module != "" {
			if _, drop := dropSet[module]; drop {
				continue
			}
			present[module] = struct{}{}
		}
		kept = append(kept, line)
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, nil, fmt.Errorf("failed to read %q: %w", goModPath, scanErr)
	}
	return kept, present, nil
}

// requireModulePath returns the module path a go.mod line requires, or "" when the line
// is a directive (module, go, toolchain, replace, ...) or carries no module token. Only
// the leading path token is compared, never a substring of the whole line.
func requireModulePath(line string) string {
	trimmed := strings.TrimSpace(line)
	if idx := strings.Index(trimmed, "//"); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	trimmed = strings.TrimPrefix(trimmed, "require ")
	fields := strings.Fields(trimmed)
	if len(fields) < 2 {
		return ""
	}
	switch fields[0] {
	case "module", "go", "toolchain", "replace", "exclude", "retract", "require":
		return ""
	}
	return fields[0]
}

// generateMigrationGuide creates a concise markdown walkthrough for the developer.
func generateMigrationGuide(plan *MigrationPlan) string {
	var sb strings.Builder
	writef(&sb, "# Migration Guide: %s -> %s\n\n", plan.Repository, plan.Framework)
	sb.WriteString("## Planned Dependency Changes\n\n")
	sb.WriteString("**Added Requirements:**\n")
	for _, a := range plan.AddedRequires {
		writef(&sb, "- `%s`\n", a)
	}
	sb.WriteString("\n**Dropped Third-Party Packages:**\n")
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
	sb.WriteString("1. Run `go mod tidy` to clean up indirect dependencies.\n")
	sb.WriteString("2. Verify compilation with `go build ./...`.\n")
	sb.WriteString("3. Run tests with `go test -v ./...`.\n")
	return sb.String()
}
