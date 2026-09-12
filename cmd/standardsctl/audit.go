package main

import (
	"context"
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
	policy       config.EffectiveOptions
	effective    *config.EffectivePolicy
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

	fmt.Printf("\nAudit Summary: 100%% Compliance with %s/%s HISS-16 baseline.\n", manifest.Repository.Owner, manifest.Repository.Name)
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
	var policy config.EffectiveOptions
	fs.StringVar(&policy.CatalogRoot, "catalog-root", "", "Root containing pinned .config/archetypes (default: audited root)")
	fs.StringVar(&policy.FleetPath, "fleet-config", "", "Explicit fleet complexity policy file")
	fs.StringVar(&policy.OrganizationPath, "organization-config", "", "Explicit organization complexity policy file")
	fs.StringVar(&policy.DeploymentPath, "deployment-config", "", "Explicit deployment complexity policy file")
	fs.StringVar(&policy.WorkstationPath, "workstation-config", "", "Explicit workstation complexity policy file")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("audit accepts no positional arguments, got %q", fs.Args())
	}

	rootDir := filepath.Dir(*manifestPath)
	policy.Root, policy.ManifestPath, policy.Audit = rootDir, *manifestPath, true
	return &auditOptions{
		rootDir:      rootDir,
		manifestPath: *manifestPath,
		baselinePath: resolveCompanion(rootDir, *baselinePath, ".standards-baseline.json"),
		agentsPath:   resolveCompanion(rootDir, *agentsPath, "AGENTS.md"),
		baseRef:      *baseRef,
		touched:      splitCSV(*touched),
		policy:       policy,
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
		func() error { return auditLockDigestsContext(ctx, manifest, rootDir) },
		func() error { return auditBaselineAndInvariants(ctx, opts) },
		func() error { return auditAgentContextAndDevcontainer(ctx, manifest, opts) },
		func() error { return auditAgentProjections(rootDir) },
		func() error { return auditBranchProtectionAndSupplyChain(manifest, rootDir) },
		func() error { return auditPaperclipHarness(manifest, rootDir) },
		func() error { return auditRunnerMatrix(ctx, manifest, rootDir) },
		func() error { return auditPreMigrationTracking(rootDir) },
		func() error { return auditAgentDefinitions(rootDir) },
		func() error { return auditGitHooks(ctx, rootDir) },
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

	scanOpts := hiss.ScanOptions{MaxFuncLOC: config.AuditMaxFuncLOC}
	if opts.effective != nil {
		scanOpts.MaxFuncLOC = opts.effective.Policy.Complexity.MaxFuncLOC
	}
	scanRep, err := hiss.Scan(ctx, opts.rootDir, scanOpts)
	if err != nil {
		return fmt.Errorf("[FAIL] Invariant audit failed: %w", err)
	}
	if scanRep.Truncated {
		return fmt.Errorf("[FAIL] Invariant audit failed: %w (%d infractions recorded before truncation)", hiss.ErrScanTruncated, scanRep.TotalInfractions)
	}
	current := fingerprintViolations(scanRep.Violations)

	touched, err := resolveTouchedFiles(ctx, opts)
	if err != nil {
		return err
	}

	ratchet := baseline.EvaluateRatchet(base, current, touched)
	if !ratchet.Passed {
		return describeRatchetFailure(ratchet)
	}
	fmt.Printf("[PASS] HISS invariant scan verified: %d active violations within %d baselined limit (%d touched files clean) (skipped: %d ignored directories, %d symlinks, %d oversize files).\n",
		ratchet.CurrentCount, base.TotalInfractions, len(touched), scanRep.Skips.DirCount, scanRep.Skips.Symlinks, scanRep.Skips.Oversize)

	return auditBaselineGrowth(ctx, opts, base)
}

// describeRatchetFailure renders the first few new and touched-file violations.
func describeRatchetFailure(ratchet *baseline.RatchetResult) error {
	var msgs []string
	for i := 0; i < len(ratchet.NewViolations) && i < maxRatchetExamples; i++ {
		v := ratchet.NewViolations[i]
		msgs = append(msgs, fmt.Sprintf("  [%s] %s:%d - %s (new)", v.RuleID, v.FilePath, v.LineNumber, v.Message))
	}
	for i := 0; i < len(ratchet.TouchedCleanViolations) && i < maxRatchetExamples; i++ {
		v := ratchet.TouchedCleanViolations[i]
		msgs = append(msgs, fmt.Sprintf("  [%s] %s:%d - %s (touched file must be clean)", v.RuleID, v.FilePath, v.LineNumber, v.Message))
	}
	if ratchet.CurrentCount > ratchet.PreviousCount {
		msgs = append(msgs, fmt.Sprintf("  total infractions rose from %d to %d", ratchet.PreviousCount, ratchet.CurrentCount))
	}
	return fmt.Errorf("[FAIL] HISS invariant violations introduced (%d total infractions, %d new unbaselined, %d in touched files):\n%s",
		ratchet.CurrentCount, len(ratchet.NewViolations), len(ratchet.TouchedCleanViolations), strings.Join(msgs, "\n"))
}

