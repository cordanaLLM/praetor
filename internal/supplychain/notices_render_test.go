package supplychain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// renderFixture is a notices file listing exactly what renderFixtureSources ships. The fenced
// block under the toolchain heading holds a line shaped like a table row, which is license
// text, not a second table.
const renderFixture = "# Third-party notices\n\nIntro prose stays as written.\n\n" +
	"## Go standard library and runtime\n\n| Component | Version | License | Copyright |\n| :-- | :-- | :-- | :-- |\n" +
	"| Go standard library and runtime | 1.27 | BSD-3-Clause | `Copyright 2009 The Go Authors.` |\n\n" +
	"```text\n| a license line | shaped | like | a row |\n```\n\n" +
	"## Go modules\n\n| Module | Version | License | Copyright |\n| :-- | :-- | :-- | :-- |\n" +
	"| `example.com/lib` | v1.2.3 | MIT AND Apache-2.0 | `Copyright (c) Someone` |\n\n" +
	"### example.com/lib LICENSE\n\n```text\nterms\n```\n\n" +
	"## Container base image\n\n| Image | Tag | License | Copyright |\n| :-- | :-- | :-- | :-- |\n" +
	"| `gcr.io/distroless/static-debian13` | nonroot | Apache-2.0 | none stated in the upstream `LICENSE` |\n\n" +
	"## npm packages of the Markdown gate\n\n| Package | Version | License | Copyright |\n| :-- | :-- | :-- | :-- |\n" +
	"| `@scope/util` | 2.0.0 | MIT | `Copyright (c) Util` |\n| `left-pad` | 1.3.0 | ISC | `Copyright (c) Pad` |\n\n" +
	"## Vendored figure engine\n\n| Component | Commit | License | Copyright |\n| :-- | :-- | :-- | :-- |\n" +
	"| `interfig` | abc123 | MIT | `Copyright (c) Engine` |\n\n" +
	"## npm packages of the figure player\n\n| Package | Version | License | Copyright |\n| :-- | :-- | :-- | :-- |\n" +
	"| `react` | 19.0.0 | MIT | `Copyright (c) React` |\n\n" +
	"## npm packages of the devcontainer CLI\n\n| Package | Version | License | Copyright |\n| :-- | :-- | :-- | :-- |\n" +
	"| `@devcontainers/cli` | 0.89.0 | MIT | `Copyright (c) Microsoft Corporation.` |\n\n" +
	"<!-- REUSE-IgnoreEnd -->\n"

// The fixture's rows, as the render writes them.
const (
	fixtureToolchainRow = "| Go standard library and runtime | 1.27 | BSD-3-Clause | `Copyright 2009 The Go Authors.` |"
	fixtureModuleRow    = "| `example.com/lib` | v1.2.3 | MIT AND Apache-2.0 | `Copyright (c) Someone` |"
	fixtureImageRow     = "| `gcr.io/distroless/static-debian13` | nonroot | Apache-2.0 | none stated in the upstream `LICENSE` |"
	fixtureUtilRow      = "| `@scope/util` | 2.0.0 | MIT | `Copyright (c) Util` |"
	fixturePadRow       = "| `left-pad` | 1.3.0 | ISC | `Copyright (c) Pad` |"
	fixtureEngineRow    = "| `interfig` | abc123 | MIT | `Copyright (c) Engine` |"
	fixtureReactRow     = "| `react` | 19.0.0 | MIT | `Copyright (c) React` |"
)

// fixtureLock is renderFixtureSources' npm lock: the root project, a nested duplicate of one
// package and a dev dependency, none of which is a row of its own.
const fixtureLock = `{"lockfileVersion":3,"packages":{` +
	`"":{"name":"app","version":"1.0.0"},` +
	`"node_modules/left-pad":{"version":"1.3.0","license":"ISC"},` +
	`"node_modules/a/node_modules/@scope/util":{"version":"2.0.0","license":"MIT"},` +
	`"node_modules/@scope/util":{"version":"2.0.0","license":"MIT"},` +
	`"node_modules/devtool":{"version":"9.9.9","license":"MIT","dev":true}}}`

