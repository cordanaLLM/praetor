// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

// The dependency inventory of the credits gate: every third-party item a manifest of the
// repository names, read from the files ReadCreditInventory (notices_sources.go) finds. Each
// reader takes one file's text and returns its items; none of them touches the file system.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
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

// maxInventoryEntries bounds the directory entries one inventory walk visits (HISS-02). The
// checkout holds about 4,000 files outside the skipped directories.
const maxInventoryEntries = 1 << 17

// inventoryDotDirectories are the hidden directories the walk enters; every other directory
// whose name starts with a dot holds agent state, editor settings or version-control data, not
// a manifest.
var inventoryDotDirectories = []string{".config", ".devcontainer", ".github"}

// inventorySkippedDirectories are directories the walk never enters: installed packages, whose
// manifests belong to the packages' authors, and test fixtures, which the tests read as inputs
// and nothing installs.
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

// inventoryWalker collects the repository files the inventory reads.
type inventoryWalker struct {
	ctx     context.Context
	root    string
	visited int
	files   []string
}

// visit is the filepath.WalkDir callback: it skips the directories the inventory does not read
// and records every file one of the readers takes.
func (w *inventoryWalker) visit(abs string, entry os.DirEntry, walkErr error) error {
	if walkErr != nil {
		return walkErr
	}
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if w.visited++; w.visited > maxInventoryEntries {
		return fmt.Errorf("the credit inventory walk exceeds %d entries", maxInventoryEntries)
	}
	rel, err := filepath.Rel(w.root, abs)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	name := entry.Name()
	if entry.IsDir() {
		hidden := strings.HasPrefix(name, ".") && rel != "." && !slices.Contains(inventoryDotDirectories, name)
		if hidden || slices.Contains(inventorySkippedDirectories, name) {
			return filepath.SkipDir
		}
		return nil
	}
	if entry.Type().IsRegular() && len(inventoryReaders(rel)) > 0 {
		w.files = append(w.files, rel)
	}
	return nil
}

// inventoryReader returns the items one file's text names.
type inventoryReader func(rel, text string) ([]InventoryItem, error)

// inventoryReaders returns the readers that take the file at rel, by its name and place.
func inventoryReaders(rel string) []inventoryReader {
	var readers []inventoryReader
	switch base := path.Base(rel); {
	case base == "go.mod":
		readers = append(readers, goModInventory)
	case base == "package.json":
		readers = append(readers, npmInventory)
	case base == "requirements.in":
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

// npmDependencyGroups are the members of a package.json that list direct dependencies.
var npmDependencyGroups = []string{"dependencies", "devDependencies", "peerDependencies", "optionalDependencies"}

// localSpecifiers start a dependency version that names a package of this repository, not a
// third-party one.
var localSpecifiers = []string{"file:", "link:", "workspace:", "portal:"}

// npmInventory lists every direct dependency of a package.json, of any dependency kind, sorted;
// a dependency on a local folder or workspace is not third-party and is skipped.
func npmInventory(rel, text string) ([]InventoryItem, error) {
	members, err := decodeManifestMembers(rel, text, strictjson.StrictJSON)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, group := range npmDependencyGroups {
		raw, present := members[group]
		if !present {
			continue
		}
		var dependencies map[string]string
		if err := json.Unmarshal(raw, &dependencies); err != nil {
			return nil, fmt.Errorf("parse %s %s: %w", rel, group, err)
		}
		for name, version := range dependencies {
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

// pypiInventory lists every requirement of a requirements.in by its lower-cased name; blank
// lines, comments and option lines name none.
func pypiInventory(rel, text string) ([]InventoryItem, error) {
	lines, err := splitNoticeLines(text)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	var items []InventoryItem
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		name := line
		if end := strings.IndexAny(line, " =<>!~;[@#"); end >= 0 {
			name = line[:end]
		}
		items = append(items, InventoryItem{Kind: inventoryPyPI, ID: strings.ToLower(name), Path: rel})
	}
	return items, nil
}

// dockerInventory lists the image repository of every FROM of a Dockerfile or Dockerfile
// template. A FROM naming an earlier stage, scratch, or a reference a build argument or a
// template action supplies names no image the file pins and is skipped.
func dockerInventory(rel, text string) ([]InventoryItem, error) {
	lines, err := splitNoticeLines(text)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	var items []InventoryItem
	stages := map[string]bool{"scratch": true}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "FROM") {
			continue
		}
		ref := firstNonFlag(fields[1:])
		if at := slices.IndexFunc(fields, func(field string) bool { return strings.EqualFold(field, "AS") }); at > 0 && at+1 < len(fields) {
			stages[strings.ToLower(fields[at+1])] = true
		}
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
