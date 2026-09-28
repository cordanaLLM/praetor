// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// FallbackDefaultBranch is the default branch of a repository that declares none and whose
// checkout records none: the branch every ruleset Praetor rendered before it read the default
// branch protected.
const FallbackDefaultBranch = "main"

// RepositoryDefaultBranch resolves the default branch of the repository at repoPath, the branch
// its ruleset protects (RepositoryRulesetRefs). It is the one resolution adoption, flavor apply,
// sync and the audit render and validate the ruleset under, first match wins:
//
//  1. repository.default_branch in manifest, or, when manifest is nil, in repoPath's
//     .standards.yaml if there is one. The declaration is committed, so every clone and every
//     CI checkout renders the same ruleset.
//  2. The origin remote's HEAD as the checkout records it (util.ReadOriginHeadBranch), which git
//     clone writes. A CI checkout usually does not have it.
//  3. FallbackDefaultBranch.
//
// Nothing asks the forge: the audit and a dry run render offline. A manifest that cannot be read
// or declares a malformed branch, and an origin HEAD git did not answer or that names a branch
// config.ValidBranchName refuses, are errors, not a reason to fall through to the next source.
func RepositoryDefaultBranch(ctx context.Context, repoPath string, manifest *config.Manifest) (string, error) {
	if ctx == nil {
		return "", errors.New("resolving the default branch requires a context")
	}
	declared, err := declaredDefaultBranch(repoPath, manifest)
	if err != nil || declared != "" {
		return declared, err
	}
	recorded, err := recordedDefaultBranch(ctx, repoPath)
	if err != nil || recorded != "" {
		return recorded, err
	}
	return FallbackDefaultBranch, nil
}

// DefaultBranchToDeclare returns the branch a manifest for the repository at repoPath declares as
// repository.default_branch so that every checkout resolves the default branch this one does
// (RepositoryDefaultBranch): the origin remote's HEAD as this checkout records it, when that is
// not FallbackDefaultBranch. A CI checkout usually records no origin HEAD and would resolve
// FallbackDefaultBranch, so a ruleset rendered here from the origin HEAD alone fails its audit.
//
// It is "" when the checkout records no origin HEAD or records FallbackDefaultBranch, which every
// checkout resolves without a declaration. An origin HEAD git did not answer or that names a
// branch config.ValidBranchName refuses is an error, as in RepositoryDefaultBranch. The manifest
// writers (adoption, init, harvester onboarding) record it in the manifest they create; adoption
// warns about an existing manifest that leaves it undeclared.
func DefaultBranchToDeclare(ctx context.Context, repoPath string) (string, error) {
	if ctx == nil {
		return "", errors.New("reading the default branch to declare requires a context")
	}
	recorded, err := recordedDefaultBranch(ctx, repoPath)
	if err != nil || recorded == FallbackDefaultBranch {
		return "", err
	}
	return recorded, nil
}

// recordedDefaultBranch returns the origin remote's HEAD as the checkout at repoPath records it
// (util.ReadOriginHeadBranch), or "" when it records none. One config.ValidBranchName refuses is
// an error that asks for a declaration.
func recordedDefaultBranch(ctx context.Context, repoPath string) (string, error) {
	recorded, ok, err := util.ReadOriginHeadBranch(ctx, repoPath)
	if err != nil {
		return "", fmt.Errorf("resolve the default branch of %s: %w", repoPath, err)
	}
	if !ok {
		return "", nil
	}
	if !config.ValidBranchName(recorded) {
		return "", fmt.Errorf("resolve the default branch of %s: the origin remote's HEAD names %q, which cannot be rendered; "+
			"declare repository.default_branch in %s", repoPath, recorded, config.ManifestFileName)
	}
	return recorded, nil
}

// declaredDefaultBranch returns manifest's repository.default_branch, or that of repoPath's
// .standards.yaml when manifest is nil, or "" when neither declares one.
func declaredDefaultBranch(repoPath string, manifest *config.Manifest) (string, error) {
	if manifest == nil {
		path := filepath.Join(repoPath, config.ManifestFileName)
		if !util.FileExists(path) {
			return "", nil
		}
		loaded, err := config.LoadManifest(path)
		if err != nil {
			return "", fmt.Errorf("read repository.default_branch: %w", err)
		}
		manifest = loaded
	}
	declared := manifest.Repository.DefaultBranch
	if declared != "" && !config.ValidBranchName(declared) {
		return "", fmt.Errorf("repository.default_branch %q is not a branch name", declared)
	}
	return declared, nil
}
