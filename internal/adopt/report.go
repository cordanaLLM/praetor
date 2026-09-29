package adopt

import "fmt"

// StepStatus is how one step of the adoption chain ended.
type StepStatus string

const (
	// StepCompleted means the step ran to the end; its warnings, if any, are on the outcome.
	StepCompleted StepStatus = "completed"
	// StepDeclined means adoption.decline in the manifest refused the step.
	StepDeclined StepStatus = "declined"
	// StepFailed means the step returned the error that stopped the chain, or recorded an
	// error on the report and carried on (harness.go does so when --force cannot find the
	// harness boundary).
	StepFailed StepStatus = "failed"
)

// StepOutcome records one step of the adoption chain and the warnings and errors it
// recorded. A step the chain never reached has no outcome.
type StepOutcome struct {
	Name     string     `json:"name"`
	Status   StepStatus `json:"status"`
	Warnings []string   `json:"warnings,omitempty"`
	Errors   []string   `json:"errors,omitempty"`
}

// stepMark is where one step's warnings, errors and action entries begin in the report.
type stepMark struct{ warnings, errors, actions int }

// mark records the current end of the report's warnings, errors and action entries, before a
// step runs.
func (r *AdoptReport) mark() stepMark {
	return stepMark{warnings: len(r.Warnings), errors: len(r.Errors), actions: len(r.ActionDetails)}
}

// recordStep appends the outcome of step name; the warnings and errors recorded since from
// belong to it, and so do the action entries (stepOfAction). A step that recorded an error and
// still returned nil did not complete: it is recorded as failed, so its pillars never earn a
// planned or success mark.
func (r *AdoptReport) recordStep(name string, status StepStatus, from stepMark) {
	outcome := StepOutcome{Name: name, Status: status, Warnings: since(r.Warnings, from.warnings), Errors: since(r.Errors, from.errors)}
	if status == StepCompleted && len(outcome.Errors) > 0 {
		outcome.Status = StepFailed
	}
	r.Steps = append(r.Steps, outcome)
	r.stepActions = append(r.stepActions, from.actions)
}

// stepOfAction returns the index in Steps of the step that recorded action entry index, or -1
// when no recorded step did (an entry recorded before the chain ran).
func (r *AdoptReport) stepOfAction(index int) int {
	owner := -1
	for k := 0; k < len(r.stepActions) && k < len(r.Steps) && k < maxAdoptSteps; k++ {
		if r.stepActions[k] <= index {
			owner = k
		}
	}
	return owner
}

// addStepError records text as an error of the run and of step, the index in Steps of the step
// it concerns, after that step's outcome was recorded. The error fails a completed step, as
// recordStep fails a step that recorded one itself. A step outside Steps gets the error on the
// run alone.
func (r *AdoptReport) addStepError(step int, text string) {
	r.Errors = append(r.Errors, text)
	if step < 0 || step >= len(r.Steps) {
		return
	}
	r.Steps[step].Errors = append(r.Steps[step].Errors, text)
	if r.Steps[step].Status == StepCompleted {
		r.Steps[step].Status = StepFailed
	}
}

// addStepWarning records text as a warning of the run and of step, like addStepError.
func (r *AdoptReport) addStepWarning(step int, text string) {
	r.Warnings = append(r.Warnings, text)
	if step >= 0 && step < len(r.Steps) {
		r.Steps[step].Warnings = append(r.Steps[step].Warnings, text)
	}
}

// since copies list from offset from; an offset outside the list selects nothing.
func since(list []string, from int) []string {
	if from < 0 || from >= len(list) {
		return nil
	}
	return append([]string(nil), list[from:]...)
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
	Errors   []string     `json:"errors,omitempty"`
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
				pillar.Warnings, pillar.Errors = step.Warnings, step.Errors
			}
		}
		pillars = append(pillars, pillar)
	}
	return pillars
}

func pillarStatus(step StepOutcome, dryRun bool) PillarStatus {
	switch {
	case step.Status == StepFailed || len(step.Errors) > 0:
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
	case PillarFailed:
		if len(p.Errors) > 0 {
			return fmt.Sprintf("%s [failed: %d error(s)]", line, len(p.Errors))
		}
		return line + " [failed]"
	default:
		return line + " [" + string(p.Status) + "]"
	}
}
