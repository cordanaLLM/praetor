// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package secrets exposes the locked Go secrets history scan gate program and assets.
package secrets

import (
	"embed"
	"io/fs"
	"slices"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// Directory is the repository-relative home of the gate.
	Directory = "tools/secrets"
	// SourceFile is the Go file carrying the go:embed directive over the assets.
	SourceFile = Directory + "/assets.go"
	// GateFile is the gate program, relative to Directory.
	GateFile = "gate/main.go"
	// RulesGenFile is the generated rules file, relative to Directory.
	RulesGenFile = "gate/rules_gen.go"
	// PlaceholderFile is the Go file that stands in for GateFile in a build without BuildTag.
	PlaceholderFile = "gate/placeholder.go"
	// BuildTag is the build constraint that keeps GateFile out of ./... patterns; PlaceholderFile
	// holds its negation.
	BuildTag = "secretsgate"
	// MaxAssets bounds all asset iteration.
	MaxAssets = 3
	// EmbedDirective is the exact go:embed directive over the assets.
	EmbedDirective = "//go:embed gate/main.go gate/rules_gen.go gate/placeholder.go"
)

var assetNames = [...]string{GateFile, RulesGenFile, PlaceholderFile}

//go:embed gate/main.go gate/rules_gen.go gate/placeholder.go
var assets embed.FS

// FS returns the embedded asset tree. It is read-only; Read is the bounded accessor.
func FS() fs.FS {
	return assets
}

// Names returns the complete deterministic asset inventory.
func Names() []string {
	return slices.Clone(assetNames[:min(len(assetNames), MaxAssets)])
}

// AssetPaths returns the repository-relative paths of all embedded assets.
func AssetPaths() []string {
	names := Names()
	paths := make([]string, len(names))
	for i, name := range names {
		paths[i] = Directory + "/" + name
	}
	return paths
}

// Read returns one canonical asset without exposing mutable embedded storage.
func Read(name string) ([]byte, error) {
	return util.ReadEmbeddedAsset(assets, "secrets", Names(), name)
}
