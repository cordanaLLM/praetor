package supplychain

// THIRD-PARTY-NOTICES.md names every third-party component the release archives and the
// container image carry, and the archives and the image carry the file. Nothing held the
// list equal to what ships, so a new require in go.mod, a Go version bump, a refreshed npm
// lock or a new base image would leave the notices silently wrong. These tests derive the
// shipped set from the files that decide it -- go.mod, the embedded npm lock the binaries
// write out, the root Dockerfile -- and fail on drift in either direction.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/cordanaLLM/praetor/internal/gomanifest"
	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
)

// noticesFile is the repository-relative notices file the release ships.
const noticesFile = "THIRD-PARTY-NOTICES.md"

// The "## " headings of noticesFile whose tables the drift check reads. Renaming one makes
// every row of its source read as missing, so a rename cannot silently disable the check.
const (
	noticesGoToolchain = "Go standard library and runtime"
	noticesGoModules   = "Go modules"
	noticesBaseImage   = "Container base image"
	noticesNPM         = "npm packages of the Markdown gate"
)

// maxNoticeLines bounds the notices and Dockerfile scans (HISS-02).
const maxNoticeLines = 8192

// maxLockPackages bounds the npm lock entries one check reads (HISS-02). The lock holds 109.
const maxLockPackages = 4096

// shippedNoticePaths are the repository paths every archive and the image must carry.
var shippedNoticePaths = []string{"LICENSE", "LICENSES", noticesFile}

// upstreamTermPrefixes name the files a Go module states its terms in, matched
// case-insensitively against the start of the file name.
var upstreamTermPrefixes = []string{"LICENSE", "LICENCE", "NOTICE", "COPYING"}

// noticeRow is one component: a table row of noticesFile, or one entry a source ships.
type noticeRow struct {
	name, version, license string
}

func (r noticeRow) String() string {
	if r.license == "" {
		return r.name + " " + r.version
	}
	return r.name + " " + r.version + " (" + r.license + ")"
}

// noticeSources are the files that decide what the release ships.
type noticeSources struct {
	goMod      []byte
	npmLock    []byte
	dockerfile string
}

// readRepoFile reads one file relative to the repository root.
func readRepoFile(t *testing.T, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return data
}

// splitNoticeLines splits text into lines without carriage returns, bounded (HISS-02).
func splitNoticeLines(text string) ([]string, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) > maxNoticeLines {
		return nil, fmt.Errorf("%d lines exceed the %d-line scan bound", len(lines), maxNoticeLines)
	}
	return lines, nil
}

// parseNoticeSections returns the data rows of every Markdown table in text, keyed by the
// "## " heading the table sits under. Each table's header and delimiter rows are dropped and
// backticks around a cell are stripped; "### " subheadings stay in their parent section.
func parseNoticeSections(text string) (map[string][]noticeRow, error) {
	lines, err := splitNoticeLines(text)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", noticesFile, err)
	}
	sections := make(map[string][]noticeRow)
	heading, tableLine := "", 0
	for _, line := range lines {
		if title, isHeading := strings.CutPrefix(line, "## "); isHeading {
			heading, tableLine = strings.TrimSpace(title), 0
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			tableLine = 0
			continue
		}
		tableLine++
		if row, ok := parseNoticeRow(line); ok && tableLine > 2 {
			sections[heading] = append(sections[heading], row)
		}
	}
	return sections, nil
}

// parseNoticeRow reads the name, version and license cells of one table row.
func parseNoticeRow(line string) (noticeRow, bool) {
	cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
	if len(cells) < 3 {
		return noticeRow{}, false
	}
	clean := func(cell string) string { return strings.Trim(strings.TrimSpace(cell), "`") }
	return noticeRow{name: clean(cells[0]), version: clean(cells[1]), license: clean(cells[2])}, true
}

// npmLockEntry is the part of one package-lock.json v2/v3 "packages" entry the check reads.
type npmLockEntry struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	License     string `json:"license"`
	Dev         bool   `json:"dev"`
	DevOptional bool   `json:"devOptional"`
	Link        bool   `json:"link"`
}

