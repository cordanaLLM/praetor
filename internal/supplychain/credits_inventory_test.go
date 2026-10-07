// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// inventoryIDs returns the kind and id of each item, as "kind id".
func inventoryIDs(items []InventoryItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.Kind+" "+item.ID)
	}
	return ids
}

// Positive: each reader lists what its file names.
func TestCreditInventoryReadersPositive(t *testing.T) {
	for name, test := range map[string]struct {
		read inventoryReader
		text string
		want []string
	}{
		"go.mod": {goModInventory, "module m\n\ngo 1.27\n\ntool (\n\texample.com/lint/cmd/lint\n)\n\ntool example.com/vet // vet\n\nrequire (\n\texample.com/lib v1.0.0\n\texample.com/dep v1.0.0 // indirect\n)\n",
			[]string{"Go tool example.com/lint/cmd/lint", "Go tool example.com/vet", "Go module example.com/lib"}},
		"package.json": {npmInventory, `{"name":"x","dependencies":{"b":"1"},"devDependencies":{"a":"2"},"peerDependencies":{"c":">=1"},"optionalDependencies":{"d":"1"}}`,
			[]string{"npm package a", "npm package b", "npm package c", "npm package d"}},
		"requirements.in": {pypiInventory, "# lock\n--require-hashes\nBlack==26.10.0\nflake8 >=7 ; python_version > '3.9'\nmkdocs[i18n]==1\n",
			[]string{"PyPI package black", "PyPI package flake8", "PyPI package mkdocs"}},
		"Dockerfile": {dockerInventory, "FROM golang:1.27@sha256:abc AS build\nFROM --platform=linux/amd64 gcr.io/distroless/static:nonroot\n",
			[]string{"image golang", "image gcr.io/distroless/static"}},
		"devcontainer.json": {featureInventory, "{\n  // the base\n  \"image\": \"mcr.microsoft.com/devcontainers/base:2\",\n  \"features\": {\"ghcr.io/devcontainers/features/go:1\": {},},\n}\n",
			[]string{"devcontainer feature ghcr.io/devcontainers/features/go", "image mcr.microsoft.com/devcontainers/base"}},
		"workflow": {actionInventory, "jobs:\n  a:\n    steps:\n      - uses: actions/checkout@v7\n      - uses: ./.github/actions/local\n      - uses: Azure/setup-helm/sub@v5\n",
			[]string{"action actions/checkout", "action azure/setup-helm"}},
	} {
		items, err := test.read(name, test.text)
		if err != nil || !slices.Equal(inventoryIDs(items), test.want) {
			t.Errorf("%s: items = %v (%v), want %v", name, inventoryIDs(items), err, test.want)
		}
	}
}

// Negative: a manifest that does not parse is an error naming the file, and a package.json that
// repeats a member is refused rather than read by whichever copy wins.
func TestCreditInventoryReadersNegative(t *testing.T) {
	for name, test := range map[string]struct {
		read inventoryReader
		text string
	}{
		"package.json, not JSON":       {npmInventory, "{"},
		"package.json, repeated key":   {npmInventory, `{"dependencies":{"a":"1"},"dependencies":{"b":"1"}}`},
		"package.json, wrong type":     {npmInventory, `{"dependencies":["a"]}`},
		"devcontainer.json, not JSONC": {featureInventory, "{\"features\": }"},
		"go.mod past its bound":        {goModInventory, strings.Repeat("\n", maxNoticeLines)},
	} {
		if _, err := test.read("manifest", test.text); err == nil || !strings.Contains(err.Error(), "manifest") {
			t.Errorf("%s: err = %v, want one naming the file", name, err)
		}
	}
}

