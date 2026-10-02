// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/util"
)

// A surface whose files audit compares byte for byte declares the .gitattributes rules that
// keep a checkout from converting them. The DevContainer bundle declares one
// (devcontainer.Attributes): its Dockerfile is compared against its render as raw bytes, so a
// Windows checkout with core.autocrlf=true would otherwise fail audit on a directory nobody
// edited (#313). A managed asset family declares its own (managedasset.Family.Attributes): the
// figure engine hashes its render files, the specs and the committed outputs
// (docs/adr/0016-figures-for-adopters.md, section 5). Adoption writes the rules of every such
// surface (ManagedAttributes) as one block at the tail of .gitattributes, so a later operator
// rule cannot override them, and removes the block once no surface declares a rule. Every line
// outside the block is the operator's and is kept; a rule there that names the DevContainer
// directory and sets another line-ending treatment is refused instead of silently overridden
// (refuseDevContainerOverride).
//
// Audit checks the block byte for byte, so it follows the replace-vs-refresh contract of every
// audit-locked file: an unedited block is verified, refreshed to the rules of this run, or
// moved to the tail with every operator line kept; a block whose lines were edited is restored
// only under --force, as a replace with its line delta and a backup, and a documentation
// disable refuses to touch it, as it refuses an edited Makefile documentation block. Unedited
// means the block of one of canonicalAttributeRuleSets, which holds the documentation-only
// block every release before the DevContainer rule wrote; a change to the rules must keep the
// outgoing block there, as priorDocumentationMakefileBlocks does for the Makefile.

const (
	gitAttributesFile         = ".gitattributes"
	gitAttributesManagedBegin = "# BEGIN praetor managed attributes (praetorctl adopt)"
	gitAttributesManagedEnd   = "# END praetor managed attributes"
	// gitAttributesHeader explains the block to whoever opens the file.
	gitAttributesHeader = "# praetorctl audit hashes these files; keep their checkout bytes the same on every platform."
	// maxGitAttributesLines bounds the .gitattributes file adoption reads (HISS-02).
	maxGitAttributesLines = 4096
	// devContainerStep is the adoption step that writes the DevContainer bundle; the attribute
	// block carries the bundle's rule unless adoption.decline lists it.
	devContainerStep = "dev-container"
)

// gitAttributesTailBlock is the attribute block adoption owns at the tail of .gitattributes.
func gitAttributesTailBlock() managedTailBlock {
	return managedTailBlock{
		file: gitAttributesFile, begin: gitAttributesManagedBegin, end: gitAttributesManagedEnd,
		maxLines: maxGitAttributesLines, duplicate: "duplicate managed attribute blocks",
	}
}

// DocumentationAttributes returns the .gitattributes rules of every docs:seo-portal family, in
// registry order.
func DocumentationAttributes() []string {
	return managedasset.AttributesOf(DocumentationFamilies())
}

// DevContainerAttributes returns the .gitattributes rules of the DevContainer bundle.
func DevContainerAttributes() []string {
	return devcontainer.Attributes()
}

// ManagedAttributes returns the rules of the attribute block, in block order: the DevContainer
// bundle's while adoption writes the bundle (devContainer), then the documentation families'
// while docs:seo-portal is enabled (documentation). Adoption and audit both read the block's
// rules here.
func ManagedAttributes(devContainer, documentation bool) []string {
	var rules []string
	if devContainer {
		rules = append(rules, DevContainerAttributes()...)
	}
	if documentation {
		rules = append(rules, DocumentationAttributes()...)
	}
	return rules
}

// canonicalAttributeRuleSets lists the rules of every block adoption writes or wrote: each
// non-empty result of ManagedAttributes. The documentation rules alone are also the block of
// every release before the DevContainer rule, which therefore stays refreshable without --force.
func canonicalAttributeRuleSets() [][]string {
	return [][]string{ManagedAttributes(true, true), ManagedAttributes(true, false), ManagedAttributes(false, true)}
}

