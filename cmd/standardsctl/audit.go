package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/paperclip"
	"github.com/cordanaLLM/praetor/internal/runner"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// auditTimeout bounds the whole governance audit, including its git and scan I/O.
	auditTimeout = 5 * time.Minute
	// preMigrationEpicFile is the pre-migration epic tracked under .workingdir/.
	preMigrationEpicFile = "PRE_MIGRATION_EPIC.md"
	// maxAuditGates bounds the governance gate loop (HISS-02).
	maxAuditGates = 32
	// maxLegacyChecks bounds the legacy-reference loop in auditRepoIdentity (HISS-02).
	maxLegacyChecks = 8
	// legacyModulePath is the pre-rename module path no repository may reference.
	legacyModulePath = "github.com/cordanaLLM/standards"
)

// auditOptions carries the resolved audit inputs. Every companion file defaults to the
// directory of the manifest, so `audit --config=/repo/.standards.yaml` audits /repo and
// never the process working directory.
type auditOptions struct {
	rootDir      string
	manifestPath string
	baselinePath string
	agentsPath   string
	baseRef      string
	touched      []string
	// debtDeltaReason, when non-empty, judges touched files on whether their debt grew rather
	// than on whether they were touched. It is a reason rather than a boolean so the exception
	// cannot be taken without saying why (#150).
	debtDeltaReason string
	policy          config.EffectiveOptions
	effective       *config.EffectivePolicy
	baseline        *baseline.Baseline
	baselineKnown   bool
	// offline skips every forge read; the live Actions checks report as not made.
	offline bool

	// allViolations lists every violation of a ratchet rejection instead of a bounded few (#598).
	allViolations bool
	// maxStale is the --max-stale-baseline-entries bound: the audit fails when more baseline
	// entries than this match nothing in the tree (#349). Negative, the default, sets no bound,
	// and stale entries are reported without failing, so a tree that passed keeps passing.
	maxStale int
}

func runAudit(args []string) error {
	opts, err := parseAuditOptions(args)
	if err != nil {
		return err
	}

	// HISS-02: every filesystem, git and scanner call below runs under this deadline.
	ctx, cancel := context.WithTimeout(context.Background(), auditTimeout)
	defer cancel()

	effective, err := auditManifestAndLockfile(ctx, opts)
	if err != nil {
		return err
	}
	opts.effective = effective
	manifest := effective.Manifest

	fmt.Printf("=== %s/%s Governance Audit ===\n", manifest.Repository.Owner, manifest.Repository.Name)
	fmt.Printf("[PASS] %s\n", effective.Evidence())

	if err := runAuditGates(ctx, manifest, opts); err != nil {
		return err
	}

	fmt.Printf("\nAudit Summary: configured governance gates passed for %s/%s; source analysis is limited to the reported HISS file scope.\n", manifest.Repository.Owner, manifest.Repository.Name)
	return nil
}

