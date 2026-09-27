// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ErrOwnerUnknown is returned when no step of owner resolution names an owner. Commands
// that act on a forge refuse instead of substituting one.
var ErrOwnerUnknown = errors.New("owner unknown: pass --owner, set repository.owner, or set forge.default_owner")

// ErrRepositoryNameUnknown is returned when neither the repository manifest nor the origin
// remote names the repository.
var ErrRepositoryNameUnknown = errors.New("repository name unknown: set repository.name in .standards.yaml or add an origin remote naming <owner>/<repo>")

// ResolveRepositoryIdentity resolves the forge owner and repository name of the checkout at
// repo (ADR-0014 §3). The owner comes from explicitOwner (a --owner flag), then the
// manifest's repository.owner, then the origin remote (util.ResolveRemoteIdentity), then
// defaultOwner (forge.default_owner); the name from the manifest's repository.name, then the
// origin remote. The checkout path is never read: a parent directory names wherever the
// checkout sits, not its owner.
//
// A manifest that cannot be read or parsed, and a remote read git did not answer (a
// cancelled context, git failing to run), are errors: identity is never resolved around a
// question that was not answered. Neither source naming an owner is ErrOwnerUnknown, neither
// naming the repository is ErrRepositoryNameUnknown, and a resolved pair GitHub would reject
// is an error naming it.
func ResolveRepositoryIdentity(ctx context.Context, repo, explicitOwner, defaultOwner string) (owner, name string, err error) {
	if ctx == nil {
		return "", "", errors.New("repository identity resolution requires a context")
	}
	if !util.DirExists(repo) {
		return "", "", fmt.Errorf("%q is not a repository directory: forge coordinates are resolved from a checkout, not from a repository name", repo)
	}
	owner, name, err = manifestIdentity(repo)
	if err != nil {
		return "", "", err
	}
	if explicitOwner != "" {
		owner = explicitOwner
	}
	if owner == "" || name == "" {
		if owner, name, err = fillFromRemote(ctx, repo, owner, name); err != nil {
			return "", "", err
		}
	}
	if owner == "" {
		owner = defaultOwner
	}
	return validatedIdentity(owner, name)
}

// manifestIdentity reads repository.owner and repository.name from .standards.yaml. A
// repository without a manifest has neither.
func manifestIdentity(repo string) (owner, name string, err error) {
	path := filepath.Join(repo, ".standards.yaml")
	if !util.FileExists(path) {
		return "", "", nil
	}
	manifest, err := LoadManifest(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve repository identity: %w", err)
	}
	return manifest.Repository.Owner, manifest.Repository.Name, nil
}

// fillFromRemote fills whichever of owner and name is still empty from the origin remote. A
// repository without a network origin remote leaves both as they are.
func fillFromRemote(ctx context.Context, repo, owner, name string) (string, string, error) {
	remoteOwner, remoteName, err := util.ResolveRemoteIdentity(ctx, repo)
	if errors.Is(err, util.ErrRepoIdentityUnresolved) {
		return owner, name, nil
	}
	if err != nil {
		return "", "", fmt.Errorf("resolve repository identity: %w", err)
	}
	if owner == "" {
		owner = remoteOwner
	}
	if name == "" {
		name = remoteName
	}
	return owner, name, nil
}

func validatedIdentity(owner, name string) (string, string, error) {
	if owner == "" {
		return "", "", ErrOwnerUnknown
	}
	if name == "" {
		return "", "", ErrRepositoryNameUnknown
	}
	if err := util.ValidateGitHubRepositoryIdentity(owner, name); err != nil {
		return "", "", fmt.Errorf("resolve repository identity: %w", err)
	}
	return owner, name, nil
}
