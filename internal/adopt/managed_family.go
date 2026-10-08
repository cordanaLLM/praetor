// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Adoption's side of the managed asset family registry (internal/managedasset): emitting a
// family, refusing to overwrite a repository's own files on first adoption, and removing the
// families of a disabled facet. The documentation gate (documentation.go) drives it for every
// family behind docs:seo-portal; nothing here names a family.

// refreshedFamilyDetail is the action detail when an earlier Praetor text of a managed asset
// family is refreshed.
const refreshedFamilyDetail = "Refreshed an earlier Praetor text to the current locked text"

// reconcileManagedFamily writes every asset of family and then its hosted workflow, rendered for
// the repository's default branch (FamilyForRepository). An existing file with the canonical
// text, in one consistent line-ending style, is verified and left alone; one holding an earlier
// Praetor text of that path (Family.Prior) or the workflow rendered for another default branch
// is refreshed; any other differing file is preserved unless --force regenerates it.
func reconcileManagedFamily(ctx context.Context, s *adoptSession, family managedasset.Family) error {
	family, err := FamilyForRepository(ctx, s.repoPath, family)
	if err != nil {
		return err
	}
	if family.RefuseForeign {
		if err := refuseForeignFamilyFiles(ctx, s, family); err != nil {
			return err
		}
	}
	names := family.Names()
	for index := 0; index < len(names) && index < family.MaxAssets; index++ {
		data, err := family.Read(names[index])
		if err != nil {
			return err
		}
		rel := family.AssetPath(names[index])
		if _, err := reconcileManagedFamilyFile(ctx, s, family, scaffold{
			rel: rel, perm: filePerm, content: data, auditLocked: true,
			created:  "Scaffolded locked " + family.AssetNoun,
			verified: "Existing " + family.AssetNoun + " preserved; audit verifies canonical text",
		}); err != nil {
			return fmt.Errorf("reconcile %s: %w", rel, err)
		}
	}
	if family.WorkflowFile == "" {
		return nil
	}
	_, err = reconcileManagedFamilyFile(ctx, s, family, scaffold{
		rel: family.WorkflowFile, perm: filePerm, content: []byte(family.Workflow), auditLocked: true,
		created:  "Scaffolded required " + family.WorkflowNoun,
		verified: "Existing " + family.WorkflowNoun + " preserved; audit verifies canonical text",
	})
	return err
}

// FamilyForRepository returns family with its hosted workflow rendered for the repository at
// repoPath: for its default branch (managedasset.Family.ForBranch), the branch the branch ruleset
// protects, resolved as forge.RepositoryDefaultBranch resolves it for the ruleset; for the
// settings its manifest declares (managedasset.Family.ForManifest), such as api.system_packages;
// and in the draft shape its manifest selects (hosted_gates.draft,
// managedasset.Family.WithDraftShape). Adoption writes this rendering and audit locks a copy to
// it, so the two resolve one branch, one manifest and one shape. A family without a hosted
// workflow is returned unchanged without reading anything.
func FamilyForRepository(ctx context.Context, repoPath string, family managedasset.Family) (managedasset.Family, error) {
	if family.WorkflowFile == "" {
		return family, nil
	}
	manifest, err := loadDeclaredManifest(ctx, repoPath)
	if err != nil {
		return managedasset.Family{}, fmt.Errorf("render the %s workflow %s: %w", family.Kind, family.WorkflowFile, err)
	}
	if family, err = family.ForManifest(manifest); err != nil {
		return managedasset.Family{}, err
	}
	if family, err = family.WithDraftShape(manifest != nil && manifest.HostedGates.DraftSkip()); err != nil {
		return managedasset.Family{}, err
	}
	if !family.BranchDependent() {
		return family, nil
	}
	branch, err := forge.RepositoryDefaultBranch(ctx, repoPath, manifest)
	if err != nil {
		return managedasset.Family{}, fmt.Errorf("render the %s workflow %s: %w", family.Kind, family.WorkflowFile, err)
	}
	return family.ForBranch(branch)
}