// parseAuditOptions parses the audit flags and anchors every companion path on the
// manifest directory unless it was set explicitly.
func parseAuditOptions(args []string) (*auditOptions, error) {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	manifestPath := fs.String("config", ".standards.yaml", "Path to .standards.yaml; its directory is the audited root")
	baselinePath := fs.String("baseline", "", "Path to .standards-baseline.json (default: <root>/.standards-baseline.json)")
	agentsPath := fs.String("agents", "", "Path to AGENTS.md (default: <root>/AGENTS.md)")
	baseRef := fs.String("base", "", "Git ref the change set is compared against (e.g. origin/main); enables the touched-file clean rule over that range and the baseline growth guard")
	touched := fs.String("touched", "", "Comma-separated files, relative to the audited root, to treat as touched instead of asking git")
	debtDelta := fs.String("touched-debt-delta-reason", "",
		"Judge touched files on whether their debt grew rather than on whether they were touched, for a provably mechanical change; the value is the recorded reason. Also read from "+debtDeltaReasonEnv)
	allViolations := fs.Bool("all-violations", false, "List every violation of a HISS ratchet rejection instead of the first three per class")
	offline := fs.Bool("offline", false, offlineFlagUsage)
	maxStale := fs.Int(maxStaleFlag, 0,
		"Fail when more than N baseline entries match nothing in the tree; unset, stale entries are reported and the audit passes")
	var policy config.EffectiveOptions
	fs.StringVar(&policy.CatalogRoot, "catalog-root", "", "Root containing pinned .config/archetypes (default: audited root)")
	fs.StringVar(&policy.FleetPath, "fleet-config", "", "Explicit fleet complexity policy file")
	fs.StringVar(&policy.OrganizationPath, "organization-config", "", "Explicit organization complexity policy file")
	fs.StringVar(&policy.DeploymentPath, "deployment-config", "", "Explicit deployment complexity policy file")
	fs.StringVar(&policy.WorkstationPath, "workstation-config", "", "Explicit workstation complexity policy file")

	if _, err := parseInterspersed(fs, args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("audit accepts no positional arguments, got %q", fs.Args())
	}
	staleBound, err := resolveStaleBound(fs, *maxStale)
	if err != nil {
		return nil, err
	}

	rootDir := filepath.Dir(*manifestPath)
	policy.Root, policy.ManifestPath, policy.Audit = rootDir, *manifestPath, true
	return &auditOptions{
		rootDir:         rootDir,
		manifestPath:    *manifestPath,
		baselinePath:    resolveCompanion(rootDir, *baselinePath, ".standards-baseline.json"),
		agentsPath:      resolveCompanion(rootDir, *agentsPath, "AGENTS.md"),
		baseRef:         *baseRef,
		touched:         splitCSV(*touched),
		debtDeltaReason: resolveDebtDeltaReason(*debtDelta),
		allViolations:   *allViolations,
		offline:         *offline,
		maxStale:        staleBound,
		policy:          policy,
	}, nil
}

// resolveCompanion returns explicit when set, otherwise name joined onto rootDir.
func resolveCompanion(rootDir, explicit, name string) string {
	if explicit != "" {
		return explicit
	}
	return filepath.Join(rootDir, name)
}

// runAuditGates executes every governance gate in order, stopping at the first failure.
func runAuditGates(ctx context.Context, manifest *config.Manifest, opts *auditOptions) error {
	rootDir := opts.rootDir
	gates := []func() error{
		func() error { return auditRepoIdentity(manifest, rootDir) },
		func() error { return auditLockDigestsContext(ctx, manifest, rootDir, opts.policy.CatalogRoot) },
		func() error { return auditBaselineAndInvariants(ctx, opts) },
		func() error { return auditReadmeGovernance(ctx, manifest, opts) },
		func() error {
			return auditDocumentationGate(ctx, manifest, rootDir, opts.effective.Policy.BranchProtection)
		},
		func() error { return auditAPICompatibilityGate(ctx, manifest, rootDir) },
		func() error { return auditAgentContextAndDevcontainer(ctx, manifest, opts) },
		func() error { return auditAgentProjections(ctx, rootDir) },
		func() error { return auditCavemanAgentSurfaces(ctx, rootDir) },
		func() error {
			return auditBranchProtectionAndSupplyChain(ctx, manifest, rootDir, &opts.effective.Policy)
		},
		func() error { return auditPaperclipHarness(ctx, manifest, rootDir) },
		func() error { return auditCavemanConfiguredSources(ctx, manifest, rootDir) },
		func() error { return auditRunnerMatrix(ctx, manifest, rootDir) },
		func() error { return auditPreMigrationTracking(rootDir) },
		func() error { return auditAgentDefinitions(ctx, manifest, rootDir) },
		func() error { return auditGitHooks(ctx, manifest, rootDir) },
		func() error { return auditLiveActions(ctx, manifest, rootDir, opts.offline) },
	}

	for i := 0; i < len(gates) && i < maxAuditGates; i++ {
		if err := gates[i](); err != nil {
			return err
		}
	}
	return nil
}

func auditManifestAndLockfile(ctx context.Context, opts *auditOptions) (*config.EffectivePolicy, error) {
	manifestBytes, err := contextopt.ReadSnapshot(ctx, opts.manifestPath)
	if err != nil {
		return nil, fmt.Errorf("[FAIL] Manifest audit failed: %w", err)
	}
	lockPath := filepath.Join(opts.rootDir, ".standards.lock")
	lockBytes, err := contextopt.ReadSnapshot(ctx, lockPath)
	if err != nil {
		return nil, fmt.Errorf("[FAIL] .standards.lock is missing or unreadable: %w", err)
	}
	// Resolve exactly the snapshots just read; diagnostics and scan policy cannot
	// accidentally describe separate reads of a concurrently changed input file.
	effective, err := config.LoadEffectivePolicyInputsContext(ctx, opts.policy, manifestBytes, lockBytes)
	if err != nil {
		return nil, fmt.Errorf("[FAIL] Effective policy audit failed: %w", err)
	}
	manifest := effective.Manifest
	fmt.Printf("[PASS] Manifest verified: %s/%s (Version %d)\n", manifest.Repository.Owner, manifest.Repository.Name, manifest.Version)
	fmt.Printf("       Profiles: %v | Facets: %v\n", manifest.Profiles, manifest.Facets)
	return effective, nil
}