// ManagedGitAttributesBlock returns the canonical block holding rules, shared by adoption and
// audit; "" when rules is empty, since adoption then writes no block.
func ManagedGitAttributesBlock(rules []string) string {
	if len(rules) == 0 {
		return ""
	}
	return gitAttributesTailBlock().render(append([]string{gitAttributesHeader}, rules...))
}

// mergeGitAttributes returns text with the block of rules as its tail, or without any block
// when rules is empty. Every line outside the block is the operator's and is kept. An operator
// rule the DevContainer rule would override is refused, and so is removing a block whose lines
// differ from every block adoption writes.
func mergeGitAttributes(text string, rules []string) (string, error) {
	block := gitAttributesTailBlock()
	if len(rules) > 0 {
		if err := refuseDevContainerOverride(text, rules); err != nil {
			return "", err
		}
		return block.merge(text, ManagedGitAttributesBlock(rules))
	}
	edited, err := gitAttributesBlockEdited(text)
	if err != nil {
		return "", err
	}
	if edited {
		return "", fmt.Errorf("refusing to remove the edited managed attribute block of %s; restore or remove it, then rerun adopt", gitAttributesFile)
	}
	stripped, _, err := block.strip(text)
	return stripped, err
}

// gitAttributesBlockEdited reports whether text holds an attribute block whose lines, line
// endings aside, are those of no block adoption writes (canonicalAttributeRuleSets): an
// operator edit.
func gitAttributesBlockEdited(text string) (bool, error) {
	inner, held, err := gitAttributesTailBlock().held(text)
	if err != nil || !held {
		return false, err
	}
	canonical := func(rules []string) bool {
		return slices.Equal(inner, append([]string{gitAttributesHeader}, rules...))
	}
	return !slices.ContainsFunc(canonicalAttributeRuleSets(), canonical), nil
}

// refuseDevContainerOverride refuses to merge rules holding the DevContainer rule into text
// while an operator line of text names the DevContainer directory and gives text or eol another
// state (overridesDevContainerRule). The block sits at the tail and git lets the later line win,
// so merging would silently override what the operator wrote; the refusal names the rule.
func refuseDevContainerOverride(text string, rules []string) error {
	managed := DevContainerAttributes()
	if len(managed) == 0 || !slices.Contains(rules, managed[0]) {
		return nil
	}
	scan, _, err := gitAttributesTailBlock().read(text)
	if err != nil {
		return err
	}
	for index := 0; index < len(scan.kept) && index < maxGitAttributesLines; index++ {
		if overridesDevContainerRule(scan.kept[index]) {
			return fmt.Errorf("%s rule %q gives the DevContainer directory another line-ending treatment than the managed rule %q, "+
				"which adoption writes at the tail of the file, where it would override that rule; remove or change the rule, "+
				"or list %s in adoption.decline, then rerun adopt",
				gitAttributesFile, strings.TrimSpace(scan.kept[index]), managed[0], devContainerStep)
		}
	}
	return nil
}

// overridesDevContainerRule reports whether line is an attribute rule whose pattern names a
// path inside the DevContainer directory and whose attributes give text or eol a state other
// than the managed rule's (lineEndingOverride). A rule on a wider pattern, such as *.json, is
// the operator's default for the repository, which the managed rule overrides for this one
// directory by design; a rule that only sets other attributes is left alone.
func overridesDevContainerRule(line string) bool {
	fields := strings.Fields(line)
	if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
		return false
	}
	pattern := strings.TrimPrefix(strings.TrimPrefix(fields[0], `"`), "/")
	if !strings.HasPrefix(pattern, path.Dir(devcontainerFile)+"/") {
		return false
	}
	return slices.ContainsFunc(fields[1:], lineEndingOverride)
}

// lineEndingOverride reports whether one attribute of a rule gives text or eol a state other
// than "text eol=lf": text unset, unspecified or auto, eol unset, unspecified or not lf, or the
// binary macro, which unsets text.
func lineEndingOverride(attribute string) bool {
	name, _, _ := strings.Cut(strings.TrimLeft(attribute, "-!"), "=")
	switch name {
	case "text":
		return attribute != "text"
	case "eol":
		return attribute != "eol=lf"
	case "binary":
		return attribute == "binary"
	}
	return false
}

