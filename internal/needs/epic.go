package needs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/topology"
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
	// marker, so fleet regeneration reports it as a skip rather than a failure.
	errRepoNotPrepared = errors.New("needs: directory is not a prepared repository")
)

// PreMigrationEpic captures the parent epic and child task issues for repo modernization.
type PreMigrationEpic struct {
	RepoName          string            `json:"repo_name"`
	TargetFramework   string            `json:"target_framework"`
	ReadinessScore    float64           `json:"readiness_score"`
	CoverageBasis     string            `json:"coverage_basis,omitempty"`
	FrameworkVersion  string            `json:"framework_version"`
	Status            string            `json:"status"`
	Blockers          []string          `json:"blockers"`
	ParentEpic        forge.IssueSpec   `json:"parent_epic"`
	ChildIssues       []forge.IssueSpec `json:"child_issues"`
	ChecklistMarkdown string            `json:"checklist_markdown"`
	// OutputPath is the file fleet regeneration wrote, or - under FleetEpicOptions.DryRun
	// - would have written. It is empty for a single-repository generation.
	OutputPath string `json:"output_path,omitempty"`
}

// GeneratePreMigrationEpic analyzes a repository and synthesizes a pre-migration epic.
//
// A selected checkout supplies the same evidence as a needs report. Module-only
// selections remain unresolved identity declarations; invalid selected paths fail.
// Local filesystem paths never reach public epic fields.
func GeneratePreMigrationEpic(ctx context.Context, repoPath, targetFramework string) (*PreMigrationEpic, error) {
	analysis, err := analyzeMigration(ctx, repoPath, targetFramework)
	if err != nil {
		return nil, fmt.Errorf("analyze pre-migration epic: %w", err)
	}
	return epicFromAnalysis(ctx, repoPath, analysis)
}

// epicFromAnalysis plans the migration of an analysed repository and builds its epic.
func epicFromAnalysis(ctx context.Context, repoPath string, analysis *migrationAnalysis) (*PreMigrationEpic, error) {
	migrationPlan, err := planMigrationFromAnalysis(ctx, repoPath, analysis)
	if err != nil {
		return nil, fmt.Errorf("plan pre-migration epic: %w", err)
	}
	return buildEpicStructure(repoPath, analysis.report, migrationPlan)
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
		CoverageBasis:     repoNeeds.Readiness.Basis,
		FrameworkVersion:  plan.FrameworkVersion,
		Status:            plan.Status,
		Blockers:          append([]string(nil), plan.Blockers...),
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
		Body:      fmt.Sprintf("## Scope\n- Review %d proposed import substitutions targeting %s.\n- Resolve a verified module version and validate API compatibility before application.\n- Executable migration admission remains unavailable.\n- Reconcile .needs.yaml capability declarations.", len(plan.Replacements), plan.Framework),
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
	fmt.Fprintf(&sb, "- **Mapping Availability**: `%.1f%%`\n", repoNeeds.Readiness.Score)
	writeMigrationEvidence(&sb, plan)
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

// PublishPreMigrationEpic publishes the pre-migration parent epic and decomposed tasks
// to the target forge, creating only the issues that do not exist yet.
//
// Publishing resolves every issue by title against the forge's issue inventory, the
// identity forge.SyncIssues upserts on, so publishing again creates no second epic: an
// issue whose title already exists is reused as it is, and only missing issues are
// created. A partial earlier publish therefore resumes, and each child chains onto the
// real number of the task before it whether that task was just created or already
// existed. Every title is checked against the inventory before the first write; a
// duplicate or ambiguous title fails the publish with nothing created.
//
// Publishing does not synchronize existing issues. Their body, labels, dependency
// references and state are never converged onto the regenerated epic, so a task the
// operator closed or relabelled stays that way, and an epic republished after its
// readiness changed keeps the body it was first published with. No result is ever
// forge.IssueUpdated.
func PublishPreMigrationEpic(ctx context.Context, f forge.Forge, epic *PreMigrationEpic) (*forge.IssueUpsertResult, []*forge.IssueUpsertResult, error) {
	if f == nil {
		return nil, nil, ErrNilForge
	}
	if epic == nil {
		return nil, nil, ErrNilEpic
	}
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}

	planned := append([]forge.IssueSpec{epic.ParentEpic}, epic.ChildIssues...)
	batch, err := forge.PrepareIssueBatch(ctx, f, planned)
	if err != nil {
		return nil, nil, fmt.Errorf("prepare epic issues: %w", err)
	}
	parentRes, err := batch.Ensure(ctx, epic.ParentEpic)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to publish parent epic issue: %w", err)
	}

	childResults := make([]*forge.IssueUpsertResult, 0, len(epic.ChildIssues))
	for i := range epic.ChildIssues {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return parentRes, childResults, ctxErr
		}
		spec := chainChildTask(epic, parentRes, epic.ChildIssues[i], childResults)
		res, cErr := batch.Ensure(ctx, spec)
		if cErr != nil {
			return parentRes, childResults, fmt.Errorf("failed to publish child task %d: %w", i+1, cErr)
		}
		childResults = append(childResults, res)
	}

	return parentRes, childResults, nil
}

