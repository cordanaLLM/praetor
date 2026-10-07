package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/needs"
	"github.com/cordanaLLM/praetor/internal/util"
)

func runNeeds(args []string) error {
	if len(args) < 1 {
		printNeedsUsage()
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	switch sub {
	case "-h", "--help", "help":
		printNeedsUsage()
		return nil
	case "scan":
		return runNeedsScan(ctx, subArgs)
	case "report":
		return runNeedsReport(ctx, subArgs)
	case "aggregate":
		return runNeedsAggregate(ctx, subArgs)
	case "migrate":
		return runNeedsMigrate(ctx, subArgs)
	case "requests":
		return runNeedsRequests(ctx, subArgs)
	case "epic":
		return runNeedsEpic(ctx, subArgs)
	case "contract":
		return runNeedsContract(ctx, subArgs)
	default:
		return fmt.Errorf("unknown needs subcommand: %s", sub)
	}
}

func printNeedsUsage() {
	fmt.Println("Usage: standardsctl needs <subcommand> [arguments]")
	fmt.Println("\nSubcommands:")
	fmt.Println("  scan [--path=.] [--write|--check]   Scan repo AST and go.mod, emit or check .needs.yaml")
	fmt.Println("  report [--path=.]                   Evaluate compatibility and replacement matrix against the target framework")
	fmt.Println("  aggregate [--dev-dir=...] [--output=...] Aggregate fleet-wide demand and output gap report")
	fmt.Println("  requests [--dev-dir=...] [--output-dir=...] Synthesize and emit deduplicated Framework Demand Requests")
	fmt.Println("  migrate [--path=.] [--apply]        Automated import and dependency rewrite to the target framework (dry run unless --apply)")
	fmt.Println("  epic [--path=.] [--dev-dir=...] [--framework=...] [--output=...] [--dry-run] Generate pre-migration hardening epic")
	fmt.Println("       [--publish --owner=... --repo=... --yes]  Publish one repository's epic to an explicit forge target")
	fmt.Println("       Fleet mode previews writes by default; pass --dry-run=false to write the listed files")
	fmt.Println("  contract export --language=<lang> --out=<file> [--framework=...]  Snapshot a target framework as a capability contract")
	fmt.Println("\nEvery subcommand reads framework.targets from the operator settings")
	fmt.Println("([--fleet-config=...] [--workstation-config=...] [--manifest=...]).")
}

func runNeedsScan(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("needs scan", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	writeManifest := fs.Bool("write", false, "Write discovered needs to .needs.yaml")
	check := fs.Bool("check", false, "Fail when the committed .needs.yaml differs from a fresh scan (updated_at ignored)")
	settings := registerOperatorSettingsFlags(fs)
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if *writeManifest && *check {
		return fmt.Errorf("--write and --check are mutually exclusive")
	}

	if err := adopt.ValidateAdoptionTarget(*path); err != nil {
		return fmt.Errorf("invalid repository target: %w", err)
	}
	selection, err := loadNeedsSelection(ctx, settings)
	if err != nil {
		return fmt.Errorf("needs scan: %w", err)
	}

	report, err := needs.ScanRepo(ctx, *path, selection.registry)
	if err != nil {
		return fmt.Errorf("failed to scan repository needs: %w", err)
	}
	if *check {
		return checkNeedsManifest(ctx, *path, report)
	}

	fmt.Printf("=== Framework Needs Scan: %s ===\n", report.Repository)
	fmt.Print(needs.FormatRepositoryFallback(report))
	fmt.Printf("Go Version: %s | Target Framework: %s\n", report.GoVersion, needs.FrameworkDisplay(report.Framework))
	fmt.Printf("Mapping availability: %s (%s, %d total third-party)\n\n",
		needs.MappingAvailability(report.Readiness), needs.FormatDependencyCounts(report.Readiness), report.Readiness.TotalThirdPartyDeps)
	fmt.Printf("Coverage basis: %s; builds and tests not run\n", report.Readiness.Basis)
	fmt.Println(needs.FormatDeprecations(report))

	fmt.Print(needs.FormatLibraryRelationships(report))

	if *writeManifest {
		return writeNeedsManifest(ctx, *path, report)
	}
	return nil
}

// writeNeedsManifest writes .needs.yaml unless the committed manifest already matches the scan
// apart from updated_at (needs.CheckNeedsManifest), which it then keeps byte for byte: a rewrite
// moved only the timestamp, so the manifest, a generated artefact (ADR-0017), never rendered
// fresh.
func writeNeedsManifest(ctx context.Context, path string, report *needs.RepoNeeds) error {
	drift, err := needs.CheckNeedsManifest(ctx, path, report)
	if err == nil && drift == "" {
		fmt.Printf("\n[PASS] %s/.needs.yaml already matches the scan; kept the recorded file.\n", path)
		return nil
	}
	if err != nil && !errors.Is(err, needs.ErrNeedsManifestMissing) {
		return err
	}
	if err := needs.WriteNeedsManifest(path, report); err != nil {
		return fmt.Errorf("failed to write .needs.yaml: %w", err)
	}
	fmt.Printf("\n[PASS] Wrote %s/.needs.yaml successfully.\n", path)
	return nil
}

// checkNeedsManifest fails when the committed .needs.yaml is not what `needs scan --write`
// would write now. Nothing regenerates the manifest on its own, so without this gate it
// silently falls behind every change to the dependencies or to the generator's schema. A
// row named after its directory is named first: its verdict depends on the checkout's
// directory name.
func checkNeedsManifest(ctx context.Context, path string, report *needs.RepoNeeds) error {
	fmt.Print(needs.FormatRepositoryFallback(report))
	drift, err := needs.CheckNeedsManifest(ctx, path, report)
	if err != nil {
		return err
	}
	if drift != "" {
		return fmt.Errorf("%s/.needs.yaml is stale against a fresh scan; run 'praetorctl needs scan --write --path=%s':\n%s", path, path, drift)
	}
	fmt.Printf("[PASS] %s/.needs.yaml matches a fresh scan (updated_at ignored).\n", path)
	return nil
}

func runNeedsReport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("needs report", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	framework := fs.String("framework", "", "Target framework repository path "+frameworkUsageDefault)
	settings := registerOperatorSettingsFlags(fs)
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}

	if err := adopt.ValidateAdoptionTarget(*path); err != nil {
		return fmt.Errorf("invalid repository target: %w", err)
	}
	selection, err := loadNeedsSelection(ctx, settings)
	if err != nil {
		return fmt.Errorf("needs report: %w", err)
	}

	fwIndex, err := needs.InspectFramework(ctx, selection.frameworkSource(*framework, flagWasSet(fs, "framework")))
	if err != nil {
		return fmt.Errorf("failed to inspect framework: %w", err)
	}

	rep, err := needs.ReportRepoWithFramework(ctx, *path, fwIndex, selection.registry)
	if err != nil {
		return fmt.Errorf("failed to scan repository: %w", err)
	}

	fmt.Print(needs.FormatReportHeader(rep, needs.RowFramework(selection.registry, rep, fwIndex)))
	fmt.Print(needs.FormatLibraryRelationships(rep))
	fmt.Print(needs.FormatUmbrellaImports(rep))
	return nil
}

func runNeedsAggregate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("needs aggregate", flag.ContinueOnError)
	devDir := fs.String("dev-dir", "", "Fleet dev root directory "+devRootUsageDefault)
	framework := fs.String("framework", "", "Framework repository path "+frameworkUsageDefault)
	outputFile := fs.String("output", "", "Optional file path to write markdown report")
	settings := registerOperatorSettingsFlags(fs)
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}

	report, _, err := aggregateFleet(ctx, fs, *devDir, *framework, settings)
	if err != nil {
		return err
	}

	md := needs.RenderFrameworkDemandMarkdown(report)
	if *outputFile != "" {
		if err := util.WriteFileAt(*outputFile, []byte(md), 0o644); err != nil {
			return fmt.Errorf("failed to write output markdown: %w", err)
		}
		fmt.Printf("[PASS] Framework demand report written to %s\n", *outputFile)
	} else {
		fmt.Println(md)
	}
	return nil
}

