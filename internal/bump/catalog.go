package bump

import (
	"context"
	"strings"
)

// FleetCatalog provides canonical, fleet-unified dependency versions.
var FleetCatalog = map[string]string{
	// Go core standards
	"gopkg.in/yaml.v3":               "v3.0.1",
	"github.com/google/uuid":         "v1.6.0",
	"github.com/spf13/cobra":         "v1.8.1",
	"github.com/stretchr/testify":    "v1.9.0",
	"golang.org/x/sync":              "v0.10.0",
	"golang.org/x/sys":               "v0.29.0",
	"golang.org/x/crypto":            "v0.32.0",
	"golang.org/x/text":              "v0.21.0",
	"google.golang.org/protobuf":     "v1.36.2",
	"google.golang.org/grpc":         "v1.70.0",
	"go.uber.org/zap":                "v1.27.0",
	"github.com/sirupsen/logrus":     "v1.9.3",

	// Node / TypeScript / Svelte standards
	"typescript":                     "^5.7.3",
	"svelte":                         "^5.19.0",
	"@types/node":                    "^22.10.7",
	"vite":                           "^6.0.7",
	"prettier":                       "^3.4.2",
	"eslint":                         "^9.18.0",
}

// ReconcileCatalog identifies dependencies in repoPath that drift from the FleetCatalog.
func ReconcileCatalog(ctx context.Context, repoPath string) ([]UpgradeCandidate, error) {
	candidates, err := ScanDependencies(ctx, repoPath, false)
	if err != nil {
		return nil, err
	}

	var unified []UpgradeCandidate
	allDiscovered := append(candidates.Stables, candidates.Prereleases...)

	for _, c := range allDiscovered {
		canonicalVer, found := FleetCatalog[c.Package]
		if !found {
			continue
		}

		cleanCurrent := strings.TrimPrefix(c.CurrentVersion, "^")
		cleanCurrent = strings.TrimPrefix(cleanCurrent, "~")
		cleanTarget := strings.TrimPrefix(canonicalVer, "^")
		cleanTarget = strings.TrimPrefix(cleanTarget, "~")

		if cleanCurrent != cleanTarget {
			unified = append(unified, UpgradeCandidate{
				Package:        c.Package,
				CurrentVersion: c.CurrentVersion,
				TargetVersion:  canonicalVer,
				Channel:        ChannelStable,
				ManifestType:   c.ManifestType,
				ModuleDir:      c.ModuleDir,
			})
		}
	}

	return unified, nil
}
