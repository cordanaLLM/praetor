// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"go.yaml.in/yaml/v3"
)

// StatusContextRename records a required job's old and new reported names.
type StatusContextRename struct {
	Old string
	New string
}

type contextRenameJob struct {
	names   []string
	callers []string
}

// RequiredStatusContextRenames proves a single rename between a fetched base commit and this
// checkout: the same workflow path and job ID must lose one context and gain one, or a workflow
// must wrap its sole changed context in a reusable caller's name. Several renames grant no
// window. Discovery uses the ruleset renderer's
// context expansion, including reusable workflows; no forge writes or fetches occur.
func RequiredStatusContextRenames(ctx context.Context, root, base, repository string, opts ...ContextOption) ([]StatusContextRename, error) {
	if ctx == nil {
		return nil, errors.New("context rename discovery requires a context")
	}
	if !config.ValidRepositoryIdentity(repository) {
		return nil, fmt.Errorf("context rename discovery needs an owner/name repository, got %q", repository)
	}
	ctx, cancel := context.WithTimeout(ctx, workflowDiscoveryTimeout)
	defer cancel()
	before, err := readCommittedWorkflowFiles(ctx, root, base)
	if err != nil {
		return nil, err
	}
	after, err := readWorkflowFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	options := newContextOptions(opts)
	oldJobs, err := requiredContextJobs(before, repository, options)
	if err != nil {
		return nil, err
	}
	newJobs, err := requiredContextJobs(after, repository, options)
	if err != nil {
		return nil, err
	}
	return singleContextRename(oldJobs, newJobs), nil
}

func singleContextRename(oldJobs, newJobs map[string]contextRenameJob) []StatusContextRename {
	if ambiguousWorkflowRenames(oldJobs, newJobs) {
		return nil
	}
	var renames []StatusContextRename
	for key, old := range oldJobs {
		newJob, exists := newJobs[key]
		if !exists {
			continue
		}
		removed, added := contextDifference(old.names, newJob.names), contextDifference(newJob.names, old.names)
		if len(removed) == 1 && len(added) == 1 {
			rename := StatusContextRename{Old: removed[0], New: added[0]}
			if !strings.Contains(key, "/") && !wrappedStatusContext(rename, newJob.callers) {
				continue
			}
			if !slices.Contains(renames, rename) {
				renames = append(renames, rename)
			}
		}
	}
	if len(renames) != 1 {
		return nil
	}
	return renames
}

// A file with several changed requirements cannot prove a single rename, even when only
// one changed job keeps its ID. Include files present on only one side of the comparison.
func ambiguousWorkflowRenames(oldJobs, newJobs map[string]contextRenameJob) bool {
	keys := maps.Clone(oldJobs)
	if keys == nil {
		keys = make(map[string]contextRenameJob)
	}
	maps.Copy(keys, newJobs)
	for key := range keys {
		if strings.Contains(key, "/") {
			continue
		}
		old, newNames := oldJobs[key].names, newJobs[key].names
		if len(contextDifference(old, newNames)) > 1 || len(contextDifference(newNames, old)) > 1 {
			return true
		}
	}
	return false
}

// requiredContextJobs indexes only required, uncovered jobs by workflow path and stable job ID.
func requiredContextJobs(files []workflowFile, repository string, opts contextOptions) (map[string]contextRenameJob, error) {
	jobs := make(map[string]contextRenameJob)
	called := newCalledWorkflows(files)
	if opts.mergeQueue {
		kept, _, err := splitMergeGroupFiles(files, called, repository)
		if err != nil {
			return nil, err
		}
		files = kept
	}
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		contexts, err := workflowContextJobs(files[i].Data, repository, called)
		if err != nil {
			return nil, fmt.Errorf("workflow %s: %w", files[i].Name, err)
		}
		var spec workflowSpec
		if err := yaml.Unmarshal(files[i].Data, &spec); err != nil {
			return nil, fmt.Errorf("workflow %s: parse: %w", files[i].Name, err)
		}
		var file contextRenameJob
		for id, names := range contexts {
			jobs[files[i].Name+"/"+id] = contextRenameJob{names: names}
			file.names = append(file.names, names...)
			if strings.TrimSpace(spec.Jobs[id].Uses) != "" {
				file.callers = append(file.callers, names...)
			}
		}
		jobs[files[i].Name] = file
	}
	return jobs, nil
}

func wrappedStatusContext(rename StatusContextRename, callers []string) bool {
	return slices.Contains(callers, rename.New) && strings.HasSuffix(rename.New, reusableContextSeparator+rename.Old)
}

