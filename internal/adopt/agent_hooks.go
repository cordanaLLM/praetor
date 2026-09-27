package adopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/agenthook"
	"github.com/cordanaLLM/praetor/internal/clientid"
	"github.com/cordanaLLM/praetor/internal/clientjson"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// pluginHookClients register hooks through an installed plugin rather than a repository file;
// adoption writes nothing for them and says so.
var pluginHookClients = [...]clientid.ID{clientid.AGY}

// hookTarget is one selected client's hook file as adoption observed it, and the pre-tool
// registration planned against it. unmergeable names why the file is left untouched instead;
// plan is nil then.
type hookTarget struct {
	file        agenthook.HookFile
	before      []byte
	exists      bool
	plan        *clientjson.HookPlan
	unmergeable string
}

// reconcileAgentHooks registers the engine's pre-tool row (agenthook.Registration.Command,
// ADR-0011 decision 1) in the native hook file of every agent client agent_clients selects, so
// the command policy runs on every shell call of an adopted repository instead of only in a
// scaffolded script nothing calls. A selected client without a native hook file, an unselected
// client and a plugin-registered client each get a not-applicable entry naming the reason.
// preflightAgentHooks has planned every selected file before the first step wrote anything.
func reconcileAgentHooks(ctx context.Context, s *adoptSession) error {
	selected, excluded, err := hookClientSelection(ctx, s.repoPath)
	if err != nil {
		return err
	}
	for _, client := range excluded {
		recordHookNotApplicable(s, client, "Not selected by agent_clients in "+manifestFile)
	}
	for _, client := range selected {
		if err := reconcileClientHook(ctx, s, client); err != nil {
			return err
		}
	}
	for _, client := range pluginHookClients {
		s.report.recordNotApplicable(string(client), "Registers hooks through its installed plugin, not a repository file")
	}
	return nil
}

// preflightAgentHooks plans the registration of every selected client without writing, so a
// hook file the step would refuse fails adoption before its first write: a symlinked file,
// backup path or directory, a file that is not a regular text file, and a file whose content
// cannot be merged. Checked only at the step, the refusal came after every earlier step and
// every earlier client had written, and left a half-adopted repository. A JSONC file the client
// reads is not a refusal: the step leaves it untouched and reports it.
func preflightAgentHooks(ctx context.Context, repoPath string) error {
	selected, _, err := hookClientSelection(ctx, repoPath)
	if err != nil {
		return err
	}
	for _, client := range selected {
		file, ok := agenthook.NativeHookFile(client)
		if !ok {
			continue
		}
		if _, err := planHookTarget(ctx, repoPath, client, file); err != nil {
			return err
		}
	}
	return nil
}

// hookClientSelection reads agent_clients from the manifest and resolves it to the clients the
// step registers and the ones it leaves out.
func hookClientSelection(ctx context.Context, repoPath string) (selected, excluded []string, err error) {
	declared, err := config.LoadDeclaredTooling(ctx, repoPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read agent_clients selection from %s: %w", manifestFile, err)
	}
	selected, excluded, err = agentcontext.SelectedClients(declared.AgentClients)
	if err != nil {
		return nil, nil, fmt.Errorf("agent_clients in %s: %w", manifestFile, err)
	}
	return selected, excluded, nil
}

// recordHookNotApplicable reports a client adoption registers nothing for, under its hook file
// when it has one and under its id otherwise.
func recordHookNotApplicable(s *adoptSession, client, reason string) {
	if file, ok := agenthook.NativeHookFile(client); ok {
		s.report.recordNotApplicable(file.Path, reason)
		return
	}
	s.report.recordNotApplicable(client, reason)
}

// reconcileClientHook plans the pre-tool registration of one selected client against its hook
// file and publishes the plan unless the session is a dry run. The report entry is the same in
// both: what the plan found or would write, or why the file is left untouched.
func reconcileClientHook(ctx context.Context, s *adoptSession, client string) error {
	file, ok := agenthook.NativeHookFile(client)
	if !ok {
		s.report.recordNotApplicable(client, "No pre-tool row in the registration table; the interceptor is not registered")
		return nil
	}
	target, err := planHookTarget(ctx, s.repoPath, client, file)
	if err != nil {
		return err
	}
	switch {
	case target.unmergeable != "":
		s.report.recordSkipped(file.Path, target.unmergeable)
	case !target.plan.Changed:
		s.report.recordReconciled(file.Path, "Pre-tool interceptor already registered: "+strings.Join(target.plan.Present, "; "))
	default:
		if err := s.publishHookFile(ctx, target); err != nil {
			return err
		}
		recordHookRegistration(s, file.Path, target.exists, target.plan.Added)
	}
	return nil
}

