// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
)

// SettingAction is what flavor apply did with one required setting.
type SettingAction string

const (
	// SettingCreated: the setting was absent and apply wrote it.
	SettingCreated SettingAction = "created"
	// SettingReplaced: the setting differed from the rendering and --force rewrote it.
	SettingReplaced SettingAction = "replaced"
	// SettingRefreshed: the setting was Praetor's rendering under earlier inputs, unedited
	// (forge.IsRepositoryRulesetRendering), and apply rewrote it to the current one without
	// --force.
	SettingRefreshed SettingAction = "refreshed"
	// SettingUnchanged: the setting already is the rendering, so nothing was written.
	SettingUnchanged SettingAction = "unchanged"
	// SettingKept: the setting differs from the rendering and stays, because --force was not
	// passed. The note says what differs.
	SettingKept SettingAction = "kept"
	// SettingDeferred: another command writes the setting (SettingItem.Producer, the note).
	SettingDeferred SettingAction = "deferred"
	// SettingDeclined: adoption.decline names the adoption step that owns the setting, so apply
	// does not write it either.
	SettingDeclined SettingAction = "declined"
)

// SettingOutcome is what one flavor apply did with one required setting.
type SettingOutcome struct {
	Path   string        `json:"path"`
	Action SettingAction `json:"action"`
	Note   string        `json:"note,omitempty"`
}

// rulesetStep is the adoption step that writes the branch ruleset, the name adoption.decline
// declines it by.
const rulesetStep = "branch-ruleset"

// maxSettings bounds the settings one apply walks (HISS-02).
const maxSettings = 64

// applySettings renders each required setting apply has a renderer for and records every
// setting's outcome in report, or does nothing under opts.TemplatesOnly. A setting that fails is
// recorded under report.Errors and the rest still run; only a cancelled context stops the walk.
func applySettings(ctx context.Context, repoPath string, settings []SettingItem, opts ApplyOptions, report *ApplyReport) error {
	if opts.TemplatesOnly {
		return nil
	}
	for i := 0; i < len(settings) && i < maxSettings; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		outcome, err := applySetting(ctx, repoPath, settings[i], opts)
		if err != nil {
			report.Errors = append(report.Errors, err.Error())
			continue
		}
		report.Settings = append(report.Settings, outcome)
	}
	return nil
}

// applySetting renders the branch ruleset, defers a setting another command produces, and
// refuses a setting with neither.
func applySetting(ctx context.Context, repoPath string, setting SettingItem, opts ApplyOptions) (SettingOutcome, error) {
	switch {
	case setting.Path == forge.RepositoryRulesetPath:
		return applyRuleset(ctx, repoPath, opts)
	case setting.Producer != "":
		return SettingOutcome{Path: setting.Path, Action: SettingDeferred, Note: setting.Producer}, nil
	default:
		return SettingOutcome{}, fmt.Errorf("setting %s has no renderer and no producer", setting.Path)
	}
}

// applyRuleset writes the branch ruleset forge.RenderRulesetForRepository renders under the
// repository's effective policy, the rendering adoption's branch-ruleset step writes, with the
// keep and force semantics of a template. An absent ruleset is written. One that already
// validates as the rendering (forge.ValidateRepositoryRuleset, the check sync and the audit
// apply) is unchanged. One Praetor rendered under an earlier policy or earlier workflows, such
// as adoption's before this apply added a workflow, is refreshed (writeRuleset). Any other one
// that differs is the repository's: kept and reported without --force, replaced with it. A
// ruleset adoption.decline refuses is never written.
func applyRuleset(ctx context.Context, repoPath string, opts ApplyOptions) (SettingOutcome, error) {
	declined, err := stepDeclined(ctx, opts, rulesetStep)
	if err != nil {
		return SettingOutcome{}, err
	}
	if declined {
		return SettingOutcome{Path: forge.RepositoryRulesetPath, Action: SettingDeclined, Note: "adoption.decline names " + rulesetStep}, nil
	}
	policy, err := rulesetPolicy(ctx, repoPath)
	if err != nil {
		return SettingOutcome{}, err
	}
	content, contexts, err := forge.RenderRulesetForRepository(ctx, repoPath, policy, nil)
	if err != nil {
		return SettingOutcome{}, fmt.Errorf("render %s: %w", forge.RepositoryRulesetPath, err)
	}
	return writeRuleset(ctx, repoPath, content, opts.Force, func(existing []byte) bool {
		return forge.ValidateRepositoryRuleset(existing, policy, contexts) == nil
	}, len(contexts))
}