// auditBaselineAndInvariants scans the audited root, evaluates the HISS-13 ratchet with
// the real change set (touched-file clean rule) and, when a base ref is given, refuses a
// baseline that grew versus the one committed on that ref.
func auditBaselineAndInvariants(ctx context.Context, opts *auditOptions) error {
	base, err := baseline.LoadBaseline(opts.baselinePath)
	if err != nil {
		return fmt.Errorf("[FAIL] Baseline audit failed: %w", err)
	}
	opts.baselineKnown = !base.Absent
	opts.baseline = base

	scanOpts := auditScanOptions(opts)
	scanRep, err := hiss.Scan(ctx, opts.rootDir, scanOpts)
	if err != nil {
		return fmt.Errorf("[FAIL] Invariant audit failed: %w", err)
	}
	fmt.Printf("[INFO] %s\n", scanRep.CoverageEvidence())
	if scanRep.Truncated {
		return fmt.Errorf("[FAIL] Invariant audit failed: %w (%d infractions recorded before truncation)", hiss.ErrScanTruncated, scanRep.TotalInfractions)
	}
	printLines(scanRep.Complexity.Lines())
	current := fingerprintViolations(scanRep.Violations)

	touched, err := resolveTouchedFiles(ctx, opts)
	if err != nil {
		return err
	}

	ratchet := baseline.EvaluateRatchetWithOptions(base, current, touched,
		baseline.RatchetOptions{DebtDelta: opts.debtDeltaReason != ""})
	if opts.debtDeltaReason != "" {
		// Stated on every run that takes the exception, so it is visible in the hook output and
		// in CI rather than being a silent relaxation of the boy-scout rule.
		fmt.Printf("[WARN] Touched-file clean rule judged by debt delta, not by touch; recorded reason: %s\n",
			opts.debtDeltaReason)
	}
	if !ratchet.Passed && base.Absent && len(current) > 0 {
		return missingBaselineFailure(opts.baselinePath, len(current))
	}
	if !ratchet.Passed {
		return fmt.Errorf("[FAIL] %s", describeRejection(ctx, opts.rootDir, opts.baselinePath, scanOpts, base, current, ratchet, opts.allViolations))
	}
	printInvariantVerdict(scanRep, fmt.Sprintf("%d active violations within %d baselined limit (%d touched files clean) (skipped: %d ignored directories, %d symlinks, %d oversize files, %d non-regular files)",
		ratchet.CurrentCount, base.TotalInfractions, len(touched), scanRep.Skips.DirCount,
		scanRep.Skips.Symlinks, scanRep.Skips.Oversize, scanRep.Skips.Irregular))

	return auditRatchetPassed(ctx, opts, base, ratchet)
}

// printInvariantVerdict prints the HISS scan verdict for the languages the scan examined and
// names every source language it did not. A clean scan used to print an unqualified PASS over
// a tree whose JavaScript, TypeScript and Svelte no rule had read (#589); now the PASS names its
// languages, and source no rule examined is reported as unscanned rather than passed. A tree
// with source in no scanned language gets no PASS line at all.
func printInvariantVerdict(scanRep *hiss.ScanReport, detail string) {
	scanned := scanRep.Coverage.ScannedLanguageNames()
	unscanned := scanRep.Coverage.UnscannedSourceSummary()
	switch {
	case len(scanned) > 0:
		fmt.Printf("[PASS] HISS invariant scan verified for %s: %s.\n", strings.Join(scanned, ", "), detail)
	case unscanned != "":
		fmt.Printf("[UNSCANNED] HISS invariant scan read no source file: %s.\n", detail)
	default:
		fmt.Printf("[PASS] HISS invariant scan verified: %s.\n", detail)
	}
	if unscanned != "" {
		fmt.Printf("[UNSCANNED] No HISS rule examined %s; the invariants are unverified there, not passed.\n", unscanned)
	}
}

