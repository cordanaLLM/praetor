package needs

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// PlanMigration analyzes a repository and builds an actionable migration plan.
func PlanMigration(ctx context.Context, repoPath, frameworkPath string) (*MigrationPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	needs, err := ScanRepo(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to scan repo for migration: %w", err)
	}

	plan := &MigrationPlan{
		Repository:    needs.Repository,
		Framework:     defaultFrameworkModule + " v0.8.0",
		AddedRequires: []string{defaultFrameworkModule + " v0.8.0"},
	}

	importReplacements := make(map[string]string)
	for _, dep := range needs.Dependencies {
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
		if walkErr != nil || ctx.Err() != nil {
			return walkErr
		}
		if shouldSkipDir(info, path) {
			return filepath.SkipDir
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".go") {
			return nil
		}

		fileActions, scanErr := scanFileForReplacements(path, replacements)
		if scanErr != nil {
			return fmt.Errorf("scan %s: %w", path, scanErr)
		}
		actions = append(actions, fileActions...)
		return nil
	})

	return actions, err
}

// scanFileForReplacements inspects a single file for matching import statements.
func scanFileForReplacements(filePath string, replacements map[string]string) ([]ReplacementAction, error) {
	// #nosec G304 -- filePath is produced by filepath.Walk under the migration root.
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
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

	return actions, nil
}

// ApplyMigration applies the planned migration changes to the target repository.
func ApplyMigration(ctx context.Context, repoPath string, plan *MigrationPlan) (*MigrationResult, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	branchName := "refactor/golusoris-adoption"
	if err := createMigrationBranch(ctx, repoPath, branchName); err != nil {
		return nil, err
	}

	changedFilesMap := make(map[string]struct{})
	for _, act := range plan.Replacements {
		target, err := confineToRepo(repoPath, act.File)
		if err != nil {
			return nil, err
		}
		if err := applyFileImportReplacement(target, act.OldImport, act.NewImport); err != nil {
			return nil, fmt.Errorf("rewrite imports in %s: %w", act.File, err)
		}
		changedFilesMap[act.File] = struct{}{}
	}

	goModPath := filepath.Join(repoPath, "go.mod")
	if util.FileExists(goModPath) {
		if err := updateGoMod(goModPath, plan.AddedRequires, plan.DroppedRequires); err != nil {
			return nil, fmt.Errorf("update go.mod: %w", err)
		}
		changedFilesMap[goModPath] = struct{}{}
	}

	guidePath := filepath.Join(repoPath, "MIGRATION.md")
	if err := util.WriteFileSecure(guidePath, []byte(plan.GuideMarkdown), 0o644); err != nil {
		return nil, fmt.Errorf("write migration guide: %w", err)
	}
	changedFilesMap[guidePath] = struct{}{}

	var changedFiles []string
	for f := range changedFilesMap {
		changedFiles = append(changedFiles, f)
	}

	return &MigrationResult{
		Repository:   plan.Repository,
		Branch:       branchName,
		FilesChanged: changedFiles,
		Success:      true,
	}, nil
}

// createMigrationBranch checks out the migration branch when repoPath is a git
// repository. A repository without git metadata is migrated in place; a failing
// checkout inside a git repository is an error, never silently ignored (HISS-07).
func createMigrationBranch(ctx context.Context, repoPath, branchName string) error {
	gitDir := filepath.Join(repoPath, ".git")
	if !util.DirExists(gitDir) && !util.FileExists(gitDir) {
		return nil
	}
	if out, err := util.RunCommand(ctx, repoPath, "git", "checkout", "-B", branchName); err != nil {
		return fmt.Errorf("create migration branch %s: %w: %s", branchName, err, out)
	}
	return nil
}

// confineToRepo resolves a planned target file against the migration root and refuses
// any path that escapes it, so a tampered plan cannot rewrite files elsewhere.
func confineToRepo(repoPath, file string) (string, error) {
	rel := file
	if filepath.IsAbs(file) {
		var err error
		if rel, err = filepath.Rel(repoPath, file); err != nil {
			return "", fmt.Errorf("migration target %q is not under %q: %w", file, repoPath, err)
		}
	}
	target, err := util.ConfinePath(repoPath, rel)
	if err != nil {
		return "", fmt.Errorf("migration target %q: %w", file, err)
	}
	return target, nil
}

// applyFileImportReplacement updates the import string inside a source file whose
// path was confined to the migration root by confineToRepo.
func applyFileImportReplacement(filePath, oldImport, newImport string) error {
	// #nosec G304 -- filePath passed util.ConfinePath against the migration root.
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	content := string(data)
	replaced := strings.ReplaceAll(content, `"`+oldImport+`"`, `"`+newImport+`"`)
	replaced = strings.ReplaceAll(replaced, `"`+oldImport+`/`, `"`+newImport+`/`)
	return util.WriteFileSecure(filePath, []byte(replaced), 0o644)
}

// updateGoMod drops superseded packages and appends the Golusoris framework require.
func updateGoMod(goModPath string, added, dropped []string) error {
	// #nosec G304 -- goModPath is <repoPath>/go.mod, built by ApplyMigration.
	file, err := os.Open(goModPath)
	if err != nil {
		return err
	}

	var newLines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !mentionsAny(line, dropped) {
			newLines = append(newLines, line)
		}
	}
	scanErr := scanner.Err()
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", goModPath, err)
	}
	if scanErr != nil {
		return fmt.Errorf("read %s: %w", goModPath, scanErr)
	}

	for _, add := range added {
		newLines = append(newLines, "require "+add)
	}

	return util.WriteFileSecure(goModPath, []byte(strings.Join(newLines, "\n")+"\n"), 0o644)
}

func mentionsAny(line string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(line, n) {
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