func runNeedsMigrate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("needs migrate", flag.ContinueOnError)
	path := fs.String("path", ".", "Target repository path")
	framework := fs.String("framework", "", "Framework repository path "+frameworkUsageDefault)
	dryRun := fs.Bool("dry-run", true, "Preview migration without mutating files")
	apply := fs.Bool("apply", false, "Request application (blocked until module-version/API evidence is validated)")
	settings := registerOperatorSettingsFlags(fs)
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}

	applyNow, err := resolveMigrationMode(fs, *apply, *dryRun)
	if err != nil {
		return err
	}

	if err := adopt.ValidateAdoptionTarget(*path); err != nil {
		return fmt.Errorf("invalid repository target: %w", err)
	}
	selection, err := loadNeedsSelection(ctx, settings)
	if err != nil {
		return fmt.Errorf("needs migrate: %w", err)
	}

	source := selection.frameworkSource(*framework, flagWasSet(fs, "framework"))
	plan, err := needs.PlanMigration(ctx, *path, source, selection.registry)
	if err != nil {
		return fmt.Errorf("failed to plan migration: %w", err)
	}

	printMigrationCandidate(plan, needs.MigrationBranch(selection.settings.Framework.MigrationBranch))

	if !applyNow {
		fmt.Println("\n[INFO] Dry-run complete. Application is blocked pending verified module version and API compatibility evidence.")
		return nil
	}

	res, err := needs.ApplyMigration(ctx, *path, plan)
	if err != nil {
		return fmt.Errorf("failed to apply migration: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("migration did not complete: %s", res.Error)
	}
	for _, warning := range res.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
	}
	fmt.Printf("\n[PASS] Migration applied on branch %s (%d files changed).\n", res.Branch, len(res.FilesChanged))
	fmt.Printf("[PASS] Migration guide generated at %s/MIGRATION.md\n", *path)
	return nil
}

