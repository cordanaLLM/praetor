package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ownerDefaultHelp is the default clause of every owner-dependent --owner flag: the
// resolution order of ADR-0014 §3, after the flag itself. No command names a built-in owner.
const ownerDefaultHelp = "(default: repository.owner, else origin remote owner, else forge.default_owner)"

// loadForgeSettings reads the forge section of the operator settings the command's
// --fleet-config, --workstation-config and --manifest flags select (operatorSettingsFlags).
func loadForgeSettings(ctx context.Context, settings *operatorSettingsFlags) (config.ForgeSettings, error) {
	loaded, err := settings.load(ctx)
	if err != nil {
		return config.ForgeSettings{}, err
	}
	return loaded.Forge, nil
}

// resolveForgeOwner resolves the owner an owner-scoped forge command acts for (ADR-0014 §3):
// explicit (the --owner flag), then the manifest's repository.owner in dir, then the origin
// remote of dir, then defaultOwner (forge.default_owner). Neither naming one is
// config.ErrOwnerUnknown; no owner is invented. Every step but the manifest is
// config.ResolveRepositoryIdentity. That resolver refuses an identity without a repository
// name, which an owner-scoped command (project boards, bare repository names) does not
// need: its ErrRepositoryNameUnknown, with no explicit or default owner passed and so no
// remote answer, means the manifest named the owner, which is then read on its own.
func resolveForgeOwner(ctx context.Context, dir, explicit, defaultOwner string) (string, error) {
	if explicit != "" {
		return validForgeOwner(explicit, "--owner")
	}
	owner, _, err := config.ResolveRepositoryIdentity(ctx, dir, "", "")
	switch {
	case err == nil:
		return owner, nil
	case errors.Is(err, config.ErrOwnerUnknown) && defaultOwner != "":
		return validForgeOwner(defaultOwner, "forge.default_owner")
	case errors.Is(err, config.ErrRepositoryNameUnknown):
		return manifestOwner(dir)
	}
	return "", fmt.Errorf("resolve forge owner of %s: %w", dir, err)
}

// validForgeOwner returns owner when GitHub would accept it, naming source otherwise.
func validForgeOwner(owner, source string) (string, error) {
	if err := util.ValidateGitHubOwner(owner); err != nil {
		return "", fmt.Errorf("%s: %w", source, err)
	}
	return owner, nil
}

// manifestOwner reads repository.owner from dir's manifest, the one owner source
// config.ResolveRepositoryIdentity cannot hand back without a repository name.
func manifestOwner(dir string) (string, error) {
	manifest, err := config.LoadManifest(filepath.Join(dir, config.ManifestFileName))
	if err != nil {
		return "", fmt.Errorf("resolve forge owner of %s: %w", dir, err)
	}
	return validForgeOwner(manifest.Repository.Owner, "repository.owner")
}

// resolveForgeRepository resolves the owner and name a repository-scoped forge command acts
// on. Without explicitRepo both come from config.ResolveRepositoryIdentity over dir, with
// explicitOwner and defaultOwner as its first and last owner steps; with it the owner is
// resolveForgeOwner's and the pair must be one GitHub accepts.
func resolveForgeRepository(ctx context.Context, dir, explicitOwner, explicitRepo, defaultOwner string) (string, string, error) {
	if explicitRepo == "" {
		owner, name, err := config.ResolveRepositoryIdentity(ctx, dir, explicitOwner, defaultOwner)
		if err != nil {
			return "", "", fmt.Errorf("resolve forge repository of %s: %w", dir, err)
		}
		return owner, name, nil
	}
	owner, err := resolveForgeOwner(ctx, dir, explicitOwner, defaultOwner)
	if err != nil {
		return "", "", err
	}
	if err := util.ValidateGitHubRepositoryIdentity(owner, explicitRepo); err != nil {
		return "", "", fmt.Errorf("--repo: %w", err)
	}
	return owner, explicitRepo, nil
}
