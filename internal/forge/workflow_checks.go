package forge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"gopkg.in/yaml.v3"
)

const (
	maxWorkflowFiles  = 64
	maxJobsPerFile    = 64
	pullRequestEvent  = "pull_request"
	workflowPathsKey  = "paths"
	workflowIgnoreKey = "paths-ignore"
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
	repository, err := contextopt.OpenDirectory(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	if err := repository.Close(); err != nil {
		return nil, err
	}
	root, err := contextopt.OpenDirectory(ctx, filepath.Join(repoPath, ".github", "workflows"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	names, err := workflowNames(root)
	if err != nil {
		return nil, err
	}
	var contexts []string
	for i := 0; i < len(names) && i < maxWorkflowFiles; i++ {
		data, err := contextopt.ReadRootSnapshot(ctx, root, names[i])
		if err != nil {
			return nil, fmt.Errorf("workflow %s: %w", names[i], err)
		}
		jobs, err := workflowPullRequestContexts(data)
		if err != nil {
			return nil, fmt.Errorf("workflow %s: %w", names[i], err)
		}
		contexts = append(contexts, jobs...)
	}
	return contexts, nil
}

func workflowNames(root *os.Root) (_ []string, err error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, directory.Close()) }()
	entries, err := directory.ReadDir(maxWorkflowFiles + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > maxWorkflowFiles {
		return nil, fmt.Errorf("workflow inventory exceeds %d entries", maxWorkflowFiles)
	}
	var names []string
	for i := 0; i < len(entries) && i < maxWorkflowFiles; i++ {
		name := entries[i].Name()
		if !entries[i].IsDir() && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// workflowSpec is the workflow subset used to select required check contexts.
type workflowSpec struct {
	On   yaml.Node              `yaml:"on"`
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	Name string `yaml:"name"`
	If   string `yaml:"if"`
}

// workflowPullRequestContexts returns the check contexts of one workflow file, or nil
// when the workflow does not run unconditionally on pull requests.
func workflowPullRequestContexts(data []byte) ([]string, error) {
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
	ids := make([]string, 0, len(spec.Jobs))
	for id := range spec.Jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var contexts []string
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		job := spec.Jobs[ids[i]]
		if strings.TrimSpace(job.If) != "" {
			continue
		}
		if job.Name != "" {
			contexts = append(contexts, job.Name)
			continue
		}
		contexts = append(contexts, ids[i])
	}
	return contexts, nil
}

// triggersOnEveryPullRequest reports whether an "on" node declares a pull_request
// trigger without paths filters (a filtered trigger does not report on every PR).
func triggersOnEveryPullRequest(on *yaml.Node) bool {
	switch on.Kind {
	case yaml.ScalarNode:
		return on.Value == pullRequestEvent
	case yaml.SequenceNode:
		for i := 0; i < len(on.Content) && i < maxJobsPerFile; i++ {
			if on.Content[i].Value == pullRequestEvent {
				return true
			}
		}
		return false
	case yaml.MappingNode:
		return mappingHasUnfilteredPullRequest(on)
	default:
		return false
	}
}

// mappingHasUnfilteredPullRequest inspects an "on:" mapping for a pull_request entry
// whose own mapping carries neither paths nor paths-ignore.
func mappingHasUnfilteredPullRequest(on *yaml.Node) bool {
	for i := 0; i+1 < len(on.Content) && i < 2*maxJobsPerFile; i += 2 {
		if on.Content[i].Value != pullRequestEvent {
			continue
		}
		value := on.Content[i+1]
		if value.Kind != yaml.MappingNode {
			return true
		}
		for j := 0; j+1 < len(value.Content) && j < 2*maxJobsPerFile; j += 2 {
			key := value.Content[j].Value
			if key == workflowPathsKey || key == workflowIgnoreKey {
				return false
			}
		}
		return true
	}
	return false
}
