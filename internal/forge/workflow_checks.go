package forge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"gopkg.in/yaml.v3"
)

const (
	maxWorkflowFiles = 64
	maxJobsPerFile   = 64
	maxMatrixLegs    = 64
	pullRequestEvent = "pull_request"
	// pull_request_target runs a contributor's branch in the base repository's context
	// with the base repository's token, so every audit that decides on pull_request has
	// to decide on it too: it is the strictly more dangerous of the two.
	pullRequestTargetEvent = "pull_request_target"
	// issue_comment also runs in the base repository's context with the base repository's
	// token, and anyone who can comment on a pull request starts it. A permission audit that
	// decides on the two pull request events has to decide on it as well.
	issueCommentEvent = "issue_comment"
	workflowPathsKey  = "paths"
	workflowIgnoreKey = "paths-ignore"
	// workflowDiscoveryTimeout bounds one required-context discovery (HISS-02).
	workflowDiscoveryTimeout = 30 * time.Second
)

// RequiredStatusContexts selects the names of the jobs that report on every pull request
// (reportsOnEveryPullRequest) from repository workflows with unfiltered pull_request triggers.
// It never substitutes Praetor's own gates. Reads are bounded and reject symlink paths;
// incomplete inventories fail.
func RequiredStatusContexts(ctx context.Context, repoPath string) ([]string, error) {
	return RequiredStatusContextsPlanned(ctx, repoPath, nil)
}

// RequiredStatusContextsPlanned is RequiredStatusContexts over the workflows repoPath holds once
// planned is applied: planned maps a repository-relative slash path to the content a caller
// would write there, or to nil for a file it would remove (overlayWorkflowFiles). An entry that
// is not a workflow document directly under .github/workflows is not read. A nil planned reads
// the workflows on disk alone.
func RequiredStatusContextsPlanned(ctx context.Context, repoPath string, planned map[string][]byte) (_ []string, err error) {
	if ctx == nil {
		return nil, errors.New("workflow context discovery requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, workflowDiscoveryTimeout)
	defer cancel()
	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	files, err = overlayWorkflowFiles(files, planned)
	if err != nil {
		return nil, err
	}
	identity, err := guardIdentity(files, repoPath)
	if err != nil {
		return nil, err
	}
	return filesContextsIn(files, identity)
}

// RequiredStatusContextsIn is RequiredStatusContexts for the checks that report inside the forge
// repository named repository ("<owner>/<name>"), the one a remote sync writes its ruleset to. A
// repository guard holds there only when its literal names that repository.
//
// RequiredStatusContexts judges a guard against the manifest identity instead, which an
// operational fork resolves to the canonical repository (repository.source, #255) so that its
// checked-in ruleset matches the canonical one. That is right for the file and wrong for the
// fork's forge: there the guard is false, a matrix job it skips is skipped before its matrix
// expands, and none of its per-leg contexts is ever reported (actions/runner#952). Requiring them
// would leave every pull request of the fork waiting forever.
func RequiredStatusContextsIn(ctx context.Context, repoPath, repository string) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("workflow context discovery requires a context")
	}
	if !config.ValidRepositoryIdentity(repository) {
		return nil, fmt.Errorf("required status contexts need an owner/name repository, got %q", repository)
	}
	ctx, cancel := context.WithTimeout(ctx, workflowDiscoveryTimeout)
	defer cancel()
	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	return filesContextsIn(files, repository)
}

// filesContextsIn collects, in file order, the required check contexts of every workflow in files
// inside the repository named identity (workflowContextsIn).
func filesContextsIn(files []workflowFile, identity string) ([]string, error) {
	var contexts []string
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		jobs, err := workflowContextsIn(files[i].Data, identity)
		if err != nil {
			return nil, fmt.Errorf("workflow %s: %w", files[i].Name, err)
		}
		contexts = append(contexts, jobs...)
	}
	return contexts, nil
}

// workflowFile is one workflow document, read once and reused by every workflow audit
// so the bounded, symlink-rejecting read has a single implementation.
type workflowFile struct {
	Name string
	Data []byte
}

// plannedWorkflowDir is the directory whose documents a planned write or removal replaces in a
// workflow inventory, in the slash form planned paths use.
const plannedWorkflowDir = ".github/workflows/"

