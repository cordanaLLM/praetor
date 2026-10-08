// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// LoadRepositoryManifest is the one reader of the manifest of the repository at repoPath that
// adoption and the ruleset's default-branch resolution share: the path is confined to repoPath
// (a symlink out of the repository is refused), the file is read as one bounded snapshot under
// ctx, and the bytes pass the decoder and every validation of ParseManifest. A repository with
// no manifest is (nil, nil), which is what a first adoption means; a manifest that exists but
// cannot be resolved, read, decoded or validated is an error, so a caller that acts on "not
// declared" fails closed instead of treating an unreadable decision as no decision.
func LoadRepositoryManifest(ctx context.Context, repoPath string) (*Manifest, error) {
	if ctx == nil {
		return nil, errors.New("loading the repository manifest requires a context")
	}
	full, err := util.ConfinePath(repoPath, ManifestFileName)
	if err != nil {
		return nil, fmt.Errorf("resolve the adoption manifest: refusing %s: %w", ManifestFileName, err)
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ManifestFileName, err)
	}
	if !exists {
		return nil, nil
	}
	return ParseManifest(ManifestFileName, data)
}