// auditScanOptions returns the scan options of the resolved policy: its function-length
// limit, which the audit enforces, its complexity limits, which it only measures, and the HISS
// exceptions the manifest declares and documents (config.EffectivePolicy.HISSScanOptions).
// Without a resolved policy the audit-compatibility ceiling applies. A declared exception left
// unhonoured is printed as a warning.
func auditScanOptions(opts *auditOptions) hiss.ScanOptions {
	scanOpts, warning := opts.effective.HISSScanOptions(opts.rootDir, hiss.ScanOptions{})
	if warning != "" {
		fmt.Printf("[WARN] %s\n", warning)
	}
	return scanOpts
}

// printLines prints report lines to standard output, one per line.
func printLines(lines []string) {
	for _, line := range lines {
		fmt.Println(line)
	}
}

// verifiedTargetList names the projections one verification read, and any agent_clients left
// out, so the audit line never claims a projection it did not check.
func verifiedTargetList(res *compiler.CompileResult) string {
	names := make([]string, 0, len(res.Files))
	for _, f := range res.Files {
		names = append(names, f.RelativePath)
	}
	list := "none"
	if len(names) > 0 {
		list = strings.Join(names, ", ")
	}
	if len(res.NotApplicable) > 0 {
		list += " (not applicable: " + strings.Join(res.NotApplicable, ", ") + ")"
	}
	return list
}

func auditAgentContextAndDevcontainer(ctx context.Context, manifest *config.Manifest, opts *auditOptions) error {
	if err := auditAgentContext(ctx, manifest, opts); err != nil {
		return err
	}
	return auditDevContainer(ctx, manifest, opts)
}

// auditAgentContext verifies the text register block, the vendor projections and the caveman lint
// of AGENTS.md, and that the agent evidence directory is ignored. A declined agent-harness step
// keeps adoption from writing the harness and covers none of these (ADR-0010 decisions 5 and
// 11): a failure says so, and the pass names the decline (adopt.AuditDecline, #600).
func auditAgentContext(ctx context.Context, manifest *config.Manifest, opts *auditOptions) error {
	harness, err := adopt.AuditDecline(manifest, "agent-harness")
	if err != nil {
		return fmt.Errorf("[FAIL] Agent context audit failed: %w", err)
	}
	root := opts.rootDir
	tr := compiler.NewTranspiler()
	if _, err := compiler.SyncRegisterBlock(ctx, root, opts.agentsPath, false); err != nil {
		return fmt.Errorf("[FAIL] Agent context text register: %w", harness.Narrow(err))
	}
	res, err := tr.VerifyCompiled(ctx, opts.agentsPath, root)
	if err != nil {
		return fmt.Errorf("[FAIL] Agent context targets out of sync: %w", harness.Narrow(err))
	}
	fmt.Printf("[PASS] Cross-agent context targets verified in sync: %s.\n", verifiedTargetList(res))
	lint, err := compiler.LintContext(ctx, opts.agentsPath)
	if err != nil {
		return fmt.Errorf("[FAIL] Agent context: %w", harness.Narrow(err))
	}
	fmt.Printf("[PASS] Agent context %s.\n", lint.Summary())
	// Whatever facets the manifest enables: the block sends agent evidence there in every repository.
	if err := compiler.CheckEvidenceIgnored(ctx, filepath.Dir(opts.agentsPath)); err != nil {
		return fmt.Errorf("[FAIL] Agent context evidence directory: %w", err)
	}
	if harness.Declined {
		fmt.Println(harness.Line("Agent harness"))
	}
	return nil
}

