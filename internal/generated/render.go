// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package generated

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

// Bounds of a rendering (HISS-02).
const (
	// maxFailureExcerpt bounds the command output a failure quotes.
	maxFailureExcerpt = 2048
	// cleanupTimeout bounds the removal of the render worktree, which runs after the render's
	// own deadline may have passed.
	cleanupTimeout = 2 * time.Minute
	// maxRenderJobs bounds the artefacts, and so the distinct commands, one rendering runs: every
	// declared artefact and every built-in one.
	maxRenderJobs = config.MaxGeneratedArtefacts + 16
)

// Runner runs one render command, argv with env added to the inherited environment, in dir and
// returns its bounded output. Tests substitute one; production runs execCommand.
type Runner func(ctx context.Context, dir string, argv, env []string) (util.CommandBytes, error)

// selfExecutable resolves SelfCommand; tests substitute it.
var selfExecutable = os.Executable

// execCommand runs argv through util.RunCommandBytes. A first word equal to SelfCommand runs
// this binary; any other is resolved as exec.Command resolves it, a relative path against dir.
func execCommand(ctx context.Context, dir string, argv, env []string) (util.CommandBytes, error) {
	name := argv[0]
	if name == SelfCommand {
		self, err := selfExecutable()
		if err != nil {
			return util.CommandBytes{}, fmt.Errorf("resolve the running praetorctl: %w", err)
		}
		name = self
	}
	if len(env) > 0 {
		var err error
		if ctx, err = util.WithCommandEnvironment(ctx, util.InheritedEnvironment(env)); err != nil {
			return util.CommandBytes{}, err
		}
	}
	return util.RunCommandBytes(ctx, dir, name, util.MaxCommandOutputBytes, argv[1:]...)
}

// renderJob is one distinct command and the artefacts it renders: two artefacts that declare
// the same command and environment render once.
type renderJob struct {
	argv    []string
	env     []string
	timeout time.Duration
	names   []string
}

// renderJobs groups the artefacts into jobs in first-seen order; a job takes the longest
// timeout of its artefacts.
func renderJobs(artefacts []Artefact) []renderJob {
	jobs := make([]renderJob, 0, len(artefacts))
	positions := make(map[string]int, len(artefacts))
	for index := 0; index < len(artefacts) && index < maxRenderJobs; index++ {
		artefact := artefacts[index]
		key := strings.Join(artefact.Command, "\x00") + "\x01" + strings.Join(artefact.Env, "\x00")
		position, seen := positions[key]
		if !seen {
			positions[key] = len(jobs)
			jobs = append(jobs, renderJob{argv: artefact.Command, env: artefact.Env, timeout: artefact.timeout})
			position = len(jobs) - 1
		}
		jobs[position].names = append(jobs[position].names, artefact.Name)
		jobs[position].timeout = max(jobs[position].timeout, artefact.timeout)
	}
	return jobs
}

// runJobs runs every job in dir, in order, each within its timeout, and returns the failure of
// each artefact whose job failed. A failed job does not stop the next one, so one run reports
// every failing artefact; a run whose own deadline passed stops.
func runJobs(ctx context.Context, run Runner, dir string, jobs []renderJob) (map[string]string, error) {
	failures := make(map[string]string)
	for index := 0; index < len(jobs) && index < maxRenderJobs; index++ {
		if err := ctx.Err(); err != nil {
			return failures, fmt.Errorf("rendering stopped before %s: %w", strings.Join(jobs[index].argv, " "), err)
		}
		job := jobs[index]
		jobCtx, cancel := context.WithTimeout(ctx, job.timeout)
		result, err := run(jobCtx, dir, job.argv, job.env)
		cut := jobCtx.Err()
		cancel()
		if err == nil {
			continue
		}
		message := describeFailure(job, result, err, cut)
		for position := 0; position < len(job.names); position++ {
			failures[job.names[position]] = message
		}
	}
	return failures, nil
}

// describeFailure names the command, why it failed and the end of what it printed.
func describeFailure(job renderJob, result util.CommandBytes, err, cut error) string {
	reason := err.Error()
	if errors.Is(cut, context.DeadlineExceeded) {
		reason = fmt.Sprintf("did not finish within %s", job.timeout)
	}
	output := strings.TrimSpace(string(result.Stderr))
	if output == "" {
		output = strings.TrimSpace(string(result.Stdout))
	}
	message := fmt.Sprintf("%s failed: %s", strings.Join(job.argv, " "), reason)
	if output != "" {
		message += ": " + util.TruncateExcerpt(output, maxFailureExcerpt)
	}
	return message
}

// session is one temporary worktree of a commit, created below the repository's
// .standards/worktrees by internal/worktree and removed with its branch afterwards.
type session struct {
	manager *worktree.Manager
	taskID  string
	path    string
}

// openSession checks out rev into a new temporary worktree of the repository at root.
func openSession(ctx context.Context, root, rev string) (*session, error) {
	manager := worktree.NewManager(root)
	taskID := fmt.Sprintf("generated-%d-%d", os.Getpid(), time.Now().UnixNano())
	created, err := manager.Create(ctx, taskID, rev)
	if err != nil {
		return nil, fmt.Errorf("check out %s for rendering: %w", rev, err)
	}
	return &session{manager: manager, taskID: taskID, path: created.Path}, nil
}

// close removes the worktree and its branch under its own bound, also after ctx has ended, so
// a rendering that ran out of time leaves nothing behind.
func (s *session) close(ctx context.Context) error {
	cleanCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	if err := s.manager.Remove(cleanCtx, s.taskID, true); err != nil {
		return fmt.Errorf("remove the render worktree %s: %w", s.path, err)
	}
	return nil
}

// state is what one artefact holds in one tree: the digest of each selected file's text, or of
// its block, keyed by path.
type state map[string]string

// snapshot reads the state of every artefact in tree, re-selecting its files there.
func snapshot(ctx context.Context, tree Tree, artefacts []Artefact) (map[string]state, error) {
	files, err := tree.Files(ctx)
	if err != nil {
		return nil, err
	}
	states := make(map[string]state, len(artefacts))
	for index := 0; index < len(artefacts); index++ {
		artefact := artefacts[index]
		if err := artefact.selectFiles(ctx, tree, files); err != nil {
			return nil, err
		}
		current, err := artefactState(ctx, tree, &artefact)
		if err != nil {
			return nil, err
		}
		states[artefact.Name] = current
	}
	return states, nil
}

// artefactState digests the artefact's selected files, or their blocks, in tree.
func artefactState(ctx context.Context, tree Tree, artefact *Artefact) (state, error) {
	current := make(state, len(artefact.Files))
	for index := 0; index < len(artefact.Files); index++ {
		rel := artefact.Files[index]
		content, _, err := readText(ctx, tree, rel)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", artefact.Name, err)
		}
		if artefact.Block != nil {
			if content, _, err = extractBlock(content, artefact.Block); err != nil {
				return nil, fmt.Errorf("%s: %s: %w", artefact.Name, rel, err)
			}
		}
		sum := sha256.Sum256([]byte(content))
		current[rel] = hex.EncodeToString(sum[:])
	}
	return current, nil
}
