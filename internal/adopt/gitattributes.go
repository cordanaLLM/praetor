// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/managedasset"
)

// A managed asset family whose files are hashed byte for byte declares the .gitattributes
// rules that keep a checkout from converting them (managedasset.Family.Attributes): the figure
// engine hashes its render files, the specs and the committed outputs, so a Windows checkout
// with core.autocrlf=true would otherwise report every figure stale
// (docs/adr/0016-figures-for-adopters.md, section 5). Adoption writes the rules of every
// enabled family as one block at the tail of .gitattributes, so a later operator rule cannot
// override them, and removes the block once no enabled family declares a rule.

const (
	gitAttributesFile         = ".gitattributes"
	gitAttributesManagedBegin = "# BEGIN praetor managed attributes (praetorctl adopt)"
	gitAttributesManagedEnd   = "# END praetor managed attributes"
	// gitAttributesHeader explains the block to whoever opens the file.
	gitAttributesHeader = "# praetorctl audit hashes these files; keep their checkout bytes the same on every platform."
	// maxGitAttributesLines bounds the .gitattributes file adoption reads (HISS-02).
	maxGitAttributesLines = 4096
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

// ManagedGitAttributesBlock returns the canonical block holding rules, shared by adoption and
// audit; "" when rules is empty, since adoption then writes no block.
func ManagedGitAttributesBlock(rules []string) string {
	if len(rules) == 0 {
		return ""
	}
	return gitAttributesTailBlock().render(append([]string{gitAttributesHeader}, rules...))
}

// mergeGitAttributes returns text with the block of rules as its tail, or without any block
// when rules is empty. Every line outside the block is the operator's and is kept.
func mergeGitAttributes(text string, rules []string) (string, error) {
	block := gitAttributesTailBlock()
	if len(rules) == 0 {
		stripped, _, err := block.strip(text)
		return stripped, err
	}
	return block.merge(text, ManagedGitAttributesBlock(rules))
}

// GitAttributesBlockPresent reports whether text carries an exact attribute-block marker line, so
// audit of a disabled facet can find a block adoption left behind. Ambiguous markers are an error.
func GitAttributesBlockPresent(text string) (bool, error) {
	_, present, err := gitAttributesTailBlock().strip(text)
	return present, err
}

// reconcileGitAttributes brings the attribute block of .gitattributes to rules: merged at the
// tail while a family declares rules, removed once none does. A file that held only the block is
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
	return publishGitAttributes(ctx, s, full, data, merged, exists)
}

// publishGitAttributes writes the merged file and reports whether it was created or extended.
func publishGitAttributes(ctx context.Context, s *adoptSession, full string, data []byte, merged string, exists bool) error {
	if !s.opts.DryRun {
		if err := contextopt.ReplaceSnapshot(ctx, full, []byte(merged),
			contextopt.ReplaceOptions{Expected: data, Exists: exists, Mode: filePerm}); err != nil {
			return fmt.Errorf("write %s: %w", gitAttributesFile, err)
		}
	}
	if exists {
		s.report.recordReconciledAs(gitAttributesFile, actionAppend,
			"Preserved existing rules and placed the managed attribute block at the tail")
		return nil
	}
	s.report.recordCreated(gitAttributesFile, "Created the managed attribute block that keeps hashed documentation files unconverted")
	return nil
}

// removeGitAttributesBlock drops the block of a disabled facet, deleting the file when nothing
// else was in it.
func removeGitAttributesBlock(ctx context.Context, s *adoptSession, full string, data []byte, stripped string) error {
	detail := "Removed the managed attribute block because " + managedasset.DocumentationFacet + " is disabled"
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
