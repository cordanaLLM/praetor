package adopt

import (
	"bytes"
	"context"
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

// reconcileAgentHooks registers the engine's pre-tool row (agenthook.Registration.Command,
// ADR-0011 decision 1) in the native hook file of every agent client agent_clients selects, so
// the command policy runs on every shell call of an adopted repository instead of only in a
// scaffolded script nothing calls. A selected client without a native hook file, an unselected
// client and a plugin-registered client each get a not-applicable entry naming the reason.
func reconcileAgentHooks(ctx context.Context, s *adoptSession) error {
	declared, err := config.LoadDeclaredTooling(ctx, s.repoPath)
	if err != nil {
		return fmt.Errorf("read agent_clients selection from %s: %w", manifestFile, err)
	}
	selected, excluded, err := agentcontext.SelectedClients(declared.AgentClients)
	if err != nil {
		return fmt.Errorf("agent_clients in %s: %w", manifestFile, err)
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
// both: what the plan found or would write.
func reconcileClientHook(ctx context.Context, s *adoptSession, client string) error {
	file, ok := agenthook.NativeHookFile(client)
	if !ok {
		s.report.recordNotApplicable(client, "No native hook file; the pre-tool interceptor is not registered")
		return nil
	}
	full, err := repoFile(s.repoPath, file.Path)
	if err != nil {
		return err
	}
	before, exists, err := readHookFile(full)
	if err != nil {
		return err
	}
	plan, err := clientjson.PlanHooks(ctx, before, preToolHooks(client, file))
	if err != nil {
		return fmt.Errorf("register %s pre-tool hook in %s: %w", client, file.Path, err)
	}
	if !plan.Changed {
		s.report.recordReconciled(file.Path, "Pre-tool interceptor already registered: "+strings.Join(plan.Present, "; "))
		return nil
	}
	if err := s.publishHookFile(ctx, file.Path, full, before, exists, plan.Content); err != nil {
		return err
	}
	recordHookRegistration(s, file.Path, exists, plan.Added)
	return nil
}

// readHookFile reads a confined hook file through readRepoFile, which refuses a FIFO or an
// oversized file instead of blocking or reading it in part. An absent file reads as no bytes.
func readHookFile(full string) ([]byte, bool, error) {
	if !fileExists(full) {
		return nil, false, nil
	}
	data, err := readRepoFile(full)
	if err != nil {
		return nil, true, err
	}
	return data, true, nil
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
// it still holds the bytes the plan was made from, and reads the result back. Every write goes
// through the root-pinned contextopt writers, so a symlinked file, backup or directory below
// the repository is refused rather than written through.
func (s *adoptSession) publishHookFile(ctx context.Context, rel, full string, before []byte, exists bool, content []byte) error {
	if s.opts.DryRun {
		return nil
	}
	if exists {
		if err := contextopt.WriteSnapshotIn(ctx, s.repoPath, filepath.FromSlash(rel+hookBackupExt), before, filePerm); err != nil {
			return fmt.Errorf("back up %s: %w", rel, err)
		}
	}
	options := contextopt.ReplaceOptions{Expected: before, Exists: exists, Mode: filePerm}
	if err := contextopt.ReplaceSnapshotIn(ctx, s.repoPath, filepath.FromSlash(rel), content, options); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	actual, err := readRepoFile(full)
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