// auditDevContainer verifies a committed .devcontainer/devcontainer.json against the one the
// declared profiles and facets synthesize from the pinned catalog; a repository without one
// passes. profile set runs it too (declarationGates). With dev-container in adoption.decline,
// adoption never writes it, so a file the repository keeps is its own and passes with the
// decline named (#600).
func auditDevContainer(ctx context.Context, manifest *config.Manifest, opts *auditOptions) error {
	devContainer, err := adopt.AuditDecline(manifest, "dev-container")
	if err != nil {
		return fmt.Errorf("[FAIL] DevContainer audit failed: %w", err)
	}
	if devContainer.Declined {
		fmt.Println(devContainer.Line("DevContainer configuration"))
		return nil
	}
	dcPath := filepath.Join(opts.rootDir, ".devcontainer", "devcontainer.json")
	if !util.FileExists(dcPath) {
		return nil
	}
	features, err := config.ResolveDevContainerFeatures(ctx, opts.effective)
	if err != nil {
		return fmt.Errorf("[FAIL] DevContainer catalog resolution failed: %w", err)
	}
	dc, err := devcontainer.SynthesizeWithFeatures(manifest, features)
	if err != nil {
		return fmt.Errorf("[FAIL] DevContainer synthesis failed: %w", err)
	}
	if err := devcontainer.Verify(ctx, dcPath, dc); err != nil {
		return fmt.Errorf("[FAIL] DevContainer out of sync with declared standards: %w", devContainerCheckoutRemedy(ctx, opts.rootDir, manifest, err))
	}
	fmt.Println("[PASS] DevContainer configuration verified in sync with declared standards.")
	return nil
}

// auditAgentProjections fails when any vendor or plugin copy of a persona under
// .agents/agents differs from its canonical source, so a loosened persona copy can no
// longer pass the audit unnoticed. Persona directories agent_clients leaves out are not
// checked, as compile-context --verify does not check them.
func auditAgentProjections(ctx context.Context, rootDir string) error {
	verified, err := compiler.VerifyAgentProjections(ctx, rootDir)
	if err != nil {
		return fmt.Errorf("[FAIL] Agent persona projections out of sync: %w", err)
	}
	if verified > 0 {
		fmt.Printf("[PASS] Agent persona projections verified (%d copies identical to .agents/agents).\n", verified)
	}
	return nil
}

// auditCavemanAgentSurfaces fails when a canonical persona or skill breaks the caveman
// lint or the AgentTextCeiling word budget. Personas and skills sit under
// config.SurfaceContext by its own doc comment, so this gate has no opt-out, the same as
// the AGENTS.md caveman gate in auditAgentContextAndDevcontainer (ADR-0010 decision 11,
// amended for personas and skills; Q-059).
func auditCavemanAgentSurfaces(ctx context.Context, rootDir string) error {
	personas, err := compiler.LintCanonicalPersonas(ctx, rootDir)
	if err != nil {
		return fmt.Errorf("[FAIL] Persona caveman lint: %w", err)
	}
	skills, err := compiler.LintCanonicalSkillFiles(ctx, rootDir)
	if err != nil {
		return fmt.Errorf("[FAIL] Skill caveman lint: %w", err)
	}
	if personas > 0 || skills > 0 {
		fmt.Printf("[PASS] Caveman lint verified (%d personas, %d skills, <= %d prose words each).\n",
			personas, skills, compiler.AgentTextCeiling)
	}
	return nil
}

// auditCavemanConfiguredSources enforces the omission-resistant register.sources inventory
// through the same checker as `caveman check --configured-sources`. An omitted register or
// sources contract is zero verified work and fails closed; a contract that declares no text
// (config.RegisterSources.DeclaresNone) passes with its reason while no Paperclip harness, the
// text adoption binds, exists (#601).
func auditCavemanConfiguredSources(ctx context.Context, manifest *config.Manifest, rootDir string) error {
	if manifest == nil || manifest.Register == nil || manifest.Register.Sources == nil {
		return errors.New("[FAIL] Caveman non-Markdown source coverage: audit requires register.sources; declare the " +
			"repository's agent-facing text, or expected: 0 with a reason when it has none")
	}
	register := manifest.EffectiveRegister()
	inputs, result, err := configuredCavemanInputs(ctx, rootDir, register)
	if err != nil {
		return fmt.Errorf("[FAIL] Caveman non-Markdown source coverage: %w", err)
	}
	if register.Sources.DeclaresNone() {
		fmt.Printf("[PASS] Caveman non-Markdown source coverage: %s.\n", declaredNoSources(register.Sources))
		return nil
	}
	if report, failed := renderCavemanChecks(inputs, 0, 0, true); failed > 0 {
		return fmt.Errorf("[FAIL] Caveman non-Markdown source lint (%d of %d values):\n%s",
			failed, len(inputs), boundedSourceReport(report))
	}
	fmt.Printf("[PASS] Caveman non-Markdown source coverage verified (%d values, %s).\n",
		len(result.Sources), result.SHA256)
	return nil
}

