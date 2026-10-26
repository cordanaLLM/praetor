// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func floatingGolangciReference(text string) bool {
	return strings.Contains(text, "golangci-lint@latest")
}

func TestGolangciPinCheckRejectsPlantedLatest(t *testing.T) {
	if !floatingGolangciReference("go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest run") {
		t.Fatal("floating golangci-lint command was not refused")
	}
	if floatingGolangciReference("go tool -modfile=tools/lint/go.mod golangci-lint run --allow-serial-runners") {
		t.Fatal("pinned golangci-lint command was refused")
	}
}

func TestGolangciPinCheckCurrentSources(t *testing.T) {
	root := filepath.Join("..", "..")
	paths := []string{
		".config/lefthook/scripts/checks.py",
		"internal/flavor/definitions.go",
		"Makefile", // lint-nested ran golangci-lint@latest for tools/schemacheck until 2026-10-18
	}
	for _, directory := range []string{"templates", "docs/guides"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			ext := filepath.Ext(path)
			if entry.IsDir() || ext != ".tmpl" && ext != ".md" && ext != ".yml" && ext != ".yaml" {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			paths = append(paths, rel)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range paths {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		if floatingGolangciReference(string(data)) {
			t.Errorf("floating golangci-lint version in %s", path)
		}
	}
}

func TestGoServiceGolangciInstallGuideUsesPinnedTool(t *testing.T) {
	service, err := Get("go-service")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range service.RequiredToolchains() {
		if item.Binary == "golangci-lint" {
			if item.InstallGuide != "go tool -modfile=tools/lint/go.mod golangci-lint run --allow-serial-runners" {
				t.Fatalf("golangci-lint install guide = %q", item.InstallGuide)
			}
			return
		}
	}
	t.Fatal("go-service has no golangci-lint toolchain")
}
