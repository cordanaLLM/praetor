// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>

package devcontainer

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	golangciModulePath = "github.com/golangci/golangci-lint/v2"
	golangciLegacyPath = "github.com/golangci/golangci-lint"
	lintModRelative    = "tools/lint/go.mod"
)

func readLintModBytes(sourceRoot string) ([]byte, error) {
	confined, err := util.ConfinePath(sourceRoot, lintModRelative)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Join(sourceRoot, lintModRelative), err)
	}
	data, err := util.ReadFileLimited(confined, contextopt.MaxSourceBytes)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", confined, err)
	}
	return data, nil
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

func checkUnpinnedLint(text string, spec *BootstrapSpec) error {
	if strings.Contains(text, "golangci-lint@latest") {
		return errors.New("bootstrap Dockerfile contains unpinned golangci-lint@latest install step; version must be pinned")
	}
	if spec != nil && (spec.GolangciLintVersion == "latest" || strings.HasSuffix(spec.GolangciLintVersion, "@latest")) {
		return errors.New("bootstrap spec contains unpinned golangci-lint version: latest")
	}
	return nil
}

func verifyRequiredLintInstall(text string, spec *BootstrapSpec) error {
	if spec == nil || spec.GolangciLintVersion == "" || !isValidLintVersion(spec.GolangciLintVersion) {
		return errors.New("devcontainer settings declare go.lintTool: golangci-lint but bootstrap spec does not pin a valid golangci-lint version")
	}
	wantInstall := fmt.Sprintf("RUN GOBIN=/out /usr/local/go/bin/go install -trimpath %s/cmd/golangci-lint@%s", golangciModulePath, spec.GolangciLintVersion)
	wantCopy := "COPY --from=praetor_build --chmod=0555 /out/golangci-lint /usr/local/bin/golangci-lint"
	if !strings.Contains(text, wantInstall) || !strings.Contains(text, wantCopy) {
		return errors.New("devcontainer settings declare go.lintTool: golangci-lint but bootstrap Dockerfile does not install golangci-lint")
	}
	return nil
}

// verifyLintToolInstall checks that a bootstrap Dockerfile installs golangci-lint pinned
// when settings configure it, and refuses unpinned installations (@latest).
func verifyLintToolInstall(dc *DevContainer, dockerfile []byte, spec *BootstrapSpec) error {
	text := string(dockerfile)
	if err := checkUnpinnedLint(text, spec); err != nil {
		return err
	}
	if !hasGoLintTool(dc) || recordsEarlierDockerfileRendering(spec) {
		return nil
	}
	return verifyRequiredLintInstall(text, spec)
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
