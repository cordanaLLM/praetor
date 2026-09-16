package forge

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	setupGoPrefix     = "actions/setup-go@"
	cacheActionPrefix = "actions/cache@"
	goCacheActionPath = "./.github/actions/go-cache"
	goBuildCacheHint  = "go-build"
	maxStepsPerJob    = 128
)

// GoCacheFinding names one job that cannot keep a Go build cache of its own.
type GoCacheFinding struct {
	Workflow string
	Job      string
	Detail   string
}

func (f GoCacheFinding) String() string {
	return fmt.Sprintf("%s: job %q: %s", f.Workflow, f.Job, f.Detail)
}

// AuditGoBuildCaches reports every job whose Go build cache is shared with another job.
//
// actions/setup-go derives its cache key from go.mod alone, so the key carries no job name
// and every job in the repository resolves to the same entry. The first job to finish a cold
// run writes it and every later run restores it -- and, because the restore is an exact hit,
// never saves its own. A job compiling with -race therefore starts from objects a job that
// never enabled -race produced, and rebuilds all of them, every run, forever. Naming the same
// cache by hand in two jobs reproduces this exactly, so both shapes are reported.
func AuditGoBuildCaches(ctx context.Context, repoPath string) ([]GoCacheFinding, error) {
	if ctx == nil {
		return nil, errors.New("go build cache audit requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	owner := make(map[string]string, len(files))
	var findings []GoCacheFinding
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		found, err := auditWorkflowGoCaches(files[i].Name, files[i].Data, owner)
		if err != nil {
			return nil, err
		}
		findings = append(findings, found...)
	}
	return findings, nil
}

// auditWorkflowGoCaches audits one already-read workflow document. owner carries the
// jobs seen so far, which is what makes the cross-file uniqueness decidable.
func auditWorkflowGoCaches(name string, data []byte, owner map[string]string) ([]GoCacheFinding, error) {
	var spec workflowSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("workflow %s: parse: %w", name, err)
	}
	ids := make([]string, 0, len(spec.Jobs))
	for id := range spec.Jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var findings []GoCacheFinding
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		job := spec.Jobs[ids[i]]
		if !jobSetsUpGo(job) {
			continue
		}
		// One root cause, one finding: while setup-go owns the cache the job has no
		// identity of its own to check, and reporting both reads as two defects.
		if detail, shared := setupGoOwnsCache(job); shared {
			findings = append(findings, GoCacheFinding{Workflow: name, Job: ids[i], Detail: detail})
			continue
		}
		findings = append(findings, auditJobCacheIdentity(name, ids[i], job, owner)...)
	}
	return findings, nil
}

func jobSetsUpGo(job workflowJob) bool {
	for i := 0; i < len(job.Steps) && i < maxStepsPerJob; i++ {
		if strings.HasPrefix(job.Steps[i].Uses, setupGoPrefix) {
			return true
		}
	}
	return false
}

// setupGoOwnsCache reports whether a setup-go step still manages the caches itself.
func setupGoOwnsCache(job workflowJob) (string, bool) {
	for i := 0; i < len(job.Steps) && i < maxStepsPerJob; i++ {
		if !strings.HasPrefix(job.Steps[i].Uses, setupGoPrefix) {
			continue
		}
		if stepInput(job.Steps[i], "cache") != "false" {
			return "setup-go still owns the caches; its key carries no job name, so this " +
				"build cache is shared with every other job in the repository", true
		}
	}
	return "", false
}

// goBuildCacheIdentity returns the names separating this job's build cache from other jobs'.
func goBuildCacheIdentity(job workflowJob) []string {
	var identity []string
	for i := 0; i < len(job.Steps) && i < maxStepsPerJob; i++ {
		step := job.Steps[i]
		if step.Uses == goCacheActionPath {
			identity = append(identity, "job:"+stepInput(step, "job"))
			continue
		}
		if strings.HasPrefix(step.Uses, cacheActionPrefix) &&
			strings.Contains(stepInput(step, "path"), goBuildCacheHint) {
			identity = append(identity, "key:"+stepInput(step, "key"))
		}
	}
	return identity
}

func auditJobCacheIdentity(workflow, id string, job workflowJob, owner map[string]string) []GoCacheFinding {
	identity := goBuildCacheIdentity(job)
	if len(identity) == 0 {
		return []GoCacheFinding{{Workflow: workflow, Job: id,
			Detail: "sets Go up but restores no build cache of its own, so every run compiles from nothing"}}
	}
	holder := workflow + "/" + id
	var findings []GoCacheFinding
	for i := 0; i < len(identity) && i < maxStepsPerJob; i++ {
		previous, taken := owner[identity[i]]
		if taken && previous != holder {
			findings = append(findings, GoCacheFinding{Workflow: workflow, Job: id,
				Detail: fmt.Sprintf("build cache %s is already claimed by %s; the two jobs overwrite each other",
					identity[i], previous)})
			continue
		}
		owner[identity[i]] = holder
	}
	return findings
}

// stepInput reads one `with:` value as text. Inputs are not all strings -- `cache: false`
// parses as a bool -- so the value is formatted rather than type-asserted.
func stepInput(step workflowStep, key string) string {
	value, present := step.With[key]
	if !present {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
