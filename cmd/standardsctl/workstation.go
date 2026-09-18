package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/clientsetup"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/workstation"
)

const workstationUsage = "usage: workstation install --source PATH [--bin-dir PATH] [--manifest PATH] " +
	"[--fleet-config PATH] [--workstation-config PATH] | " +
	"workstation status [--source PATH] [--bin-dir PATH] [--manifest PATH] [--home PATH]"

// workstationTimeout bounds a workstation install or status run, including the build.
const workstationTimeout = 5 * time.Minute

func runWorkstation(args []string) error {
	if len(args) < 1 {
		return errors.New(workstationUsage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), workstationTimeout)
	defer cancel()
	switch args[0] {
	case "install":
		return runWorkstationInstall(ctx, args[1:])
	case "status":
		return runWorkstationStatus(ctx, args[1:])
	case "help", "-h", "--help":
		fmt.Println(workstationUsage)
		return nil
	default:
		return fmt.Errorf("unknown workstation subcommand %q\n%s", args[0], workstationUsage)
	}
}

func runWorkstationInstall(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("workstation install", flag.ContinueOnError)
	source := fs.String("source", "", "Checkout to build the three engine binaries from (required)")
	binDir := fs.String("bin-dir", "", "Destination directory (default: update.bin_dir, else the per-OS default)")
	manifest := fs.String("manifest", "", "Install manifest path (default: the per-user configuration directory)")
	fleetConfig := fs.String("fleet-config", "", "Fleet settings document; recorded into the install manifest")
	workstationConfig := fs.String("workstation-config", "", "Workstation settings document; recorded into the install manifest")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("workstation install accepts no positional arguments")
	}
	opts, err := resolveInstallOptions(ctx, *source, *binDir, *manifest, *fleetConfig, *workstationConfig)
	if err != nil {
		return err
	}
	result, err := workstation.Install(ctx, opts)
	if err != nil {
		return err
	}
	return printJSON(result)
}

func resolveInstallOptions(ctx context.Context, sourceFlag, binDirFlag, manifestFlag, fleetFlag, workstationFlag string) (workstation.Options, error) {
	if sourceFlag == "" {
		return workstation.Options{}, errors.New("workstation install: --source is required")
	}
	checkout, err := filepath.Abs(sourceFlag)
	if err != nil {
		return workstation.Options{}, fmt.Errorf("workstation install: resolve --source: %w", err)
	}
	settings, err := loadInstallSettings(ctx, checkout, fleetFlag, workstationFlag)
	if err != nil {
		return workstation.Options{}, err
	}
	binDir, err := resolveInstallBinDir(binDirFlag, settings)
	if err != nil {
		return workstation.Options{}, err
	}
	return workstation.Options{
		Checkout: checkout, BinDir: binDir, ManifestPath: manifestFlag,
		FleetSettings:       settingsDocumentFlag(fleetFlag),
		WorkstationSettings: settingsDocumentFlag(workstationFlag),
	}, nil
}

// loadInstallSettings resolves the fleet/workstation layered policy documents (section 6.1)
// only when at least one is named: a plain `workstation install --source .` with neither
// flag needs no settings document and no manifest at Root/.standards.yaml.
func loadInstallSettings(ctx context.Context, root, fleetFlag, workstationFlag string) (config.OperatorSettings, error) {
	if fleetFlag == "" && workstationFlag == "" {
		return config.DefaultOperatorSettings(), nil
	}
	policy, err := config.LoadEffectivePolicyContext(ctx, config.EffectiveOptions{
		Root: root, FleetPath: fleetFlag, WorkstationPath: workstationFlag,
	})
	if err != nil {
		return config.OperatorSettings{}, fmt.Errorf("workstation install: load settings: %w", err)
	}
	return policy.OperatorSettings(), nil
}

// resolveInstallBinDir follows section 6.1's order for the one host path install itself
// resolves: the explicit flag, then the loaded update.bin_dir setting, then the per-OS
// default (C1, internal/clientsetup/roots.go).
func resolveInstallBinDir(explicit string, settings config.OperatorSettings) (string, error) {
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	if settings.Update.BinDir != "" {
		return settings.Update.BinDir, nil
	}
	home, err := resolveHomeSubdir("", "--bin-dir")
	if err != nil {
		return "", fmt.Errorf("workstation install: %w", err)
	}
	return clientsetup.Locate(clientsetup.LocationBinDir, hostClientEnv(home, false))
}

func settingsDocumentFlag(path string) config.SettingsDocument {
	if path == "" {
		return config.SettingsDocument{Origin: config.SettingsNotConfigured}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return config.SettingsDocument{Path: abs, Origin: config.SettingsFromFlag}
}

func runWorkstationStatus(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("workstation status", flag.ContinueOnError)
	source := fs.String("source", "", "Checkout to compare the installed commit against (optional)")
	binDir := fs.String("bin-dir", "", "Bin directory to check for an installation lock (default: the manifest's own bin_dir)")
	manifest := fs.String("manifest", "", "Install manifest path (default: the per-user configuration directory)")
	homeFlag := fs.String("home", "", "Workstation home directory for per-client root resolution (default: $HOME)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("workstation status accepts no positional arguments")
	}
	opts, err := resolveStatusOptions(*source, *binDir, *manifest, *homeFlag)
	if err != nil {
		return err
	}
	report, err := workstation.Status(ctx, opts)
	if err != nil {
		return err
	}
	return printJSON(report)
}

func resolveStatusOptions(sourceFlag, binDirFlag, manifestFlag, homeFlag string) (workstation.StatusOptions, error) {
	opts := workstation.StatusOptions{BinDir: binDirFlag, ManifestPath: manifestFlag}
	if sourceFlag != "" {
		checkout, err := filepath.Abs(sourceFlag)
		if err != nil {
			return workstation.StatusOptions{}, fmt.Errorf("workstation status: resolve --source: %w", err)
		}
		opts.Checkout = checkout
	}
	home, err := resolveHomeSubdir(homeFlag, "--home")
	if err != nil {
		return workstation.StatusOptions{}, fmt.Errorf("workstation status: %w", err)
	}
	opts.ClientEnv = hostClientEnv(home, homeFlag != "")
	return opts, nil
}

// printJSON writes value as one line of JSON to stdout, the convention
// cmd/standardsctl/clients_capabilities.go and cmd/standardsctl/operational.go both use for
// a machine-readable report.
func printJSON(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode workstation report: %w", err)
	}
	data = append(data, '\n')
	_, err = os.Stdout.Write(data)
	return err
}
