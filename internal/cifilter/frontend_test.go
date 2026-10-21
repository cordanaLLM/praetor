// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package cifilter_test

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/cifilter"
)

func TestFilterFrontendKinds(t *testing.T) {
	for _, name := range []string{"app.ts", "App.svelte", "app.css", "app.html", "package.json", "vite.config.js",
		"tsconfig.json", "pnpm-lock.yaml", ".gitignore", ".npmrc", ".prettierignore", ".prettierrc"} {
		t.Run(name, func(t *testing.T) {
			cs := cifilter.ClassifyChanges([]string{"apps/console/" + name})
			if cs.UnclassifiedChanged {
				t.Fatalf("known frontend kind failed closed: %+v", cs)
			}
		})
	}
}

func TestFilterConsoleOnlyAndGoInputs(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		wantTests  bool
	}{
		{"console", "apps/console/src/app.css", false},
		{"unknown", "apps/console/source.mystery", true},
		{"package markdown", "pkg/readme.md", true},
		{"testdata", "pkg/testdata/input.md", true},
		{"embedded markdown", "pkg/assets/guide.md", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := gitSandbox(t)
			writeRepoFile(t, dir, "go.mod", "module example.test/filter\n\ngo 1.27\n")
			writeRepoFile(t, dir, "pkg/p.go", "package p\nimport _ \"embed\"\n//go:embed assets/*\nvar Data string\n")
			writeRepoFile(t, dir, "pkg/assets/guide.md", "old\n")
			runRepoGit(t, dir, "init", "-q", "-b", "main")
			runRepoGit(t, dir, "add", ".")
			runRepoGit(t, dir, "commit", "-q", "-m", "base")
			for _, name := range []string{tc.path, "apps/console/src/app.ts", "apps/console/src/App.svelte",
				"docs/guide.md", "docs/credits.yaml", ".github/workflows/console.yml"} {
				writeRepoFile(t, dir, name, "changed\n")
			}
			runRepoGit(t, dir, "add", ".")
			runRepoGit(t, dir, "commit", "-q", "-m", "head")
			d, err := cifilter.AnalyzeChanges(t.Context(), cifilter.FilterOptions{RepoDir: dir, BaseRef: "HEAD~1"})
			if err != nil || d.RunTests != tc.wantTests || d.SkipHeavyGates == tc.wantTests {
				t.Fatalf("tests=%v: decision=%+v err=%v", tc.wantTests, d, err)
			}
		})
	}
}

func TestFilterConsoleRangeKindsAndEmbeddedOnly(t *testing.T) {
	for _, tc := range []struct {
		name      string
		files     []string
		wantTests bool
	}{
		{"console range kinds", []string{"apps/console/.gitignore", "apps/console/.npmrc", "apps/console/.prettierignore",
			"apps/console/.prettierrc", "apps/console/eslint.config.js", "apps/console/package.json",
			"apps/console/pnpm-lock.yaml", "apps/console/src/app.html", "apps/console/src/app.css",
			"apps/console/src/App.svelte", "apps/console/src/lib/copy.test.ts", "apps/console/static/favicon.svg",
			"apps/console/tsconfig.json", "docs/guide.md", "docs/credits.yaml", ".github/workflows/console.yml"}, false},
		{"engine workflow with frontend namesake", []string{"apps/ci/app.ts", ".github/workflows/ci.yml"}, true},
		{"embedded Markdown alone", []string{"pkg/assets/guide.md"}, true},
		{"package TypeScript", []string{"pkg/app.ts"}, true},
		{"package component", []string{"pkg/App.svelte"}, true},
		{"package stylesheet", []string{"pkg/app.css"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := gitSandbox(t)
			writeRepoFile(t, dir, "pkg/p.go", "package p\n")
			runRepoGit(t, dir, "init", "-q", "-b", "main")
			runRepoGit(t, dir, "add", ".")
			runRepoGit(t, dir, "commit", "-q", "-m", "base")
			for _, file := range tc.files {
				writeRepoFile(t, dir, file, "changed\n")
			}
			runRepoGit(t, dir, "add", ".")
			runRepoGit(t, dir, "commit", "-q", "-m", "head")
			d, err := cifilter.AnalyzeChanges(t.Context(), cifilter.FilterOptions{RepoDir: dir, BaseRef: "HEAD~1"})
			if err != nil || d.RunTests != tc.wantTests || d.ChangeSet.UnclassifiedChanged {
				t.Fatalf("tests=%v: decision=%+v err=%v", tc.wantTests, d, err)
			}
		})
	}
}
