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
		if scanErr == nil && len(fileActions) > 0 {
			actions = append(actions, fileActions...)
		}
		return nil
	})

	return actions, err
}

// scanFileForReplacements inspects a single file for matching import statements.
func scanFileForReplacements(filePath string, replacements map[string]string) ([]ReplacementAction, error) {
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
	if _, err := util.RunCommand(ctx, repoPath, "git", "checkout", "-B", branchName); err != nil {
		// Log or handle non-fatal git command failure if not in git repo
	}

	changedFilesMap := make(map[string]struct{})
	for _, act := range plan.Replacements {
		if err := applyFileImportReplacement(act.File, act.OldImport, act.NewImport); err == nil {
			changedFilesMap[act.File] = struct{}{}
		}
	}

	goModPath := filepath.Join(repoPath, "go.mod")
	if util.FileExists(goModPath) {
		if err := updateGoMod(goModPath, plan.AddedRequires, plan.DroppedRequires); err == nil {
			changedFilesMap[goModPath] = struct{}{}
		}
		if _, err := util.RunCommand(ctx, repoPath, "go", "mod", "tidy"); err != nil {
			// Non-fatal tidy attempt
		}
	}

	guidePath := filepath.Join(repoPath, "MIGRATION.md")
	if err := os.WriteFile(guidePath, []byte(plan.GuideMarkdown), 0644); err == nil {
		changedFilesMap[guidePath] = struct{}{}
	}

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

// applyFileImportReplacement updates the import string inside a source file.
func applyFileImportReplacement(filePath, oldImport, newImport string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	content := string(data)
	replaced := strings.ReplaceAll(content, `"`+oldImport+`"`, `"`+newImport+`"`)
	replaced = strings.ReplaceAll(replaced, `"`+oldImport+`/`, `"`+newImport+`/`)
	return os.WriteFile(filePath, []byte(replaced), 0644)
}

// updateGoMod drops superseded packages and appends the Golusoris framework require.
func updateGoMod(goModPath string, added, dropped []string) error {
	file, err := os.Open(goModPath)
	if err != nil {
		return err
	}
	defer file.Close()

	var newLines []string
	scanner := bufio.NewScanner(file)
	for lines := 0; lines < MaxScannedLines && scanner.Scan(); lines++ {
		line := scanner.Text()
		shouldDrop := false
		for _, d := range dropped {
			if strings.Contains(line, d) {
				shouldDrop = true
				break
			}
		}
		if !shouldDrop {
			newLines = append(newLines, line)
		}
	}

	for _, add := range added {
		newLines = append(newLines, "require "+add)
	}

	return os.WriteFile(goModPath, []byte(strings.Join(newLines, "\n")+"\n"), 0644)
}

// generateMigrationGuide creates a concise markdown walkthrough for the developer.
func generateMigrationGuide(plan *MigrationPlan) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Migration Guide: %s -> %s\n\n", plan.Repository, plan.Framework))
	sb.WriteString("## Planned Dependency Changes\n\n")
	sb.WriteString("**Added Requirements:**\n")
	for _, a := range plan.AddedRequires {
		sb.WriteString(fmt.Sprintf("- `%s`\n", a))
	}
	sb.WriteString("\n**Dropped Third-Party Packages:**\n")
	for _, d := range plan.DroppedRequires {
		sb.WriteString(fmt.Sprintf("- `%s`\n", d))
	}
	sb.WriteString("\n## File Import Replacements\n\n")
	if len(plan.Replacements) == 0 {
		sb.WriteString("No direct file import replacements identified.\n")
	} else {
		for _, r := range plan.Replacements {
			sb.WriteString(fmt.Sprintf("- `%s`: `%s` -> `%s`\n", r.File, r.OldImport, r.NewImport))
		}
	}
	sb.WriteString("\n## Next Steps\n")
	sb.WriteString("1. Run `go mod tidy` to clean up indirect dependencies.\n")
	sb.WriteString("2. Verify compilation with `go build ./...`.\n")
	sb.WriteString("3. Run tests with `go test -v ./...`.\n")
	return sb.String()
}