// printMigrationCandidate prints a blocked candidate and the branch an admitted
// application would create (framework.migration_branch). A plan against no framework
// shows its mapping availability as n/a, never 0% (needs.MappingAvailability).
func printMigrationCandidate(plan *needs.MigrationPlan, branch string) {
	availability := needs.MappingAvailability(needs.ReadinessMetrics{Score: plan.MappingAvailability, Basis: plan.CoverageBasis})
	fmt.Printf("=== Migration Candidate: %s -> %s ===\n", plan.Repository, needs.FrameworkDisplay(plan.Framework))
	fmt.Printf("Status: %s | Framework version: %s | Migration branch: %s\n", plan.Status, plan.FrameworkVersion, branch)
	fmt.Printf("Mapping availability: %s | Coverage basis: %s; builds and tests not run\n", availability, plan.CoverageBasis)
	for _, blocker := range plan.Blockers {
		fmt.Printf("Blocker: %s\n", blocker)
	}
	fmt.Printf("Added:   %s\n", strings.Join(plan.AddedRequires, ", "))
	fmt.Printf("Proposed removals: %s\n", strings.Join(plan.DroppedRequires, ", "))
	fmt.Printf("Proposed file import replacements: %d\n", len(plan.Replacements))
	for _, r := range plan.Replacements {
		fmt.Printf("  - %s: %s -> %s\n", util.CleanGitURL(r.File), r.OldImport, r.NewImport)
	}
}

// resolveMigrationMode decides whether the migration is executed. --apply implies
// --dry-run=false, so `needs migrate --apply` does what its usage line advertises;
// combining --apply with an explicit --dry-run=true is a contradiction and is rejected
// rather than silently resolved into a no-op.
func resolveMigrationMode(fs *flag.FlagSet, apply, dryRun bool) (bool, error) {
	if !apply {
		return false, nil
	}
	if dryRun && flagWasSet(fs, "dry-run") {
		return false, fmt.Errorf("--apply conflicts with --dry-run=true: pass --apply alone to execute the migration")
	}
	return true, nil
}

// flagWasSet reports whether name was given on the command line rather than defaulted.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

