// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workstation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/buildid"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// RefreshSettings is what the selected operator settings contribute to a refresh: the update
// branch (update.branch) whose checkout may refresh the install, and the settings documents
// the refreshed manifest records again.
type RefreshSettings struct {
	Branch      string
	Fleet       config.SettingsDocument
	Workstation config.SettingsDocument
}

// SettingsSelector selects the RefreshSettings for the install manifest at manifestPath.
// Refresh calls it only once the checkout and the manifest qualify, so a checkout that is not
// the engine's never reads the operator's settings.
type SettingsSelector func(ctx context.Context, manifestPath string) (RefreshSettings, error)

// RefreshOptions configures Refresh. Install is what a refresh installs with; an empty
// BinDir refreshes the bin directory the manifest recorded, and the settings documents come
// from Select. Module is the running engine's main module path, which Install.Checkout must
// declare.
type RefreshOptions struct {
	Install Options
	Module  string
	Select  SettingsSelector
}

// RefreshResult reports what Refresh did: whether it reinstalled, why, how far the install
// was behind the checkout, and the install it made.
type RefreshResult struct {
	Refreshed     bool    `json:"refreshed"`
	Reason        string  `json:"reason"`
	CommitsBehind int     `json:"commits_behind,omitempty"`
	Install       *Result `json:"install,omitempty"`
}

// Bounds on the refresh probes (HISS-02).
const (
	refreshProbeBytes = 64 << 10
	statusProbeBytes  = 1 << 20
)

// Refresh reinstalls an existing install from its checkout when the install lags it, so a
// post-merge hook or a scheduler can run it after every change without ever installing
// something the operator did not already choose (BUG-1004). It refreshes only when all hold:
// the checkout declares the running engine's module, an install manifest exists, the
// checkout is on the update branch, the installed commit is a strict ancestor of the
// checkout HEAD (never a downgrade or a sideways move) or is the HEAD itself while the
// manifest lacks a binary this engine installs (#377), and no tracked file is modified.
// Otherwise it installs nothing and reports the first reason that failed.
func Refresh(ctx context.Context, opts RefreshOptions) (RefreshResult, error) {
	if ctx == nil || opts.Select == nil {
		return RefreshResult{}, errors.New("workstation: refresh requires a context and a settings selector")
	}
	if opts.Module == "" || checkoutModule(opts.Install.Checkout) != opts.Module {
		return skipRefresh("source is not a checkout of this engine's module"), nil
	}
	plan, reason, err := planRefresh(ctx, opts)
	if err != nil || reason != "" {
		return skipRefresh(reason), err
	}
	behind, reason, err := refreshGate(ctx, plan)
	if err != nil || reason != "" {
		return skipRefresh(reason), err
	}
	result, err := Install(ctx, plan.install)
	if err != nil {
		return RefreshResult{}, err
	}
	return RefreshResult{Refreshed: true, CommitsBehind: behind, Install: &result,
		Reason: refreshReason(behind, plan.missing)}, nil
}

func skipRefresh(reason string) RefreshResult {
	return RefreshResult{Reason: reason}
}

// refreshReason says why a refresh reinstalled: the commits the install lagged, the binaries
// its manifest lacked, or both.
func refreshReason(behind int, missing []string) string {
	reasons := make([]string, 0, 2)
	if behind > 0 {
		reasons = append(reasons, "install was "+strconv.Itoa(behind)+" commits behind the checkout")
	}
	if len(missing) > 0 {
		reasons = append(reasons, "install lacked "+strings.Join(missing, ", "))
	}
	return strings.Join(reasons, "; ")
}

// refreshPlan is the install a qualifying refresh makes, the commit it replaces, the binaries
// its manifest lacks and the update branch the checkout must be on.
type refreshPlan struct {
	install   Options
	installed string
	missing   []string
	branch    string
}

