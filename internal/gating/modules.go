// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	apiassets "github.com/cordanaLLM/praetor/tools/apicompat"
)

// ErrTooManyModules reports a repository whose Go toolchain stages would exceed their bound.
var ErrTooManyModules = apiassets.ErrTooManyModules

// ErrModuleDiscoveryMissing reports a stage configuration that was built without the one
// module-discovery seam every Go-presence decision uses.
var ErrModuleDiscoveryMissing = errors.New("go module discovery is not configured")

type goModule struct {
	file string
	dir  string
}

type moduleResult struct {
	module  goModule
	elapsed time.Duration
	message string
	err     error
}

type moduleRunner func(context.Context, *stageConfig, goModule) (string, error)

func (c *stageConfig) goModules(ctx context.Context) ([]goModule, error) {
	if c.modulesDiscovered {
		return c.modules, nil
	}
	if c.moduleDiscovery == nil {
		return nil, ErrModuleDiscoveryMissing
	}
	files, err := c.moduleDiscovery(ctx, c.repoDir)
	if err != nil {
		return nil, fmt.Errorf("discover tracked Go modules: %w", err)
	}
	if len(files) > apiassets.MaxTrackedModules {
		return nil, fmt.Errorf("%w: found %d, maximum %d", ErrTooManyModules, len(files), apiassets.MaxTrackedModules)
	}
	c.modules = make([]goModule, 0, len(files))
	for index := 0; index < len(files) && index < apiassets.MaxTrackedModules; index++ {
		file := files[index]
		c.modules = append(c.modules, goModule{file: file, dir: filepath.Join(c.repoDir, filepath.Dir(filepath.FromSlash(file)))})
	}
	c.modulesDiscovered = true
	return c.modules, nil
}

// maxModulePackageLines bounds the package listing ModulePackageCount reads (HISS-02).
const maxModulePackageLines = 100000

// NoPackagesNote is the stated skip of a tool that needs Go packages in a module that has none: a
// tool-pinning module (tools/go/go.mod with only tool directives) has no package, and govulncheck,
// go vet and go test fail on an empty pattern instead of passing it.
const NoPackagesNote = "no Go packages in this module"

// ModulePackageCount returns how many packages `go list -find -f {{.ImportPath}} ./...` finds in
// the module at dir, run through run (the stage's command runner or util.RunCommand). A listing
// that fails is an error; the warning go prints for a pattern that matched nothing is not a
// package.
func ModulePackageCount(ctx context.Context, run func(context.Context, string, string, ...string) (string, error), dir string) (int, error) {
	out, err := run(ctx, dir, "go", "list", "-find", "-f", "{{.ImportPath}}", "./...")
	if err != nil {
		return 0, fmt.Errorf("list Go packages in %s: %w: %s", dir, err, strings.TrimSpace(out))
	}
	return modulePackageCount(out), nil
}

func modulePackageCount(output string) int {
	lines := strings.Split(output, "\n")
	count := 0
	for index := 0; index < len(lines) && index < maxModulePackageLines; index++ {
		line := strings.TrimSpace(lines[index])
		if line != "" && !strings.HasPrefix(line, "go: ") {
			count++
		}
	}
	return count
}

func runGoModules(ctx context.Context, cfg *stageConfig, run moduleRunner) ([]moduleResult, error) {
	modules, err := cfg.goModules(ctx)
	if err != nil {
		return nil, err
	}
	if len(modules) == 0 {
		return nil, notApplicable("no go.mod: Go module stage skipped")
	}
	results := make([]moduleResult, 0, len(modules))
	for index := 0; index < len(modules) && index < apiassets.MaxTrackedModules; index++ {
		module := modules[index]
		start := time.Now()
		message, runErr := run(ctx, cfg, module)
		results = append(results, moduleResult{module: module, elapsed: time.Since(start), message: message, err: runErr})
	}
	return results, nil
}

func moduleResultsMessage(results []moduleResult) string {
	lines := make([]string, 0, len(results))
	for index := 0; index < len(results) && index < apiassets.MaxTrackedModules; index++ {
		result := results[index]
		outcome := result.message
		if outcome == "" {
			outcome = "passed"
		}
		if result.err != nil {
			outcome = result.err.Error()
		}
		lines = append(lines, fmt.Sprintf("%s: %s (%s)", result.module.file, outcome, result.elapsed.Round(time.Millisecond)))
	}
	return joinModuleLines(lines)
}

func moduleResultsError(stage string, results []moduleResult) error {
	for index := 0; index < len(results) && index < apiassets.MaxTrackedModules; index++ {
		result := results[index]
		if result.err != nil {
			return fmt.Errorf("%s in module %s: %w", stage, result.module.dir, result.err)
		}
	}
	return nil
}

func joinModuleLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	message := lines[0]
	for index := 1; index < len(lines) && index < apiassets.MaxTrackedModules; index++ {
		message += "\n" + lines[index]
	}
	return message
}
