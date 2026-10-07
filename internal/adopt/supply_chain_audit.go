package adopt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// supplyChainGate names the gate on every line it prints.
const supplyChainGate = "Supply chain (HISS-11)"

// supplyChainScope is what every verdict states about the evidence behind it: the workflow
// files, never what a release published.
const supplyChainScope = "Measured from the workflow files under .github/workflows; published attestations and signatures were not checked."

// supplyChainGuide is the document that says how a release workflow reaches each level.
const supplyChainGuide = "docs/guides/releasing.md"

// SupplyChainOptions is what AuditSupplyChain reads: the repository root, the resolved policy,
// the manifest's exceptions list (the gate reads the entries of config.ExceptionRuleSupplyChain)
// and the day those entries' expiry is judged against.
type SupplyChainOptions struct {
	Root       string
	Policy     *config.ResolvedPolicy
	Exceptions []config.Exception
	Today      time.Time
}

// AuditSupplyChain is the HISS-11 supply-chain gate the CLI and MCP audits share. The SLSA Build
// level, cosign signing and SBOM generation policy declares are compared with what the
// repository's workflow files can produce (forge.MeasureProvenance, forge.SBOMWorkflow), and a
// declaration above the measurement fails, naming both. The audit used to read none of
// policy.SupplyChain, so a declared Level 3 with Level 2 or no provenance passed (#330).
//
// A live entry of the exceptions list with rule HISS-11 that names the release workflow the
// measurement read declares the gap instead: the gate prints the declared and measured values
// with the entry's reason and expiry and passes. An expired entry fails like a missing one, and
// an entry with no gap to excuse is stale and fails until it is removed (AGENTS.md rule 14).
//
// The measurement reads files and no forge: a pass says so, because a workflow that can
// attest is not proof that a published release carries a valid attestation.
func AuditSupplyChain(ctx context.Context, opts SupplyChainOptions) (string, error) {
	if ctx == nil || opts.Policy == nil {
		return "", errors.New("[FAIL] " + supplyChainGate + ": the audit requires a context and a resolved policy")
	}
	entries := config.ExceptionsFor(opts.Exceptions, config.ExceptionRuleSupplyChain)
	if err := config.ValidateExceptions(entries, opts.Today); err != nil {
		return "", fmt.Errorf("[FAIL] %s: %w", supplyChainGate, err)
	}
	declared := opts.Policy.SupplyChain
	if !declaresSupplyChain(declared) {
		if len(entries) > 0 {
			return "", errors.New(staleSupplyChainEntries(entries, "the policy declares no supply-chain control"))
		}
		return "[PASS] " + supplyChainGate + ": policy declares SLSA Build Level 0, no cosign signing and no SBOM; no workflow is required.", nil
	}
	gap, err := measureSupplyChainGap(ctx, opts.Root, declared)
	if err != nil {
		return "", err
	}
	if len(gap.Shortfalls) == 0 {
		if len(entries) > 0 {
			return "", errors.New(staleSupplyChainEntries(entries, "the workflows meet every declaration"))
		}
		return supplyChainPass(declared, gap), nil
	}
	return judgeSupplyChainGap(gap, entries, opts.Today)
}

// declaresSupplyChain reports whether declared asks for any supply-chain control: a level above
// 0, cosign signing or an SBOM. One that asks for none reads no workflow.
func declaresSupplyChain(declared config.SupplyChainPolicy) bool {
	return declared.SLSALevel > 0 || declared.EnforceCosign || declared.RequireSBOM
}

// supplyChainGap is what the workflow files of a repository measure against the supply chain
// its policy declares: the measurement, the first workflow that generates an SBOM ("" when none
// does or none is required), and each declaration the measurement falls short of.
type supplyChainGap struct {
	Measured   forge.ProvenanceMeasurement
	SBOM       string
	Shortfalls []supplyChainShortfall
}

// supplyChainShortfall is one declaration the measurement falls short of: summary names it in a
// few words, detail with both values and what the workflows lack.
type supplyChainShortfall struct {
	summary, detail string
}

