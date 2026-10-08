// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/util"
)

// DefaultRadarRegistryPath is the source registry `praetorctl radar` reads when the manifest
// declares a radar without naming radar.registry (#818).
const DefaultRadarRegistryPath = ".config/radar.yaml"

// RadarPolicy is the manifest's radar section. Declaring the section says the repository keeps
// a research and upstream radar; Registry names the file that lists its sources, which
// internal/radar decodes and validates. The registry lives in a file of its own so a long source
// list neither bloats the manifest nor shares its schema version. It is repository-only, like
// Documentation, and stays out of ResolvedPolicy.
type RadarPolicy struct {
	Registry string `yaml:"registry,omitempty"`
}

// RegistryPath returns the declared registry, or DefaultRadarRegistryPath when the key is
// empty. It is safe on a nil policy.
func (p *RadarPolicy) RegistryPath() string {
	if p == nil || p.Registry == "" {
		return DefaultRadarRegistryPath
	}
	return p.Registry
}

// validateManifestRadar holds a declared radar.registry to the repository path shape every other
// manifest path takes (ValidRepositoryPath).
func validateManifestRadar(m *Manifest) error {
	if m == nil || m.Radar == nil || m.Radar.Registry == "" {
		return nil
	}
	if !ValidRepositoryPath(m.Radar.Registry) {
		return fmt.Errorf("radar.registry %q must be a clean local forward-slash path of at most %d bytes",
			m.Radar.Registry, maxRepositoryPath)
	}
	return nil
}

// RepositoryRadarRegistry returns the radar registry the manifest of the repository at root
// names, as the forward-slash path the manifest spells, and whether the manifest declares a radar
// at all. A repository without a manifest, or whose manifest has no radar section, declares none.
// A manifest that does not load is an error: the caller cannot tell whether a radar was meant.
func RepositoryRadarRegistry(root string) (string, bool, error) {
	if root == "" {
		root = "."
	}
	manifestPath := filepath.Join(root, ManifestFileName)
	if !util.FileExists(manifestPath) {
		return "", false, nil
	}
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return "", false, err
	}
	if manifest.Radar == nil {
		return "", false, nil
	}
	return manifest.Radar.RegistryPath(), true, nil
}