func reconcileManagedFamilyFile(ctx context.Context, s *adoptSession, family managedasset.Family, sc scaffold) (scaffoldState, error) {
	full, err := repoFile(s.repoPath, sc.rel)
	if err != nil {
		return 0, err
	}
	actual, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return 0, err
	}
	if exists {
		equivalent, compareErr := util.CanonicalTextEquivalent(actual, sc.content)
		if compareErr != nil {
			return 0, fmt.Errorf("%s has invalid line endings: %w", sc.rel, compareErr)
		}
		if equivalent {
			s.report.recordReconciled(sc.rel, sc.verified)
			return scaffoldIdentical, nil
		}
		// An exact earlier Praetor text is refreshed in its own line-ending style, bound to
		// the observed bytes, by the refresh the scaffold earlier-text sets use.
		if known, crlf := family.PriorRendering(sc.rel, actual); known {
			return scaffoldWritten, s.replacePriorText(ctx, full, actual, crlf, sc, refreshedFamilyDetail)
		}
	}
	return s.scaffoldFile(ctx, sc)
}

// refuseForeignFamilyFiles is refuse-on-first-adopt. The family counts as adopted once any of
// its managed paths holds its canonical text; from then on a differing file is Praetor's own
// asset drifted, which --force may regenerate. Until then every existing file at a managed
// path predates adoption and belongs to the repository, so adoption fails naming it, even
// under --force, before it writes anything of the family.
func refuseForeignFamilyFiles(ctx context.Context, s *adoptSession, family managedasset.Family) error {
	paths := family.ManagedPaths()
	foreign := ""
	for index := 0; index < len(paths) && index <= family.MaxAssets; index++ {
		canonical, exists, err := observeManagedFile(ctx, s, family, paths[index])
		if err != nil {
			return err
		}
		if canonical {
			return nil
		}
		if exists && foreign == "" {
			foreign = paths[index]
		}
	}
	if foreign == "" {
		return nil
	}
	return fmt.Errorf("refusing to adopt the %s family over %s: the file predates adoption and differs from the Praetor asset; move it aside, then rerun adopt",
		family.Name, foreign)
}

// observeManagedFile reports whether rel exists and whether it holds family's canonical text
// or an earlier Praetor text of that path, either of which shows the family was adopted.
func observeManagedFile(ctx context.Context, s *adoptSession, family managedasset.Family, rel string) (canonical, exists bool, err error) {
	full, err := repoFile(s.repoPath, rel)
	if err != nil {
		return false, false, err
	}
	actual, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return false, false, fmt.Errorf("inspect managed %s asset %s: %w", family.Kind, rel, err)
	}
	if !exists {
		return false, false, nil
	}
	canonical, err = ManagedFileIsPraetors(family, rel, actual)
	return canonical, true, err
}

// ManagedFileIsPraetors reports whether actual is Praetor's own unedited text at rel: the
// family's canonical text (ManagedFileIsCanonical) or an earlier text of that path
// (Family.PriorText). Adoption treats either as an adopted file, and audit of a disabled
// facet as a Praetor file left behind.
func ManagedFileIsPraetors(family managedasset.Family, rel string, actual []byte) (bool, error) {
	canonical, err := ManagedFileIsCanonical(family, rel, actual)
	if err != nil {
		return false, err
	}
	return canonical || family.PriorText(rel, actual), nil
}

// ManagedFileIsCanonical reports whether actual is family's exact text at rel, allowing one
// consistent checkout line-ending style. Text with mixed endings is never canonical.
func ManagedFileIsCanonical(family managedasset.Family, rel string, actual []byte) (bool, error) {
	expected, owned, err := family.Canonical(rel)
	if err != nil {
		return false, err
	}
	if !owned {
		return false, fmt.Errorf("unknown %s asset %q", family.Kind, rel)
	}
	actualLF, valid := classifiableManagedText(actual)
	if !valid {
		return false, nil
	}
	expectedLF, valid := classifiableManagedText(expected)
	if !valid {
		return false, fmt.Errorf("canonical %s asset %s has invalid line endings", family.Kind, rel)
	}
	return actualLF == expectedLF, nil
}

