// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/lefthookconfig"
	"github.com/cordanaLLM/praetor/internal/util"
)

// hookRunLinters returns which of candidates a CI step starts through the repository's hook
// runner (#1313): a step that runs `pre-commit run` or `lefthook run <hook>`
// (forge.MeasureWorkflowHookRuns) runs every hook its configuration starts for that stage or
// hook, limited to the hooks or jobs the step names. A hook starts a linter when its ID or name
// names it (forge.NameRunsTool) or its command runs it (forge.CommandRunsTool). A runner whose
// configuration is absent starts nothing.
func hookRunLinters(ctx context.Context, rootDir string, candidates []string) (map[string]bool, error) {
	runs, err := forge.MeasureWorkflowHookRuns(ctx, rootDir)
	if err != nil || len(runs) == 0 {
		return nil, err
	}
	started := make(map[string]bool, len(candidates))
	for i := 0; i < len(runs); i++ {
		var hooks []runnerHook
		switch runs[i].Runner {
		case forge.HookRunnerPreCommit:
			hooks, err = preCommitStageHooks(ctx, rootDir, runs[i])
		case forge.HookRunnerLefthook:
			hooks, err = lefthookStageHooks(ctx, rootDir, runs[i])
		}
		if err != nil {
			return nil, err
		}
		markStartedLinters(ctx, hooks, candidates, started)
	}
	return started, nil
}

// runnerHook is one hook a run starts: its ID or name, and the command it runs ("" when the
// configuration does not show it, as for a hook of a remote pre-commit repository).
type runnerHook struct {
	name, command string
}

// markStartedLinters marks each candidate one of hooks starts.
func markStartedLinters(ctx context.Context, hooks []runnerHook, candidates []string, started map[string]bool) {
	for c := 0; c < len(candidates); c++ {
		for h := 0; h < len(hooks) && !started[candidates[c]]; h++ {
			if forge.NameRunsTool(hooks[h].name, candidates[c]) ||
				(hooks[h].command != "" && forge.CommandRunsTool(ctx, hooks[h].command, candidates[c])) {
				started[candidates[c]] = true
			}
		}
	}
}

// preCommitStageHooks returns the hooks of .pre-commit-config.yaml that `pre-commit run` starts
// at run.Stage, limited to run.Only, read within the bounds the hook audit applies.
func preCommitStageHooks(ctx context.Context, rootDir string, run forge.HookRun) ([]runnerHook, error) {
	var doc preCommitConfig
	err := config.ReadYAMLDocument(ctx, filepath.Join(rootDir, preCommitConfigFile), &doc, util.YAMLDocumentOptions{})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s cannot be read: %w", preCommitConfigFile, err)
	}
	if len(doc.Repos) > maxPreCommitRepos {
		return nil, fmt.Errorf("%s lists %d repos, more than the %d the audit reads", preCommitConfigFile, len(doc.Repos), maxPreCommitRepos)
	}
	var hooks []runnerHook
	seen := 0
	for _, repo := range doc.Repos {
		seen += len(repo.Hooks)
		if seen > maxPreCommitHooks {
			return nil, fmt.Errorf("%s lists more than the %d hooks the audit reads", preCommitConfigFile, maxPreCommitHooks)
		}
		for _, hook := range repo.Hooks {
			if started, ok := preCommitRunStarts(run, repo.Repo, hook, doc.DefaultStages); ok {
				hooks = append(hooks, started)
			}
		}
	}
	return hooks, nil
}

// preCommitRunStarts returns the hook run starts, or false when the run selects another stage or
// other hooks. A hook of repo: local shows the command it runs; a remote hook shows only its ID.
func preCommitRunStarts(run forge.HookRun, repo string, hook preCommitConfigHook, defaultStages []string) (runnerHook, bool) {
	if !runsAtStage(run.Stage, hook.Stages, defaultStages) || (len(run.Only) > 0 && !slices.Contains(run.Only, hook.ID)) {
		return runnerHook{}, false
	}
	started := runnerHook{name: hook.ID}
	if repo == "local" {
		started.command = strings.Join(append(strings.Fields(hook.Entry), hook.Args...), " ")
	}
	return started, true
}

// lefthookStageHooks returns the jobs of the hook run.Stage in lefthook.yml, read through
// lefthookconfig.Jobs as adoption and the flavor audit read them, limited to run.Only by job
// name. A job group, a script and the configuration's extends are not followed, so what they
// start is not credited; a commands entry whose skip is literally true never runs.
func lefthookStageHooks(ctx context.Context, rootDir string, run forge.HookRun) ([]runnerHook, error) {
	var parsed map[string]any
	err := config.ReadYAMLDocument(ctx, filepath.Join(rootDir, lefthookFile), &parsed, util.YAMLDocumentOptions{})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s cannot be read: %w", lefthookFile, err)
	}
	jobs := lefthookconfig.Jobs(parsed)
	var hooks []runnerHook
	for i := 0; i < len(jobs) && i < maxLefthookJobs; i++ {
		kind, name, _ := strings.Cut(jobs[i].Name, "/")
		if jobs[i].Hook != run.Stage || kind != "commands" || lefthookCommandSkipped(parsed, run.Stage, name) ||
			(len(run.Only) > 0 && !slices.Contains(run.Only, name)) {
			continue
		}
		hooks = append(hooks, runnerHook{name: name, command: jobs[i].Run})
	}
	return hooks, nil
}

// lefthookCommandSkipped reports whether the commands entry name of hook carries skip: true.
func lefthookCommandSkipped(parsed map[string]any, hook, name string) bool {
	section, ok := parsed[hook].(map[string]any)
	if !ok {
		return false
	}
	commands, ok := section["commands"].(map[string]any)
	if !ok {
		return false
	}
	command, ok := commands[name].(map[string]any)
	if !ok {
		return false
	}
	skip, ok := command["skip"].(bool)
	return ok && skip
}

// runsAtStage applies the pre-commit framework's stage rule: a hook's stages default to
// default_stages, which default to every stage, and the legacy names commit, push and
// merge-commit mean pre-commit, pre-push and pre-merge-commit (_STAGES in
// pre_commit/clientlib.py).
func runsAtStage(stage string, stages, defaultStages []string) bool {
	if len(stages) == 0 {
		stages = defaultStages
	}
	if len(stages) == 0 {
		return true
	}
	legacy := map[string]string{"pre-commit": "commit", "pre-push": "push", "pre-merge-commit": "merge-commit"}[stage]
	return slices.Contains(stages, stage) || (legacy != "" && slices.Contains(stages, legacy))
}