// fixtureFigureLicenses is renderFixtureSources' player license file: the interfig part, which
// carries a URL for a version and is no npm package, and one bundled package.
var fixtureFigureLicenses = "Third-party software in loader.js and player.js\n\n" +
	figureLicenseRule + "\ninterfig https://example.com/tree/abc123/interfig\nLicense: MIT\n\nMIT License\n\nengine terms\n\n" +
	figureLicenseRule + "\nreact 19.0.0\nLicense: MIT\n\nMIT License\n\nreact terms\n"

// fixtureFigureLock is renderFixtureSources' figure lock: every entry is a dev dependency, as
// in the real lock, and only the package the player bundles is a row.
const fixtureFigureLock = `{"lockfileVersion":3,"packages":{"":{"name":"figures"},` +
	`"node_modules/react":{"version":"19.0.0","license":"MIT","dev":true},` +
	`"node_modules/esbuild":{"version":"0.1.0","license":"MIT","dev":true}}}`

// fixtureInterfigVendor is renderFixtureSources' interfig pin.
const fixtureInterfigVendor = `{"repo":"https://example.com","commit":"abc123"}`

// fixtureDevContainerLock is renderFixtureSources' devcontainer CLI lock: the root project and
// the one bundled CLI package.
const fixtureDevContainerLock = `{"lockfileVersion":3,"packages":{"":{"name":"cli-pin","version":"1.0.0"},` +
	`"node_modules/@devcontainers/cli":{"version":"0.89.0","license":"MIT"}}}`

func renderFixtureSources() NoticeSources {
	return NoticeSources{
		GoMod:            []byte("module example.com/app\n\ngo 1.27\n\nrequire example.com/lib v1.2.3\n"),
		NPMLock:          []byte(fixtureLock),
		Dockerfile:       "FROM gcr.io/distroless/static-debian13:nonroot@sha256:" + strings.Repeat("a", 64) + "\n",
		FigureLock:       []byte(fixtureFigureLock),
		FigureLicenses:   []byte(fixtureFigureLicenses),
		InterfigVendor:   []byte(fixtureInterfigVendor),
		DevContainerLock: []byte(fixtureDevContainerLock),
	}
}

// bumpFigurePlayer returns sources whose player bundles react at version, rebuilt from a lock
// that pins it there.
func bumpFigurePlayer(sources NoticeSources, version string) NoticeSources {
	sources.FigureLicenses = []byte(strings.Replace(string(sources.FigureLicenses), "react 19.0.0", "react "+version, 1))
	sources.FigureLock = []byte(strings.Replace(string(sources.FigureLock), `"version":"19.0.0"`, `"version":"`+version+`"`, 1))
	return sources
}

// replaceLock returns sources with one substitution applied to the npm lock.
func replaceLock(sources NoticeSources, old, updated string) NoticeSources {
	sources.NPMLock = []byte(strings.Replace(string(sources.NPMLock), old, updated, 1))
	return sources
}

// changedLines returns each line of after that differs from the line at the same place in
// before, or fails when the two differ in length.
func changedLines(t *testing.T, before, after string) []string {
	t.Helper()
	left, right := strings.Split(before, "\n"), strings.Split(after, "\n")
	if len(left) != len(right) {
		t.Fatalf("the render changed the line count from %d to %d:\n%s", len(left), len(right), after)
	}
	var changed []string
	for i := range left {
		if left[i] != right[i] {
			changed = append(changed, right[i])
		}
	}
	return changed
}

