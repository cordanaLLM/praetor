package adopt

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

// planBaselineFiles are the companion files every governed repository carries.
var planBaselineFiles = [...]string{".standards.lock", "AGENTS.md", ".config/labels.yaml"}

// SBOMWorkflowDrift is the drift line a plan reports when policy requires an SBOM and no
// workflow generates one.
const SBOMWorkflowDrift = ".github/workflows (no workflow generates an SBOM: add an SBOM step to the release workflow or a dedicated sbom.yml)"

// PlanDrift reports the baseline files missing under root and the policy-mandated files or
// workflow steps whose absence contradicts the resolved policy.
//
// The CLI plan and the MCP standards_plan tool both report this. They used to carry one
// copy each, and both copies accepted only a file named .github/workflows/sbom.yml, so a
// repository that catalogued its release archives inside the release workflow was told it
// had no SBOM at all (#43). One implementation keeps the two surfaces from disagreeing
// (HISS-19).
func PlanDrift(ctx context.Context, root string, policy *config.ResolvedPolicy) (missing, drift []string, err error) {
	if ctx == nil {
		return nil, nil, errors.New("plan drift requires a context")
	}
	if policy == nil {
		return nil, nil, errors.New("plan drift requires a resolved policy")
	}
	for i := 0; i < len(planBaselineFiles); i++ {
		if !util.FileExists(filepath.Join(root, filepath.FromSlash(planBaselineFiles[i]))) {
			missing = append(missing, planBaselineFiles[i])
		}
	}
	if RulesetRequired(policy.BranchProtection) {
		if !util.FileExists(filepath.Join(root, filepath.FromSlash(rulesetFile))) {
			drift = append(drift, rulesetFile+" (Branch protection ruleset missing)")
		}
	}
	if policy.SupplyChain.RequireSBOM {
		workflow, err := forge.SBOMWorkflow(ctx, root)
		if err != nil {
			return nil, nil, fmt.Errorf("inspect SBOM workflows: %w", err)
		}
		if workflow == "" {
			drift = append(drift, SBOMWorkflowDrift)
		}
	}
	return missing, drift, nil
}

// PlanLiveNotCompared is the line a plan ends its status with when it did not read the branch
// protection the forge enforces. PlanDrift reads local files only, so such a plan never says that
// no change is required (#159).
const PlanLiveNotCompared = "[INFO] Live branch protection not compared with the forge: this plan read local files only. " +
	"Run 'praetorctl plan --remote' to compare what GitHub enforces with the declared policy."

// FormatPlanStatus renders the drift verdict of a reconcile plan: the missing baseline files and
// drift lines PlanDrift reported, or that the local files match the declared policy when there
// are none. Unless liveCompared, it ends with PlanLiveNotCompared; the CLI plan --remote compares
// the live branch protection after it and reports that verdict itself.
func FormatPlanStatus(missing, drift []string, liveCompared bool) string {
	status := formatLocalPlanStatus(missing, drift)
	if !liveCompared {
		status += "\n" + PlanLiveNotCompared
	}
	return status
}

// formatLocalPlanStatus renders the verdict of the local files alone.
func formatLocalPlanStatus(missing, drift []string) string {
	if len(missing) == 0 && len(drift) == 0 {
		return "\nStatus: Local files match the declared policy."
	}
	var b strings.Builder
	if len(missing) > 0 {
		fmt.Fprintf(&b, "\n[DRIFT] Missing baseline files: %s\n", strings.Join(missing, ", "))
	}
	if len(drift) > 0 {
		b.WriteString("\n[DRIFT] Policy drift detected:\n")
		for i := 0; i < len(drift); i++ {
			fmt.Fprintf(&b, "  - %s\n", drift[i])
		}
	}
	b.WriteString("\nAction: Run 'praetorctl sync' to reconcile repository configuration.")
	return b.String()
}
