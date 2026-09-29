// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// maxRunnerLabelsPerJob bounds the labels read from one job's runs-on (HISS-02).
	maxRunnerLabelsPerJob = 64
	// runsOnLabelsKey is the key of a runs-on mapping that holds its labels; the mapping's
	// other key, group, names a runner group, not a label.
	runsOnLabelsKey = "labels"
)

// WorkflowRunnerLabels lists the runner labels a workflow document's jobs run on, each once,
// job by job in job ID order: a runs-on string, every string of a runs-on list, and every
// label under a runs-on mapping's labels key. A value holding an expression (${{ ... }}) is
// resolved only when the workflow runs, so it names no label here; nor does a runner group.
// The document is read through the parser every workflow audit here uses (workflowSpec), and
// one with more jobs than the package bounds (maxJobsPerFile) is refused rather than read in
// part.
func WorkflowRunnerLabels(data []byte) ([]string, error) {
	var spec workflowSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse workflow: %w", err)
	}
	if len(spec.Jobs) > maxJobsPerFile {
		return nil, fmt.Errorf("workflow exceeds %d jobs", maxJobsPerFile)
	}
	ids := sortedJobIDs(spec.Jobs)
	var labels []string
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		runsOn := spec.Jobs[ids[i]].RunsOn
		for _, label := range runnerLabels(&runsOn) {
			if !slices.Contains(labels, label) {
				labels = append(labels, label)
			}
		}
	}
	return labels, nil
}

// runnerLabels returns the literal labels of one runs-on value, in order.
func runnerLabels(runsOn *yaml.Node) []string {
	entries := runnerLabelNodes(runsOn)
	labels := make([]string, 0, len(entries))
	for i := 0; i < len(entries) && i < maxRunnerLabelsPerJob; i++ {
		label := strings.TrimSpace(entries[i].Value)
		if entries[i].Kind == yaml.ScalarNode && label != "" && !strings.Contains(label, "${{") {
			labels = append(labels, label)
		}
	}
	return labels
}

// runnerLabelNodes returns the nodes one runs-on value names its labels with: the value itself
// when it is a string, its entries when it is a list, and the same of its labels value when it
// is a mapping.
func runnerLabelNodes(runsOn *yaml.Node) []*yaml.Node {
	value := runsOn
	if value.Kind == yaml.MappingNode {
		value = nil
		for i := 0; i+1 < len(runsOn.Content) && i < 2*maxRunnerLabelsPerJob; i += 2 {
			if runsOn.Content[i].Value == runsOnLabelsKey {
				value = runsOn.Content[i+1]
			}
		}
	}
	switch {
	case value == nil:
		return nil
	case value.Kind == yaml.ScalarNode:
		return []*yaml.Node{value}
	case value.Kind == yaml.SequenceNode:
		return value.Content
	default:
		return nil
	}
}