// Positive: a current file renders to itself byte for byte, rendering is idempotent on a file
// that drifted, and CheckNotices passes the current file, CRLF checkout included.
func TestRenderNoticesIsIdempotent(t *testing.T) {
	sources := renderFixtureSources()
	rendered, err := RenderNotices(renderFixture, sources)
	if err != nil || rendered != renderFixture {
		t.Fatalf("a current file did not render to itself (%v):\n%s", err, rendered)
	}
	drifted := replaceLock(sources, `"version":"1.3.0"`, `"version":"1.4.0"`)
	once, err := RenderNotices(renderFixture, drifted)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := RenderNotices(once, drifted)
	if err != nil || twice != once {
		t.Errorf("a second render changed the first (%v):\n%s", err, twice)
	}
	if err := CheckNotices(renderFixture, sources); err != nil {
		t.Errorf("a current file failed the check: %v", err)
	}
	crlf := strings.ReplaceAll(renderFixture, "\n", "\r\n")
	if err := CheckNotices(crlf, sources); err != nil {
		t.Errorf("a CRLF checkout of a current file failed the check: %v", err)
	}
	if rendered, err := RenderNotices(crlf, sources); err != nil || rendered != renderFixture {
		t.Errorf("a CRLF file did not render to the LF file (%v)", err)
	}
}

// A bumped lock version rewrites exactly that package's row, keeping its copyright line and
// name cell, and CheckNotices names the old row and the new one with the command to run.
func TestRenderNoticesRewritesOnlyTheBumpedRow(t *testing.T) {
	sources := replaceLock(renderFixtureSources(), `"version":"1.3.0"`, `"version":"1.3.1"`)
	rendered, err := RenderNotices(renderFixture, sources)
	if err != nil {
		t.Fatal(err)
	}
	const want = "| `left-pad` | 1.3.1 | ISC | `Copyright (c) Pad` |"
	if changed := changedLines(t, renderFixture, rendered); len(changed) != 1 || changed[0] != want {
		t.Errorf("the bump changed %q, want exactly %q", changed, want)
	}
	err = CheckNotices(renderFixture, sources)
	for _, part := range []string{"- " + fixturePadRow, "+ " + want, "`praetorctl sbom notices`"} {
		if err == nil || !strings.Contains(err.Error(), part) {
			t.Errorf("the check does not report %q: %v", part, err)
		}
	}
}

// Each way a source moves regenerates the rows it decides and nothing else.
func TestRenderNoticesFollowsEachSource(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(NoticeSources) NoticeSources
		wants []string
	}{
		{"a module version moves", func(s NoticeSources) NoticeSources {
			s.GoMod = []byte(strings.Replace(string(s.GoMod), "v1.2.3", "v1.3.0", 1))
			return s
		}, []string{"| `example.com/lib` | v1.3.0 | MIT AND Apache-2.0 | `Copyright (c) Someone` |"}},
		{"the Go version moves", func(s NoticeSources) NoticeSources {
			s.GoMod = []byte(strings.Replace(string(s.GoMod), "go 1.27", "go 1.28", 1))
			return s
		}, []string{"| Go standard library and runtime | 1.28 | BSD-3-Clause | `Copyright 2009 The Go Authors.` |"}},
		{"the base image tag moves", func(s NoticeSources) NoticeSources {
			s.Dockerfile = "FROM gcr.io/distroless/static-debian13:debug-nonroot\n"
			return s
		}, []string{"| `gcr.io/distroless/static-debian13` | debug-nonroot | Apache-2.0 | none stated in the upstream `LICENSE` |"}},
		{"a package changes to another reviewed license", func(s NoticeSources) NoticeSources {
			return replaceLock(s, `"license":"ISC"`, `"license":"MIT"`)
		}, []string{"| `left-pad` | 1.3.0 | MIT | `Copyright (c) Pad` |"}},
		{"the interfig pin moves", func(s NoticeSources) NoticeSources {
			s.InterfigVendor = []byte(`{"commit":"def456"}`)
			return s
		}, []string{"| `interfig` | def456 | MIT | `Copyright (c) Engine` |"}},
		{"the player is rebuilt with a newer bundled package", func(s NoticeSources) NoticeSources {
			return bumpFigurePlayer(s, "19.1.0")
		}, []string{"| `react` | 19.1.0 | MIT | `Copyright (c) React` |"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rendered, err := RenderNotices(renderFixture, tc.edit(renderFixtureSources()))
			if err != nil {
				t.Fatal(err)
			}
			if changed := changedLines(t, renderFixture, rendered); strings.Join(changed, "\n") != strings.Join(tc.wants, "\n") {
				t.Errorf("changed %q, want %q", changed, tc.wants)
			}
		})
	}
}