// overlayWorkflowFiles applies planned to files, the workflows on disk in name order: a planned
// workflow document directly under .github/workflows replaces or adds the file of its name, and
// a nil one removes it (applyPlannedWorkflows). Every other planned path is not a workflow and
// is left out. The result is in name order, as readWorkflowFiles returns it, and holds at most
// maxWorkflowFiles documents.
func overlayWorkflowFiles(files []workflowFile, planned map[string][]byte) ([]workflowFile, error) {
	if len(planned) == 0 {
		return files, nil
	}
	byName := make(map[string][]byte, len(files)+len(planned))
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		byName[files[i].Name] = files[i].Data
	}
	applyPlannedWorkflows(byName, planned)
	if len(byName) > maxWorkflowFiles {
		return nil, fmt.Errorf("workflow inventory exceeds %d entries", maxWorkflowFiles)
	}
	names := slices.Sorted(maps.Keys(byName))
	overlaid := make([]workflowFile, 0, len(names))
	for i := 0; i < len(names) && i < maxWorkflowFiles; i++ {
		overlaid = append(overlaid, workflowFile{Name: names[i], Data: byName[names[i]]})
	}
	return overlaid, nil
}

// applyPlannedWorkflows writes each planned workflow document into byName under its file name,
// or deletes that name for a nil one, in path order.
func applyPlannedWorkflows(byName map[string][]byte, planned map[string][]byte) {
	paths := slices.Sorted(maps.Keys(planned))
	for i := 0; i < len(paths) && i < len(planned); i++ {
		name, ok := plannedWorkflowName(paths[i])
		if !ok {
			continue
		}
		if data := planned[paths[i]]; data != nil {
			byName[name] = data
		} else {
			delete(byName, name)
		}
	}
}

// plannedWorkflowName returns the file name of a planned path that is a workflow document
// directly under .github/workflows, and false for any other path.
func plannedWorkflowName(path string) (string, bool) {
	name, ok := strings.CutPrefix(path, plannedWorkflowDir)
	if !ok || strings.Contains(name, "/") || !isYAMLDocument(name) {
		return "", false
	}
	return name, true
}

