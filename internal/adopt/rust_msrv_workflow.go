// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// Order: engine-fr-rule-checks-20261027. Only literal, straight-line commands prove a build.
var msrvCommandWord = regexp.MustCompile(`^[A-Za-z0-9_./:+=-]+$`)

func rustMSRVWorkflowProofs(files map[string][]byte) (map[string]string, error) {
	proofs := map[string]string{}
	var names []string
	for name := range files {
		if ghworkflow.IsWorkflowPath(name) {
			names = append(names, name)
		}
	}
	if len(names) > ghworkflow.MaxFiles {
		return nil, fmt.Errorf("workflow inventory exceeds %d files", ghworkflow.MaxFiles)
	}
	slices.Sort(names)
	for i := 0; i < len(names) && i < ghworkflow.MaxFiles; i++ {
		spec, err := ghworkflow.Parse(files[names[i]])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", names[i], err)
		}
		addRustMSRVJobProofs(proofs, names[i], spec)

	}
	return proofs, nil
}

func addRustMSRVJobProofs(proofs map[string]string, name string, spec ghworkflow.Spec) {
	ids := ghworkflow.SortedJobIDs(spec.Jobs)
	for j := 0; j < len(ids) && j < ghworkflow.MaxJobsPerFile; j++ {
		job := spec.Jobs[ids[j]]
		versions := rustMSRVJobVersions(job)
		for k := 0; k < len(versions) && k < ghworkflow.MaxStepsPerJob; k++ {
			proofs[versions[k]] = name + " job " + ids[j]
		}
	}
}

func rustMSRVRunnable(condition, continueOnError string) bool {
	return !ghworkflow.NeverRuns(condition) && (continueOnError == "" || continueOnError == "false")
}

func rustMSRVJobVersions(job ghworkflow.Job) []string {
	if !rustMSRVRunnable(job.If, job.ContinueOnError) {
		return nil
	}
	installed := map[string]bool{}
	active := ""
	var versions []string
	for i := 0; i < len(job.Steps) && i < ghworkflow.MaxStepsPerJob; i++ {
		step := job.Steps[i]
		if !rustMSRVRunnable(step.If, step.ContinueOnError) {
			continue
		}
		if version := rustMSRVActionVersion(step); version != "" {
			installed[version] = true
			active = version
		}
		built, known := rustMSRVRunVersions(step.Run, installed, active)
		if !known {
			active = ""
			installed = map[string]bool{}
		}
		versions = append(versions, built...)

	}
	return versions
}

func rustMSRVRunVersions(script string, installed map[string]bool, active string) ([]string, bool) {
	var versions []string
	lines := strings.Split(script, "\n")
	if len(lines) > 4096 {
		return nil, false
	}
	for j := 0; j < len(lines) && j < 4096; j++ {
		text := strings.TrimSpace(lines[j])
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		words := strings.Fields(text)
		if !rustMSRVLiteralCommand(words) {
			return nil, false
		}
		if rustMSRVInstall(words) {
			installed[words[3]] = true
			continue
		}
		if words[0] != "cargo" {
			return nil, false
		}
		if version := rustMSRVCargoVersion(words, active); installed[version] {
			versions = append(versions, version)
		}
	}
	return versions, true
}

func rustMSRVLiteralCommand(words []string) bool {
	for i := 0; i < len(words) && i < 4096; i++ {
		if !msrvCommandWord.MatchString(words[i]) || words[i] == "--ignore-rust-version" {
			return false
		}
	}
	return len(words) <= 4096
}

func rustMSRVInstall(words []string) bool {
	return len(words) == 4 && strings.Join(words[:3], " ") == "rustup toolchain install" && msrvVersion.MatchString(words[3])
}

func rustMSRVActionVersion(step ghworkflow.Step) string {
	action, ref, found := strings.Cut(step.Uses, "@")
	if !found {
		return ""
	}
	switch action {
	case "dtolnay/rust-toolchain", "actions-rs/toolchain", "actions-rust-lang/setup-rust-toolchain":
	default:
		return ""
	}
	if version, hasInput := step.With["toolchain"]; hasInput {
		text, ok := version.(string)
		if !ok {
			return "<unresolved>"
		}
		return text
	}
	if action == "dtolnay/rust-toolchain" {
		return ref
	}
	return "<unresolved>"
}

func rustMSRVCargoVersion(words []string, active string) string {
	if len(words) < 2 || words[0] != "cargo" {
		return ""
	}
	if strings.ContainsAny(strings.Join(words, " "), ";|&`$") {
		return ""
	}
	command := 1
	if strings.HasPrefix(words[1], "+") {
		active = strings.TrimPrefix(words[1], "+")
		command++
	}
	if command >= len(words) {
		return ""
	}
	switch words[command] {
	case "build", "check", "test":
		return active
	default:
		return ""
	}
}