// Rows follow the lock in both directions: a package that stops shipping loses its row, and a
// second version of a listed package gains one after the first, with the shared copyright line.
func TestRenderNoticesAddsAndDropsRowsWithTheLock(t *testing.T) {
	dropped := replaceLock(renderFixtureSources(), `"node_modules/left-pad":{"version":"1.3.0","license":"ISC"},`, "")
	rendered, err := RenderNotices(renderFixture, dropped)
	if err != nil || strings.Contains(rendered, "left-pad") || !strings.Contains(rendered, fixtureUtilRow) {
		t.Errorf("a package that stopped shipping kept its row (%v):\n%s", err, rendered)
	}
	nested := replaceLock(renderFixtureSources(), `"node_modules/a/node_modules/@scope/util":{"version":"2.0.0"`,
		`"node_modules/a/node_modules/@scope/util":{"version":"10.0.0"`)
	nested = replaceLock(nested, `"node_modules/devtool"`, `"node_modules/b/node_modules/@scope/util":{"version":"9.1.0","license":"MIT"},"node_modules/devtool"`)
	rendered, err = RenderNotices(renderFixture, nested)
	if err != nil {
		t.Fatal(err)
	}
	want := fixtureUtilRow + "\n| `@scope/util` | 9.1.0 | MIT | `Copyright (c) Util` |\n| `@scope/util` | 10.0.0 | MIT | `Copyright (c) Util` |\n" + fixturePadRow
	if !strings.Contains(rendered, want) {
		t.Errorf("the versions of one package are not in SemVer order under their shared copyright line:\n%s", rendered)
	}
	emptied := renderFixtureSources()
	emptied.Dockerfile = "FROM golang:1.27 AS build\nFROM scratch\n"
	if rendered, err := RenderNotices(renderFixture, emptied); err != nil || strings.Contains(rendered, fixtureImageRow) {
		t.Errorf("an image built from scratch kept the base-image row (%v)", err)
	}
}

