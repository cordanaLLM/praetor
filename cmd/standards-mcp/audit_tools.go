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
	var report mcpTextBuilder

	p.policy.Root, p.policy.ManifestPath, p.policy.Audit = s.rootDir, p.manifest, true
	effective, err := config.LoadEffectivePolicyContext(ctx, p.policy)
	if err != nil {
		return mcp.ErrorResult(fmt.Sprintf("[FAIL] Effective policy audit failed: %v", err))
	}
	manifest := effective.Manifest
	report.Template("audit: %s/%s governance.\nmanifest: pass; repository: %s/%s; version: %d.\npolicy: pass; evidence: %s.\n",
		manifest.Repository.Owner, manifest.Repository.Name, manifest.Repository.Owner,
		manifest.Repository.Name, manifest.Version, effective.Evidence())

	gates := []auditGate{
		func(ctx context.Context) (string, error) {
			return auditLockfile(ctx, s.rootDir, p.policy.CatalogRoot, manifest)
		},
		func(ctx context.Context) (string, error) {
			return auditBaselineRatchetWithPolicy(ctx, s.rootDir, p.baseline, effective)
		},
		func(ctx context.Context) (string, error) { return auditContextSync(ctx, manifest, p.agents, s.rootDir) },
		func(ctx context.Context) (string, error) {
			return auditBranchProtection(ctx, manifest, s.rootDir, &effective.Policy)
		},
		func(context.Context) (string, error) { return adopt.AuditLabelTaxonomy(manifest, s.rootDir) },
		func(context.Context) (string, error) { return auditHookConfig(manifest, s.rootDir) },
	}

	passed := 0
	for i := 0; i < len(gates) && i < maxAuditGates; i++ {
		if err := ctx.Err(); err != nil {
			report.Template("[FAIL] Audit cancelled: %v", err)
			return mcpComposedErrorResult(report.Text())
		}
		line, err := gates[i](ctx)
		if err != nil {
			// A gate error carries its own [FAIL] verdict and quotes repository paths.
			report.External(err.Error(), mcpTextUntrusted)
			return mcpComposedErrorResult(report.Text())
		}
		report.External(line+"\n", mcpTextUntrusted)
		passed++
	}

	report.Template("\nsummary: MCP audit gates; passed: %d/%d; repository: %s/%s; coverage: manifest, lockfile pins and digests, HISS ratchet, context sync, branch protection, labels, hooks. "+
		"next: run 'praetorctl audit' for full CLI gate set: paperclip harness, runner matrix, hook activation.",
		passed+1, len(gates)+1, manifest.Repository.Owner, manifest.Repository.Name)
	return mcpComposedTextResult(report.Text())
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
	return auditBaselineRatchetWithPolicy(ctx, root, baselinePath, nil)
}

// auditBaselineRatchetWithPolicy scans under the policy's function-length limit, which the
// ratchet enforces, its complexity limits, whose measurements follow the verdict line as
// report-only lines, and the HISS exceptions its manifest declares and documents
// (config.EffectivePolicy.HISSScanOptions), as the CLI audit does. A nil policy scans under the
// audit ceiling and no exception.
func auditBaselineRatchetWithPolicy(ctx context.Context, root, baselinePath string, policy *config.EffectivePolicy) (string, error) {
	base, err := baseline.LoadBaseline(baselinePath)
	if err != nil {
		return "", fmt.Errorf("[FAIL] Baseline audit failed: %w", err)
	}

	scanOpts, warning := policy.HISSScanOptions(root, hiss.ScanOptions{})
	scanRep, err := hiss.Scan(ctx, root, scanOpts)
	if err != nil {
		return "", fmt.Errorf("[FAIL] Invariant audit failed: %w", err)
	}

	if scanRep.Truncated {
		return "", fmt.Errorf("[FAIL] Invariant audit failed: %w", hiss.ErrScanTruncated)
	}

	current := hiss.ConvertToBaseline(scanRep.Violations)

	ratchet := baseline.EvaluateRatchet(base, current, nil)
	if !ratchet.Passed {
		// Attributed as the CLI audit attributes it, so a finding in code unchanged since the
		// baseline's commit is not reported as introduced (#599).
		hiss.AttributeRatchet(ctx, root, baselinePath, scanOpts, base, current, ratchet)
		return "", fmt.Errorf("[FAIL] %s", ratchet.Summary())
	}
	verdict := fmt.Sprintf("[PASS] Technical debt baseline verified: %d recorded legacy infractions; HISS scan found %d active violations within the baselined limit. %s",
		base.TotalInfractions, ratchet.CurrentCount, scanRep.CoverageEvidence())
	lines := append([]string{verdict}, scanRep.Complexity.Lines()...)
	if warning != "" {
		lines = append(lines, "[WARN] "+warning)
	}
	return strings.Join(lines, "\n"), nil
}

// auditContextSync verifies the compiled vendor targets match the canonical AGENTS.md. A
// declined agent-harness step does not cover these checks (adopt.DeclineAuditRetained): a
// failure says so, and a pass names the decline, as the CLI audit does (#600).
func auditContextSync(ctx context.Context, manifest *config.Manifest, agentsPath, root string) (string, error) {
	harness, err := adopt.AuditDecline(manifest, "agent-harness")
	if err != nil {
		return "", fmt.Errorf("[FAIL] Agent context audit failed: %w", err)
	}
	if _, err := compiler.SyncRegisterBlock(ctx, root, agentsPath, false); err != nil {
		return "", fmt.Errorf("[FAIL] Agent context text register: %w", harness.Narrow(err))
	}
	if err := compiler.NewTranspiler().VerifyContext(ctx, agentsPath, root); err != nil {
		return "", fmt.Errorf("[FAIL] Agent context targets out of sync: %w", harness.Narrow(err))
	}
	lint, err := compiler.LintContext(ctx, agentsPath)
	if err != nil {
		return "", fmt.Errorf("[FAIL] Agent context: %w", harness.Narrow(err))
	}
	// The CLI audit runs the same check: whatever facets the manifest enables, the block sends
	// agent evidence to the evidence directory in every repository.
	if err := compiler.CheckEvidenceIgnored(ctx, filepath.Dir(agentsPath)); err != nil {
		return "", fmt.Errorf("[FAIL] Agent context evidence directory: %w", err)
	}
	line := "[PASS] Cross-agent context targets verified in sync.\n[PASS] Agent context " + lint.Summary() + "."
	if harness.Declined {
		line += "\n" + harness.Line("Agent harness")
	}
	return line, nil
}

// auditBranchProtection delegates branch protection ruleset audit to the shared authority
// in internal/adopt, against policy: the effective policy the audit resolved, the one adopt
// rendered the ruleset from.
func auditBranchProtection(ctx context.Context, manifest *config.Manifest, root string, policy *config.ResolvedPolicy) (string, error) {
	return adopt.AuditBranchProtectionWithPolicy(ctx, manifest, root, policy)
}

// auditHookConfig requires lefthook.yml in git repositories unless adoption.decline lists
// git-hooks (adopt.AuditGitHookConfig, the gate the CLI audit shares); hook activation itself is
// a workstation concern verified by the CLI audit.
func auditHookConfig(manifest *config.Manifest, root string) (string, error) {
	if !util.PathExists(filepath.Join(root, ".git")) {
		return "[PASS] Not a git checkout: hook configuration gate skipped.", nil
	}
	line, _, err := adopt.AuditGitHookConfig(manifest, root)
	return line, err
}
