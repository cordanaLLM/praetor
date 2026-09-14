package needs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cordanallm/praetor/internal/util"
	"github.com/golusoris/golusoris/core/astx"
	"golang.org/x/mod/modfile"
)

// PlanMigration analyzes a repository and builds an actionable migration plan.
func PlanMigration(ctx context.Context, repoPath, frameworkPath string) (*MigrationPlan, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	idx, err := InspectFramework(ctx, frameworkPath)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect framework: %w", err)
	}
	needs, err := ScanRepoWith(ctx, repoPath, idx)
	if err != nil {
		return nil, fmt.Errorf("failed to scan repo for migration: %w", err)
	}

	version := idx.Version
	if version == "" || version == UnknownVersion {
		version = "latest"
	}
	plan := &MigrationPlan{
		Repository: needs.Repository,
		Framework:  idx.Name + " " + version,
	}

	importReplacements := make(map[string]string)
	requiredModules := map[string]bool{}
	for _, dep := range needs.Dependencies {
		if dep.GolusorisReplacement != "" && dep.Status == StatusCovered {
			plan.DroppedRequires = append(plan.DroppedRequires, dep.Package)
			importReplacements[dep.Package] = dep.GolusorisReplacement
			requiredModules[moduleForImport(idx, dep.GolusorisReplacement)] = true
		}
	}
	if len(requiredModules) == 0 {
		requiredModules[idx.Name] = true
	}
	for m := range requiredModules {
		plan.AddedRequires = append(plan.AddedRequires, m+" "+version)
	}
	sort.Strings(plan.AddedRequires)
	sort.Strings(plan.DroppedRequires)

	actions, err := findFileImportReplacements(ctx, repoPath, importReplacements)
	if err != nil {
		return nil, fmt.Errorf("failed to scan file import replacements: %w", err)
	}
	plan.Replacements = actions
	plan.GuideMarkdown = generateMigrationGuide(plan)

	return plan, nil
}

// moduleForImport returns the Go module that publishes importPath according
// to the framework index (its sub-modules, e.g. …/core), else the framework
// root module.
func moduleForImport(idx *FrameworkIndex, importPath string) string {
	if p, ok := idx.Packages[importPath]; ok && p.Module != "" {
		return p.Module
	}
	best := idx.Name
	for _, m := range idx.Modules {
		if strings.HasPrefix(importPath, m+"/") && len(m) > len(best) {
			best = m
		}
	}
	return best
}

// findFileImportReplacements walks every Go source file (tests included) and
// resolves its import declarations against the replacement map via the AST,
// so string literals and comments never produce false positives.
func findFileImportReplacements(ctx context.Context, rootDir string, replacements map[string]string) ([]ReplacementAction, error) {
	var actions []ReplacementAction
	opts := astx.WalkOptions{IncludeTests: true, SkipDirs: legacySkipDirs}
	err := astx.Walk(ctx, rootDir, opts, func(path string) error {
		imports, parseErr := astx.Imports(path)
		if parseErr != nil {
			return nil // unparseable files are the compiler's problem, not the planner's
		}
		for _, imp := range imports {
			target, ok := astx.Resolve(imp, replacements)
			if !ok || target == imp {
				continue
			}
			actions = append(actions, ReplacementAction{
				File:        path,
				OldImport:   imp,
				NewImport:   target,
				Description: fmt.Sprintf("Replace %s with Golusoris %s", imp, target),
			})
		}
		return nil
	})
	return actions, err
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

	changedFilesMap := applyImportReplacements(plan.Replacements)

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
	if err := os.WriteFile(guidePath, []byte(plan.GuideMarkdown), 0o644); err == nil {
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

// applyImportReplacements groups planned actions per file and rewrites each
// file's import declarations in one AST pass (aliases and comments preserved).
func applyImportReplacements(actions []ReplacementAction) map[string]struct{} {
	byFile := make(map[string]map[string]string)
	for _, act := range actions {
		if byFile[act.File] == nil {
			byFile[act.File] = make(map[string]string)
		}
		byFile[act.File][act.OldImport] = act.NewImport
	}
	changed := make(map[string]struct{})
	for file, mapping := range byFile {
		if did, err := astx.RewriteImportsFile(file, mapping); err == nil && did {
			changed[file] = struct{}{}
		}
	}
	return changed
}

// updateGoMod drops superseded requirements and adds the framework modules
// using golang.org/x/mod/modfile, the go command's own go.mod editor.
func updateGoMod(goModPath string, added, dropped []string) error {
	data, err := astx.ReadFileBounded(goModPath, astx.MaxGoModSize)
	if err != nil {
		return err
	}
	f, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return fmt.Errorf("parse %s: %w", goModPath, err)
	}
	for _, d := range dropped {
		if err := f.DropRequire(d); err != nil {
			return fmt.Errorf("drop require %s: %w", d, err)
		}
	}
	for _, add := range added {
		path, version, _ := strings.Cut(add, " ")
		if err := f.AddRequire(path, version); err != nil {
			return fmt.Errorf("add require %s: %w", add, err)
		}
	}
	f.Cleanup()
	out, err := f.Format()
	if err != nil {
		return fmt.Errorf("format %s: %w", goModPath, err)
	}
	return os.WriteFile(goModPath, out, 0o644)
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
