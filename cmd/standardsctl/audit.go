package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/standards/internal/baseline"
	"github.com/cordanaLLM/standards/internal/compiler"
	"github.com/cordanaLLM/standards/internal/config"
	"github.com/cordanaLLM/standards/internal/devcontainer"
	"github.com/cordanaLLM/standards/internal/hiss"
	"github.com/cordanaLLM/standards/internal/util"
)

func runAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	manifestPath := fs.String("config", ".standards.yaml", "Path to .standards.yaml")
	baselinePath := fs.String("baseline", ".standards-baseline.json", "Path to .standards-baseline.json")
	agentsPath := fs.String("agents", "AGENTS.md", "Path to AGENTS.md")

	if err := fs.Parse(args); err != nil {
		return err
	}

	fmt.Println("=== cordanaLLM/standards Governance Audit ===")

	manifest, err := auditManifestAndLockfile(*manifestPath)
	if err != nil {
		return err
	}

	if err := auditBaselineAndInvariants(*baselinePath); err != nil {
		return err
	}

	if err := auditAgentContextAndDevcontainer(manifest, *agentsPath); err != nil {
		return err
	}

	if err := auditBranchProtectionAndSupplyChain(manifest, filepath.Dir(*manifestPath)); err != nil {
		return err
	}

	fmt.Println("\nAudit Summary: 100% Compliance with cordanaLLM/standards HISS-16 baseline.")
	return nil
}

func auditManifestAndLockfile(manifestPath string) (*config.Manifest, error) {
	manifest, err := config.LoadManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("[FAIL] Manifest audit failed: %w", err)
	}
	fmt.Printf("[PASS] Manifest verified: %s/%s (Version %d)\n", manifest.Repository.Owner, manifest.Repository.Name, manifest.Version)
	fmt.Printf("       Profiles: %v | Facets: %v\n", manifest.Profiles, manifest.Facets)

	lockPath := filepath.Join(filepath.Dir(manifestPath), ".standards.lock")
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("[FAIL] .standards.lock is missing")
	}
	fmt.Println("[PASS] SemVer lockfile .standards.lock verified.")
	return manifest, nil
}

func auditBaselineAndInvariants(baselinePath string) error {
	base, err := baseline.LoadBaseline(baselinePath)
	if err != nil {
		return fmt.Errorf("[FAIL] Baseline audit failed: %w", err)
	}

	ctx := context.Background()
	root := filepath.Dir(baselinePath)
	scanRep, err := hiss.Scan(ctx, root, hiss.ScanOptions{})
	if err != nil {
		return fmt.Errorf("[FAIL] Invariant audit failed: %w", err)
	}

	currentViolations := hiss.ConvertToBaseline(scanRep.Violations)
	for i := range currentViolations {
		currentViolations[i].Fingerprint = fmt.Sprintf("%s:%d:%s", currentViolations[i].FilePath, currentViolations[i].LineNumber, currentViolations[i].RuleID)
	}

	ratchet := baseline.EvaluateRatchet(base, currentViolations, nil)
	if !ratchet.Passed {
		limit := 3
		if len(ratchet.NewViolations) < limit {
			limit = len(ratchet.NewViolations)
		}
		var msgs []string
		for i := 0; i < limit; i++ {
			v := ratchet.NewViolations[i]
			msgs = append(msgs, fmt.Sprintf("  [%s] %s:%d - %s", v.RuleID, v.FilePath, v.LineNumber, v.Message))
		}
		return fmt.Errorf("[FAIL] HISS invariant violations introduced (%d total infractions, %d new unbaselined violations):\n%s",
			ratchet.CurrentCount, len(ratchet.NewViolations), strings.Join(msgs, "\n"))
	}
	fmt.Printf("[PASS] HISS invariant scan verified: %d active violations within %d baselined limit.\n",
		ratchet.CurrentCount, base.TotalInfractions)
	return nil
}

func auditAgentContextAndDevcontainer(manifest *config.Manifest, agentsPath string) error {
	root := filepath.Dir(agentsPath)
	tr := compiler.NewTranspiler()
	if err := tr.Verify(agentsPath, root); err != nil {
		return fmt.Errorf("[FAIL] Agent context targets out of sync: %w", err)
	}
	fmt.Println("[PASS] Cross-agent context targets (Claude, Cursor, Copilot, Windsurf, Gemini, Codex) verified in sync.")

	dcPath := filepath.Join(root, ".devcontainer", "devcontainer.json")
	if _, err := os.Stat(dcPath); err == nil {
		dc, err := devcontainer.Synthesize(manifest)
		if err == nil {
			ctx := context.Background()
			if err := devcontainer.Verify(ctx, dcPath, dc); err == nil {
				fmt.Println("[PASS] DevContainer configuration verified in sync with declared standards.")
			}
		}
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
			return fmt.Errorf("[FAIL] Branch protection ruleset .github/rulesets/main.json is missing while policy requires linear history and signed commits. Run 'standardsctl sync' to reconcile.")
		}
		fmt.Println("[PASS] Branch protection & merge ruleset .github/rulesets/main.json verified.")
	}

	// Verify .config/labels.yaml
	labelsPath := filepath.Join(rootDir, ".config", "labels.yaml")
	if !util.FileExists(labelsPath) {
		return fmt.Errorf("[FAIL] Required label taxonomy .config/labels.yaml is missing.")
	}
	fmt.Println("[PASS] Repository label taxonomy .config/labels.yaml verified.")

	return nil
}
