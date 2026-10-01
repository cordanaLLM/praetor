// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package generated

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxRevisionBytes bounds a revision answer of git (an object name and a newline).
const maxRevisionBytes = 4096

// repository is the git repository a check or a rendering reads, by its top-level directory.
type repository struct{ root string }

// openRepository resolves dir to the top of its git work tree.
func openRepository(ctx context.Context, dir string) (repository, error) {
	if strings.TrimSpace(dir) == "" {
		dir = "."
	}
	result, err := util.RunGitProbe(ctx, dir, maxRevisionBytes, "rev-parse", "--show-toplevel")
	if err != nil {
		return repository{}, fmt.Errorf("%s is not inside a git work tree: %w", dir, err)
	}
	return repository{root: filepath.Clean(strings.TrimSpace(string(result.Stdout)))}, nil
}

// commit resolves rev to the full object name of the commit it names.
func (r repository) commit(ctx context.Context, rev string) (string, error) {
	if err := util.ValidateExecArg(rev); err != nil {
		return "", fmt.Errorf("revision %q: %w", rev, err)
	}
	result, err := util.RunGitProbe(ctx, r.root, maxRevisionBytes, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("revision %q names no commit in %s: %w", rev, r.root, err)
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

// mergeBase returns the best common ancestor of base and head, the state a pull request is
// judged against: what the default branch gained since is not the change's.
func (r repository) mergeBase(ctx context.Context, base, head string) (string, error) {
	baseCommit, err := r.commit(ctx, base)
	if err != nil {
		return "", err
	}
	result, err := util.RunGitProbe(ctx, r.root, maxRevisionBytes, "merge-base", baseCommit, head)
	if err != nil {
		return "", fmt.Errorf("%s and %s share no merge base: %w", base, head, err)
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

// changedPaths lists every path that differs between the commits from and to, a renamed file
// under both its old and its new path, so a rename cannot carry an artefact out of its paths.
func (r repository) changedPaths(ctx context.Context, from, to string) ([]string, error) {
	return gitList(ctx, r.root, "diff", "--name-only", "-z", "--no-renames", "--no-ext-diff", from, to)
}

// resolveTree resolves the declaration of the manifest tree holds; a tree without a manifest
// declares only the built-in artefacts that apply to it.
func resolveTree(ctx context.Context, tree Tree) (*Set, error) {
	data, exists, err := tree.Read(ctx, config.ManifestFileName)
	if err != nil {
		return nil, err
	}
	var manifest *config.Manifest
	if exists {
		if manifest, err = config.ParseManifest(config.ManifestFileName, data); err != nil {
			return nil, err
		}
	}
	return Resolve(ctx, manifest, tree)
}

// List resolves the declaration of the checkout at dir as its working tree holds it.
func List(ctx context.Context, dir string) (*Set, error) {
	repo, err := openRepository(ctx, dir)
	if err != nil {
		return nil, err
	}
	manifest, err := config.LoadManifest(filepath.Join(repo.root, config.ManifestFileName))
	if errors.Is(err, fs.ErrNotExist) {
		manifest, err = nil, nil
	}
	if err != nil {
		return nil, err
	}
	return Resolve(ctx, manifest, DirTree(repo.root))
}
