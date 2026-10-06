package forge

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	slashpath "path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// SLSA v1.0 Build track levels a workflow file can show (slsa.dev/spec/v1.0/levels).
const (
	// SLSABuildL1: provenance exists, describing how the artefact was built.
	SLSABuildL1 = 1
	// SLSABuildL2: the provenance is signed by the hosted build platform that ran the build.
	SLSABuildL2 = 2
	// SLSABuildL3: the build runs isolated from the workflow that starts it, and the signing
	// material is out of reach of its user-defined steps.
	SLSABuildL3 = 3
)

const (
	// attestBuildProvenanceAction writes SLSA build provenance and nothing else; since v4 it
	// wraps actions/attest.
	attestBuildProvenanceAction = "actions/attest-build-provenance"
	// attestAction writes SLSA build provenance unless sbom-path or a predicate input selects
	// another statement (github.com/actions/attest).
	attestAction = "actions/attest"
	// slsaProvenancePredicate prefixes every SLSA provenance predicate type URI.
	slsaProvenancePredicate = "https://slsa.dev/provenance/"
	// downloadArtifactAction brings into a job an artefact another job of the workflow run
	// uploaded. A called reusable workflow runs in its caller's run, so the artefact may be the
	// caller's build.
	downloadArtifactAction = "actions/download-artifact"
	// dockerBuildPushAction builds a container image with Buildx.
	dockerBuildPushAction = "docker/build-push-action"
	// cacheAction and cacheRestoreAction restore files an earlier run saved, which may be the
	// artefacts an attestation then names.
	cacheAction        = "actions/cache"
	cacheRestoreAction = "actions/cache/restore"
	makeBinary         = "make"
	// slsaGeneratorWorkflows is where the SLSA GitHub generator keeps the reusable workflows
	// that build or generate Level 3 provenance (generator_generic_slsa3.yml,
	// builder_go_slsa3.yml, ...); each name ends in slsaGeneratorSuffix.
	slsaGeneratorWorkflows = "slsa-framework/slsa-github-generator/.github/workflows/"
	slsaGeneratorSuffix    = "_slsa3.yml"
	// localWorkflowPrefix starts a job's uses: that calls a reusable workflow of this repository.
	localWorkflowPrefix = "./" + ghworkflow.Dir + "/"
	cosignBinary        = "cosign"
	// workflowCallEvent is the trigger of a reusable workflow; one with no other trigger runs
	// only when a job calls it.
	workflowCallEvent = "workflow_call"
	// maxGoreleaserSigns bounds the entries read from one GoReleaser signing block (HISS-02).
	maxGoreleaserSigns = 64
	// maxUncreditedCalls bounds the uncredited calls one workflow records: one per job, plus
	// one per job of each reusable workflow a job calls (HISS-02).
	maxUncreditedCalls = maxJobsPerFile * maxJobsPerFile
	// provenanceTimeout bounds one measurement (HISS-02).
	provenanceTimeout = 30 * time.Second
)

// slsaGeneratorTag is the only ref form under which slsa-verifier accepts the SLSA generator's
// provenance: the reusable workflow must be called by a vX.Y.Z tag, not a branch or a digest.
var slsaGeneratorTag = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// cosignSigningCommands are the cosign subcommands that sign: an artefact, a blob, or an
// attestation about one. verify and download sign nothing.
var cosignSigningCommands = map[string]bool{"sign": true, "sign-blob": true, "attest": true, "attest-blob": true}

// cosignProvenanceTypes are the cosign --type shorthands for an SLSA provenance predicate.
var cosignProvenanceTypes = map[string]bool{"slsaprovenance": true, "slsaprovenance02": true, "slsaprovenance1": true}

// buildCommands are the run: commands read as a build step: a compiler, an image build, a
// GoReleaser release or build, or a package script named build. Each is the program by its base
// name and then its arguments as written. make running a target counts too (runsMakeTarget). A
// reusable workflow's attestation counts as Level 3 only when one of them ran before it in the
// same job (MeasureProvenance).
var buildCommands = [...][]string{
	{"go", "build"}, {"cargo", "build"}, {"goreleaser", "release"}, {"goreleaser", "build"},
	{"docker", "build"}, {"docker", "buildx", "build"},
	{"npm", "run", "build"}, {"pnpm", "build"}, {"pnpm", "run", "build"}, {"yarn", "build"}, {"yarn", "run", "build"},
}