// planHookTarget observes client's hook file, and its backup path when the file exists,
// through the confined read the root-pinned writers make (contextopt.ObserveSnapshotIn), so
// whatever publishHookFile would refuse is refused here, and plans the pre-tool registration
// against the file. A file the client reads as JSONC but the strict merge refuses is left
// untouched: rewriting it would drop its comments, so the target carries the reason instead of
// a plan.
func planHookTarget(ctx context.Context, root, client string, file agenthook.HookFile) (hookTarget, error) {
	target := hookTarget{file: file}
	var err error
	target.before, target.exists, err = contextopt.ObserveSnapshotIn(ctx, root, filepath.FromSlash(file.Path))
	if err != nil {
		return hookTarget{}, fmt.Errorf("hook file %s: %w", file.Path, err)
	}
	if target.exists {
		backup := file.Path + hookBackupExt
		if _, _, err := contextopt.ObserveSnapshotIn(ctx, root, filepath.FromSlash(backup)); err != nil {
			return hookTarget{}, fmt.Errorf("hook file backup %s: %w", backup, err)
		}
	}
	hooks := preToolHooks(client, file)
	target.plan, err = clientjson.PlanHooks(ctx, target.before, hooks)
	switch {
	case err == nil:
		return target, nil
	case file.Comments && errors.Is(err, clientjson.ErrNotStrictJSON):
		target.plan, target.unmergeable = nil, unmergeableReason(client, hooks)
		return target, nil
	default:
		return hookTarget{}, fmt.Errorf("register %s pre-tool hook in %s: %w", client, file.Path, err)
	}
}

// unmergeableReason says why a JSONC hook file is left untouched and what to register by hand.
func unmergeableReason(client string, hooks []clientjson.Hook) string {
	entries := make([]string, 0, len(hooks))
	for _, hook := range hooks {
		entries = append(entries, fmt.Sprintf("%q under hooks.%s, matcher %q,", hook.Command, hook.Event, hook.Matcher))
	}
	return "Not strict JSON, and " + client + " reads comments a merge would drop: file left untouched, pre-tool " +
		"interceptor not registered. Add " + strings.Join(entries, " and ") + " by hand"
}

// preToolHooks turns the client's pre-tool registration rows into the handlers PlanHooks
// merges, with the timeout in the unit of the client's hook file.
func preToolHooks(client string, file agenthook.HookFile) []clientjson.Hook {
	var hooks []clientjson.Hook
	for _, row := range agenthook.Registrations(client) {
		if row.Event != agenthook.EventPreTool {
			continue
		}
		hooks = append(hooks, clientjson.Hook{
			Event:    row.NativeEvent,
			Matcher:  row.Matcher,
			Command:  row.Command(),
			Timeout:  int64(row.Timeout / file.TimeoutUnit),
			ServedBy: row.ServedBy,
		})
	}
	return hooks
}

// publishHookFile keeps a copy of an existing hook file beside it, replaces the file only while
// it still holds the bytes the plan was made from, and reads the result back. Every write and
// the read-back go through the root-pinned contextopt helpers, so a symlinked file, backup or
// directory below the repository is refused rather than written through.
func (s *adoptSession) publishHookFile(ctx context.Context, target hookTarget) error {
	if s.opts.DryRun {
		return nil
	}
	rel, content := target.file.Path, target.plan.Content
	if target.exists {
		if err := contextopt.WriteSnapshotIn(ctx, s.repoPath, filepath.FromSlash(rel+hookBackupExt), target.before, filePerm); err != nil {
			return fmt.Errorf("back up %s: %w", rel, err)
		}
	}
	options := contextopt.ReplaceOptions{Expected: target.before, Exists: target.exists, Mode: filePerm}
	if err := contextopt.ReplaceSnapshotIn(ctx, s.repoPath, filepath.FromSlash(rel), content, options); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	actual, _, err := contextopt.ObserveSnapshotIn(ctx, s.repoPath, filepath.FromSlash(rel))
	if err != nil {
		return fmt.Errorf("read back %s: %w", rel, err)
	}
	if !bytes.Equal(actual, content) {
		return fmt.Errorf("%s changed after it was written; compare it with %s%s", rel, rel, hookBackupExt)
	}
	return nil
}

// recordHookRegistration reports a registration as a created file, or as a merge into an
// existing one together with the backup taken of it.
func recordHookRegistration(s *adoptSession, rel string, existed bool, added []string) {
	commands := strings.Join(added, "; ")
	if !existed {
		s.report.recordCreated(rel, "Registered pre-tool interceptor: "+commands)
		return
	}
	s.report.recordReconciledAs(rel, actionMerge, "Registered pre-tool interceptor: "+commands+"; prior file kept as "+rel+hookBackupExt)
	s.report.recordCreated(rel+hookBackupExt, "Backup of "+rel+" before the pre-tool interceptor merge")
}