// writeRuleset writes the rendered ruleset content unless the file on disk already matches it
// (matches) or is the repository's own and differs without force, and reports which of those
// it was. A file that is Praetor's rendering under earlier inputs (forge.IsRepositoryRulesetRendering)
// is not the repository's own: it is refreshed without force, in its own line-ending style.
func writeRuleset(ctx context.Context, repoPath string, content []byte, force bool, matches func([]byte) bool, checks int) (SettingOutcome, error) {
	const rel = forge.RepositoryRulesetPath
	destPath := filepath.Join(repoPath, filepath.FromSlash(rel))
	target, err := readTemplateTarget(ctx, destPath, rel, force)
	if err != nil {
		return SettingOutcome{}, err
	}
	refresh := target.keep && forge.IsRepositoryRulesetRendering(target.before)
	switch {
	case target.exists && matches(target.before):
		return SettingOutcome{Path: rel, Action: SettingUnchanged}, nil
	case target.keep && !refresh:
		return SettingOutcome{Path: rel, Action: SettingKept,
			Note: "differs from the ruleset the effective policy renders; --force replaces it"}, nil
	}
	if refresh {
		_, crlf := util.NormalizeLineEndings(string(target.before))
		content = []byte(util.RestoreLineEndings(string(content), crlf))
	}
	if err := writeTarget(ctx, destPath, rel, content, target); err != nil {
		return SettingOutcome{}, err
	}
	return SettingOutcome{Path: rel, Action: rulesetWriteAction(target.exists, refresh),
		Note: fmt.Sprintf("%d required status checks derived from workflows", checks)}, nil
}

// rulesetWriteAction names a ruleset write: created where none existed, refreshed where an
// earlier Praetor rendering was, replaced under --force.
func rulesetWriteAction(existed, refreshed bool) SettingAction {
	switch {
	case !existed:
		return SettingCreated
	case refreshed:
		return SettingRefreshed
	default:
		return SettingReplaced
	}
}

// stepDeclined asks opts.Declines whether the repository declined step; nil declines nothing.
func stepDeclined(ctx context.Context, opts ApplyOptions, step string) (bool, error) {
	if opts.Declines == nil {
		return false, nil
	}
	declined, err := opts.Declines(ctx, step)
	if err != nil {
		return false, fmt.Errorf("resolve adoption.decline for %s: %w", step, err)
	}
	return declined, nil
}

// rulesetPolicy is the branch protection the ruleset is rendered under: the repository's
// effective policy as sync and the audit resolve it (config.ResolveRepositoryPolicy: the pinned
// profiles and facets with the manifest's overrides, or defaults plus overrides before a lock
// exists). A repository without .standards.yaml declares nothing to resolve, so the built-in
// default applies. A policy that does not resolve has no stand-in: the ruleset is not written.
func rulesetPolicy(ctx context.Context, repoPath string) (config.BranchProtectionPolicy, error) {
	policy, _, err := config.ResolveRepositoryPolicy(ctx, filepath.Join(repoPath, config.ManifestFileName), nil)
	if err != nil {
		return config.BranchProtectionPolicy{}, fmt.Errorf("resolve the effective policy for %s: %w", forge.RepositoryRulesetPath, err)
	}
	if policy == nil {
		return config.DefaultPolicy().BranchProtection, nil
	}
	return policy.BranchProtection, nil
}
