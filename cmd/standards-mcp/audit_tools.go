package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/mcp"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// maxAuditGates bounds the governance gate loop (HISS-02).
	maxAuditGates = 16
	// maxReportedViolations caps the violations quoted in a failing ratchet report.
	maxReportedViolations = 3
)

// auditPaths carries the resolved input files of one standards_audit call.
type auditPaths struct {
	manifest string
	baseline string
	agents   string
	policy   config.EffectiveOptions
}

// auditGate is one governance check: it returns its [PASS] line, or an error whose
// text is the [FAIL] line.
type auditGate func(ctx context.Context) (string, error)

// runAuditGates executes the MCP governance audit. Unlike the previous implementation
// it runs the HISS invariant scanner against the recorded baseline and checks the
// policy-mandated repository files, so the summary reflects what was verified instead
// of an unconditional compliance claim.
func (s *Server) runAuditGates(ctx context.Context, p auditPaths) *mcp.ToolResult {
	var report strings.Builder

	p.policy.Root, p.policy.ManifestPath, p.policy.Audit = s.rootDir, p.manifest, true
	effective, err := config.LoadEffectivePolicyContext(ctx, p.policy)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("[FAIL] Effective policy audit failed: %v", err))
	}
	manifest := effective.Manifest
	fmt.Fprintf(&report, "=== %s/%s Governance Audit ===\n", manifest.Repository.Owner, manifest.Repository.Name)
	fmt.Fprintf(&report, "[PASS] Manifest verified: %s/%s (Version %d)\n",
		manifest.Repository.Owner, manifest.Repository.Name, manifest.Version)
	fmt.Fprintf(&report, "[PASS] %s\n", effective.Evidence())

	gates := []auditGate{
		func(ctx context.Context) (string, error) {
			return auditLockfile(ctx, s.rootDir, p.policy.CatalogRoot, manifest)
		},
		func(ctx context.Context) (string, error) {
			return auditBaselineRatchetWithPolicy(ctx, s.rootDir, p.baseline, effective.Policy.Complexity)
		},
		func(ctx context.Context) (string, error) { return auditContextSync(ctx, p.agents, s.rootDir) },
		func(ctx context.Context) (string, error) {
			return auditBranchProtection(ctx, manifest, s.rootDir, &effective.Policy)
		},
		func(context.Context) (string, error) { return auditLabelTaxonomy(s.rootDir) },
		func(context.Context) (string, error) { return auditHookConfig(s.rootDir) },
	}

	passed := 0
	for i := 0; i < len(gates) && i < maxAuditGates; i++ {
		if err := ctx.Err(); err != nil {
			return mcp.ErrorResult(fmt.Sprintf("%s[FAIL] Audit cancelled: %v", report.String(), err))
		}
		line, err := gates[i](ctx)
		if err != nil {
			return mcp.ErrorResult(report.String() + err.Error())
		}
		report.WriteString(line + "\n")
		passed++
	}

	fmt.Fprintf(&report, "\nAudit Summary: %d/%d MCP audit gates passed for %s/%s "+
		"(manifest, lockfile pins and digests, HISS ratchet, context sync, branch protection, labels, hooks). "+
		"Run 'praetorctl audit' for the full CLI gate set (paperclip harness, runner matrix, hook activation).",
		passed+1, len(gates)+1, manifest.Repository.Owner, manifest.Repository.Name)
	return mcp.TextResult(report.String())
}

// auditLockfile uses the same version, entry, source and aggregate checks as the CLI,
// against the catalog the effective policy gate resolved, and fails closed without one.
func auditLockfile(ctx context.Context, root, catalogRoot string, manifest *config.Manifest) (string, error) {
	result, err := config.ValidateLockfileWithOptions(ctx, config.LockValidationOptions{
		Root: root, CatalogRoot: catalogRoot, RequireSources: true,
	}, manifest)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %w", err)
	}
	return fmt.Sprintf("[PASS] Lockfile pinned versions and digests verified (%d profiles, %d facets).", result.Profiles, result.Facets), nil
}

// auditBaselineRatchet loads the debt baseline, scans the tree for HISS violations and
// enforces the monotonic ratchet exactly like the CLI audit does.
func auditBaselineRatchet(ctx context.Context, root, baselinePath string) (string, error) {
	return auditBaselineRatchetWithPolicy(ctx, root, baselinePath, config.HISSComplexityCeiling())
}