// readWorkflowFiles reads every workflow document under .github/workflows in name order.
// A repository without the directory yields no files rather than an error.
func readWorkflowFiles(ctx context.Context, repoPath string) (_ []workflowFile, err error) {
	repository, err := contextopt.OpenDirectory(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	if err := repository.Close(); err != nil {
		return nil, err
	}
	root, err := contextopt.OpenDirectoryIn(ctx, repoPath, filepath.Join(".github", "workflows"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	names, err := boundedNames(root, maxWorkflowFiles, "workflow", isWorkflowDocument)
	if err != nil {
		return nil, err
	}
	files := make([]workflowFile, 0, len(names))
	for i := 0; i < len(names) && i < maxWorkflowFiles; i++ {
		data, err := contextopt.ReadRootSnapshot(ctx, root, names[i])
		if err != nil {
			return nil, fmt.Errorf("workflow %s: %w", names[i], err)
		}
		files = append(files, workflowFile{Name: names[i], Data: data})
	}
	return files, nil
}

// boundedNames lists a pinned directory's entries in name order, keeping the ones keep
// selects. A listing larger than bound is refused rather than truncated, so the tree being
// read cannot make a scan unbounded or make it miss an entry in silence. inventory names
// what is being listed, so a refusal says which directory grew.
//
// Every bounded listing in this package goes through here. The workflow reader, the
// container-template reader and the composite-action reader differ only in their bound and
// their predicate, and a second copy of the listing would drift from this one (HISS-19).
func boundedNames(root *os.Root, bound int, inventory string, keep func(os.DirEntry) bool) (_ []string, err error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, directory.Close()) }()
	entries, err := directory.ReadDir(bound + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > bound {
		return nil, fmt.Errorf("%s inventory exceeds %d entries", inventory, bound)
	}
	var names []string
	for i := 0; i < len(entries) && i < bound; i++ {
		if keep(entries[i]) {
			names = append(names, entries[i].Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// isWorkflowDocument selects the YAML files GitHub reads out of .github/workflows.
func isWorkflowDocument(entry os.DirEntry) bool {
	return !entry.IsDir() && isYAMLDocument(entry.Name())
}

// isYAMLDocument reports whether a name is read by the YAML parser rather than as text.
func isYAMLDocument(name string) bool {
	return strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")
}

// workflowSpec is the workflow subset used to select required check contexts.
//
// Permissions is a raw node because the key has three shapes -- a shorthand scalar, a
// scope mapping, and absence, which is not the same as an empty mapping.
type workflowSpec struct {
	On          yaml.Node              `yaml:"on"`
	Permissions yaml.Node              `yaml:"permissions"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Name            string           `yaml:"name"`
	If              string           `yaml:"if"`
	ContinueOnError string           `yaml:"continue-on-error"`
	Permissions     yaml.Node        `yaml:"permissions"`
	Strategy        workflowStrategy `yaml:"strategy"`
	Steps           []workflowStep   `yaml:"steps"`
}

// workflowStep is the step subset the Go cache audit and the portability checks decide on,
// plus the id and env the adopt workflow's credential checks follow a value through
// (internal/forge/adopt_workflow_test.go). `with:` values are not all strings --
// `cache: false` is a bool, `fetch-depth: 0` an int -- so the map is untyped.
//
// Env is a raw node because the key has two shapes: a mapping, or one expression such as
// `${{ fromJSON(vars.X) }}` that evaluates to one (the workflow schema gives step env a
// context). A typed map rejects the second shape and would fail the whole document for a key
// no production check reads.
//
// Shell is the step's `shell:`; empty is the runner default, which is pwsh on Windows.
type workflowStep struct {
	Name  string         `yaml:"name"`
	ID    string         `yaml:"id"`
	If    string         `yaml:"if"`
	Uses  string         `yaml:"uses"`
	Run   string         `yaml:"run"`
	Shell string         `yaml:"shell"`
	With  map[string]any `yaml:"with"`
	Env   yaml.Node      `yaml:"env"`
}

// workflowStrategy carries the matrix legs a job expands into. A matrix job reports one
// check per leg, so one `name:` here is several required contexts on the forge.
type workflowStrategy struct {
	Matrix struct {
		Include []map[string]string `yaml:"include"`
	} `yaml:"matrix"`
}

// workflowPullRequestContexts returns the check contexts of one workflow file, or nil
// when the workflow does not run unconditionally on pull requests. No repository identity
// is known, so a repository guard makes its job conditional.
func workflowPullRequestContexts(data []byte) ([]string, error) {
	return workflowContextsIn(data, "")
}

// workflowContextsIn is workflowPullRequestContexts inside the repository named identity
// ("<owner>/<name>", or "" when unknown). Only a job that reports on every pull request there
// (reportsOnEveryPullRequest) is a required check.
func workflowContextsIn(data []byte, identity string) ([]string, error) {
	var spec workflowSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	if !triggersOnEveryPullRequest(&spec.On) {
		return nil, nil
	}
	if len(spec.Jobs) > maxJobsPerFile {
		return nil, fmt.Errorf("workflow exceeds %d jobs", maxJobsPerFile)
	}
	ids := sortedJobIDs(spec.Jobs)
	var contexts []string
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		job := spec.Jobs[ids[i]]
		if !reportsOnEveryPullRequest(job.If, identity) {
			continue
		}
		// An advisory leg is reported to the forge as successful whether or not it passed,
		// so requiring it would install a check that can never fail. That is the same
		// unfalsifiable green this invariant exists to forbid, arrived at from the other
		// side. Advisory legs stay advisory; they do not become required checks.
		if advisoryJob(job.ContinueOnError) {
			continue
		}
		names, err := jobCheckContexts(ids[i], job)
		if err != nil {
			return nil, err
		}
		contexts = append(contexts, names...)
	}
	return contexts, nil
}

// everyRunConditions are the job conditions, whitespace removed, that consist of status
// functions alone and hold on every run that is not cancelled. `success() || failure()` and
// `!cancelled()` are one condition spelled two ways; always() holds on a cancelled run too.
var everyRunConditions = []string{"always()", "!cancelled()", "success()||failure()", "failure()||success()"}

// reportsOnEveryPullRequest reports whether a job carrying condition reports its check on every
// pull request run of its workflow inside the repository named identity: an unconditional job,
// one whose condition holds on every run (holdsOnEveryRun), such as an aggregate merge gate that
// needs path-filtered lanes, one whose condition is a disjunction with a term that holds on every
// pull request run (holdsOnEveryPullRequestRun), and one whose only condition is a repository
// guard that holds there (guardHoldsInRepository).
//
// Any other condition, such as a lane that runs only when a planner job's output selects it,
// skips its job on some pull requests. GitHub reports a job a condition skipped as successful, so
// requiring it would install a check that passes whether or not its work ran.
func reportsOnEveryPullRequest(condition, identity string) bool {
	return strings.TrimSpace(condition) == "" || holdsOnEveryRun(condition) ||
		holdsOnEveryPullRequestRun(condition) || guardHoldsInRepository(condition, identity)
}

// holdsOnEveryRun reports whether a job condition is one of everyRunConditions, bare or as one
// whole ${{ }} expression. A status function joined with anything else is not: the value of the
// rest is not knowable from the file.
func holdsOnEveryRun(condition string) bool {
	expression := strings.TrimSpace(condition)
	if inner, wrapped := strings.CutPrefix(expression, "${{"); wrapped {
		body, closed := strings.CutSuffix(inner, "}}")
		if !closed {
			return false
		}
		expression = body
	}
	return slices.Contains(everyRunConditions, strings.Join(strings.Fields(expression), ""))
}

// advisoryJob reports whether continue-on-error makes a job's result non-binding. An
// expression is treated as advisory: its value is not knowable from the file, and assuming
// the binding case would require a check that may always report success.
func advisoryJob(continueOnError string) bool {
	value := strings.TrimSpace(continueOnError)
	return value != "" && value != "false"
}

// jobCheckContexts returns every check context one job reports under, expanding a matrix
// name into one context per leg.
func jobCheckContexts(id string, job workflowJob) ([]string, error) {
	if job.Name == "" {
		return []string{id}, nil
	}
	if !strings.Contains(job.Name, "${{") {
		return []string{job.Name}, nil
	}
	return expandMatrixName(id, job.Name, job.Strategy.Matrix.Include)
}

// expandMatrixName substitutes ${{ matrix.<key> }} in a job name from each include leg.
//
// An unresolved expression must never reach the ruleset. A required status check whose
// context no run can ever report does not fail the pull request, it leaves it "expected"
// forever -- so the branch would be permanently unmergeable by a generator that thought it
// was protecting it. Emitting the literal text is the same defect class this repository
// keeps removing: a value nothing evaluated, presented as one something did.
func expandMatrixName(id, name string, include []map[string]string) ([]string, error) {
	if len(include) == 0 {
		return nil, fmt.Errorf("job %q: name %q references a matrix but declares no strategy.matrix.include", id, name)
	}
	if len(include) > maxMatrixLegs {
		return nil, fmt.Errorf("job %q: matrix exceeds %d legs", id, maxMatrixLegs)
	}
	contexts := make([]string, 0, len(include))
	for i := 0; i < len(include) && i < maxMatrixLegs; i++ {
		expanded := substituteMatrixKeys(name, include[i])
		if strings.Contains(expanded, "${{") {
			return nil, fmt.Errorf("job %q: leg %d leaves %q unresolved; a required check context that no run reports blocks the branch permanently", id, i, expanded)
		}
		contexts = append(contexts, expanded)
	}
	return contexts, nil
}

// substituteMatrixKeys replaces every ${{ matrix.<key> }} form of one leg's keys.
func substituteMatrixKeys(name string, leg map[string]string) string {
	keys := make([]string, 0, len(leg))
	for key := range leg {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i := 0; i < len(keys) && i < maxMatrixLegs; i++ {
		name = strings.ReplaceAll(name, "${{ matrix."+keys[i]+" }}", leg[keys[i]])
		name = strings.ReplaceAll(name, "${{matrix."+keys[i]+"}}", leg[keys[i]])
	}
	return name
}

// eventTrigger reports whether an "on" node declares the named trigger, and returns that
// trigger's own value node when the declaration is a mapping entry that has one. A trigger
// declared as a scalar or inside a sequence carries no value node.
func eventTrigger(on *yaml.Node, event string) (*yaml.Node, bool) {
	switch on.Kind {
	case yaml.ScalarNode:
		return nil, on.Value == event
	case yaml.SequenceNode:
		for i := 0; i < len(on.Content) && i < maxJobsPerFile; i++ {
			if on.Content[i].Value == event {
				return nil, true
			}
		}
		return nil, false
	case yaml.MappingNode:
		for i := 0; i+1 < len(on.Content) && i < 2*maxJobsPerFile; i += 2 {
			if on.Content[i].Value == event {
				return on.Content[i+1], true
			}
		}
		return nil, false
	default:
		return nil, false
	}
}

// pullRequestTriggers lists the contributor-triggered pull request events this workflow
// declares, filtered or not: a paths filter narrows which pull requests run it, but it
// does not stop the run being a pull request run, which is what a permission audit decides
// on. All three events are reported because a permission a contributor can reach is the
// subject of the audit: pull_request_target hands that contributor the base repository's
// own token, and so does issue_comment, which any commenter starts by writing a comment.
// The order is fixed so a finding reads the same way every run.
func pullRequestTriggers(on *yaml.Node) []string {
	var events []string
	for _, event := range [...]string{pullRequestEvent, pullRequestTargetEvent, issueCommentEvent} {
		if _, declared := eventTrigger(on, event); declared {
			events = append(events, event)
		}
	}
	return events
}

// triggersOnEveryPullRequest reports whether an "on" node declares a pull_request
// trigger without paths filters (a filtered trigger does not report on every PR).
func triggersOnEveryPullRequest(on *yaml.Node) bool {
	value, declared := eventTrigger(on, pullRequestEvent)
	return declared && !filtersPaths(value)
}

// filtersPaths reports whether a trigger's value node carries paths or paths-ignore.
func filtersPaths(value *yaml.Node) bool {
	if value == nil || value.Kind != yaml.MappingNode {
		return false
	}
	for j := 0; j+1 < len(value.Content) && j < 2*maxJobsPerFile; j += 2 {
		key := value.Content[j].Value
		if key == workflowPathsKey || key == workflowIgnoreKey {
			return true
		}
	}
	return false
}

// sortedJobIDs returns a workflow's job identifiers in a stable order, so every audit
// reports the same jobs in the same sequence from the same file.
func sortedJobIDs(jobs map[string]workflowJob) []string {
	return slices.Sorted(maps.Keys(jobs))
}