// Negative: what needs a person stops the render with the component and the fix named --
// a license outside the reviewed set, a lock entry without a license, a component the file
// holds no row for, rows for other versions that disagree, and a renamed section heading.
func TestRenderNoticesFailsLoudly(t *testing.T) {
	cases := []struct {
		name    string
		notices string
		sources NoticeSources
		wants   []string
	}{
		{"an unknown license", renderFixture, replaceLock(renderFixtureSources(), `"license":"ISC"`, `"license":"SSPL-1.0"`),
			[]string{"left-pad 1.3.0 (SSPL-1.0)", "names SSPL-1.0", "knownNoticeLicenses"}},
		{"an unknown term in an expression", renderFixture, replaceLock(renderFixtureSources(), `"license":"ISC"`, `"license":"(MIT OR GPL-3.0-only)"`),
			[]string{"names GPL-3.0-only"}},
		{"no license in the lock", renderFixture, replaceLock(renderFixtureSources(), `,"license":"ISC"`, ""),
			[]string{"left-pad 1.3.0 records no license"}},
		{"a package without a row", renderFixture, replaceLock(renderFixtureSources(), `"node_modules/devtool"`, `"node_modules/new":{"version":"1.0.0","license":"MIT"},"node_modules/devtool"`),
			[]string{"npm packages of the Markdown gate: new 1.0.0 (MIT) ships", "holds no row for new"}},
		{"a module without a row", renderFixture, func() NoticeSources {
			s := renderFixtureSources()
			s.GoMod = append(s.GoMod, "require example.com/other v0.1.0\n"...)
			return s
		}(), []string{"Go modules: example.com/other v0.1.0 ships", "holds no row"}},
		{"a new base image", renderFixture, func() NoticeSources {
			s := renderFixtureSources()
			s.Dockerfile = "FROM gcr.io/distroless/base-debian13:nonroot\n"
			return s
		}(), []string{"Container base image: gcr.io/distroless/base-debian13 nonroot ships"}},
		{"rows for other versions disagree", strings.Replace(renderFixture, fixturePadRow,
			fixturePadRow+"\n| `left-pad` | 1.2.0 | ISC | `Copyright (c) Someone Else` |", 1),
			replaceLock(renderFixtureSources(), `"version":"1.3.0"`, `"version":"1.4.0"`),
			[]string{"left-pad 1.4.0 (ISC) ships", "disagree"}},
		{"a renamed heading", strings.Replace(renderFixture, "## Go modules", "## Go dependencies", 1), renderFixtureSources(),
			[]string{`"## Go modules"`}},
		{"a player not rebuilt after a lock bump", renderFixture, func() NoticeSources {
			s := renderFixtureSources()
			s.FigureLock = []byte(strings.Replace(fixtureFigureLock, `"version":"19.0.0"`, `"version":"19.1.0"`, 1))
			return s
		}(), []string{"names react 19.0.0", "does not install", "bundle.mjs"}},
		{"a bundled package without a row", renderFixture, func() NoticeSources {
			s := renderFixtureSources()
			s.FigureLicenses = append(s.FigureLicenses, "\n"+figureLicenseRule+"\nscheduler 0.1.0\nLicense: MIT\n\nterms\n"...)
			s.FigureLock = []byte(strings.Replace(string(s.FigureLock), `"node_modules/esbuild"`, `"node_modules/scheduler":{"version":"0.1.0","license":"MIT","dev":true},"node_modules/esbuild"`, 1))
			return s
		}(), []string{"npm packages of the figure player: scheduler 0.1.0 (MIT) ships", "holds no row for scheduler"}},
		{"an interfig pin without a commit", renderFixture, func() NoticeSources {
			s := renderFixtureSources()
			s.InterfigVendor = []byte(`{"repo":"https://example.com"}`)
			return s
		}(), []string{"vendor.json: it pins no commit"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RenderNotices(tc.notices, tc.sources)
			if err == nil {
				t.Fatal("the render passed")
			}
			for _, want := range tc.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error does not name %q: %v", want, err)
				}
			}
			if checkErr := CheckNotices(tc.notices, tc.sources); checkErr == nil || checkErr.Error() != err.Error() {
				t.Errorf("the check did not fail with the render's error: %v", checkErr)
			}
		})
	}
}

// Boundary: malformed generated tables stop the render at the offending line, a table-shaped
// line inside a code fence or under another heading is left alone, and the scan is bounded.
func TestRenderNoticesAtTheTableEdges(t *testing.T) {
	cases := []struct {
		name, notices, want string
	}{
		{"a row with three cells", strings.Replace(renderFixture, fixturePadRow, "| `left-pad` | 1.3.0 | ISC |", 1), "has 3 cells"},
		{"an empty copyright cell", strings.Replace(renderFixture, fixturePadRow, "| `left-pad` | 1.3.0 | ISC | |", 1), "empty copyright cell"},
		{"no delimiter row", strings.Replace(renderFixture, "| Image | Tag | License | Copyright |\n| :-- | :-- | :-- | :-- |\n", "| Image | Tag | License | Copyright |\n", 1), "no delimiter row"},
		{"a header alone", strings.Replace(renderFixture, "| Image | Tag | License | Copyright |\n| :-- | :-- | :-- | :-- |\n"+fixtureImageRow+"\n", "| Image | Tag | License | Copyright |\n", 1), "no delimiter row"},
		{"a second table", strings.Replace(renderFixture, "### example.com/lib LICENSE", "| A | B | C | D |\n| - | - | - | - |", 1), "second table"},
		{"past the scan bound", renderFixture + strings.Repeat("\n", maxNoticeLines), "scan bound"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := RenderNotices(tc.notices, renderFixtureSources()); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error naming %q", err, tc.want)
			}
		})
	}
	other := renderFixture + "\n## Other\n\n| Name | Version | License | Copyright |\n| :-- | :-- | :-- | :-- |\n| `stale` | 0.0.1 | GPL-3.0-only | x |\n"
	if rendered, err := RenderNotices(other, renderFixtureSources()); err != nil || rendered != other {
		t.Errorf("a table under a heading the render does not own was changed (%v)", err)
	}
	if _, err := RenderNotices(renderFixture, NoticeSources{GoMod: renderFixtureSources().GoMod, NPMLock: []byte("lockfileVersion: 3")}); err == nil {
		t.Error("a lock that is not JSON rendered")
	}
	for name, edit := range map[string]func(*NoticeSources){
		"a figure lock that is not JSON": func(s *NoticeSources) { s.FigureLock = []byte("lockfileVersion: 3") },
		"a vendor.json that is not JSON": func(s *NoticeSources) { s.InterfigVendor = []byte("commit: abc123") },
		"a player license file without parts": func(s *NoticeSources) {
			s.FigureLicenses = []byte("Third-party software in loader.js and player.js\n")
		},
	} {
		sources := renderFixtureSources()
		edit(&sources)
		if _, err := RenderNotices(renderFixture, sources); err == nil {
			t.Errorf("%s rendered", name)
		}
	}
}

