package adopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
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
// backup root (adoptBackupRoot) or directory, a file that is not a regular text file, and a
// file whose content cannot be merged. Checked only at the step, the refusal came after every
// earlier step and every earlier client had written, and left a half-adopted repository. A
// JSONC file the client reads is not a refusal: the step leaves it untouched and reports it.
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
	legacyHookBackupWarning(s, file.Path)
	duplicateHookWarning(s, file.Path, target.plan)
	switch {
	case target.unmergeable != "":
		s.report.recordSkipped(file.Path, target.unmergeable)
	case !target.plan.Changed:
		s.report.recordReconciled(file.Path, "Pre-tool interceptor already registered: "+strings.Join(target.plan.Present, "; "))
	default:
		backup, err := s.publishHookFile(ctx, target)
		if err != nil {
			return err
		}
		recordHookRegistration(s, file.Path, target.exists, target.plan.Added, backup)
	}
	return nil
}

// duplicateHookWarning reports the handlers beyond the first that already serve a pre-tool hook
// (clientjson.HookPlan.Duplicates), such as a run_shell_command entry beside a
// ^run_shell_command$ one. An identical copy of the registered command line is redundant, and
// whether it runs again is up to the client, since some run an identical handler only once. A
// different command line, such as the skew guard beside the engine call, is a handler of its
// own, so the client evaluates the policy again. Adoption never removes adopter entries, so it
// only reports them.
func duplicateHookWarning(s *adoptSession, rel string, plan *clientjson.HookPlan) {
	if plan == nil || len(plan.Duplicates) == 0 {
		return
	}
	var copies, others []string
	for _, line := range plan.Duplicates {
		if slices.Contains(plan.Present, line) {
			copies = append(copies, line)
		} else {
			others = append(others, line)
		}
	}
	notes := ""
	if len(copies) > 0 {
		notes += "; redundant identical copy: " + strings.Join(copies, "; ")
	}
	if len(others) > 0 {
		notes += "; different command line that evaluates the policy again: " + strings.Join(others, "; ")
	}
	s.report.addWarning("%s: pre-tool interceptor registered more than once, first by %s%s; adoption leaves every entry in "+
		"place, so remove the extra ones by hand", rel, strings.Join(plan.Present, "; "), notes)
}

// planHookTarget observes client's hook file through the confined read the root-pinned writers
// make (contextopt.ObserveSnapshotIn) and plans the pre-tool registration against it. When the
// plan changes an existing file, the backup root its prior bytes would be copied to is checked
// too (checkBackupRoot), so whatever publishHookFile would refuse is refused here. A file the
// client reads as JSONC but the strict merge refuses is left untouched: rewriting it would drop
// its comments, so the target carries the reason instead of a plan.
func planHookTarget(ctx context.Context, root, client string, file agenthook.HookFile) (hookTarget, error) {
	target := hookTarget{file: file}
	var err error
	target.before, target.exists, err = contextopt.ObserveSnapshotIn(ctx, root, filepath.FromSlash(file.Path))
	if err != nil {
		return hookTarget{}, fmt.Errorf("hook file %s: %w", file.Path, err)
	}
	hooks := preToolHooks(client, file)
	target.plan, err = clientjson.PlanHooks(ctx, target.before, hooks)
	switch {
	case err == nil:
	case file.Comments && errors.Is(err, clientjson.ErrNotStrictJSON):
		target.plan, target.unmergeable = nil, unmergeableReason(client, hooks)
		return target, nil
	default:
		return hookTarget{}, fmt.Errorf("register %s pre-tool hook in %s: %w", client, file.Path, err)
	}
	if target.exists && target.plan.Changed {
		if err := checkBackupRoot(ctx, root); err != nil {
			return hookTarget{}, fmt.Errorf("hook file %s: %w", file.Path, err)
		}
	}
	return target, nil
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
// merges, with the timeout in the unit of the client's hook file (agenthook.NativeHooks).
func preToolHooks(client string, file agenthook.HookFile) []clientjson.Hook {
	return agenthook.NativeHooks(client, file, agenthook.EventPreTool)
}

// publishHookFile keeps a backup of an existing hook file under the run's backup directory when
// git ignores it (backupExisting), replaces the file only while it still holds the bytes the
// plan was made from, and reads the result back. It returns the backup note for the report.
// Every write and the read-back go through the root-pinned contextopt helpers, so a symlinked
// file, backup root or directory below the repository is refused rather than written through. A
// dry run checks the backup and writes nothing.
func (s *adoptSession) publishHookFile(ctx context.Context, target hookTarget) (string, error) {
	rel, content := target.file.Path, target.plan.Content
	backup := ""
	if target.exists {
		var err error
		if backup, err = s.backupExisting(ctx, rel, target.before); err != nil {
			return "", err
		}
	}
	if s.opts.DryRun {
		return backup, nil
	}
	options := contextopt.ReplaceOptions{Expected: target.before, Exists: target.exists, Mode: filePerm}
	if err := contextopt.ReplaceSnapshotIn(ctx, s.repoPath, filepath.FromSlash(rel), content, options); err != nil {
		return "", fmt.Errorf("write %s: %w", rel, err)
	}
	actual, _, err := contextopt.ObserveSnapshotIn(ctx, s.repoPath, filepath.FromSlash(rel))
	if err != nil {
		return "", fmt.Errorf("read back %s: %w", rel, err)
	}
	if !bytes.Equal(actual, content) {
		return "", fmt.Errorf("%s changed after it was written (%s)", rel, backup)
	}
	return backup, nil
}

// recordHookRegistration reports a registration as a created file, or as a merge into an
// existing one together with where its prior bytes were kept (publishHookFile).
func recordHookRegistration(s *adoptSession, rel string, existed bool, added []string, backup string) {
	commands := strings.Join(added, "; ")
	if !existed {
		s.report.recordCreated(rel, "Registered pre-tool interceptor: "+commands)
		return
	}
	s.report.recordReconciledAs(rel, actionMerge, "Registered pre-tool interceptor: "+commands+"; "+backup)
}
