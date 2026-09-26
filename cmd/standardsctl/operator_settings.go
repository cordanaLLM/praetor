package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/cordanaLLM/praetor/internal/config"
)

// installManifestPath locates the install manifest that records the host's settings
// documents. Tests replace it so no test reads the operator's own manifest.
var installManifestPath = config.DefaultInstallManifestPath

// operatorSettingsFlags selects the operator settings documents of one command: the fleet
// and workstation documents, and the install manifest that records them. Unset flags fall
// back to PRAETOR_FLEET_CONFIG / PRAETOR_WORKSTATION_CONFIG, then to the manifest
// (config.SelectOperatorSettings).
type operatorSettingsFlags struct {
	fleet, workstation, manifest string
}

// defaultOperatorSettingsFlags selects through the environment and the default install
// manifest only, the way a command without settings flags (hook, agent) reads its settings.
// A host with no per-user configuration directory (no home, a minimal container) selects no
// manifest instead of failing over a directory the manifest does not need to exist.
func defaultOperatorSettingsFlags() *operatorSettingsFlags {
	manifest, err := installManifestPath()
	if err != nil {
		manifest = ""
	}
	return &operatorSettingsFlags{manifest: manifest}
}

// registerOperatorSettingsFlags adds --fleet-config, --workstation-config and --manifest to
// fs. An explicit empty --manifest selects no manifest.
func registerOperatorSettingsFlags(fs *flag.FlagSet) *operatorSettingsFlags {
	opts := defaultOperatorSettingsFlags()
	fs.StringVar(&opts.fleet, "fleet-config", "", "Fleet operator settings document")
	fs.StringVar(&opts.workstation, "workstation-config", "", "Workstation operator settings document")
	fs.StringVar(&opts.manifest, "manifest", opts.manifest, "Installed settings-selection manifest")
	return opts
}

// loadPolicy resolves and loads the selected documents (config.SelectOperatorPolicy). No
// document selected returns a nil policy, whose settings are the built-in defaults.
func (o *operatorSettingsFlags) loadPolicy(ctx context.Context) (*config.EffectivePolicy, error) {
	policy, err := config.SelectOperatorPolicy(ctx, config.SettingsRequest{
		FleetFlag: o.fleet, WorkstationFlag: o.workstation, Getenv: os.Getenv, ManifestPath: o.manifest,
	})
	if err != nil {
		return nil, fmt.Errorf("load operator settings: %w", err)
	}
	return policy, nil
}

// load resolves the selected operator settings.
func (o *operatorSettingsFlags) load(ctx context.Context) (config.OperatorSettings, error) {
	policy, err := o.loadPolicy(ctx)
	if err != nil {
		return config.OperatorSettings{}, err
	}
	return policy.OperatorSettings(), nil
}