func contextDifference(left, right []string) []string {
	return slices.DeleteFunc(slices.Clone(left), func(name string) bool { return slices.Contains(right, name) })
}

// UnreportedStatusContexts returns the required status contexts declared in root that the
// default branch does not report yet, either because the fetched default-branch commit does not
// declare them in a workflow that removed no existing check, or because report shows their
// workflow is not on the forge default branch yet or has no completed run on it. Positive
// report evidence overrides absence from a potentially stale fetched commit.
func UnreportedStatusContexts(ctx context.Context, root, commit, repository string, report *WorkflowRunReport, opts ...ContextOption) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("unreported status context discovery requires a context")
	}
	if !config.ValidRepositoryIdentity(repository) {
		return nil, fmt.Errorf("unreported status context discovery needs an owner/name repository, got %q", repository)
	}
	if commit == "" && report == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, workflowDiscoveryTimeout)
	defer cancel()
	after, err := readWorkflowFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	options := newContextOptions(opts)
	var unreported []string
	if commit != "" {
		unreported, err = unreportedContextsAtCommit(ctx, root, commit, repository, after, options)
		if err != nil {
			return nil, err
		}
	}
	if report != nil {
		unreported, err = unreportedContextsInRuns(after, repository, report, options, unreported)
		if err != nil {
			return nil, err
		}
	}
	return unreported, nil
}

// unreportedContextsAtCommit computes status contexts declared in after that are not yet present in commit.
func unreportedContextsAtCommit(ctx context.Context, root, commit, repository string, after []workflowFile, options contextOptions) ([]string, error) {
	before, err := readCommittedWorkflowFiles(ctx, root, commit)
	if err != nil {
		return nil, err
	}
	committed, err := selectContexts(before, newCalledWorkflows(before), repository, options)
	if err != nil {
		return nil, err
	}
	oldJobs, err := requiredContextJobs(before, repository, options)
	if err != nil {
		return nil, err
	}
	newJobs, err := requiredContextJobs(after, repository, options)
	if err != nil {
		return nil, err
	}
	var unreported []string
	for i := 0; i < len(after) && i < maxWorkflowFiles; i++ {
		name := after[i].Name
		if len(contextDifference(oldJobs[name].names, newJobs[name].names)) > 0 {
			continue
		}
		unreported = appendMissing(unreported, contextDifference(newJobs[name].names, committed))
	}
	return unreported, nil
}

// workflowFilesReportedInRuns selects workflows without negative run evidence; positiveOnly
// additionally requires explicit positive evidence in report.
func workflowFilesReportedInRuns(after []workflowFile, report *WorkflowRunReport, positiveOnly bool) []workflowFile {
	unreportedWorkflow := make(map[string]bool, len(report.Findings))
	for i := 0; i < len(report.Findings) && i < maxWorkflowFiles; i++ {
		unreportedWorkflow[report.Findings[i].Workflow] = workflowUnreportedOnDefaultBranch(report.Findings[i].Health)
	}
	reportedFiles := make([]workflowFile, 0, len(after))
	for i := 0; i < len(after) && i < maxWorkflowFiles; i++ {
		unreported, known := unreportedWorkflow[after[i].Name]
		if !unreported && (!positiveOnly || known) {
			reportedFiles = append(reportedFiles, after[i])
		}
	}
	return reportedFiles
}

// unreportedContextsInRuns combines run and commit absence, then removes contexts with
// explicit positive run evidence so a stale commit cannot excuse reported checks.
func unreportedContextsInRuns(after []workflowFile, repository string, report *WorkflowRunReport, options contextOptions, fromCommit []string) ([]string, error) {
	called := newCalledWorkflows(after)
	declared, err := selectContexts(after, called, repository, options)
	if err != nil {
		return nil, err
	}
	reported, err := selectContexts(workflowFilesReportedInRuns(after, report, false), called, repository, options)
	if err != nil {
		return nil, err
	}
	positive, err := selectContexts(workflowFilesReportedInRuns(after, report, true), called, repository, options)
	if err != nil {
		return nil, err
	}
	return contextDifference(appendMissing(fromCommit, contextDifference(declared, reported)), positive), nil
}

// workflowUnreportedOnDefaultBranch reports whether a workflow's health status indicates it has not yet reported runs on the default branch.
func workflowUnreportedOnDefaultBranch(health WorkflowHealth) bool {
	return health == WorkflowUnknown || health == WorkflowNeverRun || health == WorkflowNoCompletedRun
}