// lockRuntimePackages lists each distinct name@version an npm lock installs outside its dev
// dependencies, sorted. The root project, links and workspace folders are not packages
// someone else wrote and are skipped.
func lockRuntimePackages(lock []byte) ([]noticeRow, error) {
	var parsed struct {
		Packages map[string]npmLockEntry `json:"packages"`
	}
	if err := json.Unmarshal(lock, &parsed); err != nil {
		return nil, fmt.Errorf("parse npm lock: %w", err)
	}
	if len(parsed.Packages) > maxLockPackages {
		return nil, fmt.Errorf("npm lock holds %d packages, more than %d", len(parsed.Packages), maxLockPackages)
	}
	const marker = "node_modules/"
	seen := make(map[noticeRow]bool, len(parsed.Packages))
	rows := make([]noticeRow, 0, len(parsed.Packages))
	for path, entry := range parsed.Packages {
		at := strings.LastIndex(path, marker)
		if at < 0 || entry.Dev || entry.DevOptional || entry.Link {
			continue
		}
		name := entry.Name
		if name == "" {
			name = path[at+len(marker):]
		}
		row := noticeRow{name: name, version: entry.Version, license: entry.License}
		if !seen[row] {
			seen[row] = true
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].String() < rows[j].String() })
	return rows, nil
}

// goModuleRows lists every module go.mod requires, after replace directives, through the
// parser the SBOM generator uses (HISS-19).
func goModuleRows(goMod []byte) ([]noticeRow, error) {
	components, err := parseGoModComponents(string(goMod))
	if err != nil {
		return nil, fmt.Errorf("parse go.mod: %w", err)
	}
	rows := make([]noticeRow, 0, len(components))
	for _, component := range components {
		rows = append(rows, noticeRow{name: component.Name, version: component.Version})
	}
	return rows, nil
}

// finalBaseImage returns the repository and tag of the last FROM in a Dockerfile, the stage
// that becomes the image. ok is false for a Dockerfile without FROM or one ending on scratch.
func finalBaseImage(dockerfile string) (noticeRow, bool, error) {
	lines, err := splitNoticeLines(dockerfile)
	if err != nil {
		return noticeRow{}, false, fmt.Errorf("parse Dockerfile: %w", err)
	}
	ref := ""
	for _, line := range lines {
		if fields := strings.Fields(line); len(fields) > 1 && strings.EqualFold(fields[0], "FROM") {
			ref = firstNonFlag(fields[1:])
		}
	}
	ref, _, _ = strings.Cut(ref, "@")
	if ref == "" || ref == "scratch" {
		return noticeRow{}, false, nil
	}
	name, tag := ref, "latest"
	if colon := strings.LastIndex(ref, ":"); colon > strings.LastIndex(ref, "/") {
		name, tag = ref[:colon], ref[colon+1:]
	}
	return noticeRow{name: name, version: tag}, true, nil
}

// firstNonFlag returns the first field that is not a "--flag", or "" when every one is.
func firstNonFlag(fields []string) string {
	for _, field := range fields {
		if !strings.HasPrefix(field, "--") {
			return field
		}
	}
	return ""
}

// noticeDrift compares the rows one notices section lists with the rows its source ships,
// in both directions. compareLicense also holds each listed license to the shipped one.
func noticeDrift(section string, shipped, listed []noticeRow, compareLicense bool) []string {
	key := func(row noticeRow) noticeRow {
		if !compareLicense {
			row.license = ""
		}
		return row
	}
	have := make(map[noticeRow]bool, len(listed))
	for _, row := range listed {
		have[key(row)] = true
	}
	want := make(map[noticeRow]bool, len(shipped))
	var drift []string
	for _, row := range shipped {
		want[key(row)] = true
		if !have[key(row)] {
			drift = append(drift, fmt.Sprintf("%s: %s ships and is not listed in %s", section, key(row), noticesFile))
		}
	}
	for _, row := range listed {
		if !want[key(row)] {
			drift = append(drift, fmt.Sprintf("%s: %s is listed in %s and no longer ships", section, key(row), noticesFile))
		}
	}
	sort.Strings(drift)
	return drift
}

