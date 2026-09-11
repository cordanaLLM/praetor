package needs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/standards/internal/forge"
)

// PreMigrationEpic captures the parent epic and child task issues for repo modernization.
type PreMigrationEpic struct {
	RepoName          string            `json:"repo_name"`
	TargetFramework   string            `json:"target_framework"`
	ReadinessScore    float64           `json:"readiness_score"`
	ParentEpic        forge.IssueSpec   `json:"parent_epic"`
	ChildIssues       []forge.IssueSpec `json:"child_issues"`
	ChecklistMarkdown string            `json:"checklist_markdown"`
}

// GeneratePreMigrationEpic analyzes a repository and synthesizes a pre-migration epic.
func GeneratePreMigrationEpic(ctx context.Context, repoPath, targetFramework string) (*PreMigrationEpic, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	repoNeeds, err := ScanRepo(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to scan repo for epic: %w", err)
	}

	if targetFramework == "" {
		targetFramework = repoNeeds.Framework
	}

	migrationPlan, mErr := PlanMigration(ctx, repoPath, targetFramework)
	if mErr != nil {
		migrationPlan = &MigrationPlan{
			Repository: repoNeeds.Repository,
			Framework:  targetFramework,
		}
	}

	return buildEpicStructure(repoNeeds, migrationPlan)
}

func buildEpicStructure(needs *RepoNeeds, plan *MigrationPlan) (*PreMigrationEpic, error) {
	repoName := needs.Repository
	if repoName == "" {
		repoName = "target-repo"
	}

	tasks := createChildTasks(repoName, needs, plan)
	checklistMD := renderEpicChecklistMarkdown(repoName, needs, plan, tasks)

	parentEpic := forge.IssueSpec{
		Title:  fmt.Sprintf("[EPIC] Pre-Migration Hardening & Framework Adoption: %s", repoName),
		Body:   checklistMD,
		State:  "open",
		Labels: []string{"epic", "governance", "adoption"},
	}

	return &PreMigrationEpic{
		RepoName:          repoName,
		TargetFramework:   plan.Framework,
		ReadinessScore:    needs.Readiness.Score,
		ParentEpic:        parentEpic,
		ChildIssues:       tasks,
		ChecklistMarkdown: checklistMD,
	}, nil
}

func createChildTasks(repoName string, needs *RepoNeeds, plan *MigrationPlan) []forge.IssueSpec {
	t1 := forge.IssueSpec{
		Title:  fmt.Sprintf("[TASK 1/4] Invariant & Complexity Hygiene: %s", repoName),
		Body:   "## Scope\n- Enforce NASA JPL Rule 4: refactor all functions to <= 60 LOC.\n- Eliminate unhandled panics, unwrap(), and raw fatal exits.\n- Add 3D unit tests (positive, negative, boundary) with race detector.",
		State:  "open",
		Labels: []string{"task", "hiss", "hygiene"},
	}

	t2 := forge.IssueSpec{
		Title:     fmt.Sprintf("[TASK 2/4] Decoupling & Config Externalization: %s", repoName),
		Body:      "## Scope\n- Eliminate in-cluster DNS and hardcoded localhost URLs.\n- Externalize secrets and tokens behind environment variables / HashiCorp Vault.\n- Decouple monorepo circular import dependencies.",
		State:     "open",
		Labels:    []string{"task", "architecture", "decoupling"},
		DependsOn: []string{fmt.Sprintf("%s#1", repoName)},
	}

	t3 := forge.IssueSpec{
		Title:     fmt.Sprintf("[TASK 3/4] Framework Dependency Substitution: %s", repoName),
		Body:      fmt.Sprintf("## Scope\n- Swap %d external dependencies for %s builder kits.\n- Apply verified import substitutions.\n- Reconcile .needs.yaml capability declarations.", len(plan.Replacements), plan.Framework),
		State:     "open",
		Labels:    []string{"task", "dependencies", "migration"},
		DependsOn: []string{fmt.Sprintf("%s#2", repoName)},
	}

	t4 := forge.IssueSpec{
		Title:     fmt.Sprintf("[TASK 4/4] Gated Verification & Ed25519 Receipt: %s", repoName),
		Body:      "## Scope\n- Run `standardsctl gate run --target=.` in isolated worktree.\n- Verify all 5 gates (prefetch, SCA, HISS-16, tests, receipts).\n- Sign Ed25519 Exit-0 receipt and submit fast-forward PR.",
		State:     "open",
		Labels:    []string{"task", "verification", "gating"},
		DependsOn: []string{fmt.Sprintf("%s#3", repoName)},
	}

	return []forge.IssueSpec{t1, t2, t3, t4}
}

func renderEpicChecklistMarkdown(repoName string, needs *RepoNeeds, plan *MigrationPlan, tasks []forge.IssueSpec) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Pre-Migration Epic: %s\n\n", repoName))
	sb.WriteString(fmt.Sprintf("- **Target Framework**: `%s`\n", plan.Framework))
	sb.WriteString(fmt.Sprintf("- **Current Readiness Score**: `%.1f%%`\n", needs.Readiness.Score))
	sb.WriteString(fmt.Sprintf("- **Third-Party Dependencies**: `%d` total (%d covered, %d gaps)\n\n",
		needs.Readiness.TotalThirdPartyDeps, needs.Readiness.CoveredDeps, needs.Readiness.GapDeps))

	sb.WriteString("## Pre-Migration Tasks\n\n")
	for i, task := range tasks {
		sb.WriteString(fmt.Sprintf("- [ ] **Task %d**: %s\n", i+1, task.Title))
		if len(task.DependsOn) > 0 {
			sb.WriteString(fmt.Sprintf("  - *Prerequisites*: Depends-On: %s\n", strings.Join(task.DependsOn, ", ")))
		}
	}

	sb.WriteString("\n## Execution Directives\n")
	sb.WriteString("1. All changes must pass `make verify-all` with zero warnings.\n")
	sb.WriteString("2. Direct commits to `main` are prohibited; changes must traverse `standardsctl gate run`.\n")
	return sb.String()
}

// WriteEpicMarkdown exports the pre-migration epic to the specified file path.
func WriteEpicMarkdown(epic *PreMigrationEpic, outputPath string) error {
	if epic == nil {
		return fmt.Errorf("epic cannot be nil")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory for %s: %w", outputPath, err)
	}

	var sb strings.Builder
	sb.WriteString(epic.ChecklistMarkdown)
	sb.WriteString("\n\n---\n\n## Decomposed Sub-Issue Definitions\n\n")
	for i, child := range epic.ChildIssues {
		sb.WriteString(fmt.Sprintf("### Issue %d: %s\n\n", i+1, child.Title))
		sb.WriteString(fmt.Sprintf("**Labels**: `%s`\n", strings.Join(child.Labels, ", ")))
		if len(child.DependsOn) > 0 {
			sb.WriteString(fmt.Sprintf("**Depends-On**: `%s`\n", strings.Join(child.DependsOn, ", ")))
		}
		sb.WriteString(fmt.Sprintf("\n%s\n\n", child.Body))
	}

	return os.WriteFile(outputPath, []byte(sb.String()), 0644)
}