func runNeedsRequests(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("needs requests", flag.ContinueOnError)
	devDir := fs.String("dev-dir", "", "Fleet dev root directory "+devRootUsageDefault)
	framework := fs.String("framework", "", "Framework repository path "+frameworkUsageDefault)
	outputDir := fs.String("output-dir", "", "Optional output directory to write demand requests")
	settings := registerOperatorSettingsFlags(fs)
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}

	report, selection, err := aggregateFleet(ctx, fs, *devDir, *framework, settings)
	if err != nil {
		return err
	}

	requests := needs.SynthesizeDemands(report, selection.targets)
	fmt.Printf("=== Framework Demand Requests: %d Synthesized ===\n", len(requests))
	fmt.Printf("%s\n\n", needs.UnroutedSummary(requests))
	for _, req := range requests {
		kit := req.TargetBuilderKit
		if kit == "" {
			kit = "unrouted"
		}
		fmt.Printf("  [%s] %s\n", req.RequestID, req.Title)
		fmt.Printf("    Target Kit: %s | Consuming Repos: %d | ROI: %s\n\n", kit, req.ConsumerCount, req.MaintenanceROI)
	}

	if *outputDir != "" {
		if err := needs.EmitDemandRequests(ctx, requests, *outputDir); err != nil {
			return fmt.Errorf("failed to emit demand requests: %w", err)
		}
		fmt.Printf("[PASS] Emitted %d demand requests and FRAMEWORK_DEMAND.yaml to %s\n", len(requests), *outputDir)
	}
	return nil
}

// epicFlags carries the parsed `needs epic` command line.
type epicFlags struct {
	path         string
	devDir       string
	framework    string
	frameworkSet bool
	settings     *operatorSettingsFlags
	output       string
	publish      bool
	token        string
	endpoint     string
	owner        string
	repo         string
	assumeYes    bool
	dryRun       bool
}

func runNeedsEpic(ctx context.Context, args []string) error {
	f, err := parseEpicFlags(args)
	if err != nil {
		return err
	}
	if f.devDir != "" && f.publish {
		return fmt.Errorf("--publish is not supported together with --dev-dir: a fleet epic carries only the repository name, " +
			"not the directory needed to resolve forge coordinates; publish one repository at a time with " +
			"'praetorctl needs epic --path=<repo> --publish --yes'")
	}
	selection, err := loadNeedsSelection(ctx, f.settings)
	if err != nil {
		return fmt.Errorf("needs epic: %w", err)
	}
	source := selection.frameworkSource(f.framework, f.frameworkSet)
	if f.devDir != "" {
		opts := needs.FleetEpicOptions{Framework: source, Registry: selection.registry, DryRun: f.dryRun}
		return runFleetNeedsEpic(ctx, f.devDir, opts)
	}
	return runSingleRepoEpic(ctx, f, selection, source)
}

// parseEpicFlags parses the `needs epic` flag set.
func parseEpicFlags(args []string) (epicFlags, error) {
	fs := flag.NewFlagSet("needs epic", flag.ContinueOnError)
	f := epicFlags{}
	fs.StringVar(&f.path, "path", ".", "Target repository path")
	fs.StringVar(&f.devDir, "dev-dir", "", "Run fleet-wide epic generation across all repositories in directory")
	fs.StringVar(&f.framework, "framework", "", "Target framework repository path "+frameworkUsageDefault)
	fs.StringVar(&f.output, "output", "", "Optional markdown file path to write pre-migration epic")
	fs.BoolVar(&f.publish, "publish", false, "Publish pre-migration parent epic and child tasks to remote forge; "+
		"creates only issues whose titles are missing and never updates existing ones")
	fs.StringVar(&f.token, "token", "", "Forge API token (default: GITHUB_TOKEN or gh auth token)")
	fs.StringVar(&f.endpoint, "endpoint", "", "Forge API endpoint (default: https://api.github.com)")
	fs.StringVar(&f.owner, "owner", "", "Forge owner to publish into (overrides the repository manifest and git remote)")
	fs.StringVar(&f.repo, "repo", "", "Forge repository name to publish into (must be given with --owner)")
	fs.BoolVar(&f.assumeYes, "yes", false, "Confirm creating issues in the resolved forge repository")
	fs.BoolVar(&f.dryRun, "dry-run", true, "With --dev-dir, list the epic files without writing them")
	f.settings = registerOperatorSettingsFlags(fs)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return epicFlags{}, err
	}
	if len(positional) != 0 {
		return epicFlags{}, fmt.Errorf("needs epic accepts no positional arguments; use --path (got %q)", positional)
	}
	f.frameworkSet = flagWasSet(fs, "framework")
	return f, nil
}

