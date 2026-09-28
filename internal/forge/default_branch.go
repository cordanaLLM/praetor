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
	recorded, ok, err := util.ReadOriginHeadBranch(ctx, repoPath)
	if err != nil {
		return "", fmt.Errorf("resolve the default branch of %s: %w", repoPath, err)
	}
	if !ok {
		return FallbackDefaultBranch, nil
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
