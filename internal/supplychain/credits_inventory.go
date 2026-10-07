// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

// The dependency inventory of the credits gate: every third-party item a manifest of the
// repository names, read from the files ReadCreditInventory (notices_sources.go) finds. Each
// reader takes one file's text and returns its items; none of them touches the file system.

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/nodemanifest"
	"github.com/cordanaLLM/praetor/internal/pymanifest"
	"github.com/cordanaLLM/praetor/internal/strictjson"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
)

// The kinds of inventory item, as a finding names them.
const (
	inventoryGoModule = "Go module"
	inventoryGoTool   = "Go tool"
	inventoryNPM      = "npm package"
	inventoryPyPI     = "PyPI package"
	inventoryAction   = "action"
	inventoryImage    = "image"
	inventoryFeature  = "devcontainer feature"
	inventoryDownload = "download"
)

// inventoryDotDirectories are the hidden directories whose files the inventory reads; every
// other directory whose name starts with a dot holds agent state, editor settings or
// version-control data, not a manifest.
var inventoryDotDirectories = []string{".config", ".devcontainer", ".github"}

// inventorySkippedDirectories are directories whose files the inventory never reads: installed
// packages, whose manifests belong to the packages' authors, and test fixtures, which the tests
// read as inputs and nothing installs.
var inventorySkippedDirectories = []string{"node_modules", "testdata"}

// workflowActionFiles are the globs, relative to the repository, of the files whose uses: lines
// the inventory reads: the workflows, the composite actions, and the CI templates adoption
// writes.
var workflowActionFiles = []string{
	".github/workflows/*.yml", ".github/workflows/*.yaml",
	".github/actions/*/action.yml", ".github/actions/*/action.yaml",
	templates.Directory + "/" + templates.Pattern,
}

// InventoryItem is one third-party item a repository file names.
type InventoryItem struct {
	// Kind is one of the inventory kinds.
	Kind string
	// ID identifies the item the way an entry's packages name it: a Go module or tool path, an
	// npm or PyPI package name, an action's owner/repository, an image or feature repository,
	// or a download id.
	ID string
	// Path is the repository-relative slash path of the file that names it.
	Path string
}

// inventoryFile reports whether the inventory reads the repository file at rel, one of the files
// listed: one of the readers takes it, and no directory above it is skipped or hidden.
func inventoryFile(rel string, listed map[string]bool) bool {
	segments := strings.Split(rel, "/")
	skipped := slices.ContainsFunc(segments[:len(segments)-1], func(dir string) bool {
		hidden := strings.HasPrefix(dir, ".") && !slices.Contains(inventoryDotDirectories, dir)
		return hidden || slices.Contains(inventorySkippedDirectories, dir)
	})
	return !skipped && len(inventoryReaders(rel, listed)) > 0
}

// pipRequirementsFile reports whether rel is a pip requirements file the inventory reads: a
// requirements*.in, which pip-compile reads, or a requirements*.txt that no .in of the same name
// sits beside among the files listed. A hand-pinned file (.config/semgrep/requirements.txt) is
// read; a pip-compile lock is not, because its .in names every package it was asked for.
func pipRequirementsFile(rel string, listed map[string]bool) bool {
	base := path.Base(rel)
	if !strings.HasPrefix(base, "requirements") {
		return false
	}
	locked, isText := strings.CutSuffix(rel, ".txt")
	return strings.HasSuffix(base, ".in") || isText && !listed[locked+".in"]
}

// inventoryReader returns the items one file's text names.
type inventoryReader func(rel, text string) ([]InventoryItem, error)

// inventoryReaders returns the readers that take the file at rel, by its name and place among
// the files listed.
func inventoryReaders(rel string, listed map[string]bool) []inventoryReader {
	var readers []inventoryReader
	switch base := path.Base(rel); {
	case base == "go.mod":
		readers = append(readers, goModInventory)
	case base == "package.json":
		readers = append(readers, npmInventory)
	case pipRequirementsFile(rel, listed):
		readers = append(readers, pypiInventory)
	case base == "devcontainer.json":
		readers = append(readers, featureInventory)
	case strings.HasPrefix(base, "Dockerfile"):
		readers = append(readers, dockerInventory)
	}
	workflow := slices.ContainsFunc(workflowActionFiles, func(pattern string) bool {
		matched, err := path.Match(pattern, rel)
		return err == nil && matched
	})
	if workflow {
		readers = append(readers, actionInventory)
	}
	return readers
}

// goModInventory lists every direct requirement and every tool directive of a go.mod.
func goModInventory(rel, text string) ([]InventoryItem, error) {
	lines, err := splitNoticeLines(text)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	var items []InventoryItem
	inRequire, inTool := false, false
	for _, line := range lines {
		if requirement, required := gomanifest.RequirementLine(line, &inRequire); required {
			parsed, ok := gomanifest.ParseRequirement(requirement)
			if ok && !gomanifest.IsIndirect(requirement) {
				items = append(items, InventoryItem{Kind: inventoryGoModule, ID: parsed.Path, Path: rel})
			}
			continue
		}
		if tool, declared := gomanifest.ToolLine(line, &inTool); declared {
			code, _, _ := strings.Cut(tool, "//")
			if fields := strings.Fields(code); len(fields) == 1 {
				items = append(items, InventoryItem{Kind: inventoryGoTool, ID: fields[0], Path: rel})
			}
		}
	}
	return items, nil
}

// localSpecifiers start a dependency version that names a package of this repository, not a
// third-party one.
var localSpecifiers = []string{"file:", "link:", "workspace:", "portal:"}

