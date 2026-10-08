// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/managedasset"
)

// Positive: every registry asset is written with its canonical bytes and listed.
func TestExportWritesEveryManagedAsset(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{root}, &stdout, &stderr); code != 0 {
		t.Fatalf("run exited %d: %s", code, stderr.String())
	}
	listed := strings.Fields(stdout.String())
	assets, err := managedasset.Assets()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != len(assets) || len(assets) == 0 {
		t.Fatalf("listed %d paths for %d assets", len(listed), len(assets))
	}
	for _, asset := range assets {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(asset.Path)))
		if err != nil || !bytes.Equal(got, asset.Data) {
			t.Errorf("%s was not exported byte for byte: %v", asset.Path, err)
		}
		if !slices.Contains(listed, asset.Path) {
			t.Errorf("%s is not listed", asset.Path)
		}
	}
}

// Negative: a wrong argument count, an empty directory name and a second export over the
// first are refused.
func TestExportRefusesBadInput(t *testing.T) {
	root := t.TempDir()
	for name, args := range map[string][]string{
		"no argument": nil,
		"two":         {root, root},
		"empty":       {""},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code == 0 {
			t.Errorf("%s: exited 0", name)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{root}, &stdout, &stderr); code != 0 {
		t.Fatalf("first export: %s", stderr.String())
	}
	if code := run([]string{root}, &stdout, &stderr); code == 0 {
		t.Error("an export over an existing tree exited 0")
	}
}

// Boundary: the registry reaches the assets the issues name.
func TestExportReachesTheNamedAssets(t *testing.T) {
	paths, err := export(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"tools/apicompat/gate/main.go",
		"tools/figures/mkdocs_hook.py",
		"tools/figures/third_party/interfig/VENDOR.md",
		".github/workflows/praetor-docs.yml",
		"tools/markdownlint/markdownlint-cli2.yaml",
	} {
		if !slices.Contains(paths, rel) {
			t.Errorf("the export does not reach %s", rel)
		}
	}
}
