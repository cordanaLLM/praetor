package needs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

// totalEpicTasks is the fixed number of child tasks a pre-migration epic decomposes into.
const totalEpicTasks = 5

// fleetEpicFileName is the path, relative to a repository root, that fleet regeneration
// writes each pre-migration epic to.
var fleetEpicFileName = filepath.Join(".workingdir", "PRE_MIGRATION_EPIC.md")

var (
	// ErrNilEpic is returned when an epic operation is given no epic.
	ErrNilEpic = errors.New("needs: epic cannot be nil")
	// ErrNilForge is returned when an epic is published without a forge driver.
	ErrNilForge = errors.New("needs: forge driver cannot be nil")
	// errRepoNotPrepared marks a discovered directory that carries no repository
	// marker, so fleet regeneration skips it without counting it as a failure.
	errRepoNotPrepared = errors.New("needs: directory is not a prepared repository")
)

// PreMigrationEpic captures the parent epic and child task issues for repo modernization.
type PreMigrationEpic struct {
	RepoName          string            `json:"repo_name"`
	TargetFramework   string            `json:"target_framework"`
	ReadinessScore    float64           `json:"readiness_score"`
	ParentEpic        forge.IssueSpec   `json:"parent_epic"`
	ChildIssues       []forge.IssueSpec `json:"child_issues"`
	ChecklistMarkdown string            `json:"checklist_markdown"`
	// OutputPath is the file fleet regeneration wrote, or - under FleetEpicOptions.DryRun
	// - would have written. It is empty for a single-repository generation.
	OutputPath string `json:"output_path,omitempty"`
}

// GeneratePreMigrationEpic analyzes a repository and synthesizes a pre-migration epic.
//
// targetFramework is honoured on both paths: it is resolved to a module path (a checkout
// through its own go.mod, a module-shaped value verbatim) and that module is what the
// epic advertises. A local filesystem path never reaches the epic body, which may be
// published as a public issue.
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
			Framework:  ResolveFrameworkModule(targetFramework) + " " + defaultFrameworkVersion,
		}
	}

	return buildEpicStructure(repoPath, repoNeeds, migrationPlan)
}

func buildEpicStructure(repoPath string, repoNeeds *RepoNeeds, plan *MigrationPlan) (*PreMigrationEpic, error) {
	repoName := repoNeeds.Repository
	if repoName == "" || repoName == "unknown" {
		repoName = filepath.Base(filepath.Clean(repoPath))
	}
	repoName = strings.TrimPrefix(repoName, "github.com/")

	tasks := createChildTasks(repoName, plan)
	checklistMD := renderEpicChecklistMarkdown(repoName, repoNeeds, plan, tasks)

	parentEpic := forge.IssueSpec{
		Title:  fmt.Sprintf("[EPIC] Pre-Migration Hardening & Framework Adoption: %s", repoName),
		Body:   checklistMD,
		State:  "open",
		Labels: []string{"epic", "governance", "adoption"},
	}

	return &PreMigrationEpic{
		RepoName:          repoName,
		TargetFramework:   plan.Framework,
		ReadinessScore:    repoNeeds.Readiness.Score,
		ParentEpic:        parentEpic,
		ChildIssues:       tasks,
		ChecklistMarkdown: checklistMD,
	}, nil
}

// taskAnchor names a sibling task inside the generated markdown.
//
// It is deliberately not a "<repo>#<n>" reference: before the tasks exist, no issue
// number is known, and a forge auto-links "<owner>/<repo>#1" to whatever issue #1 already
// is in that repository - so a published epic would point its checklist at four unrelated
// old issues. PublishPreMigrationEpic substitutes the real issue numbers once the child
// issues have been created.
func taskAnchor(n int) string {
	return fmt.Sprintf("Task %d/%d", n, totalEpicTasks)
}