// npmInventory lists every direct dependency of a package.json, of any dependency group, sorted;
// a dependency on a local folder or workspace is not third-party and is skipped. The file is read
// through the one package.json reader (nodemanifest.ParseManifest).
func npmInventory(rel, text string) ([]InventoryItem, error) {
	manifest, err := nodemanifest.ParseManifest([]byte(text))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	names := map[string]bool{}
	for _, group := range manifest.Groups() {
		for name, version := range group {
			names[name] = !slices.ContainsFunc(localSpecifiers, func(prefix string) bool { return strings.HasPrefix(version, prefix) }) || names[name]
		}
	}
	items := make([]InventoryItem, 0, len(names))
	for name, thirdParty := range names {
		if thirdParty {
			items = append(items, InventoryItem{Kind: inventoryNPM, ID: name, Path: rel})
		}
	}
	slices.SortFunc(items, func(a, b InventoryItem) int { return strings.Compare(a.ID, b.ID) })
	return items, nil
}

// manifestJSONDepth bounds the nesting of a JSON manifest the inventory reads (HISS-02).
const manifestJSONDepth = 64

// decodeManifestMembers decodes the top-level object of one JSON or JSONC manifest through the
// one strict reader (strictjson), which refuses a repeated member, trailing text and input past
// the source bound, and returns its members undecoded.
func decodeManifestMembers(rel, text string, dialect strictjson.Dialect) (map[string]json.RawMessage, error) {
	var members map[string]json.RawMessage
	opts := strictjson.Options{MaxBytes: contextopt.MaxSourceBytes, MaxDepth: manifestJSONDepth, Dialect: dialect}
	if err := strictjson.Decode([]byte(text), &members, opts); err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	return members, nil
}

// pypiInventory lists every requirement of a pip requirements file by its lower-cased name, read
// through the one requirement reader (pymanifest.ParseRequirement); blank lines, comments and
// option lines, a --hash continuation line among them, name none.
func pypiInventory(rel, text string) ([]InventoryItem, error) {
	lines, err := splitNoticeLines(text)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	var items []InventoryItem
	for _, line := range lines {
		if requirement, ok := pymanifest.ParseRequirement(line); ok {
			items = append(items, InventoryItem{Kind: inventoryPyPI, ID: requirement.Name, Path: rel})
		}
	}
	return items, nil
}

// dockerInventory lists the image repository of every FROM of a Dockerfile or Dockerfile
// template, read through the one FROM reader (util.ParseDockerFrom). A FROM naming an earlier
// stage, scratch, or a reference a build argument or a template action supplies names no image
// the file pins and is skipped.
func dockerInventory(rel, text string) ([]InventoryItem, error) {
	lines, err := splitNoticeLines(text)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	var items []InventoryItem
	stages := map[string]bool{"scratch": true}
	for _, line := range lines {
		from, ok := util.ParseDockerFrom(line)
		if !ok {
			continue
		}
		if from.Stage != "" {
			stages[from.Stage] = true
		}
		ref := from.Image
		repository, _, _ := util.SplitImageReference(ref)
		if ref == "" || stages[strings.ToLower(ref)] || strings.ContainsAny(ref, "${") {
			continue
		}
		items = append(items, InventoryItem{Kind: inventoryImage, ID: repository, Path: rel})
	}
	return items, nil
}

// featureInventory lists the image and every feature repository a devcontainer.json names,
// sorted. The file is JSON with comments, read through the one JSONC reader (strictjson).
func featureInventory(rel, text string) ([]InventoryItem, error) {
	members, err := decodeManifestMembers(rel, text, strictjson.JSONC)
	if err != nil {
		return nil, err
	}
	var image string
	var features map[string]json.RawMessage
	if raw, present := members["image"]; present {
		if err := json.Unmarshal(raw, &image); err != nil {
			return nil, fmt.Errorf("parse %s image: %w", rel, err)
		}
	}
	if raw, present := members["features"]; present {
		if err := json.Unmarshal(raw, &features); err != nil {
			return nil, fmt.Errorf("parse %s features: %w", rel, err)
		}
	}
	var items []InventoryItem
	if image != "" {
		repository, _, _ := util.SplitImageReference(image)
		items = append(items, InventoryItem{Kind: inventoryImage, ID: repository, Path: rel})
	}
	for ref := range features {
		repository, _, _ := util.SplitImageReference(ref)
		items = append(items, InventoryItem{Kind: inventoryFeature, ID: repository, Path: rel})
	}
	slices.SortFunc(items, func(a, b InventoryItem) int { return strings.Compare(a.ID, b.ID) })
	return items, nil
}

// actionInventory lists the owner/repository of every remote action a workflow, a composite
// action or a CI template uses.
func actionInventory(rel, text string) ([]InventoryItem, error) {
	_, uses, err := util.ScanActionUses(text, maxNoticeLines)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	var items []InventoryItem
	for _, use := range uses {
		if repository, remote := remoteActionRepository(use.Ref); remote {
			items = append(items, InventoryItem{Kind: inventoryAction, ID: repository, Path: rel})
		}
	}
	return items, nil
}

// remoteActionRepository returns the owner/repository a uses: value runs, lower-cased, or
// false for a local action or a Docker reference.
func remoteActionRepository(ref string) (string, bool) {
	action, _, _ := strings.Cut(ref, "@")
	parts := strings.Split(action, "/")
	if strings.HasPrefix(action, "./") || strings.HasPrefix(action, "docker://") || len(parts) < 2 {
		return "", false
	}
	return strings.ToLower(parts[0] + "/" + parts[1]), true
}
