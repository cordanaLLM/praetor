// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
)

// StatusContextRename records a required job's old and new reported names.
type StatusContextRename struct {
	Old string
	New string
}

// RequiredStatusContextRenames proves a single rename between a fetched base commit and this
// checkout: the same workflow path and job ID must lose one context and gain one, or a workflow
// must wrap its sole changed context in a reusable caller's name. Several renames grant no
// window. Discovery uses the ruleset renderer's
// context expansion, including reusable workflows; no forge writes or fetches occur.
func RequiredStatusContextRenames(ctx context.Context, root, base, repository string) ([]StatusContextRename, error) {
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
	oldJobs, err := requiredContextJobs(before, repository)
	if err != nil {
		return nil, err
	}
	newJobs, err := requiredContextJobs(after, repository)
	if err != nil {
		return nil, err
	}
	return singleContextRename(oldJobs, newJobs), nil
}

func singleContextRename(oldJobs, newJobs map[string][]string) []StatusContextRename {
	var renames []StatusContextRename
	for key, old := range oldJobs {
		newNames, exists := newJobs[key]
		if !exists {
			continue
		}
		removed, added := contextDifference(old, newNames), contextDifference(newNames, old)
		if len(removed) == 1 && len(added) == 1 {
			rename := StatusContextRename{Old: removed[0], New: added[0]}
			if !strings.Contains(key, "/") && !wrappedStatusContext(rename) {
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

// requiredContextJobs indexes only required, uncovered jobs by workflow path and stable job ID.
func requiredContextJobs(files []workflowFile, repository string) (map[string][]string, error) {
	jobs := make(map[string][]string)
	called := newCalledWorkflows(files)
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		contexts, err := workflowContextJobs(files[i].Data, repository, called)
		if err != nil {
			return nil, fmt.Errorf("workflow %s: %w", files[i].Name, err)
		}
		for id, names := range contexts {
			jobs[files[i].Name+"/"+id] = names
			jobs[files[i].Name] = append(jobs[files[i].Name], names...)
		}
	}
	return jobs, nil
}

func wrappedStatusContext(rename StatusContextRename) bool {
	return strings.HasSuffix(rename.New, reusableContextSeparator+rename.Old) ||
		strings.HasSuffix(rename.Old, reusableContextSeparator+rename.New)
}

func contextDifference(left, right []string) []string {
	return slices.DeleteFunc(slices.Clone(left), func(name string) bool { return slices.Contains(right, name) })
}