// maxAuditSourceReportLines bounds the lint report one audit prints; a contract may hold
// thousands of values. `caveman check --configured-sources` prints every report.
const maxAuditSourceReportLines = 60

func boundedSourceReport(report string) string {
	lines := strings.SplitN(strings.TrimSpace(report), "\n", maxAuditSourceReportLines+1)
	if len(lines) <= maxAuditSourceReportLines {
		return strings.Join(lines, "\n")
	}
	omitted := strings.Count(lines[maxAuditSourceReportLines], "\n") + 1
	return strings.Join(lines[:maxAuditSourceReportLines], "\n") + fmt.Sprintf(
		"\n... %d more lines; run `praetorctl caveman check --configured-sources` for the full report", omitted)
}

// auditBranchProtectionAndSupplyChain checks the committed ruleset against policy, the
// effective policy the audit resolved and adopt rendered the ruleset from.
func auditBranchProtectionAndSupplyChain(ctx context.Context, manifest *config.Manifest, rootDir string, policy *config.ResolvedPolicy) error {
	if err := auditBranchProtection(ctx, manifest, rootDir, policy); err != nil {
		return err
	}

	// The label taxonomy gate standards_audit runs too; it honours a labels decline (#600).
	labels, err := adopt.AuditLabelTaxonomy(manifest, rootDir)
	if err != nil {
		return err
	}
	fmt.Println(labels)
	return nil
}

// auditBranchProtection checks the committed branch protection ruleset against policy, the
// ruleset the declared profiles and facets select. profile set runs it too (declarationGates).
func auditBranchProtection(ctx context.Context, manifest *config.Manifest, rootDir string, policy *config.ResolvedPolicy) error {
	summary, err := adopt.AuditBranchProtectionWithPolicy(ctx, manifest, rootDir, policy)
	if err != nil {
		return err
	}
	fmt.Println(summary)
	return nil
}

// legacyRefCheck describes one file that must not mention a legacy identity.
type legacyRefCheck struct {
	rel     string
	needle  string
	message string
}

// run reads the file when present and fails on a legacy reference or on any read error.
func (c legacyRefCheck) run(rootDir string) error {
	found, err := repoFileContains(rootDir, c.rel, c.needle)
	if err != nil {
		return err
	}
	if found {
		return fmt.Errorf("[FAIL] %s", c.message)
	}
	return nil
}

func auditRepoIdentity(manifest *config.Manifest, rootDir string) error {
	owner, name := manifest.Repository.Owner, manifest.Repository.Name
	if owner == "" || name == "" {
		return fmt.Errorf("[FAIL] Manifest repository owner and name must not be empty")
	}

	checks := []legacyRefCheck{
		{rel: "go.mod", needle: legacyModulePath,
			message: fmt.Sprintf("go.mod contains obsolete module path '%s'. Expected 'github.com/%s/%s'", legacyModulePath, owner, name)},
		{rel: ".needs.yaml", needle: legacyModulePath,
			message: fmt.Sprintf(".needs.yaml contains obsolete repository reference '%s'", legacyModulePath)},
	}
	if name != "standards" {
		checks = append(checks, legacyRefCheck{rel: ".standards-baseline.json", needle: `"repository": "cordanaLLM/standards"`,
			message: ".standards-baseline.json contains obsolete repository 'cordanaLLM/standards'"})
	}
	for i := 0; i < len(checks) && i < maxLegacyChecks; i++ {
		if err := checks[i].run(rootDir); err != nil {
			return err
		}
	}

	fmt.Printf("[PASS] Repository identity verified (%s/%s, zero legacy references).\n", owner, name)
	return nil
}

// repoFileContains reports whether the file rel under rootDir contains needle. A
// missing file is not an error; an unreadable one is.
func repoFileContains(rootDir, rel, needle string) (bool, error) {
	path, err := util.ConfinePath(rootDir, rel)
	if err != nil {
		return false, fmt.Errorf("[FAIL] Resolve %s: %w", rel, err)
	}
	if !util.FileExists(path) {
		return false, nil
	}
	// #nosec G304 -- path is confined to the audited root by ConfinePath.
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("[FAIL] Repository identity audit cannot read %s: %w", rel, err)
	}
	return strings.Contains(string(data), needle), nil
}

