// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hisscatalog

import "strings"

// Pipeline is one enforcement pipeline adoption can generate in an adopted repository. A
// check is stated only for the pipelines one adoption run actually generated: a preserved
// custom verify-all, a lefthook.yml praetor did not write, or a lefthook.yml whose hooks
// lefthook never installed runs none of praetor's checks, so a harness that claimed them
// anyway would describe a gate the repository does not have (BUG-804).
type Pipeline uint8

const (
	// PipelineVerifyAll is the Makefile verify-all target adoption generates.
	PipelineVerifyAll Pipeline = 1 << iota
	// PipelineLefthook is praetor's lefthook.yml with its hooks installed by lefthook.
	PipelineLefthook
	// AllPipelines is every pipeline a full adoption generates. standards_explain_rule, which
	// answers for no particular repository, states the checks against it.
	AllPipelines = PipelineVerifyAll | PipelineLefthook
)

// Stage is one place a generated pipeline runs a check. Stages are bits, so a set of them
// is one comparable value and Rule stays comparable.
type Stage uint8

const (
	// StageVerifyAll is the generated `make verify-all` target.
	StageVerifyAll Stage = 1 << iota
	// StagePreCommit is the generated lefthook pre-commit hook.
	StagePreCommit
	// StagePrePush is the generated lefthook pre-push hook.
	StagePrePush
	// StagePostCommit is the generated lefthook post-commit hook, which cannot block.
	StagePostCommit
)

const (
	// NotEnforced is the adopted check of an invariant adoption generates no check for.
	NotEnforced = "not enforced"
	// Advisory is the failure action of an invariant adoption generates no check for.
	Advisory = "advisory"
	// recordsOnly is the failure action of a check whose every stage records and blocks nothing.
	recordsOnly = "none; records only"

	adoptedAudit = "`praetorctl audit` HISS scan"
)

// stageOrder lists every stage in pipeline order, the order a table cell names them in.
var stageOrder = [...]Stage{StageVerifyAll, StagePreCommit, StagePrePush, StagePostCommit}

// stageFacts is what one stage belongs to, how the harness names it, and what a failing check
// there blocks; an empty consequence means the stage records and blocks nothing.
type stageFacts struct {
	pipeline    Pipeline
	hook        string
	consequence string
}

var stages = map[Stage]stageFacts{
	StageVerifyAll:  {pipeline: PipelineVerifyAll, consequence: "fails verify-all"},
	StagePreCommit:  {pipeline: PipelineLefthook, hook: "pre-commit", consequence: "blocks commit"},
	StagePrePush:    {pipeline: PipelineLefthook, hook: "pre-push", consequence: "blocks push"},
	StagePostCommit: {pipeline: PipelineLefthook, hook: "post-commit"},
}

// auditStages are the generated stages that run `praetorctl audit`: the verify-all target and
// the lefthook pre-commit and pre-push hooks (internal/adopt buildMakefile, buildLefthookYAMLFor).
const auditStages = StageVerifyAll | StagePreCommit | StagePrePush

// AdoptedCheck is the check an adopted repository gets for one invariant and the generated
// stages that run it. All text is agent-facing, so it is written in the internal register.
type AdoptedCheck struct {
	// Check names the command or scan, such as "`praetorctl audit` HISS scan".
	Check string
	// Coverage qualifies what the check decides, such as the languages it reads; optional.
	Coverage string
	// Trigger is what fails the check, such as "new finding".
	Trigger string
	// Stages is the set of generated stages that run the check.
	Stages Stage
	// Languages are the source languages the check decides; zero means it reads no source
	// language (a baseline ratchet, a context drift check) and holds in every repository.
	Languages Language
}

// scannedLanguages are the languages internal/hiss reads that a directive clause can name (.go,
// .rs, .py and native C/C++ sources). The scan also reads JavaScript, TypeScript, Svelte, shell,
// systemd units and Ansible playbooks, which have no language bit yet and count as
// LanguageOther, so a repository carrying only those renders its scan rows as not enforced: an
// understatement, never a borrowed claim.
const scannedLanguages = LanguageGo | LanguageRust | LanguagePython | LanguageC