// makeValueOptions are the GNU make options whose value is the next field, so that field names
// no target.
var makeValueOptions = map[string]bool{
	"-C": true, "--directory": true, "-f": true, "--file": true, "--makefile": true, "-I": true, "--include-dir": true,
	"-o": true, "--old-file": true, "--assume-old": true, "-W": true, "--what-if": true, "--new-file": true, "--assume-new": true,
}

// makeCountOptions take an optional number as the next field.
var makeCountOptions = map[string]bool{"-j": true, "--jobs": true, "-l": true, "--load-average": true, "--max-load": true}

// makeNoBuildOptions make make print, check or describe instead of running a recipe.
var makeNoBuildOptions = map[string]bool{
	"-n": true, "--just-print": true, "--dry-run": true, "--recon": true, "-q": true, "--question": true,
	"-p": true, "--print-data-base": true, "-v": true, "--version": true, "-h": true, "--help": true,
}

// importCommands are the run: commands that download an artefact another job of the workflow
// run uploaded, as downloadArtifactAction does.
var importCommands = [...][]string{{"gh", "run", "download"}}

// ProvenanceMeasurement is what a repository's workflow files show about the provenance and
// signatures its releases carry. It is read from the files alone: no published attestation,
// signature or forge record is consulted.
type ProvenanceMeasurement struct {
	// Level is the highest SLSA Build level any workflow reaches, 0 when none writes provenance.
	Level int
	// LevelWorkflow is the first workflow, in name order, that reaches Level; "" at Level 0.
	LevelWorkflow string
	// CosignWorkflow is the first workflow with a cosign signing step; "" when none has one.
	CosignWorkflow string
	// Uncredited names each reusable workflow call no level was credited for, with the reason.
	Uncredited []string
}

