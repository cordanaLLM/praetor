// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"go.yaml.in/yaml/v3"
)

// reconcileDeclaredLintersExceptions measures which declared linters under the effective policy
// are not run by any workflow after workflows are rendered, and records or updates exception
// entries in .standards.yaml (#1289).
func (s *adoptSession) reconcileDeclaredLintersExceptions(ctx context.Context) error {
	if ctx == nil {
		return errors.New("reconcile declared linters exceptions requires a context")
	}
	policy := s.policy
	if policy == nil {
		p, err := prospectivePolicy(ctx, s, s.opts.LockSourceRoot)
		if err != nil {
			if errors.Is(err, ErrLockSourceRequired) {
				return nil
			}
			return err
		}
		policy = p
	}
	if policy == nil {
		return nil
	}
	missing, err := s.measureMissingDeclaredLinters(ctx, policy)
	if err != nil {
		return err
	}
	full, err := repoFile(s.repoPath, manifestFile)
	if err != nil {
		return err
	}
	return s.syncDeclaredLintersExceptions(ctx, full, missing)
}

func (s *adoptSession) measureMissingDeclaredLinters(ctx context.Context, policy *config.EffectivePolicy) ([]config.DeclaredLinter, error) {
	declared, err := policy.DeclaredLinters(ctx)
	if err != nil {
		return nil, err
	}
	if len(declared) == 0 {
		return nil, nil
	}
	inputs, err := loadRepoLinterInputs(ctx, s.repoPath)
	if err != nil {
		return nil, err
	}
	applicable, _ := filterApplicableLinters(declared, inputs)
	if len(applicable) == 0 {
		return nil, nil
	}
	return checkMissingWorkflowLinters(ctx, s.repoPath, applicable)
}

func (s *adoptSession) syncDeclaredLintersExceptions(ctx context.Context, full string, missing []config.DeclaredLinter) error {
	data, err := s.readManifestData(ctx, full)
	if err != nil || len(data) == 0 {
		return err
	}
	manifest, err := config.DecodeManifest(data)
	if err != nil {
		return err
	}
	nextExceptions, changed := reconcileLintersExceptionsList(manifest.Exceptions, missing, time.Now())
	if !changed {
		return nil
	}
	updated, err := setManifestExceptions(ctx, data, nextExceptions)
	if err != nil {
		return err
	}
	if bytes.Equal(data, updated) {
		return nil
	}
	if s.opts.DryRun {
		return s.applyDryRunManifestExceptions(ctx, updated)
	}
	return s.applyLiveManifestExceptions(ctx, full, data, updated)
}

func (s *adoptSession) readManifestData(ctx context.Context, full string) ([]byte, error) {
	if s.opts.DryRun {
		return plannedManifestBytes(ctx, s)
	}
	if fileExists(full) {
		data, _, err := contextopt.ObserveSnapshot(ctx, full)
		return data, err
	}
	return nil, nil
}

func (s *adoptSession) applyDryRunManifestExceptions(ctx context.Context, updated []byte) error {
	lock, err := plannedLock(ctx, s, updated)
	if err != nil {
		return err
	}
	policy, err := resolvePlannedPolicy(ctx, s, s.opts.LockSourceRoot, updated, lock)
	if err != nil {
		return err
	}
	s.policy = policy
	return nil
}

func (s *adoptSession) applyLiveManifestExceptions(ctx context.Context, full string, expected, updated []byte) error {
	if err := contextopt.ReplaceSnapshot(ctx, full, updated, contextopt.ReplaceOptions{
		Expected: expected, Exists: true, Mode: filePerm,
	}); err != nil {
		return err
	}
	policy, err := config.LoadEffectivePolicyContext(ctx, config.EffectiveOptions{Root: s.repoPath, Audit: true})
	if err != nil {
		return err
	}
	s.policy = policy
	return nil
}

func reconcileLintersExceptionsList(existing []config.Exception, missing []config.DeclaredLinter, today time.Time) ([]config.Exception, bool) {
	missingMap := make(map[string]config.DeclaredLinter, len(missing))
	for i := 0; i < len(missing) && i < config.MaxExceptions; i++ {
		missingMap[missing[i].Name] = missing[i]
	}
	result, kept, changed := filterExistingExceptions(existing, missingMap, today)
	for i := 0; i < len(missing) && i < config.MaxExceptions; i++ {
		linter := missing[i]
		if kept[linter.Name] {
			continue
		}
		result = append(result, newDeclaredLinterException(linter, today))
		kept[linter.Name] = true
		changed = true
	}
	return result, changed
}

func filterExistingExceptions(existing []config.Exception, missingMap map[string]config.DeclaredLinter, today time.Time) ([]config.Exception, map[string]bool, bool) {
	kept := make(map[string]bool, len(missingMap))
	result := make([]config.Exception, 0, len(existing)+len(missingMap))
	var changed bool
	for i := 0; i < len(existing) && i < config.MaxExceptions; i++ {
		entry := existing[i]
		if entry.Rule != config.ExceptionRuleDeclaredLinters {
			result = append(result, entry)
			continue
		}
		if _, isMissing := missingMap[entry.Tool]; !isMissing {
			changed = true
			continue
		}
		if entry.Expired(today) || kept[entry.Tool] {
			changed = true
			continue
		}
		result = append(result, entry)
		kept[entry.Tool] = true
	}
	return result, kept, changed
}

func newDeclaredLinterException(linter config.DeclaredLinter, today time.Time) config.Exception {
	return config.Exception{
		Rule:    config.ExceptionRuleDeclaredLinters,
		Tool:    linter.Name,
		Reason:  fmt.Sprintf("adoption scaffolds no CI job for %s declared by %s; run it in a workflow, or remove this entry", linter.Name, linter.Source),
		Expires: config.ExceptionDay(today).AddDate(0, 0, config.MaxExceptionDays).Format(config.ExceptionDateLayout),
	}
}

func setManifestExceptions(ctx context.Context, data []byte, exceptions []config.Exception) ([]byte, error) {
	document, err := decodeAdoptManifestNode(ctx, data)
	if err != nil {
		return nil, err
	}
	root := document.Content[0]
	at := mappingKeyIndex(root, "exceptions")
	if len(exceptions) == 0 {
		if at >= 0 {
			root.Content = append(root.Content[:at], root.Content[at+2:]...)
		}
		return encodeAdoptManifest(document)
	}

	var excNode yaml.Node
	if err := excNode.Encode(exceptions); err != nil {
		return nil, fmt.Errorf("encode exceptions: %w", err)
	}
	valNode := &excNode
	if excNode.Kind == yaml.DocumentNode && len(excNode.Content) == 1 {
		valNode = excNode.Content[0]
	}
	if at >= 0 {
		root.Content[at+1] = valNode
	} else {
		appendAdoptYAMLMapping(root, "exceptions", valNode)
	}
	return encodeAdoptManifest(document)
}