func classifiableManagedText(data []byte) (string, bool) {
	normalized, _, err := util.NormalizeLineEndingsStrict(string(data))
	return normalized, err == nil
}

// managedFamilyOwning returns the family among families whose managed paths include rel.
func managedFamilyOwning(families []managedasset.Family, rel string) (managedasset.Family, bool, error) {
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		_, owned, err := families[index].Canonical(rel)
		if err != nil || owned {
			return families[index], owned, err
		}
	}
	return managedasset.Family{}, false, nil
}

// managedPathsOf returns every managed path of families, family by family in registry order.
func managedPathsOf(families []managedasset.Family) []string {
	paths := make([]string, 0, len(families)*2)
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		paths = append(paths, families[index].ManagedPaths()...)
	}
	return paths
}

// managedRemoval is one canonical managed file planned for removal, with the bytes observed
// so the removal fails if the file changes in between.
type managedRemoval struct {
	rel, full string
	expected  []byte
	family    managedasset.Family
}

// removeManagedFamilies deletes the canonical files of every family, and files holding an
// earlier Praetor text of their path, in two passes: the first inspects every path and
// refuses a drifted file or one with mixed line endings, the second removes. A refusal
// therefore deletes nothing.
func removeManagedFamilies(ctx context.Context, s *adoptSession, families []managedasset.Family) error {
	removals := make([]managedRemoval, 0, len(families)*2)
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		planned, err := planManagedFamilyRemovals(ctx, s, families[index])
		if err != nil {
			return err
		}
		removals = append(removals, planned...)
	}
	return applyManagedFamilyRemovals(ctx, s, removals)
}

func planManagedFamilyRemovals(ctx context.Context, s *adoptSession, family managedasset.Family) ([]managedRemoval, error) {
	paths := family.ManagedPaths()
	removals := make([]managedRemoval, 0, len(paths))
	for index := 0; index < len(paths) && index <= family.MaxAssets; index++ {
		rel := paths[index]
		full, err := repoFile(s.repoPath, rel)
		if err != nil {
			return nil, err
		}
		expected, _, err := family.Canonical(rel)
		if err != nil {
			return nil, err
		}
		actual, exists, err := contextopt.ObserveSnapshot(ctx, full)
		if err != nil {
			return nil, fmt.Errorf("inspect disabled %s asset %s: %w", family.Kind, rel, err)
		}
		if !exists {
			continue
		}
		equivalent, compareErr := util.CanonicalTextEquivalent(actual, expected)
		if compareErr != nil {
			return nil, fmt.Errorf("refusing to remove %s asset %s with invalid line endings: %w",
				family.Kind, rel, compareErr)
		}
		if !equivalent && !family.PriorText(rel, actual) {
			return nil, fmt.Errorf("refusing to remove drifted %s asset %s", family.Kind, rel)
		}
		removals = append(removals, managedRemoval{rel: rel, full: full, expected: actual, family: family})
	}
	return removals, nil
}

func applyManagedFamilyRemovals(ctx context.Context, s *adoptSession, removals []managedRemoval) error {
	for index := 0; index < len(removals); index++ {
		removal := removals[index]
		if !s.opts.DryRun {
			if err := contextopt.RemoveSnapshot(ctx, removal.full, removal.expected); err != nil {
				return fmt.Errorf("remove disabled %s asset %s: %w", removal.family.Kind, removal.rel, err)
			}
		}
		s.planDryRunRemoval(removal.rel)
		s.report.recordReconciledAs(removal.rel, actionRemove, fmt.Sprintf(
			"Removed canonical %s asset because %s is disabled", removal.family.Kind, removal.family.Facet))
	}
	return nil
}