// MeasureProvenance reads every workflow under .github/workflows and measures the SLSA Build
// level and the cosign signing its jobs show (#330).
//
//   - Level 1: this tool's provenance command writes a statement nothing signs.
//   - Level 2: GitHub's attestation action in provenance mode, a cosign attestation of an SLSA
//     provenance type, or a cosign attestation of the statement this tool's provenance command
//     wrote earlier in the job: provenance signed on the hosted runner that ran the build.
//   - Level 3: a job that calls the SLSA GitHub generator's reusable workflow by a vX.Y.Z tag,
//     or a reusable workflow of this repository whose job builds the artefacts and then runs
//     GitHub's attestation action. GitHub documents Build Level 3 for that shape only when the
//     reusable workflow that builds the software also attests it: a build step (buildCommands,
//     docker/build-push-action, a GoReleaser action release or build) must come before the
//     attestation in its job, and no artefact downloaded from another job of the run may, since
//     that artefact may be the caller's build. Such an attestation is Level 2 and is named in
//     Uncredited with the reason.
//
// A workflow whose only trigger is workflow_call is measured through the jobs that call it. A
// reusable workflow in another repository cannot be read here and is listed in Uncredited. An
// empty or malformed workflow is an error, because what it would release cannot be read.
func MeasureProvenance(ctx context.Context, repoPath string) (ProvenanceMeasurement, error) {
	if ctx == nil {
		return ProvenanceMeasurement{}, errors.New("provenance measurement requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, provenanceTimeout)
	defer cancel()
	workflows, err := parseProvenanceWorkflows(ctx, repoPath)
	if err != nil {
		return ProvenanceMeasurement{}, err
	}
	reader := provenanceReader{ctx: ctx, repoPath: repoPath, called: make(map[string]workflowEvidence, len(workflows))}
	if err := reader.measureReusableWorkflows(workflows); err != nil {
		return ProvenanceMeasurement{}, err
	}
	var measured ProvenanceMeasurement
	for i := 0; i < len(workflows) && i < maxWorkflowFiles; i++ {
		if slices.Equal(triggerNames(&workflows[i].spec.On), []string{workflowCallEvent}) {
			continue
		}
		evidence, err := reader.joinJobs(&workflows[i].spec, reader.job)
		if err != nil {
			return ProvenanceMeasurement{}, fmt.Errorf("workflow %s: %w", workflows[i].name, err)
		}
		measured.record(workflows[i].name, evidence)
	}
	return measured, nil
}

// namedWorkflow is one parsed workflow document and its file name under .github/workflows.
type namedWorkflow struct {
	name string
	spec workflowSpec
}

// parseProvenanceWorkflows parses every workflow document and refuses one that declares no
// jobs: GitHub rejects such a file, and an empty release workflow measures nothing honestly.
func parseProvenanceWorkflows(ctx context.Context, repoPath string) ([]namedWorkflow, error) {
	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	workflows := make([]namedWorkflow, 0, len(files))
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		spec, err := ghworkflow.Parse(files[i].Data)
		if err != nil {
			return nil, fmt.Errorf("workflow %s: %w", files[i].Name, err)
		}
		if len(spec.Jobs) == 0 {
			return nil, fmt.Errorf("workflow %s declares no jobs, so what it releases cannot be measured", files[i].Name)
		}
		workflows = append(workflows, namedWorkflow{name: files[i].Name, spec: spec})
	}
	return workflows, nil
}

// record folds one workflow's evidence into the measurement.
func (m *ProvenanceMeasurement) record(name string, evidence workflowEvidence) {
	if evidence.level > m.Level {
		m.Level, m.LevelWorkflow = evidence.level, name
	}
	if evidence.cosign && m.CosignWorkflow == "" {
		m.CosignWorkflow = name
	}
	for i := 0; i < len(evidence.uncredited) && i < maxUncreditedCalls; i++ {
		m.Uncredited = append(m.Uncredited, name+": "+evidence.uncredited[i])
	}
}

// workflowEvidence is what one job, or every job of one workflow, shows.
type workflowEvidence struct {
	level int
	// githubAttested is true when GitHub's attestation action wrote provenance.
	githubAttested bool
	cosign         bool
	uncredited     []string
}

// join folds other into e.
func (e *workflowEvidence) join(other workflowEvidence) {
	e.level = max(e.level, other.level)
	e.githubAttested = e.githubAttested || other.githubAttested
	e.cosign = e.cosign || other.cosign
	e.uncredited = append(e.uncredited, other.uncredited...)
}

// provenanceReader measures the jobs of the workflows one repository holds. called holds the
// evidence of each of its reusable workflows (workflow_call), by file name.
type provenanceReader struct {
	ctx      context.Context
	repoPath string
	called   map[string]workflowEvidence
}

// measureReusableWorkflows records the evidence of every reusable workflow (on: workflow_call)
// before any caller is read, so a calling job reads that evidence instead of walking into the
// workflow: the walk never re-enters itself (HISS-01).
func (r provenanceReader) measureReusableWorkflows(workflows []namedWorkflow) error {
	for i := 0; i < len(workflows) && i < maxWorkflowFiles; i++ {
		if _, reusable := eventTrigger(&workflows[i].spec.On, workflowCallEvent); !reusable {
			continue
		}
		uses := localWorkflowPrefix + workflows[i].name
		evidence, err := r.joinJobs(&workflows[i].spec, func(job *workflowJob) (workflowEvidence, error) {
			return r.calledJob(uses, job)
		})
		if err != nil {
			return fmt.Errorf("workflow %s: %w", workflows[i].name, err)
		}
		r.called[workflows[i].name] = evidence
	}
	return nil
}

// joinJobs joins what measure finds in each job of spec, in job ID order. A job whose if: is the
// literal false never runs and shows nothing (ghworkflow.NeverRuns).
func (r provenanceReader) joinJobs(spec *workflowSpec, measure func(*workflowJob) (workflowEvidence, error)) (workflowEvidence, error) {
	ids := ghworkflow.SortedJobIDs(spec.Jobs)
	var evidence workflowEvidence
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		job := spec.Jobs[ids[i]]
		if ghworkflow.NeverRuns(job.If) {
			continue
		}
		found, err := measure(&job)
		if err != nil {
			return workflowEvidence{}, fmt.Errorf("job %s: %w", ids[i], err)
		}
		evidence.join(found)
	}
	return evidence, nil
}

