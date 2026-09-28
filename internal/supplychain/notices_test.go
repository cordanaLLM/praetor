package supplychain

// THIRD-PARTY-NOTICES.md names every third-party component the release archives and the
// container image carry, and the archives and the image carry the file. RenderNotices
// generates its component tables from the files that decide what ships -- go.mod, the
// embedded npm lock the binaries write out, the root Dockerfile -- and these tests fail when
// the committed file is not what the render writes, when a Go module's upstream terms are not
// carried verbatim, and when an archive or the image drops the notices.

import (
	"context"
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

// shippedNoticePaths are the repository paths every archive and the image must carry.
var shippedNoticePaths = []string{"LICENSE", "LICENSES", NoticesFile}

// upstreamTermPrefixes name the files a Go module states its terms in, matched
// case-insensitively against the start of the file name.
var upstreamTermPrefixes = []string{"LICENSE", "LICENCE", "NOTICE", "COPYING"}

// readRepoFile reads one file relative to the repository root.
func readRepoFile(t *testing.T, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return data
}

// The committed notices are exactly what `praetorctl sbom notices` writes for this checkout,
// through the --check path that command takes (CheckNotices). The npm lock read from the
// checkout is the one the binaries embed.
func TestThirdPartyNoticesMatchWhatShips(t *testing.T) {
	sources, err := ReadNoticeSources(context.Background(), filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	embedded, err := markdownassets.Read("package-lock.json")
	if err != nil {
		t.Fatalf("read the embedded npm lock: %v", err)
	}
	if string(embedded) != string(sources.NPMLock) {
		t.Fatalf("%s differs from the lock the binaries embed", noticesLockFile)
	}
	// A check that derived nothing to compare is not a check that passed (HISS-21).
	modules, modErr := goModuleRows(sources.GoMod)
	packages, lockErr := lockRuntimePackages(sources.NPMLock)
	if err := errors.Join(modErr, lockErr); err != nil || len(modules) == 0 || len(packages) == 0 {
		t.Fatalf("derived %d Go modules and %d npm packages (%v): the sources stopped parsing", len(modules), len(packages), err)
	}
	if err := CheckNotices(string(readRepoFile(t, NoticesFile)), sources); err != nil {
		t.Error(err)
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
			missing = append(missing, fmt.Sprintf("%s: %s is not reproduced verbatim in %s", module, name, NoticesFile))
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
	notices := string(readRepoFile(t, NoticesFile))
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