func createChildTasks(repoName string, plan *MigrationPlan) []forge.IssueSpec {
	t1 := forge.IssueSpec{
		Title:  fmt.Sprintf("[TASK 1/5] Invariant & Complexity Hygiene: %s", repoName),
		Body:   "## Scope\n- Enforce NASA JPL Rule 4: refactor all functions to <= 60 LOC.\n- Eliminate unhandled panics, unwrap(), and raw fatal exits.\n- Add 3D unit tests (positive, negative, boundary) with race detector.",
		State:  "open",
		Labels: []string{"task", "hiss", "hygiene"},
	}

	t2 := forge.IssueSpec{
		Title:     fmt.Sprintf("[TASK 2/5] Decoupling & Config Externalization: %s", repoName),
		Body:      "## Scope\n- Eliminate in-cluster DNS and hardcoded localhost URLs.\n- Externalize secrets and tokens behind environment variables / HashiCorp Vault.\n- Decouple monorepo circular import dependencies.",
		State:     "open",
		Labels:    []string{"task", "architecture", "decoupling"},
		DependsOn: []string{taskAnchor(1)},
	}

	t3 := forge.IssueSpec{
		Title:     fmt.Sprintf("[TASK 3/5] Framework Dependency Substitution: %s", repoName),
		Body:      fmt.Sprintf("## Scope\n- Swap %d external dependencies for %s builder kits.\n- Apply verified import substitutions.\n- Reconcile .needs.yaml capability declarations.", len(plan.Replacements), plan.Framework),
		State:     "open",
		Labels:    []string{"task", "dependencies", "migration"},
		DependsOn: []string{taskAnchor(2)},
	}

	t4 := forge.IssueSpec{
		Title:     fmt.Sprintf("[TASK 4/5] Gated Verification & Ed25519 Receipt: %s", repoName),
		Body:      "## Scope\n- Run diff-aware CI verification via `praetorctl ci filter`.\n- Run `praetorctl gate run --target=.` in isolated worktree.\n- Verify all 5 gates (prefetch, SCA, HISS-16, tests, receipts).\n- Sign Ed25519 Exit-0 receipt and submit fast-forward PR.",
		State:     "open",
		Labels:    []string{"task", "verification", "gating"},
		DependsOn: []string{taskAnchor(3)},
	}

	t5 := forge.IssueSpec{
		Title:     fmt.Sprintf("[TASK 5/5] Full Praetor Activation & Governance Lockdown: %s", repoName),
		Body:      "## Scope\n- Reconcile and lock branch protection rulesets via `praetorctl sync`.\n- Transition .standards.yaml enforcement level to `strict-zero-debt`.\n- Synthesize Paperclip agent harness (`praetorctl paperclip harness`).\n- Configure ARC/fleet runner routing policy and enroll into bot gating webhook.",
		State:     "open",
		Labels:    []string{"task", "activation", "governance"},
		DependsOn: []string{taskAnchor(4)},
	}

	return []forge.IssueSpec{t1, t2, t3, t4, t5}
}

func renderEpicChecklistMarkdown(repoName string, repoNeeds *RepoNeeds, plan *MigrationPlan, tasks []forge.IssueSpec) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Pre-Migration Epic: %s\n\n", repoName)
	fmt.Fprintf(&sb, "- **Target Framework**: `%s`\n", plan.Framework)
	fmt.Fprintf(&sb, "- **Current Readiness Score**: `%.1f%%`\n", repoNeeds.Readiness.Score)
	fmt.Fprintf(&sb, "- **Third-Party Dependencies**: `%d` total (%d covered, %d gaps)\n\n",
		repoNeeds.Readiness.TotalThirdPartyDeps, repoNeeds.Readiness.CoveredDeps, repoNeeds.Readiness.GapDeps)

	sb.WriteString("## Pre-Migration Tasks\n\n")
	for i, task := range tasks {
		fmt.Fprintf(&sb, "- [ ] **Task %d**: %s\n", i+1, task.Title)
		if len(task.DependsOn) > 0 {
			fmt.Fprintf(&sb, "  - *Prerequisites*: Depends-On: %s\n", strings.Join(task.DependsOn, ", "))
		}
	}

	sb.WriteString("\n## Execution Directives\n")
	sb.WriteString("1. All changes must pass the repository verification gate with zero warnings.\n")
	sb.WriteString("2. Direct commits to `main` are prohibited; changes must traverse `praetorctl gate run`.\n")
	sb.WriteString("3. Diff-aware CI efficiency: CI runs targeted gates on changed paths; docs-only and state-only changes skip heavy test/fuzz suites.\n")
	sb.WriteString("4. Ed25519 Exit-0 receipts mandatory on all pull requests.\n")
	return sb.String()
}

// PublishPreMigrationEpic synchronizes the pre-migration parent epic and decomposed tasks to the target forge.
func PublishPreMigrationEpic(ctx context.Context, f forge.Forge, epic *PreMigrationEpic) (*forge.IssueResponse, []*forge.IssueResponse, error) {
	if f == nil {
		return nil, nil, ErrNilForge
	}
	if epic == nil {
		return nil, nil, ErrNilEpic
	}
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}

	parentRes, err := f.CreateIssue(ctx, epic.ParentEpic)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create parent epic issue: %w", err)
	}

	childResults := make([]*forge.IssueResponse, 0, len(epic.ChildIssues))
	for i := range epic.ChildIssues {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return parentRes, childResults, ctxErr
		}
		res, cErr := publishChildTask(ctx, f, epic, parentRes, epic.ChildIssues[i], childResults)
		if cErr != nil {
			return parentRes, childResults, fmt.Errorf("failed to create child task %d: %w", i+1, cErr)
		}
		childResults = append(childResults, res)
	}

	return parentRes, childResults, nil
}

// publishChildTask creates one child task, chaining it onto the task published before it
// by its real issue number rather than the pre-publish anchor.
func publishChildTask(ctx context.Context, f forge.Forge, epic *PreMigrationEpic,
	parent *forge.IssueResponse, spec forge.IssueSpec, published []*forge.IssueResponse) (*forge.IssueResponse, error) {
	if len(published) > 0 {
		prev := published[len(published)-1]
		spec.DependsOn = []string{fmt.Sprintf("%s#%d", epic.RepoName, prev.Number)}
	}

	body := spec.Body
	if len(spec.DependsOn) > 0 {
		body = fmt.Sprintf("%s\n\nDepends-On: %s", body, strings.Join(spec.DependsOn, ", "))
	}
	spec.Body = fmt.Sprintf("%s\n\n---\n*Part of Epic #%d (%s)*\n", body, parent.Number, parent.URL)

	return f.CreateIssue(ctx, spec)
}