// job measures one job of a workflow a trigger starts: its steps, or the reusable workflow it
// calls.
func (r provenanceReader) job(job *workflowJob) (workflowEvidence, error) {
	uses := strings.TrimSpace(job.Uses)
	if uses == "" {
		steps, err := r.steps(job.Steps)
		return steps.evidence(), err
	}
	name, local := strings.CutPrefix(uses, localWorkflowPrefix)
	if !local {
		return remoteReusableWorkflow(uses), nil
	}
	called, ok := r.called[name]
	if !ok {
		return workflowEvidence{}, fmt.Errorf("calls %s, which is no reusable workflow (on: workflow_call) in %s", uses, ghworkflow.Dir)
	}
	return called, nil
}

// calledJob measures one job of workflow, a reusable workflow of this repository. GitHub's
// attestation action signs there with the reusable workflow's identity, isolated from the
// caller, and GitHub documents Build Level 3 when that reusable workflow also builds what it
// attests ("The reusable workflow you use to build your software must also generate artifact
// attestations"). The job is Level 3 only when a build step ran before the attestation and no
// artefact came in from another job of the run; otherwise the attestation stays Level 2 and is
// named with the reason. A reusable workflow it calls in turn is credited only when it is the
// SLSA generator; another one is not followed.
func (r provenanceReader) calledJob(workflow string, job *workflowJob) (workflowEvidence, error) {
	uses := strings.TrimSpace(job.Uses)
	if strings.HasPrefix(uses, localWorkflowPrefix) {
		return workflowEvidence{uncredited: []string{uses + ": a reusable workflow called from a reusable workflow is not followed"}}, nil
	}
	if uses != "" {
		return remoteReusableWorkflow(uses), nil
	}
	steps, err := r.steps(job.Steps)
	if err != nil {
		return workflowEvidence{}, err
	}
	evidence := steps.evidence()
	switch {
	case steps.buildAttested:
		evidence.level = SLSABuildL3
	case steps.attestGap != "":
		evidence.uncredited = append(evidence.uncredited, workflow+": its attestation is Level 2, because "+steps.attestGap+
			"; Level 3 needs the reusable workflow to build what it attests")
	}
	return evidence, nil
}

// remoteReusableWorkflow credits a call to a reusable workflow in another repository: Level 3
// for the SLSA generator called by a vX.Y.Z tag, nothing for any other, which cannot be read.
func remoteReusableWorkflow(uses string) workflowEvidence {
	path, ref, _ := strings.Cut(uses, "@")
	name, generator := strings.CutPrefix(strings.ToLower(path), slsaGeneratorWorkflows)
	switch {
	case !generator || !strings.HasSuffix(name, slsaGeneratorSuffix) || strings.Contains(name, "/"):
		return workflowEvidence{uncredited: []string{uses + ": a reusable workflow in another repository is not read"}}
	case !slsaGeneratorTag.MatchString(ref):
		return workflowEvidence{uncredited: []string{uses + ": the SLSA generator's provenance verifies only when it is called by a vX.Y.Z tag"}}
	}
	return workflowEvidence{level: SLSABuildL3}
}

// steps reads the steps of one job, leaving out a step whose if: is the literal false.
func (r provenanceReader) steps(steps []workflowStep) (jobProvenance, error) {
	if len(steps) > maxStepsPerJob {
		return jobProvenance{}, fmt.Errorf("job exceeds %d steps", maxStepsPerJob)
	}
	var job jobProvenance
	for i := 0; i < len(steps) && i < maxStepsPerJob; i++ {
		if ghworkflow.NeverRuns(steps[i].If) {
			continue
		}
		if err := job.read(r.ctx, r.repoPath, &steps[i]); err != nil {
			return jobProvenance{}, fmt.Errorf("step %d: %w", i+1, err)
		}
	}
	return job, nil
}

// unbuiltAttestation and importedAttestation say why an attestation does not cover a build of
// its own job.
const (
	unbuiltAttestation = "no build step (go build, cargo build, docker build, GoReleaser, make, or an npm, pnpm or " +
		"yarn build) runs before it in its job"
	importedAttestation = "an artefact downloaded from another job of the run, which may be the caller's build, " +
		"or restored from a cache over the attested files, comes before it in its job"
)

