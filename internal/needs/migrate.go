package needs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// migrationBranch is the branch ApplyMigration checks out before mutating a repository.
const migrationBranch = "refactor/golusoris-adoption"

// ErrNilMigrationPlan is returned when a migration is applied without a plan.
var ErrNilMigrationPlan = errors.New("needs: migration plan cannot be nil")

// CommandRunner executes name with args inside dir and returns the trimmed combined
// output. It is the seam ApplyMigration uses for its git and `go mod tidy` calls so that
// a caller - a hermetic test in particular - can apply a migration without invoking git,
// the module proxy or the network.
type CommandRunner func(ctx context.Context, dir, name string, args ...string) (string, error)

// MigrationOptions configures ApplyMigrationWithOptions.
type MigrationOptions struct {
	// Runner executes the external commands; nil selects util.RunCommand, which
	// enforces a context deadline on every subprocess (HISS-02).
	Runner CommandRunner
	// SkipTidy suppresses the `go mod tidy` invocation, which would otherwise reach
	// the module proxy.
	SkipTidy bool
}

// PlanMigration analyzes a repository and builds an actionable migration plan.
//
// frameworkPath is honoured: a checkout is resolved through its own go.mod and a
// module-shaped value is used verbatim, so the plan names the framework the operator
// asked for rather than a hard-coded constant, and never a local filesystem path.
func PlanMigration(ctx context.Context, repoPath, frameworkPath string) (*MigrationPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	repoNeeds, err := ScanRepo(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to scan repo for migration: %w", err)
	}

	framework := ResolveFrameworkModule(frameworkPath) + " " + defaultFrameworkVersion
	plan := &MigrationPlan{
		Repository:    repoNeeds.Repository,
		Framework:     framework,
		AddedRequires: []string{framework},
	}

	importReplacements := make(map[string]string)
	for _, dep := range repoNeeds.Dependencies {
		if dep.GolusorisReplacement != "" && dep.Status == StatusCovered {
			plan.DroppedRequires = append(plan.DroppedRequires, dep.Package)
			importReplacements[dep.Package] = dep.GolusorisReplacement
		}
	}

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

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if shouldSkipDir(info, path) {
			return filepath.SkipDir
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".go") {
			return nil
		}

		actions = append(actions, scanFileForReplacements(path, replacements)...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %q for import replacements: %w", rootDir, err)
	}

	return actions, nil
}

// scanFileForReplacements inspects a single file for matching import statements.
//
// An unreadable source file contributes no action rather than aborting the plan, so its
// read error is deliberately not propagated.
func scanFileForReplacements(filePath string, replacements map[string]string) []ReplacementAction {
	// #nosec G304 -- filePath comes from filepath.Walk over the repository being
	// migrated; no caller-supplied string reaches this read.
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}

	var actions []ReplacementAction
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		for oldPkg, newPkg := range replacements {
			if strings.Contains(trimmed, `"`+oldPkg) {
				actions = append(actions, ReplacementAction{
					File:        filePath,
					OldImport:   oldPkg,
					NewImport:   newPkg,
					Description: fmt.Sprintf("Replace %s with Golusoris %s", oldPkg, newPkg),
				})
			}
		}
	}

	return actions
}

// ApplyMigration applies the planned migration changes to the target repository using
// the audited command runner.
func ApplyMigration(ctx context.Context, repoPath string, plan *MigrationPlan) (*MigrationResult, error) {
	return ApplyMigrationWithOptions(ctx, repoPath, plan, MigrationOptions{})
}

// ApplyMigrationWithOptions applies the planned migration changes under caller-supplied
// options. Every step that does not succeed is recorded in MigrationResult.Warnings
// instead of being discarded, and Success reports whether the migration completed with
// no warning at all.
func ApplyMigrationWithOptions(ctx context.Context, repoPath string, plan *MigrationPlan, opts MigrationOptions) (*MigrationResult, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if plan == nil {
		return nil, ErrNilMigrationPlan
	}

	run := opts.Runner
	if run == nil {
		run = util.RunCommand
	}

	result := &MigrationResult{Repository: plan.Repository, Branch: migrationBranch}
	if out, err := run(ctx, repoPath, "git", "checkout", "-B", migrationBranch); err != nil {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("git checkout -B %s: %v: %s", migrationBranch, err, out))
	}

	changed := make(map[string]struct{})
	applyPlannedReplacements(repoPath, plan, result, changed)
	applyGoModUpdate(ctx, repoPath, plan, result, changed, run, opts.SkipTidy)
	writeMigrationGuide(repoPath, plan, result, changed)

	result.FilesChanged = sortedPaths(changed)
	result.Success = len(result.Warnings) == 0
	return result, nil
}

// applyPlannedReplacements rewrites the imports named by the plan, recording every
// rewrite that failed.
func applyPlannedReplacements(repoPath string, plan *MigrationPlan, result *MigrationResult, changed map[string]struct{}) {
	for _, act := range plan.Replacements {
		if err := applyFileImportReplacement(repoPath, act.File, act.OldImport, act.NewImport); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("rewrite %s: %v", act.File, err))
			continue
		}
		changed[act.File] = struct{}{}
	}
}