// shippedNoticeDrift derives every shipped component from sources and reports each one the
// notices sections do not list, and each listed one that no longer ships.
func shippedNoticeDrift(sections map[string][]noticeRow, sources noticeSources) ([]string, error) {
	modules, err := goModuleRows(sources.goMod)
	if err != nil {
		return nil, err
	}
	packages, err := lockRuntimePackages(sources.npmLock)
	if err != nil {
		return nil, err
	}
	var toolchain []noticeRow
	if version, declared := gomanifest.GoDirective(sources.goMod); declared {
		toolchain = append(toolchain, noticeRow{name: noticesGoToolchain, version: version, license: "BSD-3-Clause"})
	}
	var images []noticeRow
	image, based, err := finalBaseImage(sources.dockerfile)
	if err != nil {
		return nil, err
	}
	if based {
		images = append(images, image)
	}
	drift := noticeDrift(noticesGoToolchain, toolchain, sections[noticesGoToolchain], true)
	drift = append(drift, noticeDrift(noticesGoModules, modules, sections[noticesGoModules], false)...)
	drift = append(drift, noticeDrift(noticesBaseImage, images, sections[noticesBaseImage], false)...)
	return append(drift, noticeDrift(noticesNPM, packages, sections[noticesNPM], true)...), nil
}

func TestThirdPartyNoticesMatchWhatShips(t *testing.T) {
	sources := noticeSources{goMod: readRepoFile(t, "go.mod"), dockerfile: string(readRepoFile(t, "Dockerfile"))}
	lock, err := markdownassets.Read("package-lock.json")
	if err != nil {
		t.Fatalf("read the embedded npm lock: %v", err)
	}
	sources.npmLock = lock
	// A check that derived nothing to compare is not a check that passed (HISS-21).
	modules, modErr := goModuleRows(sources.goMod)
	packages, lockErr := lockRuntimePackages(lock)
	if err := errors.Join(modErr, lockErr); err != nil || len(modules) == 0 || len(packages) == 0 {
		t.Fatalf("derived %d Go modules and %d npm packages (%v): the sources stopped parsing", len(modules), len(packages), err)
	}
	sections, err := parseNoticeSections(string(readRepoFile(t, noticesFile)))
	if err != nil {
		t.Fatal(err)
	}
	drift, err := shippedNoticeDrift(sections, sources)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range drift {
		t.Error(finding)
	}
}

// upstreamTermTexts returns the normalized content of every license or notice file at the
// root of an extracted module, keyed by file name.
func upstreamTermTexts(moduleDir string) (map[string]string, error) {
	entries, err := os.ReadDir(moduleDir)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", moduleDir, err)
	}
	texts := make(map[string]string)
	for _, entry := range entries {
		upper := strings.ToUpper(entry.Name())
		if entry.IsDir() || !slices.ContainsFunc(upstreamTermPrefixes, func(p string) bool { return strings.HasPrefix(upper, p) }) {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(moduleDir, entry.Name()))
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), readErr)
		}
		texts[entry.Name()] = normalizeNoticeText(string(data))
	}
	return texts, nil
}

// normalizeNoticeText drops carriage returns and surrounding blank space, so a checkout
// with CRLF line endings compares equal to the upstream file.
func normalizeNoticeText(text string) string {
	return strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
}

// missingUpstreamTexts names each upstream text the notices do not carry verbatim, sorted.
// A module without any license file is reported too: its terms cannot be carried.
func missingUpstreamTexts(module string, notices string, texts map[string]string) []string {
	if len(texts) == 0 {
		return []string{module + " ships no LICENSE, NOTICE or COPYING file; record its terms by hand"}
	}
	normalized := normalizeNoticeText(notices)
	var missing []string
	for name, text := range texts {
		if !strings.Contains(normalized, text) {
			missing = append(missing, fmt.Sprintf("%s: %s is not reproduced verbatim in %s", module, name, noticesFile))
		}
	}
	sort.Strings(missing)
	return missing
}

