package adopt

import "fmt"

// StepStatus is how one step of the adoption chain ended.
type StepStatus string

const (
	// StepCompleted means the step ran to the end; its warnings, if any, are on the outcome.
	StepCompleted StepStatus = "completed"
	// StepDeclined means adoption.decline in the manifest refused the step.
	StepDeclined StepStatus = "declined"
	// StepFailed means the step returned the error that stopped the chain.
	StepFailed StepStatus = "failed"
)

// StepOutcome records one step of the adoption chain and the warnings it raised. A step the
// chain never reached has no outcome.
type StepOutcome struct {
	Name     string     `json:"name"`
	Status   StepStatus `json:"status"`
	Warnings []string   `json:"warnings,omitempty"`
}

// recordStep appends the outcome of step name; warnings raised since warnFrom belong to it.
func (r *AdoptReport) recordStep(name string, status StepStatus, warnFrom int) {
	outcome := StepOutcome{Name: name, Status: status}
	if warnFrom >= 0 && warnFrom < len(r.Warnings) {
		outcome.Warnings = append([]string(nil), r.Warnings[warnFrom:]...)
	}
	r.Steps = append(r.Steps, outcome)
}

// AdoptOutcome classifies a finished adoption run once, for every renderer.
type AdoptOutcome string

const (
	// OutcomeIncomplete means the run recorded an error; it wins over a dry run.
	OutcomeIncomplete AdoptOutcome = "incomplete"
	// OutcomeSimulated means a dry run finished without errors and wrote nothing.
	OutcomeSimulated AdoptOutcome = "simulated"
	// OutcomeApplied means the run finished without errors and wrote its changes.
	OutcomeApplied AdoptOutcome = "applied"
)

// Outcome reports whether the run is incomplete, simulated or applied. An error makes a run
// incomplete even when it was a dry run: a failed plan is not a plan (BUG-871).
func (r *AdoptReport) Outcome() AdoptOutcome {
	switch {
	case len(r.Errors) > 0:
		return OutcomeIncomplete
	case r.DryRun:
		return OutcomeSimulated
	default:
		return OutcomeApplied
	}
}

// PillarStatus is the state of one governance pillar, derived from the step that owns it.
type PillarStatus string

const (
	PillarReady    PillarStatus = "ready"
	PillarPlanned  PillarStatus = "planned"
	PillarWarned   PillarStatus = "warned"
	PillarDeclined PillarStatus = "declined"
	PillarFailed   PillarStatus = "failed"
	PillarNotRun   PillarStatus = "not-run"
)

// Pillar is one governance surface of an adoption report and the status its step earned.
type Pillar struct {
	Name     string       `json:"name"`
	Summary  string       `json:"summary"`
	Step     string       `json:"step"`
	Status   PillarStatus `json:"status"`
	Warnings []string     `json:"warnings,omitempty"`
}

// governancePillars names each pillar and the adoption step that produces it.
var governancePillars = [...]Pillar{
	{Name: "Universal Harness", Step: "agent-harness", Summary: "Canonical AGENTS.md, caveman-linted by compile-context --verify and audit"},
	{Name: "AI Context Sync", Step: "agent-harness", Summary: "6 targets (Claude Code, Cursor, Copilot, Windsurf, Codex, Gemini)"},
	{Name: "IDE Ecosystem", Step: "editors", Summary: "VS Code, JetBrains (CLion/GoLand/PyCharm), Neovim"},
	{Name: "DevContainer", Step: "dev-container", Summary: "Containerized deterministic dev environment (.devcontainer)"},
	{Name: "Verification Gate", Step: "makefile", Summary: "Makefile 'verify-all' standard entrypoint"},
}

// Pillars derives every governance pillar from the step outcomes, so a renderer never
// prints a success mark the report does not carry: a step that warned, failed, was declined
// or never ran says so.
func (r *AdoptReport) Pillars() []Pillar {
	pillars := make([]Pillar, 0, len(governancePillars))
	for _, pillar := range governancePillars {
		pillar.Status = PillarNotRun
		for _, step := range r.Steps {
			if step.Name == pillar.Step {
				pillar.Status = pillarStatus(step, r.DryRun)
				pillar.Warnings = step.Warnings
			}
		}
		pillars = append(pillars, pillar)
	}
	return pillars
}

func pillarStatus(step StepOutcome, dryRun bool) PillarStatus {
	switch {
	case step.Status == StepFailed:
		return PillarFailed
	case step.Status == StepDeclined:
		return PillarDeclined
	case len(step.Warnings) > 0:
		return PillarWarned
	case dryRun:
		return PillarPlanned
	default:
		return PillarReady
	}
}

var pillarMarks = map[PillarStatus]string{
	PillarReady: "✓", PillarPlanned: "○", PillarWarned: "⚠",
	PillarDeclined: "-", PillarFailed: "✗", PillarNotRun: "·",
}

// Line renders the pillar as one report line; every status but ready names itself.
func (p Pillar) Line() string {
	line := fmt.Sprintf("%s %-17s : %s", pillarMarks[p.Status], p.Name, p.Summary)
	switch p.Status {
	case PillarReady:
		return line
	case PillarWarned:
		return fmt.Sprintf("%s [warned: %d warning(s)]", line, len(p.Warnings))
	default:
		return line + " [" + string(p.Status) + "]"
	}
}
