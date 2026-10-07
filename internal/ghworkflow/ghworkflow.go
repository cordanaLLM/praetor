// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package ghworkflow is the one model of a GitHub Actions workflow document. Every workflow
// audit in internal/forge and the HISS scan of run: blocks in internal/hiss decode a workflow
// into Spec and walk it with the helpers here, so no second reading of the format can drift
// from this one (HISS-19). The package depends on nothing in the module but internal/util, so
// both of those packages can import it.
package ghworkflow

import (
	"fmt"
	"maps"
	slashpath "path"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

const (
	// Dir is the directory GitHub reads workflows from, relative to the repository root and
	// slash-separated. Only the YAML documents directly in it are workflows.
	Dir = ".github/workflows"
	// MaxFiles bounds the workflow documents one read takes from Dir (HISS-02).
	MaxFiles = 64
	// MaxJobsPerFile bounds the jobs read from one document (HISS-02). A document with more is
	// refused rather than read in part.
	MaxJobsPerFile = 64
	// MaxStepsPerJob bounds the steps read from one job (HISS-02).
	MaxStepsPerJob = 128
)

// Spec is the subset of a workflow document the audits and the run: block scan decide on.
//
// On and Permissions are raw nodes because each key has several shapes: a trigger is one event,
// a list or a mapping, and permissions are a shorthand scalar, a scope mapping, or absent, which
// is not the same as an empty mapping. Env is raw for the reason Step.Env is.
type Spec struct {
	On          yaml.Node      `yaml:"on"`
	Permissions yaml.Node      `yaml:"permissions"`
	Env         yaml.Node      `yaml:"env"`
	Defaults    Defaults       `yaml:"defaults"`
	Jobs        map[string]Job `yaml:"jobs"`
}

// Defaults is a workflow's or a job's defaults: key.
type Defaults struct {
	Run RunDefaults `yaml:"run"`
}

// RunDefaults is defaults.run: the shell every run: step without its own shell: runs under.
type RunDefaults struct {
	Shell string `yaml:"shell"`
}

// Job is the subset of one job the audits and the run: block scan decide on.
//
// RunsOn is a raw node because the key has three shapes: one label, a list of labels, and a
// mapping of a runner group and its labels (RunnerLabels). Container is raw because it is an
// image name or a mapping; only its presence changes the default shell (StepShell). Needs is raw
// because the key is one job id or a list of them (NeedIDs).
//
// Uses is the reusable workflow a job calls instead of running steps: a path under Dir of this
// repository ("./.github/workflows/<file>") or "<owner>/<repo>/.github/workflows/<file>@<ref>".
// Env is raw for the reason Step.Env is.
type Job struct {
	Name            string    `yaml:"name"`
	Uses            string    `yaml:"uses"`
	If              string    `yaml:"if"`
	Needs           yaml.Node `yaml:"needs"`
	ContinueOnError string    `yaml:"continue-on-error"`
	Permissions     yaml.Node `yaml:"permissions"`
	Env             yaml.Node `yaml:"env"`
	Strategy        Strategy  `yaml:"strategy"`
	Steps           []Step    `yaml:"steps"`
	RunsOn          yaml.Node `yaml:"runs-on"`
	Defaults        Defaults  `yaml:"defaults"`
	Container       yaml.Node `yaml:"container"`
}

// Step is the step subset the Go cache audit and the portability checks decide on, plus the id
// and env the adopt workflow's credential checks follow a value through
// (internal/forge/adopt_workflow_test.go), and where the run: script sits in the document.
// `with:` values are not all strings -- `cache: false` is a bool, `fetch-depth: 0` an int -- so
// the map is untyped.
//
// Env is a raw node because the key has two shapes: a mapping, or one expression such as
// `${{ fromJSON(vars.X) }}` that evaluates to one (the workflow schema gives step env a
// context). A typed map rejects the second shape and would fail the whole document over a key
// that holds a value the file cannot show; EnvValue reads both.
//
// Shell is the step's `shell:`; empty means the job's or the workflow's defaults.run.shell, or
// else the runner's default (StepShell). ContinueOnError is the step's `continue-on-error:` as
// written, a literal or an expression, so an aggregate's failing step can be told from one whose
// failure is forgiven (internal/forge/workflow_aggregate.go).
type Step struct {
	Name            string         `yaml:"name"`
	ID              string         `yaml:"id"`
	If              string         `yaml:"if"`
	Uses            string         `yaml:"uses"`
	Run             string         `yaml:"run"`
	Shell           string         `yaml:"shell"`
	ContinueOnError string         `yaml:"continue-on-error"`
	With            map[string]any `yaml:"with"`
	Env             yaml.Node      `yaml:"env"`
	// RunLine is the document line the run: value starts on, its | or > indicator for a block
	// scalar, and 0 for a step without run:.
	RunLine int `yaml:"-"`
	// RunLiteral reports a literal block scalar (run: |), whose script line n is document line
	// RunLine+n. Any other style folds or escapes its lines, so only RunLine is known.
	RunLiteral bool `yaml:"-"`
}

// stepFields is Step without its decoder, so UnmarshalYAML decodes the fields without calling
// itself.
type stepFields Step

// UnmarshalYAML decodes a step and records where its run: value sits in the document.
func (s *Step) UnmarshalYAML(node *yaml.Node) error {
	if err := node.Decode((*stepFields)(s)); err != nil {
		return err
	}
	if run := util.YAMLMappingValue(node, "run"); run != nil {
		s.RunLine, s.RunLiteral = run.Line, run.Kind == yaml.ScalarNode && run.Style&yaml.LiteralStyle != 0
	}
	return nil
}

// NeedIDs returns the ids of the jobs j needs, in document order: the id of a scalar needs:, or
// each scalar entry of a list, at most MaxJobsPerFile of them (a document naming more jobs is
// refused by Parse). A YAML null names no job. Any other shape, and an absent key, needs no job.
func (j *Job) NeedIDs() []string {
	switch j.Needs.Kind {
	case yaml.ScalarNode:
		if id := strings.TrimSpace(j.Needs.Value); id != "" && j.Needs.ShortTag() != "!!null" {
			return []string{id}
		}
	case yaml.SequenceNode:
		var ids []string
		for i := 0; i < len(j.Needs.Content) && i < MaxJobsPerFile; i++ {
			entry := j.Needs.Content[i]
			if id := strings.TrimSpace(entry.Value); entry.Kind == yaml.ScalarNode && id != "" && entry.ShortTag() != "!!null" {
				ids = append(ids, id)
			}
		}
		return ids
	}
	return nil
}

// Strategy carries the matrix a job expands into. A matrix job reports one check per leg, so
// one `name:` is several required contexts on the forge.
//
// Matrix is a raw node because the key has several shapes: a mapping of literal axes with
// include and exclude lists whose values may be mappings, or one expression such as
// `${{ fromJSON(needs.plan.outputs.matrix) }}` that evaluates to one. A typed decode rejects
// most of them and would fail the whole document, and every audit that reads it, over a job no
// check may read.
type Strategy struct {
	Matrix yaml.Node `yaml:"matrix"`
}

// Parse decodes one workflow document and refuses one with more than MaxJobsPerFile jobs rather
// than reading it in part.
func Parse(data []byte) (Spec, error) {
	var spec Spec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return Spec{}, fmt.Errorf("parse workflow: %w", err)
	}
	if len(spec.Jobs) > MaxJobsPerFile {
		return Spec{}, fmt.Errorf("workflow exceeds %d jobs", MaxJobsPerFile)
	}
	return spec, nil
}