// jobProvenance accumulates what the steps of one job do, in file order.
type jobProvenance struct {
	githubAttested bool
	// buildAttested: GitHub's attestation action ran after a build step of this job and before
	// any artefact came in from another job; attestGap says why an attestation did not.
	buildAttested bool
	attestGap     string
	// built: a build step ran; imported: an artefact another job uploaded was downloaded.
	built, imported bool
	// cached holds the paths an actions/cache restore brought in, which import the attested
	// files when they overlap (cacheImports).
	cached []string
	// signedProvenance: a cosign attestation of an SLSA provenance type, or of a statement this
	// tool's provenance command wrote earlier in the job.
	signedProvenance bool
	// generated: this tool's provenance command ran; outputs are the files it wrote.
	generated bool
	outputs   []string
	cosign    bool
}

// evidence returns the level and signing the job's steps reach.
func (j *jobProvenance) evidence() workflowEvidence {
	evidence := workflowEvidence{githubAttested: j.githubAttested, cosign: j.cosign}
	switch {
	case j.githubAttested || j.signedProvenance:
		evidence.level = SLSABuildL2
	case j.generated:
		evidence.level = SLSABuildL1
	}
	return evidence
}

// read records one step: an action (readAction), or a run script command by command, each
// GoReleaser command of it read on its own (invocations).
func (j *jobProvenance) read(ctx context.Context, repoPath string, step *workflowStep) error {
	if action := actionPath(step.Uses); action != "" {
		return j.readAction(ctx, repoPath, step, action)
	}
	fields, err := scriptFields(step.Run)
	if err != nil {
		return err
	}
	j.readScript(fields)
	calls := invocations(fields, goreleaserBinary)
	for i := 0; i < len(calls) && i < maxRunScriptFields; i++ {
		if err := j.readGoreleaser(ctx, repoPath, calls[i]); err != nil {
			return err
		}
	}
	return nil
}

// readAction records one uses: step: GitHub's attestation actions, an artefact download, a cache
// restore, an image build, or a GoReleaser release. Any other action is no evidence.
func (j *jobProvenance) readAction(ctx context.Context, repoPath string, step *workflowStep, action string) error {
	switch {
	case action == attestBuildProvenanceAction, action == attestAction && attestsProvenance(*step):
		j.attest(stepInputList(*step, "subject-path"))
	case action == downloadArtifactAction:
		j.imported = true
	case action == cacheAction, action == cacheRestoreAction:
		j.cached = append(j.cached, stepInputList(*step, "path")...)
	case action == dockerBuildPushAction:
		j.built = true
	case action == goreleaserActionPath:
		return j.readGoreleaserAction(ctx, repoPath, step)
	}
	return nil
}

// attest records GitHub's attestation action writing provenance over subjects (its
// subject-path), and whether it covers a build of this job: a build step came first and no
// artefact came in from another job or a cache.
func (j *jobProvenance) attest(subjects []string) {
	j.githubAttested = true
	imported := j.imported || cacheImports(j.cached, subjects)
	switch {
	case j.built && !imported:
		j.buildAttested = true
	case imported:
		j.attestGap = importedAttestation
	default:
		j.attestGap = unbuiltAttestation
	}
}

// stepInputList splits a with: input that lists paths, one per line or comma-separated.
func stepInputList(step workflowStep, key string) []string {
	return strings.FieldsFunc(stepInput(step, key), func(r rune) bool { return r == '\n' || r == ',' })
}

// cacheImports reports whether a cache restore brought in files an attestation names: a cached
// path and a subject path overlap when the literal directory before the first glob character of
// one contains the other. An attestation naming no subject path (subject-digest,
// subject-checksums) cannot be told apart, so any restore counts. A cached path outside the
// workspace (~ or /) never holds a relative subject.
func cacheImports(cached, subjects []string) bool {
	if len(cached) > 0 && len(subjects) == 0 {
		return true
	}
	for i := 0; i < len(cached) && i < maxRunScriptFields; i++ {
		for k := 0; k < len(subjects) && k < maxRunScriptFields; k++ {
			if pathsOverlap(literalPrefix(cached[i]), literalPrefix(subjects[k])) {
				return true
			}
		}
	}
	return false
}

