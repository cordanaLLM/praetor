// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// Forge is the kind of forge that hosts the repository, declared as repository.forge in
// .standards.yaml. It selects the review push the Paperclip harness prescribes
// (internal/paperclip): Forgejo implements the AGit refs/for/<branch> push that opens a review,
// GitHub and GitLab do not (#321).
type Forge string

const (
	// ForgeGitHub is GitHub, the forge an undeclared repository on github.com resolves to.
	ForgeGitHub Forge = "github"
	// ForgeForgejo is Forgejo, the one declared forge whose harness carries the AGit push.
	ForgeForgejo Forge = "forgejo"
	// ForgeGitLab is GitLab.
	ForgeGitLab Forge = "gitlab"
)

// ForgeKey is the manifest key every forge error names.
const ForgeKey = "repository.forge"

// githubHost is the origin remote host under which an undeclared forge resolves to GitHub.
const githubHost = "github.com"

// forges is every value repository.forge accepts, in the order errors list them.
var forges = [...]Forge{ForgeGitHub, ForgeForgejo, ForgeGitLab}

// ErrForgeUndeclared marks a repository whose forge needs repository.forge: its origin remote
// names a host other than github.com, or no host at all.
var ErrForgeUndeclared = errors.New(ForgeKey + " required")

// forgeChoices names the accepted values for an error.
func forgeChoices() string {
	names := make([]string, 0, len(forges))
	for _, forge := range forges {
		names = append(names, string(forge))
	}
	return strings.Join(names, ", ")
}

// Forges returns every value repository.forge accepts, so a caller that renders per forge
// (the Paperclip refresh key) enumerates exactly that set.
func Forges() []Forge {
	return slices.Clone(forges[:])
}

// validForge reports whether forge is one repository.forge accepts.
func validForge(forge Forge) bool {
	return slices.Contains(forges[:], forge)
}

// UnmarshalYAML refuses a non-string, empty or unknown forge where the manifest is decoded, so
// the strict decoder (DecodeManifest) rejects it with the key named. An omitted key stays the
// zero value, which RepositoryForge resolves from the origin remote's host.
func (f *Forge) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return fmt.Errorf("%s must be a string: one of %s", ForgeKey, forgeChoices())
	}
	forge := Forge(node.Value)
	if !validForge(forge) {
		return fmt.Errorf("%s %q unsupported: one of %s", ForgeKey, node.Value, forgeChoices())
	}
	*f = forge
	return nil
}

// RepositoryForge is the forge of a repository that declares declared and whose origin remote
// names host (util.GitRemote.Host, lower case). A declaration wins. Without one, a repository on
// github.com is GitHub; any other host, and an empty one (no network origin remote), names no
// forge kind by itself, since Forgejo, Gitea and GitLab instances run under any host name, so
// the result wraps ErrForgeUndeclared and names the key instead of guessing.
func RepositoryForge(declared Forge, host string) (Forge, error) {
	if declared != "" {
		if !validForge(declared) {
			return "", fmt.Errorf("%s %q unsupported: one of %s", ForgeKey, declared, forgeChoices())
		}
		return declared, nil
	}
	if host == githubHost {
		return ForgeGitHub, nil
	}
	where := "the origin remote names host " + host
	if host == "" {
		where = "no network origin remote names a host"
	}
	return "", fmt.Errorf("%w: %s, not %s; declare %s (%s) in %s",
		ErrForgeUndeclared, where, githubHost, ForgeKey, forgeChoices(), ManifestFileName)
}

// ResolveRepositoryForge resolves the forge of the checkout at repo: the manifest's
// repository.forge, else the origin remote's host through RepositoryForge. A manifest that
// cannot be read or parsed and a remote read git did not answer are errors, as in
// ResolveRepositoryIdentity.
func ResolveRepositoryForge(ctx context.Context, repo string) (Forge, error) {
	if err := requireIdentityInputs(ctx, repo); err != nil {
		return "", err
	}
	metadata, err := manifestRepository(repo)
	if err != nil {
		return "", err
	}
	if metadata.Forge != "" {
		return RepositoryForge(metadata.Forge, "")
	}
	remote, err := util.ReadOriginRemote(ctx, repo)
	if err != nil && !errors.Is(err, util.ErrRepoIdentityUnresolved) {
		return "", fmt.Errorf("resolve %s: %w", ForgeKey, err)
	}
	return RepositoryForge("", remote.Host)
}
