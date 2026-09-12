package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

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
	report.WriteString("=== cordanaLLM/praetor Governance Audit ===\n")

	manifest, err := config.LoadManifest(p.manifest)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("[FAIL] Manifest audit failed: %v", err))
	}
	fmt.Fprintf(&report, "[PASS] Manifest verified: %s/%s (Version %d)\n",
		manifest.Repository.Owner, manifest.Repository.Name, manifest.Version)

	gates := []auditGate{
		func(ctx context.Context) (string, error) { return auditLockfile(ctx, s.rootDir, manifest) },
		func(ctx context.Context) (string, error) { return auditBaselineRatchet(ctx, s.rootDir, p.baseline) },
		func(context.Context) (string, error) { return auditContextSync(p.agents, s.rootDir) },
		func(context.Context) (string, error) { return auditBranchProtection(manifest, s.rootDir) },
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

// auditLockfile uses the same version, entry, source and aggregate checks as the CLI.
func auditLockfile(ctx context.Context, root string, manifest *config.Manifest) (string, error) {
	result, err := config.ValidateLockfile(ctx, root, manifest)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %w", err)
	}
	return fmt.Sprintf("[PASS] Lockfile pinned versions and digests verified (%d profiles, %d facets).", result.Profiles, result.Facets), nil
}

// auditBaselineRatchet loads the debt baseline, scans the tree for HISS violations and
// enforces the monotonic ratchet exactly like the CLI audit does.
func auditBaselineRatchet(ctx context.Context, root, baselinePath string) (string, error) {
	base, err := baseline.LoadBaseline(baselinePath)
	if err != nil {
		return "", fmt.Errorf("[FAIL] Baseline audit failed: %w", err)
	}

	scanRep, err := hiss.Scan(ctx, root, hiss.ScanOptions{})
	if err != nil {
		return "", fmt.Errorf("[FAIL] Invariant audit failed: %w", err)
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
	return fmt.Sprintf("[PASS] Technical debt baseline verified: %d recorded legacy infractions; HISS scan found %d active violations within the baselined limit.",
		base.TotalInfractions, ratchet.CurrentCount), nil
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
func auditContextSync(agentsPath, root string) (string, error) {
	tr := compiler.NewTranspiler()
	if err := tr.Verify(agentsPath, root); err != nil {
		return "", fmt.Errorf("[FAIL] Agent context targets out of sync: %w", err)
	}
	return "[PASS] Cross-agent context targets verified in sync.", nil
}

// auditBranchProtection requires the ruleset file whenever the resolved policy demands
// linear history or signed commits.
func auditBranchProtection(manifest *config.Manifest, root string) (string, error) {
	policy := config.DefaultPolicy()
	policy.ApplyOverrides(manifest.Overrides)
	if !policy.BranchProtection.EnforceLinearHistory && !policy.BranchProtection.RequireSignedCommits {
		return "[PASS] Branch protection ruleset not required by policy.", nil
	}
	if !util.FileExists(filepath.Join(root, ".github", "rulesets", "main.json")) {
		return "", fmt.Errorf("[FAIL] Branch protection ruleset .github/rulesets/main.json is missing while policy requires linear history or signed commits")
	}
	return "[PASS] Branch protection & merge ruleset .github/rulesets/main.json verified.", nil
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

// planDrift reports the baseline files missing under root and the policy-mandated files
// whose absence contradicts the resolved policy. It is the root-aware twin of the CLI
// plan's drift check, which resolves paths against the working directory.
func planDrift(root string, policy *config.ResolvedPolicy) (missing, drift []string) {
	for _, rel := range []string{".standards.lock", "AGENTS.md", filepath.Join(".config", "labels.yaml")} {
		if !util.FileExists(filepath.Join(root, rel)) {
			missing = append(missing, filepath.ToSlash(rel))
		}
	}

	if policy.BranchProtection.EnforceLinearHistory || policy.BranchProtection.RequireSignedCommits {
		if !util.FileExists(filepath.Join(root, ".github", "rulesets", "main.json")) {
			drift = append(drift, ".github/rulesets/main.json (Branch protection ruleset missing)")
		}
	}
	if policy.SupplyChain.RequireSBOM && !util.FileExists(filepath.Join(root, ".github", "workflows", "sbom.yml")) {
		drift = append(drift, ".github/workflows/sbom.yml (SBOM & SLSA Level 3 workflow missing)")
	}
	return missing, drift
}

// writePlanStatus appends the drift verdict of a reconcile plan to b.
func writePlanStatus(b *strings.Builder, missing, drift []string) {
	if len(missing) == 0 && len(drift) == 0 {
		b.WriteString("\nStatus: Local state matches declared policy. No changes required.")
		return
	}
	if len(missing) > 0 {
		fmt.Fprintf(b, "\n[DRIFT] Missing baseline files: %s\n", strings.Join(missing, ", "))
	}
	if len(drift) > 0 {
		b.WriteString("\n[DRIFT] Policy drift detected:\n")
		for _, d := range drift {
			fmt.Fprintf(b, "  - %s\n", d)
		}
	}
	b.WriteString("\nAction: Run 'praetorctl sync' to reconcile repository configuration.")
}