// Apache-2.0 section 4(d) and the MIT and BSD notice conditions travel with the binary, so
// each Go module's upstream LICENSE and NOTICE must appear in the notices file as written.
// The module cache is the upstream copy the build itself compiled.
func TestThirdPartyNoticesCarryGoModuleTermsVerbatim(t *testing.T) {
	modules, err := goModuleRows(readRepoFile(t, "go.mod"))
	if err != nil || len(modules) == 0 {
		t.Fatalf("derived %d Go modules from go.mod: %v", len(modules), err)
	}
	cacheRoot, ok := gomanifest.ModuleCacheRoot()
	if !ok {
		t.Skip("no Go module cache root resolves from GOMODCACHE, GOPATH or the home directory; the upstream texts cannot be read")
	}
	notices := string(readRepoFile(t, noticesFile))
	for _, module := range modules {
		rel, err := gomanifest.ModuleCacheDir(module.name, module.version)
		if err != nil {
			t.Fatalf("locate %s in the module cache: %v", module, err)
		}
		dir := filepath.Join(cacheRoot, rel)
		if _, statErr := os.Stat(dir); statErr != nil {
			t.Skipf("%s is not extracted under %s (%v); run go mod download to compare its terms", module, cacheRoot, statErr)
		}
		texts, err := upstreamTermTexts(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, finding := range missingUpstreamTexts(module.String(), notices, texts) {
			t.Error(finding)
		}
	}
}

// goreleaserNoticeConfig is the part of .goreleaser.yaml that decides what ships beside the
// binaries.
type goreleaserNoticeConfig struct {
	Archives []struct {
		ID    string      `yaml:"id"`
		Files []yaml.Node `yaml:"files"`
	} `yaml:"archives"`
	Dockers []struct {
		ID         string   `yaml:"id"`
		ExtraFiles []string `yaml:"extra_files"`
	} `yaml:"dockers_v2"`
}

// archiveFileSources returns the src of every archives.files entry, which GoReleaser accepts
// either as a plain string or as a mapping with src.
func archiveFileSources(files []yaml.Node) ([]string, error) {
	sources := make([]string, 0, len(files))
	for i := range files {
		if files[i].Kind == yaml.ScalarNode {
			sources = append(sources, files[i].Value)
			continue
		}
		var entry struct {
			Src string `yaml:"src"`
		}
		if err := files[i].Decode(&entry); err != nil {
			return nil, fmt.Errorf("decode archives.files entry: %w", err)
		}
		sources = append(sources, entry.Src)
	}
	return sources, nil
}

// coversPath reports whether one of sources names rel itself or a path beneath it, so both
// "LICENSES" and "LICENSES/*" carry the directory.
func coversPath(sources []string, rel string) bool {
	return slices.ContainsFunc(sources, func(source string) bool {
		source = strings.TrimSuffix(source, "/")
		return source == rel || strings.HasPrefix(source, rel+"/")
	})
}

// dockerCopySources returns every build-context path a COPY or ADD instruction reads. A
// copy from another stage (--from) reads no context path and is skipped.
func dockerCopySources(dockerfile string) ([]string, error) {
	lines, err := splitNoticeLines(dockerfile)
	if err != nil {
		return nil, fmt.Errorf("parse Dockerfile: %w", err)
	}
	var sources []string
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 3 || !isContextCopy(fields) {
			continue
		}
		for _, field := range fields[1 : len(fields)-1] {
			if !strings.HasPrefix(field, "--") {
				sources = append(sources, strings.TrimSuffix(field, "/"))
			}
		}
	}
	return sources, nil
}

// isContextCopy reports whether an instruction's fields are a COPY or ADD that reads the
// build context rather than another stage.
func isContextCopy(fields []string) bool {
	copies := strings.EqualFold(fields[0], "COPY") || strings.EqualFold(fields[0], "ADD")
	return copies && !slices.ContainsFunc(fields, func(f string) bool { return strings.HasPrefix(f, "--from") })
}

// noticeShippingViolations reports each archive, image or Dockerfile that leaves out one of
// shippedNoticePaths.
func noticeShippingViolations(goreleaser []byte, dockerfile string) ([]string, error) {
	var config goreleaserNoticeConfig
	if err := yaml.Unmarshal(goreleaser, &config); err != nil {
		return nil, fmt.Errorf("parse goreleaser: %w", err)
	}
	copied, err := dockerCopySources(dockerfile)
	if err != nil {
		return nil, err
	}
	var violations []string
	if len(config.Archives) == 0 || len(config.Dockers) == 0 {
		violations = append(violations, fmt.Sprintf("goreleaser declares %d archives and %d dockers_v2 images; the notices need both listed", len(config.Archives), len(config.Dockers)))
	}
	for _, archive := range config.Archives {
		sources, err := archiveFileSources(archive.Files)
		if err != nil {
			return nil, err
		}
		violations = append(violations, missingPaths("archive "+archive.ID+" files", sources)...)
	}
	for _, image := range config.Dockers {
		violations = append(violations, missingPaths("dockers_v2 "+image.ID+" extra_files", image.ExtraFiles)...)
	}
	if len(config.Dockers) > 0 {
		violations = append(violations, missingPaths("Dockerfile COPY", copied)...)
	}
	return violations, nil
}