// All three directions for the license check: an expression of reviewed identifiers passes
// whatever its grammar, an unreviewed identifier or exception is named, and nothing is named
// for an empty expression, which checkNoticeLicense refuses separately.
func TestUnknownLicenseTerms(t *testing.T) {
	for expression, want := range map[string]string{
		"MIT":                              "",
		"(MIT OR Apache-2.0) AND ISC":      "",
		"MIT AND GPL-3.0-only":             "GPL-3.0-only",
		"Apache-2.0 WITH LLVM-exception":   "LLVM-exception",
		"":                                 "",
		"BSD-2-Clause OR (BSD-3-Clause)":   "",
		"(Python-2.0 OR SSPL-1.0 OR BUSL)": "SSPL-1.0,BUSL",
	} {
		if got := strings.Join(unknownLicenseTerms(expression), ","); got != want {
			t.Errorf("unknownLicenseTerms(%q) = %q, want %q", expression, got, want)
		}
	}
	if err := checkNoticeLicense("S", noticeRow{name: "x", version: "1", license: " "}); err == nil {
		t.Error("a blank license passed")
	}
}

// ReadNoticeSources reads the seven files from the checkout root and names the one missing.
func TestReadNoticeSources(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module m\n\ngo 1.27\n", "Dockerfile": "FROM scratch\n", noticesLockFile: fixtureLock,
		figureLockFile: fixtureFigureLock, figureLicensesFile: fixtureFigureLicenses, interfigVendorFile: fixtureInterfigVendor,
		devcontainerLockFile: fixtureDevContainerLock,
	}
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sources, err := ReadNoticeSources(context.Background(), root)
	if err != nil || string(sources.GoMod) != files["go.mod"] || sources.Dockerfile != files["Dockerfile"] || string(sources.NPMLock) != fixtureLock ||
		string(sources.FigureLock) != fixtureFigureLock || string(sources.FigureLicenses) != fixtureFigureLicenses || string(sources.InterfigVendor) != fixtureInterfigVendor ||
		string(sources.DevContainerLock) != fixtureDevContainerLock {
		t.Fatalf("ReadNoticeSources = %+v, %v", sources, err)
	}
	for _, rel := range []string{devcontainerLockFile, figureLicensesFile, noticesLockFile} {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadNoticeSources(context.Background(), root); err == nil || !strings.Contains(err.Error(), rel) {
			t.Errorf("a missing %s was not named: %v", rel, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadNoticeSources(ctx, root); err == nil {
		t.Error("a cancelled context read the sources")
	}
}

// Boundary: the edges of each source parser -- an empty lock, a lock past its bound, a
// Dockerfile ending on scratch or naming no tag, and a replaced module.
func TestNoticeSourceParsersAtTheirEdges(t *testing.T) {
	if rows, err := lockRuntimePackages([]byte(`{"packages":{"":{"name":"app"},"packages/ws":{"version":"1.0.0"},"node_modules/ws":{"link":true}}}`)); err != nil || len(rows) != 0 {
		t.Errorf("root, workspace and link entries were read as packages: %v %v", rows, err)
	}
	if rows, err := lockRuntimePackages([]byte(`{"packages":{"node_modules/alias":{"name":"real","version":"2.0.0","license":"MIT"}}}`)); err != nil || len(rows) != 1 || rows[0].name != "real" {
		t.Errorf("an aliased package lost its real name: %v %v", rows, err)
	}
	var entries []string
	for i := 0; i <= maxLockPackages; i++ {
		entries = append(entries, fmt.Sprintf(`"node_modules/p%d":{"version":"1.0.0"}`, i))
	}
	if _, err := lockRuntimePackages([]byte(`{"packages":{` + strings.Join(entries, ",") + `}}`)); err == nil {
		t.Error("a lock past the package bound parsed")
	}
	for dockerfile, want := range map[string]noticeRow{
		"FROM golang:1.27 AS build\nFROM --platform=$BUILDPLATFORM example.com:5000/base\n": {name: "example.com:5000/base", version: "latest"},
		"FROM golang:1.27 AS build\nFROM scratch\n":                                         {},
		"# no stage\n": {},
	} {
		got, based, err := finalBaseImage(dockerfile)
		if err != nil || got != want || based != (want.name != "") {
			t.Errorf("finalBaseImage(%q) = %v, %v, %v; want %v", dockerfile, got, based, err, want)
		}
	}
	rows, err := goModuleRows([]byte("module m\n\nrequire (\n\ta.example/x v1.0.0\n)\n\nreplace a.example/x => b.example/y v2.0.0\n"))
	if err != nil || len(rows) != 1 || rows[0] != (noticeRow{name: "b.example/y", version: "v2.0.0"}) {
		t.Errorf("a replaced module was not recorded as what builds: %v %v", rows, err)
	}
}

// Boundary: the player license parser reads a CRLF checkout like the LF file, takes the
// interfig part for a part without turning it into a package row, and refuses a part cut off
// after its rule or missing its license line.
func TestFigurePlayerSourcesAtTheirEdges(t *testing.T) {
	crlf := strings.ReplaceAll(fixtureFigureLicenses, "\n", "\r\n")
	parts, err := bundledFigureParts([]byte(crlf))
	if err != nil || len(parts) != 2 || parts[0] != (noticeRow{name: "interfig", version: "https://example.com/tree/abc123/interfig", license: "MIT"}) ||
		parts[1] != (noticeRow{name: "react", version: "19.0.0", license: "MIT"}) {
		t.Fatalf("CRLF license file parsed to %v, %v", parts, err)
	}
	rows, err := figurePlayerRows([]byte(crlf), []byte(fixtureFigureLock))
	if err != nil || len(rows) != 1 || rows[0] != (noticeRow{name: "react", version: "19.0.0", license: "MIT"}) {
		t.Fatalf("player rows = %v, %v; want react alone, dev flag notwithstanding", rows, err)
	}
	for name, text := range map[string]string{
		"a rule at the end":      fixtureFigureLicenses + figureLicenseRule + "\n",
		"no license line":        figureLicenseRule + "\nreact 19.0.0\nMIT\n",
		"a heading of one field": figureLicenseRule + "\nreact\nLicense: MIT\n",
	} {
		if _, err := bundledFigureParts([]byte(text)); err == nil || !strings.Contains(err.Error(), "a part opens with") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := bundledFigureParts([]byte(strings.Repeat("\n", maxNoticeLines))); err == nil || !strings.Contains(err.Error(), "scan bound") {
		t.Errorf("a license file past the scan bound parsed: %v", err)
	}
}