// paperclipHarnessRel is the Paperclip harness adoption writes, audit validates, and a
// register.sources contract that declares no text requires absent.
const paperclipHarnessRel = ".paperclip/harness.json"

// auditPaperclipHarness validates .paperclip/harness.json. With paperclip in adoption.decline,
// adoption never writes it, so an absent harness passes with the decline named (#600); one the
// repository keeps is still validated, since register.sources binds its text.
func auditPaperclipHarness(ctx context.Context, manifest *config.Manifest, rootDir string) error {
	decline, err := adopt.AuditDecline(manifest, "paperclip")
	if err != nil {
		return fmt.Errorf("[FAIL] Paperclip harness audit failed: %w", err)
	}
	harnessPath := filepath.Join(rootDir, filepath.FromSlash(paperclipHarnessRel))
	if !util.FileExists(harnessPath) && decline.Declined {
		fmt.Println(decline.Line("Paperclip agent runtime harness " + paperclipHarnessRel))
		return nil
	}
	if !util.FileExists(harnessPath) {
		return fmt.Errorf("[FAIL] Paperclip agent runtime harness %s is missing; run 'praetorctl adopt' to reconcile", paperclipHarnessRel)
	}
	h, err := paperclip.LoadHarnessContext(ctx, harnessPath)
	if err != nil {
		return fmt.Errorf("[FAIL] Paperclip harness validation failed: %w", err)
	}
	expectedPlatform := fmt.Sprintf("%s/%s", manifest.Repository.Owner, manifest.Repository.Name)
	if h.Platform != expectedPlatform {
		return fmt.Errorf("[FAIL] Paperclip harness platform mismatch: got %q, expected %q; "+
			"run '%s' to set platform (every other harness value kept); "+
			"with adoption.decline listing paperclip, adopt never writes the harness, so set platform by hand",
			h.Platform, expectedPlatform, adopt.ForceCommand(""))
	}
	fmt.Printf("[PASS] Paperclip agent runtime harness verified (%s, %d rules).\n", h.Platform, len(h.OperatingContract))
	return nil
}

func auditRunnerMatrix(ctx context.Context, manifest *config.Manifest, rootDir string) error {
	policy, err := config.LoadCascadingRunnerConfigContext(ctx, rootDir, manifest.Repository.Owner)
	if err != nil {
		return fmt.Errorf("[FAIL] Cascading runner config failed: %w", err)
	}

	targets := []struct {
		osName string
		arch   string
		isGPU  bool
	}{
		{"linux", "amd64", false},
		{"linux", "arm64", false},
		{"linux", "amd64", true},
		{"darwin", "arm64", false},
		{"darwin", "amd64", false},
	}

	for _, target := range targets {
		spec, err := runner.ResolveRunner(policy, target.osName, target.arch, target.isGPU)
		if err != nil {
			return fmt.Errorf("[FAIL] Runner matrix resolution failed for %s/%s (gpu=%v): %w", target.osName, target.arch, target.isGPU, err)
		}
		if spec.Type == "" || len(spec.RunsOn) == 0 {
			return fmt.Errorf("[FAIL] Empty runner spec for %s/%s", target.osName, target.arch)
		}
	}
	fmt.Println("[PASS] External runner matrix & ARC platform routing policies verified.")
	return nil
}

func auditPreMigrationTracking(rootDir string) error {
	// The epic lives in the local working directory; the repository root is only the
	// legacy location. Checking just the root silently skipped the whole gate.
	epicPath := resolvePreMigrationEpic(rootDir)
	if epicPath != "" {
		// #nosec G304 -- epicPath is one of two fixed locations under the audited root.
		content, err := os.ReadFile(epicPath)
		if err != nil {
			return fmt.Errorf("[FAIL] Read %s failed: %w", epicPath, err)
		}
		requiredStages := []string{"[TASK 1/5]", "[TASK 2/5]", "[TASK 3/5]", "[TASK 4/5]", "[TASK 5/5]"}
		text := string(content)
		for _, stage := range requiredStages {
			if !strings.Contains(text, stage) {
				return fmt.Errorf("[FAIL] %s missing required stage %s", epicPath, stage)
			}
		}
		fmt.Printf("[PASS] Pre-migration epic (5-stage lifecycle) verified: %s\n", epicPath)
	}

	needsPath, err := util.ConfinePath(rootDir, ".needs.yaml")
	if err != nil {
		return fmt.Errorf("[FAIL] Resolve .needs.yaml: %w", err)
	}
	if util.FileExists(needsPath) {
		// #nosec G304 -- needsPath is confined to the audited root by ConfinePath.
		data, err := os.ReadFile(needsPath)
		if err != nil || len(data) == 0 {
			return fmt.Errorf("[FAIL] .needs.yaml is missing or empty")
		}
		fmt.Println("[PASS] Polyglot framework demand & needs analysis (.needs.yaml) verified.")
	}
	return nil
}