// chainChildTask renders one child task for publishing, chaining it onto the task
// published before it by its real issue number rather than the pre-publish anchor. The
// parent's URL is cited only when known: an epic that already existed is resolved from
// the issue inventory, which carries its number but no URL.
func chainChildTask(epic *PreMigrationEpic, parent *forge.IssueUpsertResult,
	spec forge.IssueSpec, published []*forge.IssueUpsertResult) forge.IssueSpec {
	if len(published) > 0 {
		prev := published[len(published)-1]
		spec.DependsOn = []string{fmt.Sprintf("%s#%d", epic.RepoName, prev.Number)}
	}

	body := spec.Body
	if len(spec.DependsOn) > 0 {
		body = fmt.Sprintf("%s\n\nDepends-On: %s", body, strings.Join(spec.DependsOn, ", "))
	}
	backlink := fmt.Sprintf("*Part of Epic #%d*", parent.Number)
	if parent.URL != "" {
		backlink = fmt.Sprintf("*Part of Epic #%d (%s)*", parent.Number, parent.URL)
	}
	spec.Body = fmt.Sprintf("%s\n\n---\n%s\n", body, backlink)
	return spec
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

// FleetEpicSkip names a discovered directory fleet regeneration generated no epic for,
// and why. A skip is not a failure, but it is reported: a checkout the operator expected
// to be covered must not vanish from the run without a trace.
type FleetEpicSkip struct {
	RepoDir string `json:"repo_dir"`
	Reason  string `json:"reason"`
}

// notPreparedReason is the skip reason for a directory without a repository marker.
const notPreparedReason = "no repository marker: not a Git checkout with HEAD metadata, and no .standards.yaml or .needs.yaml"

// noAnalyzerReason is the skip reason for an undeclared checkout in which no analyzer
// recognises a project, such as a documentation-only repository.
const noAnalyzerReason = "no language analyzer recognises a project in this checkout, and it declares no needs"

// duplicateReasonPrefix starts the skip reason for a collapsed linked worktree.
const duplicateReasonPrefix = "linked worktree of "

// RegenerateFleetEpics discovers all prepared repositories in fleetRoot and regenerates
// their pre-migration epics.
//
// Discovery is the fleet aggregation's (discoverFleet), and every epic scores its
// repository through scanRepository, so an epic's readiness equals the repository's
// `needs aggregate` row. Per-repository failures are collected and returned joined: a run
// in which nothing could be generated or written reports an error instead of an empty
// success. A repository that declares needs (.standards.yaml or .needs.yaml) but in which
// no analyzer recognises a project is such a failure. Returned as skips, each with its
// reason, are: discovered directories that are not prepared repositories, undeclared
// checkouts no analyzer recognises, and linked worktrees collapsed onto their repository.
func RegenerateFleetEpics(ctx context.Context, fleetRoot string, opts FleetEpicOptions) ([]*PreMigrationEpic, []FleetEpicSkip, error) {
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}

	layout, err := discoverFleet(ctx, fleetRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("failed discovering fleet repos: %w", err)
	}

	run := &fleetEpicRun{opts: opts}
	for _, dup := range layout.duplicates {
		run.skips = append(run.skips, FleetEpicSkip{RepoDir: dup.Dir, Reason: duplicateReasonPrefix + dup.Of})
	}
	for i := 0; i < len(layout.repos); i++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			run.failures = append(run.failures, ctxErr)
			return run.epics, run.skips, errors.Join(run.failures...)
		}
		run.regenerate(ctx, layout.repos[i])
	}

	return run.epics, run.skips, errors.Join(run.failures...)
}

// fleetEpicRun accumulates the outcome of one fleet epic regeneration.
type fleetEpicRun struct {
	opts     FleetEpicOptions
	epics    []*PreMigrationEpic
	skips    []FleetEpicSkip
	failures []error
}

// regenerate records one repository's epic, skip or failure.
func (r *fleetEpicRun) regenerate(ctx context.Context, repo *fleetRepo) {
	epic, err := regenerateRepoEpic(ctx, repo, r.opts)
	switch {
	case err == nil:
		r.epics = append(r.epics, epic)
	case errors.Is(err, errRepoNotPrepared):
		r.skips = append(r.skips, FleetEpicSkip{RepoDir: repo.root, Reason: notPreparedReason})
	case errors.Is(err, ErrNoAnalyzer) && !repo.declared:
		r.skips = append(r.skips, FleetEpicSkip{RepoDir: repo.root, Reason: noAnalyzerReason})
	default:
		r.failures = append(r.failures, err)
	}
}

// regenerateRepoEpic regenerates one repository's epic, writing it unless opts.DryRun.
func regenerateRepoEpic(ctx context.Context, repo *fleetRepo, opts FleetEpicOptions) (*PreMigrationEpic, error) {
	repoDir := repo.root
	if !repoIsPrepared(repoDir) {
		return nil, errRepoNotPrepared
	}

	analysis, err := analyzeMigrationWith(ctx, opts.FrameworkPath, func(framework *FrameworkIndex) (*RepoNeeds, error) {
		return scanRepositoryWithFramework(ctx, repo, framework)
	})
	if err != nil {
		return nil, fmt.Errorf("generate epic for %s: %w", repoDir, err)
	}
	epic, err := epicFromAnalysis(ctx, repoDir, analysis)
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
//
// The git marker is decided by the shared checkout detection, so a linked worktree or an
// initialized submodule - whose .git is a gitlink file rather than a directory - counts as
// prepared, while a stray .git directory holding no HEAD does not. The .standards.yaml and
// .needs.yaml manifests remain the non-git fallback for a directory that is not a checkout.
//
// An empty repoDir is rejected before any marker is looked up: every marker path is built
// with filepath.Join, which turns "" into a relative path, so an empty argument would probe
// the process working directory and report whatever that happens to contain.
func repoIsPrepared(repoDir string) bool {
	if repoDir == "" {
		return false
	}
	return topology.HasValidGitRepo(repoDir) ||
		util.FileExists(filepath.Join(repoDir, ".standards.yaml")) ||
		util.FileExists(filepath.Join(repoDir, ".needs.yaml"))
}