// GitAttributesCanonical reports whether text is a .gitattributes adoption leaves for a
// repository whose documentation facet is enabled or not (documentation): it ends with the
// block of ManagedAttributes, or holds no block where that block has no rules. One consistent
// checkout line-ending style is allowed. The block is accepted with and without the
// DevContainer rule: a repository adopted before that rule existed keeps passing audit, which
// names the rule where its absence shows, on a bundle a checkout converted. Ambiguous markers
// and mixed line endings are an error.
func GitAttributesCanonical(text string, documentation bool) (bool, error) {
	normalized, _, err := util.NormalizeLineEndingsStrict(text)
	if err != nil {
		return false, fmt.Errorf("%s line endings are inconsistent: %w", gitAttributesFile, err)
	}
	_, held, err := gitAttributesTailBlock().held(normalized)
	if err != nil {
		return false, err
	}
	for _, devContainer := range [...]bool{true, false} {
		block := ManagedGitAttributesBlock(ManagedAttributes(devContainer, documentation))
		if (block == "" && !held) || (block != "" && strings.HasSuffix(normalized, block)) {
			return true, nil
		}
	}
	return false, nil
}

// GitAttributesBlockPresent reports whether text carries an exact attribute-block marker line, so
// audit of a disabled facet can find a block adoption left behind. Ambiguous markers are an error.
func GitAttributesBlockPresent(text string) (bool, error) {
	_, present, err := gitAttributesTailBlock().strip(text)
	return present, err
}

// reconcileManagedAttributes brings the attribute block to the rules of this run
// (ManagedAttributes): the DevContainer rule unless adoption.decline lists dev-container, and
// the documentation rules while documentation says the facet is enabled. It is the one writer
// of the block, called by the documentation gate, the step that runs in every adoption.
func reconcileManagedAttributes(ctx context.Context, s *adoptSession, documentation bool) error {
	declined, err := ArtifactDeclined(s.declined, devContainerStep)
	if err != nil {
		return err
	}
	return reconcileGitAttributes(ctx, s, ManagedAttributes(!declined, documentation))
}

// preflightManagedAttributes refuses, before the first step writes anything, a .gitattributes
// the DevContainer rule cannot be merged into: an operator rule it would override, ambiguous
// block markers, mixed line endings or a file over the line bound. Checked only when the block
// is written, the refusal came after the manifest, the lock and the bundle were already there.
func preflightManagedAttributes(ctx context.Context, s *adoptSession, declined map[string]bool) error {
	if declined[devContainerStep] {
		return nil
	}
	full, err := repoFile(s.repoPath, gitAttributesFile)
	if err != nil {
		return err
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", gitAttributesFile, err)
	}
	if !exists {
		return nil
	}
	_, err = mergeGitAttributes(string(data), DevContainerAttributes())
	return err
}

// reconcileGitAttributes brings the attribute block of .gitattributes to rules: merged at the
// tail while a surface declares rules, removed once none does. A file that held only the block is
// deleted rather than left empty; a repository without .gitattributes and without rules is left
// alone.
func reconcileGitAttributes(ctx context.Context, s *adoptSession, rules []string) error {
	full, err := repoFile(s.repoPath, gitAttributesFile)
	if err != nil {
		return err
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", gitAttributesFile, err)
	}
	if !exists && len(rules) == 0 {
		return nil
	}
	merged, err := mergeGitAttributes(string(data), rules)
	if err != nil {
		return err
	}
	if exists && merged == string(data) {
		if len(rules) > 0 {
			s.report.recordReconciled(gitAttributesFile, "Managed attribute block already at the tail")
		}
		return nil
	}
	if len(rules) == 0 {
		return removeGitAttributesBlock(ctx, s, full, data, merged)
	}
	return publishGitAttributes(ctx, s, gitAttributesWrite{full: full, data: data, merged: merged, exists: exists})
}