// auditCheck is an adopted `praetorctl audit` HISS scan qualified by what it decides and the
// languages the scan decides it in, a subset of scannedLanguages.
func auditCheck(languages Language, coverage string) AdoptedCheck {
	return AdoptedCheck{Check: adoptedAudit, Coverage: coverage, Trigger: "new finding", Stages: auditStages, Languages: languages}
}

// Adopted returns the check an adopted repository gets from the pipelines adoption generated
// there, and what its failure blocks. With no generating stage among them it returns
// NotEnforced and Advisory, so the rule is never credited with a gate the repository lacks.
func (r Rule) Adopted(generated Pipeline) (check, failure string) {
	active := r.Adoption.stagesIn(generated)
	if len(active) == 0 {
		return NotEnforced, Advisory
	}
	check = r.Adoption.Check + " in " + stageLabels(active)
	if r.Adoption.Coverage != "" {
		check += ": " + r.Adoption.Coverage
	}
	return check, failureText(r.Adoption.Trigger, active)
}

// AdoptedFor is Adopted for one repository: a rule none of whose clauses applies to the
// repository's languages, or whose check decides none of them, is not enforced there whatever
// pipeline runs the scan, so no row names a rule "n/a" and credits a failing gate beside it.
// Unknown languages (zero) keep every check, as they keep every clause.
func (r Rule) AdoptedFor(generated Pipeline, f Facts) (check, failure string) {
	if f.Languages != 0 && !r.decidedIn(f.Languages) {
		return NotEnforced, Advisory
	}
	return r.Adopted(generated)
}

// decidedIn reports whether the rule binds a repository carrying languages and its check reads
// one of them.
func (r Rule) decidedIn(languages Language) bool {
	if r.AdoptedDirective(Facts{Languages: languages}) == noAnalogue {
		return false
	}
	return r.Adoption.Languages == 0 || r.Adoption.Languages&languages != 0
}

// AdoptedExplanation is Explanation followed by the line that states what an adopted
// repository enforces, so an agent working in one never reads praetor's own gate as its own.
// standards_explain_rule serves it. The tool answers for no particular repository, so the
// line states every pipeline a full adoption generates and says the check holds only where
// adoption generated it.
func (r Rule) AdoptedExplanation() string {
	adopted := NotEnforced + "; adoption generates no check for this rule."
	if check, failure := r.Adopted(AllPipelines); check != NotEnforced {
		adopted = strings.ReplaceAll(check, "`", "'") + "; on failure: " + failure +
			". Holds only where adoption generated that pipeline; a preserved custom verify-all, or a lefthook.yml praetor did not write or lefthook never installed, runs none of it."
	}
	return r.Explanation() + "\nAdopted repositories: " + adopted
}

// stagesIn keeps the stages of the check whose pipeline adoption generated, in stage order.
func (a AdoptedCheck) stagesIn(generated Pipeline) []Stage {
	active := make([]Stage, 0, len(stageOrder))
	for _, stage := range stageOrder {
		if a.Stages&stage != 0 && stages[stage].pipeline&generated != 0 {
			active = append(active, stage)
		}
	}
	return active
}

// stageLabels names the stages as "verify-all + lefthook pre-commit/pre-push".
func stageLabels(active []Stage) string {
	var labels, hooks []string
	for _, stage := range active {
		facts := stages[stage]
		if facts.hook == "" {
			labels = append(labels, "verify-all")
			continue
		}
		hooks = append(hooks, facts.hook)
	}
	if len(hooks) > 0 {
		labels = append(labels, "lefthook "+strings.Join(hooks, "/"))
	}
	return strings.Join(labels, " + ")
}

// failureText joins what each stage blocks behind the trigger: "new finding fails verify-all,
// blocks commit". A check that only records, such as a post-commit hook, blocks nothing.
func failureText(trigger string, active []Stage) string {
	var consequences []string
	for _, stage := range active {
		if consequence := stages[stage].consequence; consequence != "" {
			consequences = append(consequences, consequence)
		}
	}
	if len(consequences) == 0 {
		return recordsOnly
	}
	return trigger + " " + strings.Join(consequences, ", ")
}