// literalPrefix returns the cleaned directory of a path pattern before its first glob
// character, "" for one that starts with a pattern; a negated (!) pattern excludes files and
// returns "~" so it overlaps nothing.
func literalPrefix(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if strings.HasPrefix(pattern, "!") {
		return "~"
	}
	if at := strings.IndexAny(pattern, "*?[{"); at >= 0 {
		pattern = slashpath.Dir(pattern[:at] + "x")
	}
	if pattern = slashpath.Clean(strings.TrimPrefix(pattern, "./")); pattern == "." {
		return ""
	}
	return pattern
}

// pathsOverlap reports whether two literal directories (literalPrefix) can hold a common file:
// one is the other or below it, or one is the workspace root "" and the other is relative.
func pathsOverlap(a, b string) bool {
	outside := func(p string) bool { return strings.HasPrefix(p, "~") || strings.HasPrefix(p, "/") }
	switch {
	case a == "" || b == "":
		return !outside(a) && !outside(b)
	case a == b:
		return true
	}
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

// readGoreleaserAction records a goreleaser-action step. Without args the action names no
// command, so it is read as neither a build nor a release.
func (j *jobProvenance) readGoreleaserAction(ctx context.Context, repoPath string, step *workflowStep) error {
	args, ok := step.With["args"].(string)
	if !ok {
		return nil
	}
	fields := strings.Fields(args)
	j.built = j.built || runsCommand(slices.Concat([]string{goreleaserBinary}, fields), buildCommands[:])
	return j.readGoreleaser(ctx, repoPath, fields)
}

// runsCommand reports whether fields start with one of commands: its program by base name
// (commandName), then each of its arguments as written.
func runsCommand(fields []string, commands [][]string) bool {
	for i := 0; i < len(commands); i++ {
		command := commands[i]
		if len(command) > 0 && len(fields) >= len(command) && commandName(fields[0]) == command[0] &&
			slices.Equal(fields[1:len(command)], command[1:]) {
			return true
		}
	}
	return false
}

// attestsProvenance reports whether an actions/attest step runs in provenance mode: without
// sbom-path, and either without predicate inputs or with an SLSA provenance predicate type.
func attestsProvenance(step workflowStep) bool {
	if stepInput(step, "sbom-path") != "" {
		return false
	}
	if predicateType := stepInput(step, "predicate-type"); predicateType != "" {
		return strings.HasPrefix(predicateType, slsaProvenancePredicate)
	}
	return stepInput(step, "predicate") == "" && stepInput(step, "predicate-path") == ""
}

// readScript records the commands of one run: script, command by command. What an echo or
// printf prints is not run (printedFields).
func (j *jobProvenance) readScript(fields []string) {
	printed := printedFields(fields)
	for i := 0; i < len(fields) && i < maxRunScriptFields; i++ {
		switch {
		case printed[i]:
			continue
		case invokesCosignSigning(fields[i:]):
			j.cosign = true
			j.readCosign(fields[i+1], commandSegment(fields[i+2:]))
		case invokesPraetor(fields[i:], "provenance"):
			j.generated = true
			if out := provenanceOutput(commandSegment(fields[i+2:])); out != "" {
				j.outputs = append(j.outputs, out)
			}
		case runsCommand(fields[i:], buildCommands[:]), runsMakeTarget(fields[i:]):
			j.built = true
		case runsCommand(fields[i:], importCommands[:]):
			j.imported = true
		}
	}
}

// runsMakeTarget reports whether fields start with make running a target: a bare make, or one
// that only prints, checks or describes (makeNoBuildOptions), builds nothing a step can be
// sure of.
func runsMakeTarget(fields []string) bool {
	if len(fields) == 0 || commandName(fields[0]) != makeBinary {
		return false
	}
	args := commandSegment(fields[1:])
	value, target := false, false
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		switch arg := args[i]; {
		case value:
			value = false
		case makeNoBuildOptions[arg]:
			return false
		case makeOptionTakesValue(args, i):
			value = true
		case !strings.HasPrefix(arg, "-") && !strings.Contains(arg, "="):
			target = true
		}
	}
	return target
}