// auditBaselineRatchetWithPolicy scans under the policy's function-length limit, which the
// ratchet enforces, and its complexity limits, whose measurements follow the verdict line as
// report-only lines.
func auditBaselineRatchetWithPolicy(ctx context.Context, root, baselinePath string, policy config.ComplexityPolicy) (string, error) {
	base, err := baseline.LoadBaseline(baselinePath)
	if err != nil {
		return "", fmt.Errorf("[FAIL] Baseline audit failed: %w", err)
	}

	scanRep, err := hiss.Scan(ctx, root, policy.ScanOptions(hiss.ScanOptions{}))
	if err != nil {
		return "", fmt.Errorf("[FAIL] Invariant audit failed: %w", err)
	}

	if scanRep.Truncated {
		return "", fmt.Errorf("[FAIL] Invariant audit failed: %w", hiss.ErrScanTruncated)
	}

	current := hiss.ConvertToBaseline(scanRep.Violations)
	for i := range current {
		current[i].Fingerprint = fmt.Sprintf("%s:%d:%s", current[i].FilePath, current[i].LineNumber, current[i].RuleID)
	}

	ratchet := baseline.EvaluateRatchet(base, current, nil)
	if !ratchet.Passed {
		return "", fmt.Errorf("[FAIL] HISS invariant violations introduced (%d total infractions, %d new unbaselined violations):\n%s",
			ratchet.CurrentCount, len(ratchet.NewViolations), formatViolations(ratchet.NewViolations))
	}
	verdict := fmt.Sprintf("[PASS] Technical debt baseline verified: %d recorded legacy infractions; HISS scan found %d active violations within the baselined limit. %s",
		base.TotalInfractions, ratchet.CurrentCount, scanRep.CoverageEvidence())
	return strings.Join(append([]string{verdict}, scanRep.Complexity.Lines()...), "\n"), nil
}

// formatViolations renders up to maxReportedViolations infractions for a failure line.
func formatViolations(violations []baseline.Infraction) string {
	limit := len(violations)
	if limit > maxReportedViolations {
		limit = maxReportedViolations
	}
	lines := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		v := violations[i]
		lines = append(lines, fmt.Sprintf("  [%s] %s:%d - %s", v.RuleID, v.FilePath, v.LineNumber, v.Message))
	}
	return strings.Join(lines, "\n")
}

// auditContextSync verifies the compiled vendor targets match the canonical AGENTS.md.
func auditContextSync(ctx context.Context, agentsPath, root string) (string, error) {
	tr := compiler.NewTranspiler()
	if _, err := compiler.SyncRegisterBlock(ctx, root, agentsPath, false); err != nil {
		return "", fmt.Errorf("[FAIL] Agent context text register: %w", err)
	}
	if err := tr.VerifyContext(ctx, agentsPath, root); err != nil {
		return "", fmt.Errorf("[FAIL] Agent context targets out of sync: %w", err)
	}
	lint, err := compiler.LintContext(ctx, agentsPath)
	if err != nil {
		return "", fmt.Errorf("[FAIL] Agent context: %w", err)
	}
	return "[PASS] Cross-agent context targets verified in sync.\n[PASS] Agent context " + lint.Summary() + ".", nil
}

// auditBranchProtection delegates branch protection ruleset audit to the shared authority
// in internal/adopt, against policy: the effective policy the audit resolved, the one adopt
// rendered the ruleset from.
func auditBranchProtection(ctx context.Context, manifest *config.Manifest, root string, policy *config.ResolvedPolicy) (string, error) {
	return adopt.AuditBranchProtectionWithPolicy(ctx, manifest, root, policy)
}

// auditLabelTaxonomy requires the repository label taxonomy.
func auditLabelTaxonomy(root string) (string, error) {
	if !util.FileExists(filepath.Join(root, ".config", "labels.yaml")) {
		return "", fmt.Errorf("[FAIL] Required label taxonomy .config/labels.yaml is missing")
	}
	return "[PASS] Repository label taxonomy .config/labels.yaml verified.", nil
}

// auditHookConfig requires lefthook.yml in git repositories; hook activation itself is
// a workstation concern verified by the CLI audit.
func auditHookConfig(root string) (string, error) {
	if !util.PathExists(filepath.Join(root, ".git")) {
		return "[PASS] Not a git checkout: hook configuration gate skipped.", nil
	}
	if !util.FileExists(filepath.Join(root, "lefthook.yml")) {
		return "", fmt.Errorf("[FAIL] lefthook.yml configuration is missing from repository root")
	}
	return "[PASS] Git hook configuration lefthook.yml verified.", nil
}