// measureSupplyChainGap measures the workflow files under root against declared: the audit
// judges the result, and adoption records an exception for the gap a fresh repository has
// (supplyChainException), so both read one measurement (HISS-19).
func measureSupplyChainGap(ctx context.Context, root string, declared config.SupplyChainPolicy) (supplyChainGap, error) {
	measured, err := forge.MeasureProvenance(ctx, root)
	if err != nil {
		return supplyChainGap{}, fmt.Errorf("[FAIL] %s: cannot measure the workflows: %w", supplyChainGate, err)
	}
	gap := supplyChainGap{Measured: measured}
	if declared.RequireSBOM {
		if gap.SBOM, err = forge.SBOMWorkflow(ctx, root); err != nil {
			return supplyChainGap{}, fmt.Errorf("[FAIL] %s: cannot inspect the SBOM workflows: %w", supplyChainGate, err)
		}
	}
	gap.Shortfalls = supplyChainShortfalls(declared, gap.Measured, gap.SBOM)
	return gap, nil
}

// releaseWorkflow is the workflow a HISS-11 exceptions entry names for this gap: the one that
// reached the measured level, or, when no workflow writes provenance, the release workflow still
// to be added, .github/workflows/release.yml.
func (g supplyChainGap) releaseWorkflow() string {
	if g.Measured.LevelWorkflow != "" {
		return ghworkflow.Dir + "/" + g.Measured.LevelWorkflow
	}
	return ghworkflow.Dir + "/release.yml"
}

// excuses reports whether entry names this gap's release workflow. With no workflow writing
// provenance there is none to name, so any HISS-11 entry, whose path the validator keeps
// inside .github/workflows, declares the gap.
func (g supplyChainGap) excuses(entry config.Exception) bool {
	return g.Measured.LevelWorkflow == "" || entry.Matches(g.releaseWorkflow())
}

// summary names every shortfall in a few words, for an exceptions entry's reason.
func (g supplyChainGap) summary() string {
	parts := make([]string, 0, len(g.Shortfalls))
	for index := 0; index < len(g.Shortfalls); index++ {
		parts = append(parts, g.Shortfalls[index].summary)
	}
	return strings.Join(parts, "; ")
}

// supplyChainShortfalls lists each declaration the measurement falls short of.
func supplyChainShortfalls(declared config.SupplyChainPolicy, measured forge.ProvenanceMeasurement, sbom string) []supplyChainShortfall {
	var shortfalls []supplyChainShortfall
	if declared.SLSALevel > measured.Level {
		shortfalls = append(shortfalls, supplyChainShortfall{
			summary: fmt.Sprintf("SLSA Build Level %d declared, Level %d measured", declared.SLSALevel, measured.Level),
			detail:  slsaLevelShortfall(declared.SLSALevel, measured),
		})
	}
	if declared.EnforceCosign && measured.CosignWorkflow == "" {
		shortfalls = append(shortfalls, supplyChainShortfall{summary: "enforce_cosign without a cosign signing step",
			detail: "policy sets enforce_cosign but no workflow signs with cosign " +
				"(a cosign sign, sign-blob, attest or attest-blob step, or a GoReleaser release whose signs, binary_signs or " +
				"docker_signs block runs cosign)."})
	}
	if declared.RequireSBOM && sbom == "" {
		shortfalls = append(shortfalls, supplyChainShortfall{summary: "require_sbom without an SBOM step",
			detail: "policy sets require_sbom but no workflow generates an SBOM " +
				"(add an SBOM step to the release workflow or a dedicated sbom.yml)."})
	}
	return shortfalls
}

// judgeSupplyChainGap passes a gap a live entry declares and fails any other: one [FAIL] line
// per shortfall, then why no entry declares it. An entry that names another workflow than the
// one the measurement read is stale and fails either way.
func judgeSupplyChainGap(gap supplyChainGap, entries []config.Exception, today time.Time) (string, error) {
	live, expired, stale := selectSupplyChainEntries(gap, entries, today)
	var staleLine []string
	if len(stale) > 0 {
		staleLine = []string{staleSupplyChainEntries(stale, "the measurement read "+gap.releaseWorkflow())}
	}
	if live != nil && len(stale) == 0 {
		return exceptedSupplyChainGap(gap, *live), nil
	}
	if live != nil {
		return "", errors.New(staleLine[0])
	}
	lines := make([]string, 0, len(gap.Shortfalls)+2)
	for index := 0; index < len(gap.Shortfalls); index++ {
		lines = append(lines, "[FAIL] "+supplyChainGate+": "+gap.Shortfalls[index].detail)
	}
	lines = append(lines, unexcusedSupplyChainGap(gap, expired))
	return "", errors.New(strings.Join(append(lines, staleLine...), "\n"))
}

