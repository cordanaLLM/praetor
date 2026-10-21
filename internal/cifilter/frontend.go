// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package cifilter

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// frontendExtensions covers browser sources and styles, including the console's actual range.
var frontendExtensions = []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs", ".svelte", ".css", ".html"}
var frontendConfigNames = []string{".gitignore", ".npmrc", ".prettierignore", ".prettierrc"}

func isFrontendConfig(p string) bool {
	app := strings.HasPrefix(p, "apps/") || strings.HasPrefix(p, "console/")
	return app && (strings.HasSuffix(p, ".json") || slices.Contains(frontendConfigNames, path.Base(p)))
}

func frontendOrDocsOnly(files []string) bool {
	if len(files) == 0 || len(files) > maxFilesToInspect {
		return false
	}
	for i := 0; i < len(files) && i < maxFilesToInspect; i++ {
		p := files[i]
		if isAgent(p) || !frontendOrDocsPath(p) {
			return false
		}
	}
	return true
}

func frontendOrDocsPath(p string) bool {
	if isDocumentation(p) || slices.Contains(frontendExtensions, strings.ToLower(path.Ext(p))) || isFrontendConfig(p) {
		return true
	}
	if strings.HasPrefix(p, "apps/") || strings.HasPrefix(p, "console/") {
		return slices.Contains([]string{"package-lock.json", "pnpm-lock.yaml", "yarn.lock"}, path.Base(p))
	}
	// The console workflow is a dedicated frontend check. Other workflow edits retain
	// the full matrix; a coincidentally named app cannot turn an engine workflow light.
	return p == ".github/workflows/console.yml"
}

// classifyGoInputs checks only whether a light range can affect Go; it computes no package
// selection. Any asset below a tracked Go package is conservatively an input, covering embed
// targets (including deletions), test-only embeds and testdata without a second embed parser.
func classifyGoInputs(ctx context.Context, dir, head string, cs *ChangeSet) error {
	if !frontendOrDocsOnly(cs.Files) {
		return nil
	}
	if head == "" {
		head = "HEAD"
	}
	out, err := util.RunGit(ctx, dir, "ls-tree", "-r", "--name-only", "-z", head)
	if err != nil {
		return err
	}
	dirs, err := goInputDirectories(out)
	if err != nil {
		return err
	}
	for i := 0; i < len(cs.Files) && i < maxFilesToInspect; i++ {
		if beneathGoPackage(cs.Files[i], dirs) {
			cs.CodeChanged, cs.DocsOnly = true, false
			return nil
		}
	}
	cs.NonGoOnly = true
	return nil
}

func goInputDirectories(output string) (map[string]bool, error) {
	files := strings.Split(strings.TrimSuffix(output, "\x00"), "\x00")
	if len(files) > 100000 {
		return nil, errors.New("go input inventory exceeds 100000 tracked files")
	}
	dirs := make(map[string]bool)
	for i := 0; i < len(files) && i < 100000; i++ {
		p := files[i]
		if strings.HasSuffix(p, ".go") && !strings.Contains("/"+p, "/testdata/") {
			dirs[path.Dir(p)] = true
		}
	}
	return dirs, nil
}

func beneathGoPackage(p string, dirs map[string]bool) bool {
	dir := path.Dir(p)
	for i := 0; i < len(p) && i < 4096; i++ {
		if dirs[dir] {
			return true
		}
		if dir == "." {
			return false
		}
		dir = path.Dir(dir)
	}
	return true // An over-deep path fails closed.
}
