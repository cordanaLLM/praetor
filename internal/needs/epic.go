package needs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/gating"
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
	// OmittedTasks lists the task slots the repository gives nothing to do, each with its
	// reason. The checklist names them; they are never published as issues.
	OmittedTasks []OmittedEpicTask `json:"omitted_tasks,omitempty"`
	// OutputPath is the file fleet regeneration wrote, or - under FleetEpicOptions.DryRun
	// - would have written. It is empty for a single-repository generation.
	OutputPath string `json:"output_path,omitempty"`
}

// OmittedEpicTask names one of the epic's task slots that plans no issue, and why: the
// scanned repository gives the task nothing to do, such as a substitution task when no
// target framework is configured.
type OmittedEpicTask struct {
	Task   int    `json:"task"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
}

// GeneratePreMigrationEpic analyzes a repository and synthesizes a pre-migration epic.
//
// A selected checkout supplies the same evidence as a needs report. Module-only
// selections remain unresolved identity declarations; invalid selected paths fail.
// Local filesystem paths never reach public epic fields. registry supplies the analyzers
// and framework targets; nil selects DefaultRegistry.
func GeneratePreMigrationEpic(ctx context.Context, repoPath string, framework FrameworkSource, registry *AnalyzerRegistry) (*PreMigrationEpic, error) {
	analysis, err := analyzeMigration(ctx, repoPath, framework, registry)
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
	// The hygiene task restates the function-length limit the repository's audit enforces.
	// An unresolvable policy falls back to the HISS ceiling, which is never looser than that
	// audit; its notice is not part of the epic.
	complexity, _, err := config.ResolveRepositoryComplexity(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolve pre-migration epic complexity: %w", err)
	}
	routing, err := resolveRunnerRouting(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolve pre-migration epic runner routing: %w", err)
	}
	facts := &epicFacts{
		languages:     detectedLanguageSteps(analysis.report),
		maxFuncLOC:    complexity.MaxFuncLOC,
		rootGoModule:  util.FileExists(filepath.Join(repoPath, "go.mod")),
		kubernetes:    (&flavor.InfraK8sFlavor{}).Detect(repoPath),
		runnerRouting: routing,
	}
	return buildEpicStructure(repoPath, analysis.report, migrationPlan, facts)
}

func buildEpicStructure(repoPath string, repoNeeds *RepoNeeds, plan *MigrationPlan, facts *epicFacts) (*PreMigrationEpic, error) {
	repoName := repoNeeds.Repository
	if repoName == "" || repoName == "unknown" {
		repoName = repositoryDirName(repoPath)
	}
	repoName = strings.TrimPrefix(repoName, "github.com/")

	tasks := createChildTasks(repoName, plan, facts)
	children, omitted := splitEpicTasks(tasks)
	checklistMD := renderEpicChecklistMarkdown(repoName, repoNeeds, plan, facts, tasks)

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
		ChildIssues:       children,
		OmittedTasks:      omitted,
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

// epicFacts are the repository facts the epic's tasks are scoped by. Every task and every
// language-specific step derives from them, so the epic prescribes no work the scanned
// repository gives no reason for: no race detector for a Rust workspace, no in-cluster DNS
// without Kubernetes manifests, no substitution task without a target framework.
type epicFacts struct {
	// languages are the step sets of the detected languages (detectedLanguageSteps).
	languages []languageSteps
	// maxFuncLOC is the function-length limit the repository's resolved policy imposes.
	maxFuncLOC int
	// rootGoModule reports a go.mod at the repository root, the only module the gate's
	// prefetch, security and race-test stages check.
	rootGoModule bool
	// kubernetes reports Kubernetes manifests at the repository root as the infra-k8s
	// flavor detects them: a Helm chart, a Kustomization or a helmfile.
	kubernetes bool
	// runnerRouting is what the repository declares about runner routing.
	runnerRouting runnerRouting
}

// runnerRouting is what the runner routing `praetorctl audit` resolves says about one
// repository (config.LoadCascadingRunnerConfigContext).
type runnerRouting struct {
	// declared reports routing that differs from the built-in defaults.
	declared bool
	// loadErr is why the routing configuration does not load; nil when it loads. Its text
	// names local paths, so it never reaches the epic.
	loadErr error
}

// resolveRunnerRouting reads the runner routing the repository declares in its fleet tier
// (.config/fleet.yaml) and repository tier. The organisation tier is keyed by the forge
// owner, which the scan does not resolve, so routing declared only there is not seen. A
// configuration that does not load is recorded rather than returned: the epic turns it into
// repair work, as an unresolved complexity policy falls back to the ceiling. Only a
// cancelled or expired context is an error.
func resolveRunnerRouting(ctx context.Context, repoPath string) (runnerRouting, error) {
	policy, err := config.LoadCascadingRunnerConfigContext(ctx, repoPath, "")
	switch {
	case err == nil:
		return runnerRouting{declared: !reflect.DeepEqual(*policy, config.DefaultRunnerPolicy())}, nil
	case ctx.Err() != nil:
		return runnerRouting{}, fmt.Errorf("load runner routing: %w", ctx.Err())
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return runnerRouting{}, fmt.Errorf("load runner routing: %w", err)
	default:
		return runnerRouting{loadErr: err}, nil
	}
}

// languageSteps phrases the hygiene work of one language: how its 3D tests run and which
// error-handling defects its audit removes.
type languageSteps struct {
	// ids are the languages a needs analyzer reports for it (RepoNeeds.Language, .Languages).
	ids  []string
	name string
	// tests is how the language's tests run, with its race checker where one applies.
	tests string
	// audit lists the error-handling defects HISS-07 removes in the language.
	audit string
	// cycles reports that the language's toolchain accepts circular dependencies between
	// modules. The Go compiler rejects import cycles and Cargo rejects cycles between crates.
	cycles bool
	// gated reports that the gate's prefetch, security and race-test stages check the
	// language when its module sits at the repository root.
	gated bool
}

// epicLanguageSteps covers every language the needs analyzers recognise, in the order task
// bodies list them.
var epicLanguageSteps = []languageSteps{
	{ids: []string{"go"}, name: "Go", tests: "`go test -race ./...`", gated: true,
		audit: "unhandled panics, discarded errors and `os.Exit` or `log.Fatal` outside `main`"},
	{ids: []string{"rust"}, name: "Rust", tests: "`cargo test --workspace`",
		audit: "`.unwrap()`, `.expect()` and `panic!` on fallible paths"},
	{ids: []string{"python"}, name: "Python", tests: "the project's test runner, such as `python3 -m pytest`", cycles: true,
		audit: "bare `except:` clauses, swallowed exceptions and `sys.exit` outside entry points"},
	{ids: []string{"typescript", "svelte"}, name: "TypeScript", tests: "the package's `test` script", cycles: true,
		audit: "unhandled promise rejections, empty `catch` blocks and `process.exit` outside entry points"},
	{ids: []string{"native", "c", "cpp", "cuda"}, name: "C/C++", cycles: true,
		tests: "the build system's test target, such as `ctest` or `meson test`, under ThreadSanitizer",
		audit: "unchecked return codes and `abort()` or `exit()` in library code"},
}

// genericLanguageSteps phrases the hygiene work of a repository none of whose languages
// epicLanguageSteps covers. It names no language and claims no toolchain.
var genericLanguageSteps = languageSteps{
	tests: "the repository's test runner",
	audit: "unhandled panics, unwrapped errors and raw fatal exits",
}

// detectedLanguageSteps returns the steps of every language the scan detected, in
// epicLanguageSteps order, or genericLanguageSteps alone when it covers none of them.
func detectedLanguageSteps(repoNeeds *RepoNeeds) []languageSteps {
	detected := map[string]bool{repoNeeds.Language: true}
	for _, language := range repoNeeds.Languages {
		detected[language] = true
	}
	var steps []languageSteps
	for _, entry := range epicLanguageSteps {
		if slices.ContainsFunc(entry.ids, func(id string) bool { return detected[id] }) {
			steps = append(steps, entry)
		}
	}
	if len(steps) == 0 {
		return []languageSteps{genericLanguageSteps}
	}
	return steps
}

// languageNames names the step sets keep selects, in order; the generic set has no name.
func languageNames(steps []languageSteps, keep func(languageSteps) bool) string {
	var names []string
	for _, step := range steps {
		if step.name != "" && keep(step) {
			names = append(names, step.name)
		}
	}
	return strings.Join(names, ", ")
}

// languageLine prefixes a step with the language it is phrased for. A step of the generic
// set names no language and starts the sentence itself.
func languageLine(name, step string) string {
	if name != "" {
		return name + ": " + step
	}
	if step == "" {
		return ""
	}
	return strings.ToUpper(step[:1]) + step[1:]
}

// scopeBody renders a task body's scope list.
func scopeBody(lines []string) string {
	return "## Scope\n- " + strings.Join(lines, "\n- ")
}

// epicTask is one of the epic's task slots: the issue it plans, or why it plans none.
type epicTask struct {
	slot int
	spec forge.IssueSpec
	// omitted is why the repository gives this task nothing to do; empty for a planned task.
	omitted string
}

// createChildTasks lays the epic out as its five task slots, each scoped by facts. A task
// the repository gives nothing to do keeps its slot as omitted, with its reason, so the
// checklist still names all five and each planned task depends on the planned task before it.
func createChildTasks(repoName string, plan *MigrationPlan, facts *epicFacts) []epicTask {
	tasks := []epicTask{
		hygieneTask(facts),
		decouplingTask(facts),
		substitutionTask(plan),
		verificationTask(facts),
		activationTask(facts),
	}
	previous := 0
	for i := range tasks {
		slot := i + 1
		tasks[i].slot = slot
		tasks[i].spec.Title = fmt.Sprintf("[TASK %d/%d] %s: %s", slot, totalEpicTasks, tasks[i].spec.Title, repoName)
		tasks[i].spec.State = "open"
		if tasks[i].omitted != "" {
			continue
		}
		if previous > 0 {
			tasks[i].spec.DependsOn = []string{taskAnchor(previous)}
		}
		previous = slot
	}
	return tasks
}

// splitEpicTasks separates the issues the epic plans from the slots it omits.
func splitEpicTasks(tasks []epicTask) ([]forge.IssueSpec, []OmittedEpicTask) {
	children := make([]forge.IssueSpec, 0, len(tasks))
	var omitted []OmittedEpicTask
	for _, task := range tasks {
		if task.omitted != "" {
			omitted = append(omitted, OmittedEpicTask{Task: task.slot, Title: task.spec.Title, Reason: task.omitted})
			continue
		}
		children = append(children, task.spec)
	}
	return children, omitted
}

// hygieneTask is task 1: the function-length limit the repository's policy imposes, and the
// error-handling audit and 3D tests phrased for each detected language.
func hygieneTask(facts *epicFacts) epicTask {
	lines := []string{fmt.Sprintf("Enforce NASA JPL Rule 4: refactor all functions to <= %d LOC.", facts.maxFuncLOC)}
	for _, lang := range facts.languages {
		lines = append(lines,
			languageLine(lang.name, "eliminate "+lang.audit+"."),
			languageLine(lang.name, "add 3D unit tests (positive, negative, boundary) and run them with "+lang.tests+"."))
	}
	return epicTask{spec: forge.IssueSpec{
		Title: "Invariant & Complexity Hygiene", Body: scopeBody(lines), Labels: []string{"task", "hiss", "hygiene"},
	}}
}

// decouplingTask is task 2. Hardcoded endpoints and secrets are work in any repository;
// in-cluster DNS names only where Kubernetes manifests are detected, and circular module
// dependencies only in languages whose toolchain accepts them.
func decouplingTask(facts *epicFacts) epicTask {
	lines := []string{"Replace hardcoded localhost URLs and host names with configuration."}
	if facts.kubernetes {
		lines = append(lines, "Replace in-cluster DNS names (`<service>.<namespace>.svc.cluster.local`) with configured endpoints, so the code runs outside the cluster.")
	}
	lines = append(lines, "Move secrets and tokens out of the source into environment variables or a secret store.")
	if names := languageNames(facts.languages, func(s languageSteps) bool { return s.cycles }); names != "" {
		lines = append(lines, "Break circular dependencies between modules ("+names+").")
	}
	return epicTask{spec: forge.IssueSpec{
		Title: "Decoupling & Config Externalization", Body: scopeBody(lines), Labels: []string{"task", "architecture", "decoupling"},
	}}
}

// substitutionTask is task 3, omitted when there is nothing to substitute: no target
// framework is configured, or the scan proposes no substitution against the one that is.
func substitutionTask(plan *MigrationPlan) epicTask {
	task := epicTask{spec: forge.IssueSpec{
		Title: "Framework Dependency Substitution", Labels: []string{"task", "dependencies", "migration"},
	}}
	switch {
	case plan.Framework == "":
		task.omitted = nothingToRewrite
	case len(plan.Replacements) == 0 && len(plan.DroppedRequires) == 0:
		task.omitted = fmt.Sprintf("no substitution is proposed against `%s`: substitutions are proposed only for Go modules its contract replaces", plan.Framework)
	default:
		task.spec.Body = scopeBody([]string{
			fmt.Sprintf("Review %d proposed import substitutions and %d dropped module requirements targeting `%s`.",
				len(plan.Replacements), len(plan.DroppedRequires), plan.Framework),
			"Resolve a verified module version and validate API compatibility before application.",
			"Executable migration admission remains unavailable.",
			"Reconcile .needs.yaml capability declarations.",
		})
	}
	return task
}

// verificationTask is task 4. The gate's prefetch, security and race-test stages check only
// a Go module at the repository root, so every other detected language is named as tested
// and audited outside the gate.
func verificationTask(facts *epicFacts) epicTask {
	lines := []string{
		"Run diff-aware CI verification via `praetorctl ci filter`.",
		"Run `" + gating.RepoRunCommand + "` in isolated worktree.",
		"Confirm all 6 gate stages (lockfiles and prefetch, HISS ratchet, security scans, flavor conformance, race tests, receipt) report passed or not_applicable; skipped is not a pass.",
	}
	ungated := languageNames(facts.languages, func(s languageSteps) bool { return !s.gated || !facts.rootGoModule })
	if ungated != "" {
		lines = append(lines, "The gate's prefetch, security and race-test stages check only a Go module at the repository root: run the task 1 tests and audits for "+ungated+" outside the gate.")
	}
	lines = append(lines, "Sign Ed25519 Exit-0 receipt and submit fast-forward PR.")
	return epicTask{spec: forge.IssueSpec{
		Title: "Gated Verification & Ed25519 Receipt", Body: scopeBody(lines), Labels: []string{"task", "verification", "gating"},
	}}
}

// activationTask is task 5. Runner routing is work only where the repository declares
// routing, or where its routing configuration does not load.
func activationTask(facts *epicFacts) epicTask {
	lines := []string{
		"Reconcile branch protection rulesets, labels and repository metadata on the forge with `praetorctl sync --remote`; without `--remote`, `praetorctl sync` leaves the forge untouched.",
		"Pin the receipt signing key: set `receipt.public_key` in .standards.yaml to the public key `praetorctl gate keygen` prints, so `praetorctl gate verify` rejects receipts any other key signed.",
		"Synthesize Paperclip agent harness (`praetorctl paperclip harness`).",
	}
	switch {
	case facts.runnerRouting.loadErr != nil:
		lines = append(lines, "Repair the runner routing configuration: it does not load, and `praetorctl audit` names the file and the error.")
	case facts.runnerRouting.declared:
		lines = append(lines, "Check the declared runner routing: `praetorctl audit` resolves every platform target through it and refuses a Darwin target routed to a self-hosted Linux runner.")
	}
	return epicTask{spec: forge.IssueSpec{
		Title: "Full Praetor Activation & Governance Lockdown", Body: scopeBody(lines), Labels: []string{"task", "activation", "governance"},
	}}
}

func renderEpicChecklistMarkdown(repoName string, repoNeeds *RepoNeeds, plan *MigrationPlan, facts *epicFacts, tasks []epicTask) string {
	var sb strings.Builder
	writef(&sb, "# Pre-Migration Epic: %s\n\n", repoName)
	if plan.Framework == "" {
		sb.WriteString("- **Target Framework**: not configured\n")
		writef(&sb, "- **Mapping Availability**: %s\n", MappingAvailability(repoNeeds.Readiness))
	} else {
		writef(&sb, "- **Target Framework**: `%s`\n", plan.Framework)
		writef(&sb, "- **Mapping Availability**: `%s`\n", MappingAvailability(repoNeeds.Readiness))
	}
	if names := languageNames(facts.languages, func(languageSteps) bool { return true }); names != "" {
		writef(&sb, "- **Detected Languages**: %s\n", names)
	}
	writeMigrationEvidence(&sb, plan)
	writef(&sb, "- **Third-Party Dependencies**: `%d` total (%d covered, %d gaps)\n\n",
		repoNeeds.Readiness.TotalThirdPartyDeps, repoNeeds.Readiness.CoveredDeps, repoNeeds.Readiness.GapDeps)

	writeEpicTaskChecklist(&sb, tasks)

	sb.WriteString("\n## Execution Directives\n")
	sb.WriteString("1. All changes must pass the repository verification gate with zero warnings.\n")
	sb.WriteString("2. Direct commits to `main` are prohibited; changes must traverse `praetorctl gate run`.\n")
	// Target intent, not an observed fact: nothing here reads the repository's workflows,
	// so the directive never claims the filter already runs.
	sb.WriteString("3. Diff-aware CI (HISS-18): CI should call `praetorctl ci filter` so docs-only and state-only changes skip the heavy test and security gates. This epic does not inspect the repository's workflows and does not claim the filter already runs.\n")
	sb.WriteString("4. Ed25519 Exit-0 receipts mandatory on all pull requests.\n")
	return sb.String()
}

// writeEpicTaskChecklist lists every task slot in order: a planned task as an open item with
// its prerequisite, an omitted one with the reason it has nothing to do.
func writeEpicTaskChecklist(sb *strings.Builder, tasks []epicTask) {
	sb.WriteString("## Pre-Migration Tasks\n\n")
	for _, task := range tasks {
		if task.omitted != "" {
			writef(sb, "- **Task %d**: %s\n  - *Omitted*: %s.\n", task.slot, task.spec.Title, task.omitted)
			continue
		}
		writef(sb, "- [ ] **Task %d**: %s\n", task.slot, task.spec.Title)
		if len(task.spec.DependsOn) > 0 {
			writef(sb, "  - *Prerequisites*: Depends-On: %s\n", strings.Join(task.spec.DependsOn, ", "))
		}
	}
}

// PublishPreMigrationEpic publishes the pre-migration parent epic and decomposed tasks
// to the target forge, creating only the issues that do not exist yet. An epic with no
// target framework is refused before the forge is contacted (ErrFrameworkNotConfigured).
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
	if epic.TargetFramework == "" {
		return nil, nil, fmt.Errorf("refusing to publish the pre-migration epic of %s: %w", epic.RepoName, ErrFrameworkNotConfigured)
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
// directory and the file are created owner-only rather than world-readable. The file is
// written through a pinned handle on its own directory (util.WriteFileConfined), the only
// root an operator-named path has: a link planted at outputPath is refused instead of
// written through, and the replace is atomic (BUG-826).
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
	if err := util.WriteFileAt(outputPath, []byte(renderEpicDocument(epic)), util.SecureFilePerm); err != nil {
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
	// Framework selects the framework every epic scores against (SelectFrameworkSource).
	Framework FrameworkSource
	// Registry supplies the analyzers and framework targets; nil selects DefaultRegistry.
	Registry *AnalyzerRegistry
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

// duplicateReasonPrefix starts the skip reason for a collapsed linked worktree, or for a
// submodule checked out in one.
const duplicateReasonPrefix = "linked-worktree checkout of "

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
// checkouts no analyzer recognises, and linked worktrees (and the submodules checked out
// in them) collapsed onto their repository.
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

	analysis, err := analyzeMigrationWith(ctx, opts.Framework, opts.Registry, func(framework *FrameworkIndex) (*RepoNeeds, error) {
		return scanRepositoryWithFramework(ctx, repo, framework, opts.Registry)
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
