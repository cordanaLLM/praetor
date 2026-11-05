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
	"time"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// MeasureReleasePublishers lists workflows that create, upload or publish GitHub releases.
// It shares the bounded workflow and shell readers used by the other release measurements.
func MeasureReleasePublishers(ctx context.Context, repoPath string) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("release publisher measurement requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("read release publishers: %w", err)
	}
	var publishers []string
	for i := 0; i < len(files) && i < ghworkflow.MaxFiles; i++ {
		publishes, err := workflowPublishesRelease(files[i].Data)
		if err != nil {
			return nil, fmt.Errorf("workflow %s/%s: %w", ghworkflow.Dir, files[i].Name, err)
		}
		if publishes {
			publishers = append(publishers, ghworkflow.Dir+"/"+files[i].Name)
		}
	}
	return publishers, nil
}

// workflowPublishesRelease checks executable steps rather than names, comments or printed text.
func workflowPublishesRelease(data []byte) (bool, error) {
	spec, err := ghworkflow.Parse(data)
	if err != nil {
		return false, fmt.Errorf("read release publisher: %w", err)
	}
	ids := ghworkflow.SortedJobIDs(spec.Jobs)
	found := false
	for i := 0; i < len(ids) && i < ghworkflow.MaxJobsPerFile; i++ {
		publishes, err := jobPublishesRelease(spec.Jobs[ids[i]])
		if err != nil {
			return false, fmt.Errorf("job %s: %w", ids[i], err)
		}
		found = found || publishes
	}
	return found, nil
}

// jobPublishesRelease refuses oversized jobs and ignores steps that can never execute.
func jobPublishesRelease(job ghworkflow.Job) (bool, error) {
	if len(job.Steps) > ghworkflow.MaxStepsPerJob {
		return false, fmt.Errorf("release publisher job exceeds %d steps", ghworkflow.MaxStepsPerJob)
	}
	if ghworkflow.NeverRuns(job.If) {
		return false, nil
	}
	for i := 0; i < len(job.Steps) && i < ghworkflow.MaxStepsPerJob; i++ {
		step := job.Steps[i]
		if ghworkflow.NeverRuns(step.If) {
			continue
		}
		if actionPublishesRelease(step) {
			return true, nil
		}
		publishes, err := scriptPublishesRelease(step.Run)
		if err != nil {
			return false, fmt.Errorf("step %d: %w", i+1, err)
		}
		if publishes {
			return true, nil
		}
	}
	return false, nil
}

// scriptPublishesRelease reads actual commands with the shared shell parser without execution.
func scriptPublishesRelease(script string) (bool, error) {
	fields, err := scriptFields(script)
	if err != nil {
		return false, fmt.Errorf("read release publisher command: %w", err)
	}
	commands := scriptCommands(fields)
	for i := 0; i < len(commands) && i < maxRunScriptFields; i++ {
		cmd := parseShellCommand(commands[i])
		if cmd.program == "gh" && ghCommandPublishesRelease(cmd.args) {
			return true, nil
		}
	}
	return false, nil
}

// ghCommandPublishesRelease recognizes release creation, asset uploads and draft publication.
func ghCommandPublishesRelease(args []string) bool {
	if len(args) < 2 || args[0] != "release" {
		return false
	}
	switch args[1] {
	case "create", "upload":
		return true
	case "edit":
		return flagValue(args[2:], "--draft") == "false"
	}
	return false
}

// releaseActions create or publish a GitHub release whenever they run.
var releaseActions = []string{
	"jellysin/release-helper/actions/publish", "softprops/action-gh-release", "ncipollo/release-action", "actions/create-release",
}

// goreleaserRootFlags are the arguments GoReleaser's root command runs alone instead of release.
var goreleaserRootFlags = []string{"-h", "--help", "-v", "--version"}

// actionPublishesRelease reports whether step's action creates or publishes a GitHub release.
func actionPublishesRelease(step ghworkflow.Step) bool {
	action := actionPath(step.Uses)
	if slices.Contains(releaseActions, action) {
		return true
	}
	return action == "goreleaser/goreleaser-action" && goreleaserPublishes(step.With)
}

// goreleaserPublishes reads the install-only and args inputs of goreleaser-action. GoReleaser
// runs release when no subcommand is given, except for a lone help or version flag, and release
// publishes unless --snapshot or a --skip list holding publish is set (cmd/root.go
// shouldPrependRelease and cmd/release.go of goreleaser/goreleaser at 2c336825258e, read
// 2026-11-05).
func goreleaserPublishes(with map[string]any) bool {
	if fmt.Sprint(with["install-only"]) == "true" {
		return false
	}
	var fields []string
	if args, ok := with["args"].(string); ok {
		fields = strings.Fields(args)
	}
	if len(fields) == 1 && slices.Contains(goreleaserRootFlags, fields[0]) {
		return false
	}
	if len(fields) > 0 && !strings.HasPrefix(fields[0], "-") && fields[0] != "release" {
		return false
	}
	return !goreleaserSkipsPublish(fields)
}

// goreleaserSkipsPublish reports a --snapshot flag or a --skip list, as =value or as the next
// argument, that names publish.
func goreleaserSkipsPublish(fields []string) bool {
	for i := 0; i < len(fields) && i < maxRunScriptFields; i++ {
		if fields[i] == "--snapshot" || fields[i] == "--snapshot=true" {
			return true
		}
		value, ok := strings.CutPrefix(fields[i], "--skip=")
		if !ok && fields[i] == "--skip" && i+1 < len(fields) {
			value, ok = fields[i+1], true
		}
		if ok && slices.Contains(strings.Split(value, ","), "publish") {
			return true
		}
	}
	return false
}
