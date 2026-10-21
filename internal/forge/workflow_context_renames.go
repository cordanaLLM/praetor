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
