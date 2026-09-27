package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/workstation"
)

// runWorkstationRefresh is `workstation install --if-stale`: it refreshes an existing install
// from --source only when workstation.Refresh finds it lagging on the update branch, and
// otherwise prints why it installed nothing. A post-merge hook or a scheduler runs it after
// every change (BUG-1004).
func runWorkstationRefresh(ctx context.Context, settings operatorSettingsFlags, sourceFlag, binDirFlag string) error {
	if sourceFlag == "" {
		return errors.New("workstation install: --source is required")
	}
	checkout, err := filepath.Abs(sourceFlag)
	if err != nil {
		return fmt.Errorf("workstation install: resolve --source: %w", err)
	}
	binDir, err := absOrEmpty(binDirFlag)
	if err != nil {
		return fmt.Errorf("workstation install: resolve --bin-dir: %w", err)
	}
	manifestPath, err := refreshManifestPath(settings.manifest)
	if err != nil {
		return err
	}
	result, err := workstation.Refresh(ctx, workstation.RefreshOptions{
		Install: workstation.Options{Checkout: checkout, BinDir: binDir, ManifestPath: manifestPath},
		Module:  engineBuild().Module,
		Select:  refreshSelector(settings),
	})
	if err != nil {
		return err
	}
	return printJSON(result)
}

// refreshManifestPath absolutizes an explicit --manifest, or selects the default install
// manifest through installManifestPath, which tests replace.
func refreshManifestPath(flagValue string) (string, error) {
	if flagValue != "" {
		path, err := filepath.Abs(flagValue)
		if err != nil {
			return "", fmt.Errorf("workstation install: resolve --manifest: %w", err)
		}
		return path, nil
	}
	path, err := installManifestPath()
	if err != nil {
		return "", fmt.Errorf("workstation install: resolve default manifest path: %w", err)
	}
	return path, nil
}

// refreshSelector selects a refresh's update branch and settings documents: an explicit
// --fleet-config or --workstation-config, else the document the install manifest recorded
// (config.SelectOperatorSettings without the environment). A refresh reinstalls what was
// installed, so it records the same documents again unless a flag names another.
func refreshSelector(flags operatorSettingsFlags) workstation.SettingsSelector {
	return func(ctx context.Context, manifestPath string) (workstation.RefreshSettings, error) {
		fleet, err := absOrEmpty(flags.fleet)
		if err != nil {
			return workstation.RefreshSettings{}, fmt.Errorf("resolve --fleet-config: %w", err)
		}
		workstationDoc, err := absOrEmpty(flags.workstation)
		if err != nil {
			return workstation.RefreshSettings{}, fmt.Errorf("resolve --workstation-config: %w", err)
		}
		selection, err := config.SelectOperatorSettings(ctx, config.SettingsRequest{
			FleetFlag: fleet, WorkstationFlag: workstationDoc, ManifestPath: manifestPath,
		})
		if err != nil {
			return workstation.RefreshSettings{}, err
		}
		settings, err := config.LoadOperatorSettings(ctx, selection)
		if err != nil {
			return workstation.RefreshSettings{}, err
		}
		return workstation.RefreshSettings{Branch: settings.Update.Branch,
			Fleet: selection.Fleet, Workstation: selection.Workstation}, nil
	}
}