// resolvePreMigrationEpic returns the pre-migration epic path, preferring the working
// directory location over the legacy repository-root one. It returns "" when neither
// exists.
func resolvePreMigrationEpic(rootDir string) string {
	candidates := []string{
		filepath.Join(rootDir, ".workingdir", preMigrationEpicFile),
		filepath.Join(rootDir, preMigrationEpicFile),
	}
	for _, candidate := range candidates {
		if util.FileExists(candidate) {
			return candidate
		}
	}
	return ""
}

// auditGitHooks requires lefthook.yml and an active pre-commit hook in a Git checkout. With
// git-hooks in adoption.decline the repository owns its hooks, so neither is required; the
// configuration check is the one standards_audit runs too (adopt.AuditGitHookConfig, #600).
func auditGitHooks(ctx context.Context, manifest *config.Manifest, rootDir string) error {
	gitDir := filepath.Join(rootDir, ".git")
	if !util.DirExists(gitDir) && !util.FileExists(gitDir) {
		return nil
	}

	line, declined, err := adopt.AuditGitHookConfig(manifest, rootDir)
	if err != nil {
		return err
	}
	if declined {
		fmt.Println(line)
		return nil
	}

	if os.Getenv("CI") == "true" || os.Getenv("GITHUB_ACTIONS") == "true" {
		fmt.Println("[PASS] CI environment detected: lefthook.yml verified (local hook installation skipped).")
		return nil
	}

	hooksDir, err := adopt.ResolveGitHooksDir(ctx, rootDir)
	if err != nil {
		return fmt.Errorf("[FAIL] Resolve git hooks directory: %w", err)
	}
	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	if !util.FileExists(preCommitPath) {
		return fmt.Errorf("[FAIL] Pre-commit hook %s is missing or inactive; run 'lefthook install' or 'praetorctl adopt' to activate", preCommitPath)
	}

	fmt.Printf("[PASS] Local Git hooks (%s via lefthook) verified active.\n", preCommitPath)
	return nil
}

// debtDeltaReasonEnv carries the debt-delta reason for a commit made through the generated
// pre-commit hook. That hook runs bare "praetorctl audit", so a flag cannot reach it without
// editing a hook Praetor installs and verifies; an environment variable can, and the reason is
// still mandatory because the variable's value is the reason:
//
//	PRAETOR_TOUCHED_DEBT_DELTA_REASON="rename module path to cordanaLLM" git commit
const debtDeltaReasonEnv = "PRAETOR_TOUCHED_DEBT_DELTA_REASON"

// resolveDebtDeltaReason prefers an explicit flag over the environment, and treats a value that
// is only whitespace as no reason at all -- an empty justification is not a justification.
func resolveDebtDeltaReason(flagValue string) string {
	if reason := strings.TrimSpace(flagValue); reason != "" {
		return reason
	}
	return strings.TrimSpace(os.Getenv(debtDeltaReasonEnv))
}

// missingBaselineFailure explains a ratchet failure whose real cause is that no baseline exists.
//
// A missing baseline loads as zero recorded debt, so the ratchet reports every existing
// infraction as new. That is the correct verdict for a greenfield repository, which has no debt to
// record -- and it is why a repository with no infractions still passes here. For a repository
// that does carry debt, "N new unbaselined" blames code that predates the change, when the fix is
// to record a baseline. Saying so turns an unexplained wall of violations into one command (#138).
func missingBaselineFailure(path string, infractions int) error {
	return fmt.Errorf("[FAIL] no baseline exists at %s, so all %d existing infractions are being ratcheted as new debt; "+
		"this is not a regression in the change -- record the existing debt with 'praetorctl baseline --record' "+
		"(or adopt, which records a first baseline unless --record-baseline=false), then re-run the audit", path, infractions)
}