// makeOptionTakesValue reports whether args[i] is a make option whose value is args[i+1]:
// always for makeValueOptions, and for makeCountOptions when a count follows.
func makeOptionTakesValue(args []string, i int) bool {
	return makeValueOptions[args[i]] || makeCountOptions[args[i]] && i+1 < len(args) && isCount(args[i+1])
}

// isCount reports whether field is a decimal count, such as the value of make -j.
func isCount(field string) bool {
	return field != "" && strings.Trim(field, "0123456789") == ""
}

// invokesCosignSigning reports whether fields start with cosign running a signing subcommand.
func invokesCosignSigning(fields []string) bool {
	return len(fields) >= 2 && commandName(fields[0]) == cosignBinary && cosignSigningCommands[fields[1]]
}

// readCosign records whether a cosign attestation signs SLSA provenance: by its --type, or by
// signing as --statement or --predicate a file this tool's provenance command wrote.
func (j *jobProvenance) readCosign(subcommand string, args []string) {
	if subcommand != "attest" && subcommand != "attest-blob" {
		return
	}
	predicateType := flagValue(args, "--type")
	if cosignProvenanceTypes[predicateType] || strings.HasPrefix(predicateType, slsaProvenancePredicate) {
		j.signedProvenance = true
		return
	}
	signed := flagValues(args, "--statement", "--predicate")
	for i := 0; i < len(signed) && i < maxRunScriptFields; i++ {
		if slices.Contains(j.outputs, signed[i]) {
			j.signedProvenance = true
		}
	}
}

// provenanceOutput returns the file this tool's provenance command writes: its -out value, or
// the target of a > redirect when it prints the statement; "" when neither is named.
func provenanceOutput(args []string) string {
	if out := flagValue(args, "-out", "--out"); out != "" {
		return out
	}
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		target, redirect := strings.CutPrefix(args[i], ">")
		if !redirect {
			continue
		}
		if target = strings.TrimPrefix(target, ">"); target == "" && i+1 < len(args) {
			return args[i+1]
		}
		return target
	}
	return ""
}

// readGoreleaser records a GoReleaser release whose configuration signs with cosign. --skip
// sign (goreleaser release --skip=sign) leaves every signing block unrun.
func (j *jobProvenance) readGoreleaser(ctx context.Context, repoPath string, args []string) error {
	config, found, err := goreleaserReleaseConfig(ctx, repoPath, args)
	if err != nil || !found || skipsSigning(args) {
		return err
	}
	j.cosign = j.cosign || config.signsWithCosign()
	return nil
}

// skipsSigning reports whether GoReleaser args skip signing: --skip takes a comma-separated
// list and may repeat.
func skipsSigning(args []string) bool {
	skipped := flagValues(args, "--skip")
	for i := 0; i < len(skipped) && i < maxRunScriptFields; i++ {
		if slices.Contains(strings.Split(skipped[i], ","), "sign") {
			return true
		}
	}
	return false
}

// signsWithCosign reports whether any signing block entry runs cosign over some artifacts. The
// defaults are GoReleaser's: signs runs gpg over no artifacts, binary_signs runs gpg over the
// binaries, and docker_signs runs cosign over the images.
func (c *goreleaserConfig) signsWithCosign() bool {
	blocks := [...]struct {
		entries               []goreleaserSign
		defaultCmd, artifacts string
	}{
		{c.Signs, "gpg", "none"},
		{c.BinarySigns, "gpg", "binary"},
		{c.DockerSigns, cosignBinary, ""},
	}
	for i := 0; i < len(blocks); i++ {
		for k := 0; k < len(blocks[i].entries) && k < maxGoreleaserSigns; k++ {
			entry := blocks[i].entries[k]
			cmd := cmp.Or(strings.TrimSpace(entry.Cmd), blocks[i].defaultCmd)
			artifacts := cmp.Or(strings.TrimSpace(entry.Artifacts), blocks[i].artifacts)
			if commandName(cmd) == cosignBinary && artifacts != "none" {
				return true
			}
		}
	}
	return false
}