func auditAgentContextAndDevcontainer(ctx context.Context, manifest *config.Manifest, opts *auditOptions) error {
	root := opts.rootDir
	tr := compiler.NewTranspiler()
	if err := tr.VerifyContext(ctx, opts.agentsPath, root); err != nil {
		return fmt.Errorf("[FAIL] Agent context targets out of sync: %w", err)
	}
	fmt.Println("[PASS] Cross-agent context targets (Claude, Cursor, Copilot, Windsurf, Gemini, Codex) verified in sync.")

	dcPath := filepath.Join(root, ".devcontainer", "devcontainer.json")
	if !util.FileExists(dcPath) {
		return nil
	}
	dc, err := devcontainer.Synthesize(manifest)
	if err != nil {
		return fmt.Errorf("[FAIL] DevContainer synthesis failed: %w", err)
	}
	if err := devcontainer.Verify(ctx, dcPath, dc); err != nil {
		return fmt.Errorf("[FAIL] DevContainer out of sync with declared standards: %w", err)
	}
	fmt.Println("[PASS] DevContainer configuration verified in sync with declared standards.")
	return nil
}

// auditAgentProjections fails when any vendor or plugin copy of a persona under
// .agents/agents differs from its canonical source, so a loosened persona copy can no
// longer pass the audit unnoticed.
func auditAgentProjections(rootDir string) error {
	verified, err := verifyAgentProjections(rootDir)
	if err != nil {
		return fmt.Errorf("[FAIL] Agent persona projections out of sync: %w", err)
	}
	if verified > 0 {
		fmt.Printf("[PASS] Agent persona projections verified (%d copies identical to .agents/agents).\n", verified)
	}
	return nil
}

func auditBranchProtectionAndSupplyChain(manifest *config.Manifest, rootDir string) error {
	policy := config.DefaultPolicy()
	policy.ApplyOverrides(manifest.Overrides)

	// Verify .github/rulesets/main.json if linear history or signed commits required
	rulesetPath := filepath.Join(rootDir, ".github", "rulesets", "main.json")
	if policy.BranchProtection.EnforceLinearHistory || policy.BranchProtection.RequireSignedCommits {
		if !util.FileExists(rulesetPath) {
			return fmt.Errorf("[FAIL] Branch protection ruleset .github/rulesets/main.json is missing while policy requires linear history or signed commits; run 'praetorctl sync' to reconcile")
		}
		fmt.Println("[PASS] Branch protection & merge ruleset .github/rulesets/main.json verified.")
	}

	// Verify .config/labels.yaml
	labelsPath := filepath.Join(rootDir, ".config", "labels.yaml")
	if !util.FileExists(labelsPath) {
		return fmt.Errorf("[FAIL] Required label taxonomy .config/labels.yaml is missing")
	}
	fmt.Println("[PASS] Repository label taxonomy .config/labels.yaml verified.")

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

func auditPaperclipHarness(manifest *config.Manifest, rootDir string) error {
	harnessPath := filepath.Join(rootDir, ".paperclip", "harness.json")
	if !util.FileExists(harnessPath) {
		return fmt.Errorf("[FAIL] Paperclip agent runtime harness .paperclip/harness.json is missing; run 'praetorctl adopt' to reconcile")
	}
	h, err := paperclip.LoadHarness(harnessPath)
	if err != nil {
		return fmt.Errorf("[FAIL] Paperclip harness validation failed: %w", err)
	}
	expectedPlatform := fmt.Sprintf("%s/%s", manifest.Repository.Owner, manifest.Repository.Name)
	if h.Platform != expectedPlatform {
		return fmt.Errorf("[FAIL] Paperclip harness platform mismatch: got %q, expected %q; run 'praetorctl adopt --force' to reconcile", h.Platform, expectedPlatform)
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

func auditAgentDefinitions(rootDir string) error {
	agentsDir := filepath.Join(rootDir, ".agents", "agents")
	if util.DirExists(agentsDir) {
		entries, err := os.ReadDir(agentsDir)
		if err != nil {
			return fmt.Errorf("[FAIL] Failed to inspect .agents/agents: %w", err)
		}
		count := 0
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				count++
			}
		}
		if count == 0 {
			return fmt.Errorf("[FAIL] .agents/agents directory exists but contains zero agent definitions")
		}
		fmt.Printf("[PASS] Agent definitions verified (%d agents registered).\n", count)
	}
	return nil
}

func auditGitHooks(ctx context.Context, rootDir string) error {
	gitDir := filepath.Join(rootDir, ".git")
	if !util.DirExists(gitDir) && !util.FileExists(gitDir) {
		return nil
	}

	lhPath := filepath.Join(rootDir, "lefthook.yml")
	if !util.FileExists(lhPath) {
		return fmt.Errorf("[FAIL] lefthook.yml configuration is missing from repository root")
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