// Boundary: a local dependency, an indirect requirement, a FROM naming an earlier stage, scratch
// or a build argument, and a requirement line holding only options name nothing; a dependency
// local in one group and third-party in another is listed once.
func TestCreditInventoryReadersBoundary(t *testing.T) {
	items, err := npmInventory("p", `{"dependencies":{"w":"workspace:*","f":"file:../f","a":"1"},"devDependencies":{"a":"link:../a"}}`)
	if err != nil || !slices.Equal(inventoryIDs(items), []string{"npm package a"}) {
		t.Fatalf("npm: %v %v", inventoryIDs(items), err)
	}
	items, err = dockerInventory("d", "FROM golang:1.27 AS build\nFROM build\nFROM scratch\nFROM ${BASE}\nFROM {{ .Image }}\n")
	if err != nil || !slices.Equal(inventoryIDs(items), []string{"image golang"}) {
		t.Fatalf("docker: %v %v", inventoryIDs(items), err)
	}
	items, err = goModInventory("g", "require example.com/a v1.0.0 // indirect\ntoolchain go1.27.0\n")
	if err != nil || len(items) != 0 {
		t.Fatalf("go.mod: %v %v", inventoryIDs(items), err)
	}
	items, err = pypiInventory("r", "-r base.in\n\n# only options\n")
	if err != nil || len(items) != 0 {
		t.Fatalf("requirements.in: %v %v", inventoryIDs(items), err)
	}
}

// ReadCreditInventory lists the checkout through git: it reads the manifests where they are,
// skips installed packages, test fixtures, hidden directories other than .config, .devcontainer
// and .github, and files git ignores, and records the file each item comes from. A directory
// that is no git work tree has no listing and fails.
func TestReadCreditInventory(t *testing.T) {
	root := t.TempDir()
	if _, err := ReadCreditInventory(context.Background(), root); err == nil {
		t.Fatal("a directory outside any git work tree was listed")
	}
	testsupport.InitGitRepoWithOrigin(t, root, "")
	writeRepoFile(t, root, ".gitignore", "/build/\n")
	writeRepoFile(t, root, "build/package.json", `{"dependencies":{"ignored-output":"1"}}`)
	writeRepoFile(t, root, "go.mod", "module m\n\nrequire example.com/lib v1.0.0\n")
	writeRepoFile(t, root, "tools/web/package.json", `{"dependencies":{"left-pad":"1"}}`)
	writeRepoFile(t, root, "tools/web/node_modules/left-pad/package.json", `{"dependencies":{"inner":"1"}}`)
	writeRepoFile(t, root, "internal/x/testdata/package.json", `{"dependencies":{"fixture":"1"}}`)
	writeRepoFile(t, root, ".claude/worktrees/w/package.json", `{"dependencies":{"copy":"1"}}`)
	writeRepoFile(t, root, ".config/lint/requirements.in", "yamllint==1\n")
	writeRepoFile(t, root, ".github/workflows/ci.yml", "jobs:\n  a:\n    steps:\n      - uses: actions/checkout@v7\n")
	writeRepoFile(t, root, ".github/notes.yml", "uses: not/an-action@v1\n")
	writeRepoFile(t, root, "templates/go/Dockerfile.tmpl", "FROM gcr.io/distroless/static:nonroot\n")
	items, err := ReadCreditInventory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	got := inventoryIDs(items)
	slices.Sort(got)
	want := []string{"Go module example.com/lib", "PyPI package yamllint", "action actions/checkout", "image gcr.io/distroless/static", "npm package left-pad"}
	if !slices.Equal(got, want) {
		t.Fatalf("inventory = %v, want %v", got, want)
	}
	for _, item := range items {
		if item.ID == "left-pad" && item.Path != "tools/web/package.json" {
			t.Fatalf("left-pad recorded from %s", item.Path)
		}
	}
	writeRepoFile(t, root, "broken/package.json", "{")
	if _, err := ReadCreditInventory(context.Background(), root); err == nil || !strings.Contains(err.Error(), filepath.ToSlash("broken/package.json")) {
		t.Fatalf("a broken manifest: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadCreditInventory(ctx, root); err == nil {
		t.Fatal("a cancelled context walked the repository")
	}
}