// gitAttributesWrite is one planned write of .gitattributes: the bytes observed at full, whether
// the file existed, and the merged text that replaces them.
type gitAttributesWrite struct {
	full   string
	data   []byte
	merged string
	exists bool
}

// publish writes the merged text, bound to the observed bytes.
func (w gitAttributesWrite) publish(ctx context.Context) error {
	if err := contextopt.ReplaceSnapshot(ctx, w.full, []byte(w.merged),
		contextopt.ReplaceOptions{Expected: w.data, Exists: w.exists, Mode: filePerm}); err != nil {
		return fmt.Errorf("write %s: %w", gitAttributesFile, err)
	}
	return nil
}

// detail says what the write does to an existing file: it places the block at the tail, or,
// where the file held an unedited block of other rules (a facet toggled, a decline added or
// removed, or the block of an earlier release), brings that block to the rules of this run.
func (w gitAttributesWrite) detail() (string, error) {
	block := gitAttributesTailBlock()
	before, held, err := block.held(string(w.data))
	if err != nil {
		return "", err
	}
	after, _, err := block.held(w.merged)
	if err != nil {
		return "", err
	}
	if held && !slices.Equal(before, after) {
		return "Preserved existing rules and refreshed the managed attribute block at the tail to the rules of this run", nil
	}
	return "Preserved existing rules and placed the managed attribute block at the tail", nil
}

// publishGitAttributes writes the merged file and reports whether it was created, extended or,
// over an edited block under --force, replaced with a backup. Without --force an edited block
// is refused and the file stays as it is.
func publishGitAttributes(ctx context.Context, s *adoptSession, w gitAttributesWrite) error {
	edited, err := gitAttributesBlockEdited(string(w.data))
	if err != nil {
		return err
	}
	if edited && !s.opts.Force {
		return fmt.Errorf("%s managed attribute block was edited; review it and rerun %s", gitAttributesFile, s.forceCommand())
	}
	if edited {
		return s.replaceExisting(ctx, replacement{
			rel: gitAttributesFile, before: w.data, after: []byte(w.merged),
			detail: "Restored the managed attribute block at the tail", publish: w.publish,
		})
	}
	detail, err := w.detail()
	if err != nil {
		return err
	}
	if !s.opts.DryRun {
		if err := w.publish(ctx); err != nil {
			return err
		}
	}
	if w.exists {
		s.report.recordReconciledAs(gitAttributesFile, actionAppend, detail)
		return nil
	}
	s.report.recordCreated(gitAttributesFile, "Created the managed attribute block that keeps the files audit compares byte for byte unconverted")
	return nil
}

// removeGitAttributesBlock drops the block once no surface declares a rule: the documentation
// facet is disabled and the dev-container step is declined. The file is deleted when nothing
// else was in it.
func removeGitAttributesBlock(ctx context.Context, s *adoptSession, full string, data []byte, stripped string) error {
	detail := "Removed the managed attribute block because " + managedasset.DocumentationFacet + " is disabled and " +
		devContainerStep + " is declined"
	if stripped != "" {
		if !s.opts.DryRun {
			if err := contextopt.ReplaceSnapshot(ctx, full, []byte(stripped),
				contextopt.ReplaceOptions{Expected: data, Exists: true, Mode: filePerm}); err != nil {
				return fmt.Errorf("write %s: %w", gitAttributesFile, err)
			}
		}
		s.report.recordReconciledAs(gitAttributesFile, actionRemove, detail)
		return nil
	}
	if !s.opts.DryRun {
		if err := contextopt.RemoveSnapshot(ctx, full, data); err != nil {
			return fmt.Errorf("remove %s: %w", gitAttributesFile, err)
		}
	}
	s.planDryRunRemoval(gitAttributesFile)
	s.report.recordReconciledAs(gitAttributesFile, actionRemove, detail+"; the file held nothing else")
	return nil
}