// SortedJobIDs returns a workflow's job identifiers in a stable order, so every reader reports
// the same jobs in the same sequence from the same file.
func SortedJobIDs(jobs map[string]Job) []string {
	return slices.Sorted(maps.Keys(jobs))
}

// IsYAMLName reports whether a file name is read by the YAML parser rather than as text: the
// names GitHub reads as workflows in Dir, and as action manifests.
func IsYAMLName(name string) bool {
	return strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")
}

// IsWorkflowPath reports whether rel, a slash-separated path relative to the repository root,
// is a document GitHub reads as a workflow: a YAML file directly in Dir.
func IsWorkflowPath(rel string) bool {
	dir, name := slashpath.Split(rel)
	return dir == Dir+"/" && IsYAMLName(name)
}

// NeverRuns reports whether an if: condition of a job or step is the literal false, bare or as
// the expression ${{ false }}: GitHub then skips it on every run. Any other condition may hold,
// so it is read as running.
func NeverRuns(condition string) bool {
	expression, closed := UnwrapExpression(condition)
	return closed && expression == "false"
}

// EnvValue returns the value the environment variable name has for a step, read from scopes in
// the order GitHub applies them, the innermost first: the step's env:, its job's, then the
// workflow's. set is false when no scope declares name. known is false when the scope that
// decides is one expression, an env: such as `${{ fromJSON(vars.X) }}` whose variables the file
// cannot show, so name may be set to anything there.
func EnvValue(name string, scopes ...*yaml.Node) (value string, set, known bool) {
	for i := 0; i < len(scopes); i++ {
		scope := scopes[i]
		if scope == nil || scope.Kind == 0 || scope.Tag == "!!null" {
			continue
		}
		if scope.Kind != yaml.MappingNode {
			return "", false, false
		}
		if entry := util.YAMLMappingValue(scope, name); entry != nil {
			return entry.Value, true, true
		}
	}
	return "", false, true
}

// RunStep is one run: step of a workflow, with the job it runs in.
type RunStep struct {
	// JobID is the job's key under jobs:.
	JobID string
	Job   *Job
	Step  *Step
	// Index is the step's position among all steps of its job, from 0, `uses:` steps counted.
	Index int
}

// RunSteps lists the steps of spec that run a script, job by job in job ID order and step by
// step in file order. A step whose run: is empty or blank runs nothing and is left out. A job
// with more than MaxStepsPerJob steps is refused rather than read in part.
func RunSteps(spec *Spec) ([]RunStep, error) {
	ids := SortedJobIDs(spec.Jobs)
	var runs []RunStep
	for i := 0; i < len(ids) && i < MaxJobsPerFile; i++ {
		job := spec.Jobs[ids[i]]
		if len(job.Steps) > MaxStepsPerJob {
			return nil, fmt.Errorf("workflow job %s exceeds %d steps", ids[i], MaxStepsPerJob)
		}
		for j := 0; j < len(job.Steps) && j < MaxStepsPerJob; j++ {
			if strings.TrimSpace(job.Steps[j].Run) != "" {
				runs = append(runs, RunStep{JobID: ids[i], Job: &job, Step: &job.Steps[j], Index: j})
			}
		}
	}
	return runs, nil
}