// missingPaths names each shipped notice path that sources do not carry.
func missingPaths(where string, sources []string) []string {
	var missing []string
	for _, rel := range shippedNoticePaths {
		if !coversPath(sources, rel) {
			missing = append(missing, fmt.Sprintf("%s: %s is not included", where, rel))
		}
	}
	return missing
}

func TestReleaseArtifactsCarryTheNotices(t *testing.T) {
	violations, err := noticeShippingViolations(readRepoFile(t, ".goreleaser.yaml"), string(readRepoFile(t, "Dockerfile")))
	if err != nil {
		t.Fatal(err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
	// GoReleaser only warns when an archive glob matches nothing, so a missing file would
	// ship an archive without it; the image build would fail instead. Both need the files.
	for _, rel := range shippedNoticePaths {
		if _, err := os.Stat(filepath.Join("..", "..", rel)); err != nil {
			t.Errorf("%s is shipped and absent from the repository: %v", rel, err)
		}
	}
}

// noticesFixture is a notices file listing exactly what noticesFixtureSources ships.
const noticesFixture = "# Third-party notices\n\n" +
	"## Go standard library and runtime\n\n| Component | Version | License | Copyright |\n| :-- | :-- | :-- | :-- |\n" +
	"| Go standard library and runtime | 1.27 | BSD-3-Clause | `Copyright 2009 The Go Authors.` |\n\n" +
	"## Go modules\n\n| Module | Version | License | Copyright |\n| :-- | :-- | :-- | :-- |\n" +
	"| `example.com/lib` | v1.2.3 | MIT | `Copyright (c) Someone` |\n\n### example.com/lib LICENSE\n\n```text\nterms\n```\n\n" +
	"## Container base image\n\n| Image | Tag | License | Copyright |\n| :-- | :-- | :-- | :-- |\n" +
	"| `gcr.io/distroless/static-debian13` | nonroot | Apache-2.0 | none |\n\n" +
	"## npm packages of the Markdown gate\n\n| Package | Version | License | Copyright |\n| :-- | :-- | :-- | :-- |\n" +
	"| `left-pad` | 1.3.0 | WTFPL | `Copyright (c) Pad` |\n| `@scope/util` | 2.0.0 | MIT | `Copyright (c) Util` |\n"

func noticesFixtureSources() noticeSources {
	return noticeSources{
		goMod: []byte("module example.com/app\n\ngo 1.27\n\nrequire example.com/lib v1.2.3\n"),
		npmLock: []byte(`{"lockfileVersion":3,"packages":{` +
			`"":{"name":"app","version":"1.0.0"},` +
			`"node_modules/left-pad":{"version":"1.3.0","license":"WTFPL"},` +
			`"node_modules/a/node_modules/@scope/util":{"version":"2.0.0","license":"MIT"},` +
			`"node_modules/@scope/util":{"version":"2.0.0","license":"MIT"},` +
			`"node_modules/devtool":{"version":"9.9.9","license":"MIT","dev":true}}}`),
		dockerfile: "FROM gcr.io/distroless/static-debian13:nonroot@sha256:" + strings.Repeat("a", 64) + "\n",
	}
}

func fixtureDrift(t *testing.T, notices string, sources noticeSources) []string {
	t.Helper()
	sections, err := parseNoticeSections(notices)
	if err != nil {
		t.Fatalf("parse the fixture notices: %v", err)
	}
	drift, err := shippedNoticeDrift(sections, sources)
	if err != nil {
		t.Fatalf("derive drift: %v", err)
	}
	return drift
}

// Positive: a notices file that lists exactly what ships reports nothing, and the nested
// duplicate of one npm package is one component, not two.
func TestShippedNoticeDriftAcceptsAnExactList(t *testing.T) {
	if drift := fixtureDrift(t, noticesFixture, noticesFixtureSources()); len(drift) != 0 {
		t.Errorf("an exact list reported drift: %v", drift)
	}
}

// Negative: each way a source can move past the notices is reported, naming the section and
// the component, in both directions.
func TestShippedNoticeDriftReportsEachMove(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(*noticeSources) string
		wants []string
	}{
		{"go.mod gains a require", func(s *noticeSources) string {
			s.goMod = append(s.goMod, []byte("require example.com/new v0.1.0\n")...)
			return noticesFixture
		}, []string{"Go modules: example.com/new v0.1.0 ships"}},
		{"a module version moves", func(s *noticeSources) string {
			s.goMod = []byte(strings.Replace(string(s.goMod), "v1.2.3", "v1.2.4", 1))
			return noticesFixture
		}, []string{"example.com/lib v1.2.4 ships", "example.com/lib v1.2.3 is listed"}},
		{"the Go version moves", func(s *noticeSources) string {
			s.goMod = []byte(strings.Replace(string(s.goMod), "go 1.27", "go 1.28", 1))
			return noticesFixture
		}, []string{"Go standard library and runtime 1.28 (BSD-3-Clause) ships"}},
		{"the lock gains a runtime package", func(s *noticeSources) string {
			s.npmLock = []byte(strings.Replace(string(s.npmLock), `"node_modules/devtool"`, `"node_modules/new":{"version":"1.0.0","license":"ISC"},"node_modules/devtool"`, 1))
			return noticesFixture
		}, []string{"new 1.0.0 (ISC) ships"}},
		{"a package changes license", func(s *noticeSources) string {
			s.npmLock = []byte(strings.Replace(string(s.npmLock), `"license":"WTFPL"`, `"license":"MIT"`, 1))
			return noticesFixture
		}, []string{"left-pad 1.3.0 (MIT) ships", "left-pad 1.3.0 (WTFPL) is listed"}},
		{"the base image changes", func(s *noticeSources) string {
			s.dockerfile = "FROM gcr.io/distroless/base-debian13:nonroot\n"
			return noticesFixture
		}, []string{"gcr.io/distroless/base-debian13 nonroot ships", "gcr.io/distroless/static-debian13 nonroot is listed"}},
		{"a section heading is renamed", func(*noticeSources) string {
			return strings.Replace(noticesFixture, "## Go modules", "## Go dependencies", 1)
		}, []string{"Go modules: example.com/lib v1.2.3 ships"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sources := noticesFixtureSources()
			notices := tc.edit(&sources)
			drift := strings.Join(fixtureDrift(t, notices, sources), "\n")
			for _, want := range tc.wants {
				if !strings.Contains(drift, want) {
					t.Errorf("drift does not report %q:\n%s", want, drift)
				}
			}
		})
	}
}

// Boundary: the edges of each source parser -- an empty lock, a lock that is not JSON, a
// Dockerfile ending on scratch or naming no tag, a replaced module, and table rows outside
// any table or before the delimiter.
func TestNoticeSourceParsersAtTheirEdges(t *testing.T) {
	if rows, err := lockRuntimePackages([]byte(`{"packages":{"":{"name":"app"},"packages/ws":{"version":"1.0.0"},"node_modules/ws":{"link":true}}}`)); err != nil || len(rows) != 0 {
		t.Errorf("root, workspace and link entries were read as packages: %v %v", rows, err)
	}
	if rows, err := lockRuntimePackages([]byte(`{"packages":{"node_modules/alias":{"name":"real","version":"2.0.0","license":"MIT"}}}`)); err != nil || len(rows) != 1 || rows[0].name != "real" {
		t.Errorf("an aliased package lost its real name: %v %v", rows, err)
	}
	if _, err := lockRuntimePackages([]byte("lockfileVersion: 3")); err == nil {
		t.Error("a lock that is not JSON parsed")
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
	sections, err := parseNoticeSections("| stray | row | outside |\n\n## S\n\n| Name | Version | License |\n| :-- | :-- | :-- |\n| `a` | 1 | MIT |\n| b |\n")
	if err != nil || len(sections["S"]) != 1 || sections["S"][0] != (noticeRow{name: "a", version: "1", license: "MIT"}) || len(sections[""]) != 0 {
		t.Errorf("table rows were misread: %v %v", sections, err)
	}
	if _, err := parseNoticeSections(strings.Repeat("\n", maxNoticeLines)); err == nil {
		t.Error("a notices file past the scan bound parsed")
	}
}

// All three directions for the verbatim check: a carried text passes whatever the line
// endings, a missing or altered one is reported, and a module with no license file is
// reported rather than passing for lack of anything to compare.
func TestMissingUpstreamTextsComparesVerbatim(t *testing.T) {
	texts := map[string]string{"LICENSE": normalizeNoticeText("\nMIT terms\nline two\n"), "NOTICE": "Copyright 2011 Someone."}
	carried := "# notices\n\n```text\r\nMIT terms\r\nline two\r\n```\n\n```text\nCopyright 2011 Someone.\n```\n"
	if missing := missingUpstreamTexts("m v1", carried, texts); len(missing) != 0 {
		t.Errorf("carried texts reported missing: %v", missing)
	}
	altered := strings.Replace(carried, "line two", "line 2", 1)
	if missing := missingUpstreamTexts("m v1", altered, texts); len(missing) != 1 || !strings.Contains(missing[0], "LICENSE") {
		t.Errorf("an altered LICENSE was not reported alone: %v", missing)
	}
	if missing := missingUpstreamTexts("m v1", carried, nil); len(missing) != 1 {
		t.Errorf("a module without license files passed: %v", missing)
	}
	dir := t.TempDir()
	for name, content := range map[string]string{"LICENSE.md": "terms", "notice": "n", "README.md": "r"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	found, err := upstreamTermTexts(dir)
	if err != nil || len(found) != 2 || found["LICENSE.md"] != "terms" || found["notice"] != "n" {
		t.Errorf("upstreamTermTexts read %v, %v; want LICENSE.md and notice only", found, err)
	}
	if _, err := upstreamTermTexts(filepath.Join(dir, "absent")); err == nil {
		t.Error("an absent module directory was read")
	}
}

// All three directions for the shipping check: a configuration carrying every file passes
// in either archives.files form, each omission is reported where it happens, and a
// configuration with no archive or image at all is not a pass.
func TestNoticeShippingViolations(t *testing.T) {
	const complete = "archives:\n  - id: default\n    files:\n      - LICENSE\n      - src: LICENSES/*\n      - THIRD-PARTY-NOTICES.md\n" +
		"dockers_v2:\n  - id: img\n    extra_files: [LICENSE, LICENSES/, THIRD-PARTY-NOTICES.md]\n"
	const dockerfile = "FROM scratch\nCOPY --from=build /x /x\nCOPY --chmod=0444 LICENSE THIRD-PARTY-NOTICES.md /usr/local/share/praetor/\nADD LICENSES/ /usr/local/share/praetor/LICENSES\n"
	cases := []struct {
		name, config, dockerfile string
		wants                    []string
	}{
		{"everything carried", complete, dockerfile, nil},
		{"archive drops the notices", strings.Replace(complete, "      - THIRD-PARTY-NOTICES.md\n", "", 1), dockerfile, []string{"archive default files: THIRD-PARTY-NOTICES.md"}},
		{"image drops LICENSES", strings.Replace(complete, "LICENSES/, ", "", 1), dockerfile, []string{"dockers_v2 img extra_files: LICENSES"}},
		{"Dockerfile copies no LICENSE", complete, strings.Replace(dockerfile, "LICENSE THIRD", "THIRD", 1), []string{"Dockerfile COPY: LICENSE"}},
		{"a stage copy is not a context copy", complete, "FROM scratch\nCOPY --from=build LICENSE LICENSES THIRD-PARTY-NOTICES.md /x/\n", []string{"Dockerfile COPY: LICENSE", "Dockerfile COPY: LICENSES", "Dockerfile COPY: THIRD-PARTY-NOTICES.md"}},
		{"nothing declared", "project_name: x\n", dockerfile, []string{"0 archives and 0 dockers_v2 images"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			violations, err := noticeShippingViolations([]byte(tc.config), tc.dockerfile)
			if err != nil {
				t.Fatal(err)
			}
			if len(violations) != len(tc.wants) {
				t.Fatalf("got %d violations, want %d: %v", len(violations), len(tc.wants), violations)
			}
			joined := strings.Join(violations, "\n")
			for _, want := range tc.wants {
				if !strings.Contains(joined, want) {
					t.Errorf("violations do not report %q:\n%s", want, joined)
				}
			}
		})
	}
	if _, err := noticeShippingViolations([]byte("archives: [\n"), dockerfile); err == nil {
		t.Error("a malformed goreleaser configuration was accepted")
	}
}