// runSingleRepoEpic generates, writes and optionally publishes the epic of one repository.
func runSingleRepoEpic(ctx context.Context, f epicFlags, selection *needsSelection, source needs.FrameworkSource) error {
	if err := adopt.ValidateAdoptionTarget(f.path); err != nil {
		return fmt.Errorf("invalid repository target: %w", err)
	}

	epic, err := needs.GeneratePreMigrationEpic(ctx, f.path, source, selection.registry)
	if err != nil {
		return fmt.Errorf("failed to generate pre-migration epic: %w", err)
	}

	if f.output != "" {
		if wErr := needs.WriteEpicMarkdown(ctx, epic, f.output); wErr != nil {
			return fmt.Errorf("failed to write epic markdown: %w", wErr)
		}
		fmt.Printf("[PASS] Pre-migration epic written to %s\n", f.output)
	} else if !f.publish {
		fmt.Println(epic.ChecklistMarkdown)
	}

	if f.publish {
		return publishEpicToForge(ctx, f, epic, selection.settings.Forge.DefaultOwner)
	}
	return nil
}

func runFleetNeedsEpic(ctx context.Context, devDir string, opts needs.FleetEpicOptions) error {
	fmt.Printf("=== Rerunning Fleet Pre-Migration Epics across %s ===\n\n", devDir)
	epics, skips, err := needs.RegenerateFleetEpics(ctx, devDir, opts)
	if err != nil {
		printFleetEpicSkips(skips)
		return fmt.Errorf("fleet epic regeneration failed: %w", err)
	}

	verb := "Generated and updated"
	if opts.DryRun {
		verb = "Would generate"
	}
	fmt.Printf("[PASS] %s %d pre-migration epics:\n\n", verb, len(epics))
	for i := 0; i < len(epics); i++ {
		ep := epics[i]
		readiness := needs.MappingAvailability(needs.ReadinessMetrics{Score: ep.ReadinessScore, Basis: ep.CoverageBasis})
		fmt.Printf("  [%2d/%2d] %-35s (Readiness: %s, %d tasks) -> %s\n",
			i+1, len(epics), ep.RepoName, readiness, len(ep.ChildIssues), ep.OutputPath)
	}
	printFleetEpicSkips(skips)
	if opts.DryRun {
		fmt.Println("\n[INFO] Dry-run complete. Pass --dry-run=false to write these files.")
	}
	return nil
}

// printFleetEpicSkips lists the discovered directories that got no epic, with the reason,
// so a checkout the operator expected to be covered does not vanish from the run.
func printFleetEpicSkips(skips []needs.FleetEpicSkip) {
	if len(skips) == 0 {
		return
	}
	fmt.Printf("\n[SKIP] %d discovered directories got no epic:\n", len(skips))
	for _, skip := range skips {
		fmt.Printf("  - %s: %s\n", skip.RepoDir, skip.Reason)
	}
}

// describeEpicIssue renders where one epic issue landed and whether publishing created it.
func describeEpicIssue(target string, res *forge.IssueUpsertResult) string {
	line := fmt.Sprintf("%s#%d %s", target, res.Number, res.Outcome)
	if res.Outcome != forge.IssueCreated {
		line = fmt.Sprintf("%s#%d already published (%s)", target, res.Number, res.Outcome)
	}
	if res.URL != "" {
		line += " " + res.URL
	}
	return line
}

