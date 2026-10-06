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

// ErrRepositoryNameInvalid is returned by ResolveRepositoryName and ManifestRepositoryName
// when the name repository.name or the origin remote gives is one GitHub would reject, such
// as the "." of an origin remote ending in "/.".
var ErrRepositoryNameInvalid = errors.New("repository name invalid: repository.name in .standards.yaml or the origin remote names no valid repository")

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
	if err := requireIdentityInputs(ctx, repo); err != nil {
		return "", "", err
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

// ResolveRepositoryName resolves the name of the repository checked out at repo the way
// ResolveRepositoryIdentity resolves it, without an owner: the manifest's repository.name,
// then the origin remote. A command that only names the repository, such as a needs scan or
// a pre-migration epic, names it through this resolver, so it gives the name the forge
// coordinates of the same checkout carry, whatever directory the checkout sits in.
//
// Neither source naming the repository is ErrRepositoryNameUnknown and a name GitHub would
// reject is ErrRepositoryNameInvalid. A manifest that cannot be read or parsed and a remote
// read git did not answer are errors, as in ResolveRepositoryIdentity.
func ResolveRepositoryName(ctx context.Context, repo string) (string, error) {
	if err := requireIdentityInputs(ctx, repo); err != nil {
		return "", err
	}
	_, name, err := manifestIdentity(repo)
	if err != nil {
		return "", err
	}
	if name == "" {
		if _, name, err = fillFromRemote(ctx, repo, "", ""); err != nil {
			return "", err
		}
	}
	return validatedName(name)
}

// ManifestRepositoryName is the first step of ResolveRepositoryName alone: the manifest's
// repository.name. A directory that is no checkout has no origin remote of its own (git
// would read the remote of a checkout above it), so it is named through this step only. The
// errors are ResolveRepositoryName's.
func ManifestRepositoryName(repo string) (string, error) {
	if !util.DirExists(repo) {
		return "", notRepositoryDirError(repo)
	}
	_, name, err := manifestIdentity(repo)
	if err != nil {
		return "", err
	}
	return validatedName(name)
}

// requireIdentityInputs refuses a nil context and a repo that is not a directory.
func requireIdentityInputs(ctx context.Context, repo string) error {
	if ctx == nil {
		return errors.New("repository identity resolution requires a context")
	}
	if !util.DirExists(repo) {
		return notRepositoryDirError(repo)
	}
	return nil
}

// notRepositoryDirError refuses to resolve identity from anything but a directory.
func notRepositoryDirError(repo string) error {
	return fmt.Errorf("%q is not a repository directory: forge coordinates are resolved from a checkout, not from a repository name", repo)
}

// manifestIdentity reads repository.owner and repository.name from .standards.yaml. A
// repository without a manifest has neither.
func manifestIdentity(repo string) (owner, name string, err error) {
	metadata, err := manifestRepository(repo)
	return metadata.Owner, metadata.Name, err
}

// manifestRepository reads the repository block of repo's .standards.yaml through the strict
// manifest loader. A repository without a manifest declares nothing.
func manifestRepository(repo string) (RepositoryMetadata, error) {
	path := filepath.Join(repo, ManifestFileName)
	if !util.FileExists(path) {
		return RepositoryMetadata{}, nil
	}
	manifest, err := LoadManifest(path)
	if err != nil {
		return RepositoryMetadata{}, fmt.Errorf("resolve repository identity: %w", err)
	}
	return manifest.Repository, nil
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

// validatedName returns name when it names a repository GitHub would accept.
func validatedName(name string) (string, error) {
	if name == "" {
		return "", ErrRepositoryNameUnknown
	}
	if err := util.ValidateGitHubRepositoryName(name); err != nil {
		return "", fmt.Errorf("%w: %w", ErrRepositoryNameInvalid, err)
	}
	return name, nil
}