// WriteEpicMarkdown exports the pre-migration epic to the specified file path.
//
// The epic enumerates a repository's dependency inventory and readiness, so the
// directory and the file are created owner-only rather than world-readable.
func WriteEpicMarkdown(ctx context.Context, epic *PreMigrationEpic, outputPath string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if epic == nil {
		return ErrNilEpic
	}
	if strings.TrimSpace(outputPath) == "" {
		return fmt.Errorf("needs: epic output path must not be empty")
	}

	if err := util.MkdirSecure(filepath.Dir(outputPath), util.SecureDirPerm); err != nil {
		return fmt.Errorf("failed to create directory for %s: %w", outputPath, err)
	}
	if err := util.WriteFileSecure(outputPath, []byte(renderEpicDocument(epic)), util.SecureFilePerm); err != nil {
		return fmt.Errorf("failed to write %s: %w", outputPath, err)
	}
	return nil
}

// renderEpicDocument renders the checklist plus the decomposed sub-issue definitions.
func renderEpicDocument(epic *PreMigrationEpic) string {
	var sb strings.Builder
	sb.WriteString(epic.ChecklistMarkdown)
	sb.WriteString("\n\n---\n\n## Decomposed Sub-Issue Definitions\n\n")
	for i, child := range epic.ChildIssues {
		fmt.Fprintf(&sb, "### Issue %d: %s\n\n", i+1, child.Title)
		fmt.Fprintf(&sb, "**Labels**: `%s`\n", strings.Join(child.Labels, ", "))
		if len(child.DependsOn) > 0 {
			fmt.Fprintf(&sb, "**Depends-On**: `%s`\n", strings.Join(child.DependsOn, ", "))
		}
		fmt.Fprintf(&sb, "\n%s\n\n", child.Body)
	}
	return sb.String()
}

// FleetEpicOptions configures RegenerateFleetEpics.
type FleetEpicOptions struct {
	// FrameworkPath is the operator-supplied framework location or module path.
	FrameworkPath string
	// DryRun generates every epic and records the file it would write in
	// PreMigrationEpic.OutputPath without touching any repository. Fleet regeneration
	// writes into every discovered repository, so - like `needs migrate` - the caller
	// has to ask for the mutation explicitly.
	DryRun bool
}

// RegenerateFleetEpics discovers all prepared repositories in fleetRoot and regenerates
// their pre-migration epics.
//
// Per-repository failures are collected and returned joined: a run in which nothing could
// be generated or written reports an error instead of an empty success.
func RegenerateFleetEpics(ctx context.Context, fleetRoot string, opts FleetEpicOptions) ([]*PreMigrationEpic, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	repos, err := discoverFleetRepos(ctx, fleetRoot)
	if err != nil {
		return nil, fmt.Errorf("failed discovering fleet repos: %w", err)
	}

	var epics []*PreMigrationEpic
	var failures []error
	for i := 0; i < len(repos); i++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			failures = append(failures, ctxErr)
			return epics, errors.Join(failures...)
		}
		epic, rErr := regenerateRepoEpic(ctx, repos[i], opts)
		switch {
		case rErr == nil:
			epics = append(epics, epic)
		case errors.Is(rErr, errRepoNotPrepared):
		default:
			failures = append(failures, rErr)
		}
	}

	return epics, errors.Join(failures...)
}

// regenerateRepoEpic regenerates one repository's epic, writing it unless opts.DryRun.
func regenerateRepoEpic(ctx context.Context, repoDir string, opts FleetEpicOptions) (*PreMigrationEpic, error) {
	if !repoIsPrepared(repoDir) {
		return nil, errRepoNotPrepared
	}

	epic, err := GeneratePreMigrationEpic(ctx, repoDir, opts.FrameworkPath)
	if err != nil {
		return nil, fmt.Errorf("generate epic for %s: %w", repoDir, err)
	}
	epic.OutputPath = filepath.Join(repoDir, fleetEpicFileName)

	if opts.DryRun {
		return epic, nil
	}
	if wErr := WriteEpicMarkdown(ctx, epic, epic.OutputPath); wErr != nil {
		return nil, fmt.Errorf("write epic for %s: %w", repoDir, wErr)
	}
	return epic, nil
}

// repoIsPrepared reports whether a discovered directory carries a repository marker.
func repoIsPrepared(repoDir string) bool {
	return util.DirExists(filepath.Join(repoDir, ".git")) ||
		util.FileExists(filepath.Join(repoDir, ".standards.yaml")) ||
		util.FileExists(filepath.Join(repoDir, ".needs.yaml"))
}
