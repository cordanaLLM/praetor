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
	workflowPathsKey       = "paths"
	workflowIgnoreKey      = "paths-ignore"
)

// RequiredStatusContexts selects unconditional job names from repository workflows
// with unfiltered pull_request triggers. It never substitutes Praetor's own gates.
// Reads are bounded and reject symlink paths; incomplete inventories fail.
func RequiredStatusContexts(ctx context.Context, repoPath string) (_ []string, err error) {
	if ctx == nil {
		return nil, errors.New("workflow context discovery requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	identity, err := guardIdentity(files, repoPath)
	if err != nil {
		return nil, err
	}
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

// workflowStep is the step subset the Go cache audit and the portability checks decide on.
// `with:` values are not all strings -- `cache: false` is a bool, `fetch-depth: 0` an int --
// so the map is untyped.
type workflowStep struct {
	Name string         `yaml:"name"`
	If   string         `yaml:"if"`
	Uses string         `yaml:"uses"`
	Run  string         `yaml:"run"`
	With map[string]any `yaml:"with"`
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
// is known, so every job condition makes its job conditional.
func workflowPullRequestContexts(data []byte) ([]string, error) {
	return workflowContextsIn(data, "")
}

// workflowContextsIn is workflowPullRequestContexts inside the repository named identity
// ("<owner>/<name>", or "" when unknown). A job whose only condition is a repository guard
// that holds for identity reports on every pull request there and stays a required check.
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
		if strings.TrimSpace(job.If) != "" && !guardHoldsInRepository(job.If, identity) {
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
// on. Both events are reported because a permission a contributor can reach is the subject
// of the audit, and pull_request_target hands that contributor the base repository's own
// token. The order is fixed so a finding reads the same way every run.
func pullRequestTriggers(on *yaml.Node) []string {
	var events []string
	for _, event := range [...]string{pullRequestEvent, pullRequestTargetEvent} {
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
