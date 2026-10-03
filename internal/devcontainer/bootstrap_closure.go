package devcontainer

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"sort"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// praetorModulePath is the module a bootstrap source must declare (declaresPraetorModule);
	// an import below it names a package of the captured tree.
	praetorModulePath = "github.com/cordanaLLM/praetor"
	// bootstrapBuildPackage is the package the recorded Dockerfile builds and the root of the
	// captured import closure.
	bootstrapBuildPackage = "cmd/standardsctl"
)

// bootstrapModuleFiles are the non-Go inputs go build reads beside the package sources.
var bootstrapModuleFiles = map[string]bool{"go.mod": true, "go.sum": true, "LICENSE": true}

// closureCapture is one bounded read of the in-module import closure of bootstrapBuildPackage
// over a candidate inventory. Every file is read once: its imports come from the parse that
// validates it, and the running byte total bounds only what is captured.
type closureCapture struct {
	ctx      context.Context
	root     string
	packages map[string][]string
	others   []string
	queued   map[string]bool
	queue    []string
	files    []bootstrapSourceFile
	total    int
}

// captureBootstrapClosure reads the part of the inventory go build ./cmd/standardsctl reads:
// every non-test Go file of every module package the build package reaches through its
// imports, go.mod, go.sum, LICENSE, and the assets of each go:embed family whose embedding
// source is captured. Build constraints are not evaluated: a reached package contributes all
// its non-test files, and their imports are followed whatever their tags, so the closure can
// only be larger than a single platform's build, never smaller. The walk is an iterative
// worklist (HISS-01) and never uses the go command, so capture needs no Go toolchain.
//
// A family asset is never read as package source, even a .go file: the API compatibility
// gate's program is an asset whose build constraint keeps it out of every package, and the
// closure captures it, as any asset, when its family's embedding source is reached.
func captureBootstrapClosure(ctx context.Context, root string, inventory []string) ([]bootstrapSourceFile, error) {
	capture := &closureCapture{ctx: ctx, root: root, packages: map[string][]string{}, queued: map[string]bool{}}
	for _, name := range inventory {
		asset, err := isBootstrapAsset(name)
		if err != nil {
			return nil, err
		}
		if util.IsGoNonTestSource(name) && !asset {
			dir := path.Dir(name)
			capture.packages[dir] = append(capture.packages[dir], name)
			continue
		}
		capture.others = append(capture.others, name)
	}
	if len(capture.packages[bootstrapBuildPackage]) > 0 {
		capture.enqueue(bootstrapBuildPackage)
	}
	if err := capture.walk(); err != nil {
		return nil, err
	}
	if err := capture.addCompanions(); err != nil {
		return nil, err
	}
	sort.Slice(capture.files, func(i, j int) bool { return capture.files[i].Name < capture.files[j].Name })
	return capture.files, nil
}

// walk drains the worklist. Each package is queued at most once and the inventory holds at
// most maxBootstrapFiles files, so maxBootstrapFiles packages bound the loop (HISS-02).
func (c *closureCapture) walk() error {
	for step := 0; step < maxBootstrapFiles && len(c.queue) > 0; step++ {
		dir := c.queue[0]
		c.queue = c.queue[1:]
		if err := c.capturePackage(dir); err != nil {
			return err
		}
	}
	if len(c.queue) > 0 {
		return fmt.Errorf("bootstrap build closure exceeds %d packages", maxBootstrapFiles)
	}
	return nil
}

func (c *closureCapture) enqueue(dir string) {
	c.queued[dir] = true
	c.queue = append(c.queue, dir)
}

// capturePackage reads every inventoried Go file of one package and queues each module
// package it imports. An import of a module package the inventory holds no Go source for
// is refused: go build ./cmd/standardsctl could not build from the capture.
func (c *closureCapture) capturePackage(dir string) error {
	for _, name := range c.packages[dir] {
		data, err := c.read(name)
		if err != nil {
			return err
		}
		parsed, err := parseBootstrapSourceFile(name, data)
		if err != nil {
			return err
		}
		c.files = append(c.files, bootstrapSourceFile{Name: name, Data: data})
		if err := c.follow(name, util.GoImportPaths(parsed)); err != nil {
			return err
		}
	}
	return nil
}

// follow queues the module packages among one file's imports.
func (c *closureCapture) follow(name string, imports []string) error {
	for _, importPath := range imports {
		dependency, inModule := util.ModuleImportDir(importPath, praetorModulePath)
		if !inModule || c.queued[dependency] {
			continue
		}
		if len(c.packages[dependency]) == 0 {
			return fmt.Errorf("bootstrap source %s imports %s, which has no captured Go source", name, importPath)
		}
		c.enqueue(dependency)
	}
	return nil
}

// addCompanions captures the module files and the assets of every family whose embedding
// source the closure reached; assets of an unreached family are not build inputs.
func (c *closureCapture) addCompanions() error {
	families, err := bootstrapAssetFamilies()
	if err != nil {
		return err
	}
	reached := map[string]bool{}
	for _, family := range families {
		if containsBootstrapSource(c.files, family.source) {
			for _, asset := range family.assets {
				reached[asset] = true
			}
		}
	}
	for _, name := range c.others {
		if !bootstrapModuleFiles[name] && !reached[name] {
			continue
		}
		data, err := c.read(name)
		if err != nil {
			return err
		}
		if err := validateBootstrapSourceFile(name, data); err != nil {
			return err
		}
		c.files = append(c.files, bootstrapSourceFile{Name: name, Data: data})
	}
	return nil
}

// read reads one inventoried file and holds the capture to maxBootstrapSourceBytes.
func (c *closureCapture) read(name string) ([]byte, error) {
	data, err := contextopt.ReadSnapshot(c.ctx, filepath.Join(c.root, filepath.FromSlash(name)))
	if err != nil {
		return nil, fmt.Errorf("bootstrap source %s: %w", name, err)
	}
	c.total += len(data)
	if c.total > maxBootstrapSourceBytes {
		return nil, fmt.Errorf("bootstrap source exceeds %d MiB", maxBootstrapSourceBytes>>20)
	}
	return data, nil
}