// selectSupplyChainEntries sorts the HISS-11 entries against gap: the last live one that names
// its release workflow, the first expired one that does, and every one that names another.
func selectSupplyChainEntries(gap supplyChainGap, entries []config.Exception, today time.Time) (live, expired *config.Exception, stale []config.Exception) {
	for index := 0; index < len(entries) && index < config.MaxExceptions; index++ {
		switch {
		case !gap.excuses(entries[index]):
			stale = append(stale, entries[index])
		case !entries[index].Expired(today):
			live = &entries[index]
		case expired == nil:
			expired = &entries[index]
		}
	}
	return live, expired, stale
}

// unexcusedSupplyChainGap says why no entry declares the gap, its entry expired or none names
// the release workflow, and the two ways out.
func unexcusedSupplyChainGap(gap supplyChainGap, expired *config.Exception) string {
	if expired != nil {
		return expiredExceptionEntry(supplyChainGate, "the gap above fails", "close the gap", *expired)
	}
	return fmt.Sprintf("[FAIL] %s: no exceptions entry declares the gap above; raise the workflows (%s), or declare the "+
		"gap in the exceptions list of .standards.yaml (rule %s, path %s, a reason, and an expiry at most %d days ahead).",
		supplyChainGate, supplyChainGuide, config.ExceptionRuleSupplyChain, gap.releaseWorkflow(), config.MaxExceptionDays)
}

// staleSupplyChainEntries is the [FAIL] line for entries with no gap to excuse.
func staleSupplyChainEntries(entries []config.Exception, why string) string {
	return staleExceptionEntries(supplyChainGate, config.ExceptionRuleSupplyChain, "gap", entries, why)
}

// exceptedSupplyChainGap is the [PASS] line of a declared gap: the entry with its expiry and
// reason, every shortfall with the declared and measured values, and the measurement's scope.
func exceptedSupplyChainGap(gap supplyChainGap, entry config.Exception) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[PASS] %s: declared gap, excepted until %s by the exceptions entry (rule %s, %s): %s",
		supplyChainGate, entry.Expires, entry.Rule, entry.Target(), entry.Reason)
	for index := 0; index < len(gap.Shortfalls); index++ {
		b.WriteString("\n  - " + gap.Shortfalls[index].detail)
	}
	b.WriteString("\n" + supplyChainScope)
	return b.String()
}

// slsaLevelShortfall names the declared level, the measured one and the workflow it was read
// from, what the next level needs, and every reusable workflow call no level was credited for.
func slsaLevelShortfall(declared int, measured forge.ProvenanceMeasurement) string {
	var b strings.Builder
	fmt.Fprintf(&b, "policy declares SLSA Build Level %d but the workflows reach Level %d", declared, measured.Level)
	if measured.LevelWorkflow != "" {
		fmt.Fprintf(&b, " (%s/%s)", ghworkflow.Dir, measured.LevelWorkflow)
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
		return "Level 3 needs the build and its provenance in an isolated reusable workflow: a reusable workflow of " +
			"this repository whose job builds the artefacts and then runs actions/attest-build-provenance, or the SLSA " +
			"GitHub generator (slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@<vX.Y.Z>); " +
			"an attestation signed in the caller's build job, or over an artefact downloaded from another job, is Level 2."
	case declared == forge.SLSABuildL2:
		return "Level 2 needs provenance signed on the hosted runner, such as actions/attest-build-provenance in the release job."
	default:
		return "Level 1 needs a workflow that writes a provenance statement for the released artefacts."
	}
}

// supplyChainPass is the [PASS] line: each declaration with the workflow that meets it, and the
// scope of the measurement.
func supplyChainPass(declared config.SupplyChainPolicy, gap supplyChainGap) string {
	parts := []string{fmt.Sprintf("SLSA Build Level %d declared, Level %d measured", declared.SLSALevel, gap.Measured.Level)}
	if gap.Measured.LevelWorkflow != "" {
		parts[0] += " from " + gap.Measured.LevelWorkflow
	}
	if declared.EnforceCosign {
		parts = append(parts, "cosign signing in "+gap.Measured.CosignWorkflow)
	}
	if declared.RequireSBOM {
		parts = append(parts, "SBOM generated in "+gap.SBOM)
	}
	return "[PASS] " + supplyChainGate + ": " + strings.Join(parts, "; ") + ". " + supplyChainScope
}