// planRefresh reads the install manifest and the selected settings into the install a
// refresh would make, or returns the reason there is nothing installed to refresh.
func planRefresh(ctx context.Context, opts RefreshOptions) (refreshPlan, string, error) {
	install := opts.Install
	var err error
	if install.ManifestPath, err = manifestPathOrDefault(install.ManifestPath); err != nil {
		return refreshPlan{}, "", err
	}
	manifest, err := config.ReadInstallManifest(ctx, install.ManifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return refreshPlan{}, "no install manifest at " + install.ManifestPath + "; nothing installed to refresh", nil
	}
	if err != nil {
		return refreshPlan{}, "", fmt.Errorf("workstation: refresh: %w", err)
	}
	settings, err := opts.Select(ctx, install.ManifestPath)
	if err != nil {
		return refreshPlan{}, "", fmt.Errorf("workstation: refresh: select settings: %w", err)
	}
	install.FleetSettings, install.WorkstationSettings = settings.Fleet, settings.Workstation
	if install.BinDir == "" {
		install.BinDir = manifest.BinDir
	}
	return refreshPlan{install: install, installed: manifest.EngineCommit, missing: missingBinaries(manifest),
		branch: settings.Branch}, "", nil
}

// manifestPathOrDefault returns path, or the default install manifest path when path is
// empty: the one default Install, Status and Refresh all resolve.
func manifestPathOrDefault(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	resolved, err := config.DefaultInstallManifestPath()
	if err != nil {
		return "", fmt.Errorf("workstation: resolve default manifest path: %w", err)
	}
	return resolved, nil
}

// refreshGate returns how many commits the install is behind the plan's checkout, or the
// reason the checkout must not refresh it; see Refresh. An install at the checkout HEAD
// qualifies only while its manifest lacks a binary.
func refreshGate(ctx context.Context, plan refreshPlan) (int, string, error) {
	checkout := plan.install.Checkout
	current, err := currentBranch(ctx, checkout)
	if err != nil {
		return 0, "", err
	}
	if plan.branch == "" || current != plan.branch {
		return 0, fmt.Sprintf("checkout is on %q, not the update branch %q", current, plan.branch), nil
	}
	behind, ancestor, err := InstallLag(ctx, checkout, plan.installed)
	if err != nil {
		return 0, "", err
	}
	if !ancestor || (behind == 0 && len(plan.missing) == 0) {
		return 0, "installed commit " + buildid.Short(plan.installed) + " is not behind the checkout HEAD", nil
	}
	dirty, err := trackedChanges(ctx, checkout)
	if err != nil || dirty {
		return 0, "checkout has modified tracked files", err
	}
	return behind, "", nil
}

// InstallLag reports how many commits checkout HEAD is ahead of the installed commit.
// ancestor is false, with a zero count, when installed is not an ancestor of HEAD or is not
// in the checkout at all: the install then is ahead, on another line, or from another
// repository, and no count of commits behind describes it.
func InstallLag(ctx context.Context, checkout, installed string) (behind int, ancestor bool, err error) {
	if !objectNamePattern.MatchString(installed) {
		return 0, false, nil
	}
	_, status, err := util.RunGitProbeStatus(ctx, checkout, refreshProbeBytes,
		"rev-parse", "--verify", "--quiet", installed+"^{commit}")
	if err != nil || status != 0 {
		return 0, false, wrapLagErr(err)
	}
	if _, status, err = util.RunGitProbeStatus(ctx, checkout, refreshProbeBytes,
		"merge-base", "--is-ancestor", installed, "HEAD"); err != nil || status != 0 {
		return 0, false, wrapLagErr(err)
	}
	counted, err := util.RunGitProbe(ctx, checkout, refreshProbeBytes, "rev-list", "--count", installed+"..HEAD")
	if err != nil {
		return 0, false, wrapLagErr(err)
	}
	behind, err = strconv.Atoi(strings.TrimSpace(string(counted.Stdout)))
	if err != nil {
		return 0, false, wrapLagErr(err)
	}
	return behind, true, nil
}

func wrapLagErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("workstation: compare the installed commit with the checkout: %w", err)
}

// currentBranch returns the short name of the branch checkout has checked out, or "" for a
// detached HEAD.
func currentBranch(ctx context.Context, checkout string) (string, error) {
	result, status, err := util.RunGitProbeStatus(ctx, checkout, refreshProbeBytes,
		"symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", fmt.Errorf("workstation: read the checkout branch: %w", err)
	}
	if status != 0 {
		return "", nil
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

// trackedChanges reports whether any tracked file in checkout differs from HEAD or the
// index: an install built from it would carry edits no commit names.
func trackedChanges(ctx context.Context, checkout string) (bool, error) {
	result, err := util.RunGitProbeWithin(ctx, checkout, statusProbeBytes, buildInputProbeTimeout,
		"status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, fmt.Errorf("workstation: read the checkout status: %w", err)
	}
	return len(strings.TrimSpace(string(result.Stdout))) > 0, nil
}
