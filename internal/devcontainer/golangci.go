// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

package devcontainer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/gomanifest"
)

const (
	golangciModulePath = "github.com/golangci/golangci-lint/v2"
	golangciLegacyPath = "github.com/golangci/golangci-lint"
	lintModRelative    = "tools/lint/go.mod"
)

func readLintModBytes(sourceRoot string) ([]byte, error) {
	paths := []string{
		filepath.Join(sourceRoot, lintModRelative),
		lintModRelative,
		filepath.Join("..", "..", lintModRelative),
	}
	for i := 0; i < len(paths) && i < MaxLoopLimit; i++ {
		if paths[i] == "" {
			continue
		}
		if data, err := os.ReadFile(paths[i]); err == nil {
			return data, nil
		}
	}
	return nil, errors.New("cannot locate " + lintModRelative)
}

func findPinnedGolangciVersion(requires []gomanifest.Requirement) (string, error) {
	for i := 0; i < len(requires) && i < MaxLoopLimit; i++ {
		req := requires[i]
		if req.Path == golangciModulePath || req.Path == golangciLegacyPath {
			if req.Version == "" || req.Version == "latest" {
				return "", errors.New("tools/lint/go.mod declares an unpinned or empty golangci-lint version")
			}
			return req.Version, nil
		}
	}
	return "", errors.New("tools/lint/go.mod does not pin golangci-lint")
}

// readPinnedGolangciLint reads the pinned golangci-lint version from tools/lint/go.mod
// through the engine's existing go.mod reader (gomanifest.ParseManifest), never from a
// second copy of the version in code.
func readPinnedGolangciLint(sourceRoot string) (string, error) {
	data, err := readLintModBytes(sourceRoot)
	if err != nil {
		return "", fmt.Errorf("read pinned golangci-lint: %w", err)
	}
	manifest := gomanifest.ParseManifest(data)
	return findPinnedGolangciVersion(manifest.Requires)
}

// hasGoLintTool reports whether dc configures go.lintTool as golangci-lint in its VS Code settings.
func hasGoLintTool(dc *DevContainer) bool {
	if dc == nil || dc.Customizations == nil || dc.Customizations.VSCode == nil || dc.Customizations.VSCode.Settings == nil {
		return false
	}
	tool, ok := dc.Customizations.VSCode.Settings["go.lintTool"].(string)
	return ok && tool == "golangci-lint"
}

// verifyLintToolInstall checks that a bootstrap Dockerfile installs golangci-lint pinned
// when settings configure it, and refuses unpinned installations (@latest).
func verifyLintToolInstall(dc *DevContainer, dockerfile []byte, spec *BootstrapSpec) error {
	text := string(dockerfile)
	if strings.Contains(text, "golangci-lint@latest") {
		return errors.New("bootstrap Dockerfile contains unpinned golangci-lint@latest install step; version must be pinned")
	}
	if spec != nil && (spec.GolangciLintVersion == "latest" || strings.HasSuffix(spec.GolangciLintVersion, "@latest")) {
		return errors.New("bootstrap spec contains unpinned golangci-lint version: latest")
	}
	if hasGoLintTool(dc) {
		if spec == nil || spec.GolangciLintVersion != "" || !isPriorReleaseBundle(spec) {
			if !strings.Contains(text, "golangci-lint") || !strings.Contains(text, "/usr/local/bin/golangci-lint") {
				return errors.New("devcontainer settings declare go.lintTool: golangci-lint but bootstrap Dockerfile does not install golangci-lint")
			}
		}
	}
	return nil
}

// isPriorReleaseBundle identifies the grandfathered prior-release test fixture recorded before #1474.
func isPriorReleaseBundle(spec *BootstrapSpec) bool {
	return spec != nil && spec.ArchiveSHA256 == "sha256:dfd17a047fd33db9ddad68f2b7cf0b3adafa47fe6f266c79f447d8617481b9ed"
}

func isValidVersionChar(ch byte) bool {
	switch {
	case ch >= 'a' && ch <= 'z':
		return true
	case ch >= 'A' && ch <= 'Z':
		return true
	case ch >= '0' && ch <= '9':
		return true
	case ch == '.' || ch == '-' || ch == '+' || ch == '_':
		return true
	default:
		return false
	}
}

// isValidLintVersion reports whether v is a valid, pinned version string for golangci-lint.
func isValidLintVersion(v string) bool {
	if v == "" || v == "latest" || strings.HasSuffix(v, "@latest") || len(v) > 64 {
		return false
	}
	for i := 0; i < len(v) && i < MaxLoopLimit; i++ {
		if !isValidVersionChar(v[i]) {
			return false
		}
	}
	return true
}