func publishEpicToForge(ctx context.Context, f epicFlags, epic *needs.PreMigrationEpic, defaultOwner string) error {
	tok := resolveForgeAuthToken(ctx, f.token)
	if tok == "" {
		return fmt.Errorf("epic publishing requires GITHUB_TOKEN, GH_TOKEN, or an authenticated 'gh' CLI session")
	}

	owner, repo, err := resolveEpicTarget(ctx, f, defaultOwner)
	if err != nil {
		return fmt.Errorf("failed resolving repository coordinates for %s: %w", f.path, err)
	}
	if !f.assumeYes {
		return fmt.Errorf("refusing to create issues in %s/%s without confirmation: the target is derived from repository content "+
			"(.standards.yaml, the git origin remote or forge.default_owner); re-run with --yes, or pass --owner/--repo explicitly", owner, repo)
	}

	ghDriver := forge.NewGitHubDriver(tok, f.endpoint)
	ghDriver.SetRepository(owner, repo)

	fmt.Printf("[INFO] Publishing Pre-Migration Epic to %s/%s...\n", owner, repo)
	parentRes, childResults, err := needs.PublishPreMigrationEpic(ctx, ghDriver, epic)
	if err != nil {
		return fmt.Errorf("failed to publish epic to %s/%s: %w", owner, repo, err)
	}

	target := owner + "/" + repo
	fmt.Printf("[PASS] Parent Epic: %s\n", describeEpicIssue(target, parentRes))
	for i, c := range childResults {
		fmt.Printf("       Task %d/%d: %s\n", i+1, len(childResults), describeEpicIssue(target, c))
	}
	return nil
}

// resolveForgeAuthToken resolves the forge token from the flag, the environment, or the
// gh CLI session, all bounded by the caller's context (HISS-02).
func resolveForgeAuthToken(ctx context.Context, explicitToken string) string {
	return util.ResolveAuthTokenContext(ctx, explicitToken)
}

// resolveEpicTarget returns the forge coordinates the epic is published to. Explicit
// --owner/--repo win; otherwise the coordinates are resolved from the repository directory
// (resolveRepoCoordinates).
func resolveEpicTarget(ctx context.Context, f epicFlags, defaultOwner string) (string, string, error) {
	if f.owner != "" && f.repo != "" {
		return f.owner, f.repo, nil
	}
	if f.owner != "" || f.repo != "" {
		return "", "", fmt.Errorf("--owner and --repo must be given together")
	}
	return resolveRepoCoordinates(ctx, f.path, defaultOwner)
}

// resolveRepoCoordinates resolves the forge owner and repository name of the checkout at
// path through config.ResolveRepositoryIdentity: the manifest, then the origin remote, with
// forge.default_owner (defaultOwner) as the last owner step. path must be a real directory:
// a bare repository *name* cannot identify a forge repository, and resolving one relative to
// the working directory used to publish issues into a guessed organization. The checkout path
// never names the owner.
//
// HISS-02: the git lookup inherits the caller's deadline instead of context.Background().
func resolveRepoCoordinates(ctx context.Context, path, defaultOwner string) (string, string, error) {
	owner, name, err := config.ResolveRepositoryIdentity(ctx, path, "", defaultOwner)
	if err != nil {
		return "", "", fmt.Errorf("resolve forge coordinates of %s: %w", path, err)
	}
	return owner, name, nil
}

// aggregateFleet resolves the fleet dev root, the operator's framework targets and the
// framework of `needs aggregate` and `needs requests`, then aggregates the fleet's framework
// demand.
func aggregateFleet(ctx context.Context, fs *flag.FlagSet, devDir, framework string,
	settings *operatorSettingsFlags) (*needs.FleetDemandReport, *needsSelection, error) {
	root, err := resolveDevRootDir(devDir, "--dev-dir")
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", fs.Name(), err)
	}
	selection, err := loadNeedsSelection(ctx, settings)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", fs.Name(), err)
	}
	source := selection.frameworkSource(framework, flagWasSet(fs, "framework"))
	report, err := needs.AggregateFleet(ctx, root, source, selection.registry)
	if err != nil {
		return nil, nil, fmt.Errorf("fleet aggregation failed: %w", err)
	}
	return report, selection, nil
}