// applyGoModUpdate rewrites go.mod and runs `go mod tidy` when the repository has one.
func applyGoModUpdate(ctx context.Context, repoPath string, plan *MigrationPlan, result *MigrationResult,
	changed map[string]struct{}, run CommandRunner, skipTidy bool) {
	goModPath := filepath.Join(repoPath, "go.mod")
	if !util.FileExists(goModPath) {
		return
	}

	if err := updateGoMod(goModPath, plan.AddedRequires, plan.DroppedRequires); err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("update %s: %v", goModPath, err))
	} else {
		changed[goModPath] = struct{}{}
	}

	if skipTidy {
		return
	}
	if out, err := run(ctx, repoPath, "go", "mod", "tidy"); err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("go mod tidy: %v: %s", err, out))
	}
}

// writeMigrationGuide writes MIGRATION.md into the repository.
func writeMigrationGuide(repoPath string, plan *MigrationPlan, result *MigrationResult, changed map[string]struct{}) {
	guidePath := filepath.Join(repoPath, "MIGRATION.md")
	if err := util.WriteFileSecure(guidePath, []byte(plan.GuideMarkdown), util.SecureFilePerm); err != nil {
		result.Warnings = append(result.Warnings, fmt.Sprintf("write %s: %v", guidePath, err))
		return
	}
	changed[guidePath] = struct{}{}
}

// sortedPaths returns the keys of a path set in a deterministic order.
func sortedPaths(set map[string]struct{}) []string {
	paths := make([]string, 0, len(set))
	for p := range set {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// applyFileImportReplacement updates the import string inside a source file, keeping the
// file's existing permission bits.
func applyFileImportReplacement(repoRoot, filePath, oldImport, newImport string) error {
	target, err := confineRepoFile(repoRoot, filePath)
	if err != nil {
		return err
	}

	// #nosec G304 -- target was confined to the repository root by confineRepoFile.
	data, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("read %q: %w", target, err)
	}
	content := string(data)
	replaced := strings.ReplaceAll(content, `"`+oldImport+`"`, `"`+newImport+`"`)
	replaced = strings.ReplaceAll(replaced, `"`+oldImport+`/`, `"`+newImport+`/`)

	return writePreservingMode(target, []byte(replaced))
}

// confineRepoFile guarantees that a planned mutation stays inside the repository being
// migrated: a ReplacementAction is data, and a plan produced elsewhere must not be able
// to rewrite a file outside the target tree.
func confineRepoFile(repoRoot, filePath string) (string, error) {
	rel, err := filepath.Rel(repoRoot, filePath)
	if err != nil {
		return "", fmt.Errorf("locate %q inside %q: %w", filePath, repoRoot, err)
	}
	confined, err := util.ConfinePath(repoRoot, rel)
	if err != nil {
		return "", fmt.Errorf("confine %q to %q: %w", filePath, repoRoot, err)
	}
	return confined, nil
}

// writePreservingMode rewrites an existing file with its current permission bits, with
// any world-write bit stripped, so a migration neither widens nor narrows the tree it
// edits.
func writePreservingMode(path string, data []byte) error {
	perm := util.SecureFilePerm
	if info, err := os.Stat(path); err == nil {
		if existing := info.Mode().Perm() &^ 0o002; existing != 0 {
			perm = existing
		}
	}
	if err := util.WriteFileSecure(path, data, perm); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}
	return nil
}

// updateGoMod drops superseded packages and appends the Golusoris framework require.
func updateGoMod(goModPath string, added, dropped []string) error {
	// #nosec G304 -- goModPath is the repository's own manifest, built by joining the
	// migration target root with the constant "go.mod".
	content, err := os.ReadFile(goModPath)
	if err != nil {
		return fmt.Errorf("read %q: %w", goModPath, err)
	}

	var newLines []string
	for _, line := range strings.Split(strings.TrimSuffix(string(content), "\n"), "\n") {
		if !isDroppedRequire(line, dropped) {
			newLines = append(newLines, line)
		}
	}

	for _, add := range added {
		newLines = append(newLines, "require "+add)
	}

	return writePreservingMode(goModPath, []byte(strings.Join(newLines, "\n")+"\n"))
}

// isDroppedRequire reports whether a go.mod line names one of the superseded packages.
func isDroppedRequire(line string, dropped []string) bool {
	for _, d := range dropped {
		if strings.Contains(line, d) {
			return true
		}
	}
	return false
}

// generateMigrationGuide creates a concise markdown walkthrough for the developer.
func generateMigrationGuide(plan *MigrationPlan) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Migration Guide: %s -> %s\n\n", plan.Repository, plan.Framework)
	sb.WriteString("## Planned Dependency Changes\n\n")
	sb.WriteString("**Added Requirements:**\n")
	for _, a := range plan.AddedRequires {
		fmt.Fprintf(&sb, "- `%s`\n", a)
	}
	sb.WriteString("\n**Dropped Third-Party Packages:**\n")
	for _, d := range plan.DroppedRequires {
		fmt.Fprintf(&sb, "- `%s`\n", d)
	}
	sb.WriteString("\n## File Import Replacements\n\n")
	if len(plan.Replacements) == 0 {
		sb.WriteString("No direct file import replacements identified.\n")
	} else {
		for _, r := range plan.Replacements {
			fmt.Fprintf(&sb, "- `%s`: `%s` -> `%s`\n", r.File, r.OldImport, r.NewImport)
		}
	}
	sb.WriteString("\n## Next Steps\n")
	sb.WriteString("1. Run `go mod tidy` to clean up indirect dependencies.\n")
	sb.WriteString("2. Verify compilation with `go build ./...`.\n")
	sb.WriteString("3. Run tests with `go test -v ./...`.\n")
	return sb.String()
}
