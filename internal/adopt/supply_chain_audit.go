package adopt

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// supplyChainGate names the gate on every line it prints.
const supplyChainGate = "Supply chain (HISS-11)"

// supplyChainScope is what every verdict states about the evidence behind it: the workflow
// files, never what a release published.
const supplyChainScope = "Measured from the workflow files under .github/workflows; published attestations and signatures were not checked."

// AuditSupplyChain is the HISS-11 supply-chain gate the CLI and MCP audits share. The SLSA Build
// level, cosign signing and SBOM generation policy declares are compared with what the
// repository's workflow files can produce (forge.MeasureProvenance, forge.SBOMWorkflow), and a
// declaration above the measurement fails, naming both. The audit used to read none of
// policy.SupplyChain, so a declared Level 3 with Level 2 or no provenance passed (#330).
//
// The measurement reads files and no forge: a pass says so, because a workflow that can
// attest is not proof that a published release carries a valid attestation.
func AuditSupplyChain(ctx context.Context, rootDir string, policy *config.ResolvedPolicy) (string, error) {
	if ctx == nil || policy == nil {
		return "", errors.New("[FAIL] " + supplyChainGate + ": the audit requires a context and a resolved policy")
	}
	declared := policy.SupplyChain
	if declared.SLSALevel <= 0 && !declared.EnforceCosign && !declared.RequireSBOM {
		return "[PASS] " + supplyChainGate + ": policy declares SLSA Build Level 0, no cosign signing and no SBOM; no workflow is required.", nil
	}
	measured, err := forge.MeasureProvenance(ctx, rootDir)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: cannot measure the workflows: %w", supplyChainGate, err)
	}
	sbom := ""
	if declared.RequireSBOM {
		if sbom, err = forge.SBOMWorkflow(ctx, rootDir); err != nil {
			return "", fmt.Errorf("[FAIL] %s: cannot inspect the SBOM workflows: %w", supplyChainGate, err)
		}
	}
	if failures := supplyChainFailures(declared, measured, sbom); len(failures) > 0 {
		return "", errors.New(strings.Join(failures, "\n"))
	}
	return supplyChainPass(declared, measured, sbom), nil
}

// supplyChainFailures lists one [FAIL] line for each declaration the measurement falls short of.
func supplyChainFailures(declared config.SupplyChainPolicy, measured forge.ProvenanceMeasurement, sbom string) []string {
	var failures []string
	if declared.SLSALevel > measured.Level {
		failures = append(failures, slsaLevelFailure(declared.SLSALevel, measured))
	}
	if declared.EnforceCosign && measured.CosignWorkflow == "" {
		failures = append(failures, "[FAIL] "+supplyChainGate+": policy sets enforce_cosign but no workflow signs with cosign "+
			"(a cosign sign, sign-blob, attest or attest-blob step, or a GoReleaser release whose signs, binary_signs or "+
			"docker_signs block runs cosign).")
	}
	if declared.RequireSBOM && sbom == "" {
		failures = append(failures, "[FAIL] "+supplyChainGate+": policy sets require_sbom but no workflow generates an SBOM "+
			"(add an SBOM step to the release workflow or a dedicated sbom.yml).")
	}
	return failures
}

// slsaLevelFailure names the declared level, the measured one and the workflow it was read
// from, what the next level needs, and every reusable workflow call no level was credited for.
func slsaLevelFailure(declared int, measured forge.ProvenanceMeasurement) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[FAIL] %s: policy declares SLSA Build Level %d but the workflows reach Level %d", supplyChainGate, declared, measured.Level)
	if measured.LevelWorkflow != "" {
		fmt.Fprintf(&b, " (.github/workflows/%s)", measured.LevelWorkflow)
	}
	b.WriteString(". " + slsaLevelRemedy(declared))
	if len(measured.Uncredited) > 0 {
		b.WriteString(" Not credited: " + strings.Join(measured.Uncredited, "; ") + ".")
	}
	return b.String()
}

// slsaLevelRemedy says what the workflows lack for the declared level.
func slsaLevelRemedy(declared int) string {
	switch {
	case declared >= forge.SLSABuildL3:
		return "Level 3 needs the build and its provenance in an isolated reusable workflow: the SLSA GitHub generator " +
			"(slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@<vX.Y.Z>), or a reusable " +
			"workflow of this repository whose job builds the artefacts and then runs actions/attest-build-provenance; an " +
			"attestation signed in the caller's build job, or over an artefact downloaded from another job, is Level 2."
	case declared == forge.SLSABuildL2:
		return "Level 2 needs provenance signed on the hosted runner, such as actions/attest-build-provenance in the release job."
	default:
		return "Level 1 needs a workflow that writes a provenance statement for the released artefacts."
	}
}

// supplyChainPass is the [PASS] line: each declaration with the workflow that meets it, and the
// scope of the measurement.
func supplyChainPass(declared config.SupplyChainPolicy, measured forge.ProvenanceMeasurement, sbom string) string {
	parts := []string{fmt.Sprintf("SLSA Build Level %d declared, Level %d measured", declared.SLSALevel, measured.Level)}
	if measured.LevelWorkflow != "" {
		parts[0] += " from " + measured.LevelWorkflow
	}
	if declared.EnforceCosign {
		parts = append(parts, "cosign signing in "+measured.CosignWorkflow)
	}
	if declared.RequireSBOM {
		parts = append(parts, "SBOM generated in "+sbom)
	}
	return "[PASS] " + supplyChainGate + ": " + strings.Join(parts, "; ") + ". " + supplyChainScope
}
