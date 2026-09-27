package supplychain

// The files that decide what the release ships -- go.mod, the root Dockerfile and the npm
// lock the binaries embed and write out for the Markdown gate -- and the readers that derive
// the shipped components from them. RenderNotices lists exactly these components in
// THIRD-PARTY-NOTICES.md.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/semver"
	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
)

// maxNoticeLines bounds the notices and Dockerfile scans (HISS-02).
const maxNoticeLines = 8192

// maxLockPackages bounds the npm lock entries one read takes (HISS-02). The lock holds 110.
const maxLockPackages = 4096

// noticesLockFile is the repository-relative npm lock of the Markdown gate: the file the
// binaries embed (tools/markdownlint) and write into an adopting repository.
const noticesLockFile = markdownassets.Directory + "/package-lock.json"

// NoticeSources are the files that decide what the release archives and the image ship.
type NoticeSources struct {
	// GoMod is go.mod: the Go version the binaries link and every module they require.
	GoMod []byte
	// NPMLock is the Markdown gate's package-lock.json the binaries embed.
	NPMLock []byte
	// Dockerfile is the root Dockerfile, whose last stage names the image's base.
	Dockerfile string
}

// ReadNoticeSources reads go.mod, the root Dockerfile and the Markdown gate's npm lock from
// the top of the Praetor checkout at root.
func ReadNoticeSources(ctx context.Context, root string) (NoticeSources, error) {
	goMod, err := readNoticeSource(ctx, root, "go.mod")
	if err != nil {
		return NoticeSources{}, err
	}
	dockerfile, err := readNoticeSource(ctx, root, "Dockerfile")
	if err != nil {
		return NoticeSources{}, err
	}
	lock, err := readNoticeSource(ctx, root, noticesLockFile)
	if err != nil {
		return NoticeSources{}, err
	}
	return NoticeSources{GoMod: goMod, NPMLock: lock, Dockerfile: string(dockerfile)}, nil
}

// readNoticeSource reads one repository-relative file below root through the bounded,
// symlink-resistant snapshot reader (HISS-02).
func readNoticeSource(ctx context.Context, root, rel string) ([]byte, error) {
	data, err := contextopt.ReadSnapshot(ctx, filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	return data, nil
}

// noticeRow is one shipped component: its name, version and license. The license is empty
// where the source does not record one (go.mod, a Dockerfile).
type noticeRow struct {
	name, version, license string
}

func (r noticeRow) String() string {
	if r.license == "" {
		return r.name + " " + r.version
	}
	return r.name + " " + r.version + " (" + r.license + ")"
}

// sortNoticeRows orders rows by name, and one name's versions by SemVer precedence where
// both parse (9.0.0 before 10.0.0) and by text otherwise.
func sortNoticeRows(rows []noticeRow) {
	slices.SortStableFunc(rows, func(a, b noticeRow) int {
		if byName := strings.Compare(a.name, b.name); byName != 0 {
			return byName
		}
		left, leftParsed := semver.Parse(a.version)
		right, rightParsed := semver.Parse(b.version)
		if leftParsed && rightParsed {
			return semver.Compare(left, right)
		}
		return strings.Compare(a.version, b.version)
	})
}

// splitNoticeLines splits text into lines without carriage returns, bounded (HISS-02).
func splitNoticeLines(text string) ([]string, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) > maxNoticeLines {
		return nil, fmt.Errorf("%d lines exceed the %d-line scan bound", len(lines), maxNoticeLines)
	}
	return lines, nil
}

// npmLockEntry is the part of one package-lock.json v2/v3 "packages" entry the read takes.
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
	sortNoticeRows(rows)
	return rows, nil
}

// goModuleRows lists every module go.mod requires, after replace directives, sorted, through
// the parser the SBOM generator uses (HISS-19).
func goModuleRows(goMod []byte) ([]noticeRow, error) {
	components, err := parseGoModComponents(string(goMod))
	if err != nil {
		return nil, fmt.Errorf("parse go.mod: %w", err)
	}
	rows := make([]noticeRow, 0, len(components))
	for _, component := range components {
		rows = append(rows, noticeRow{name: component.Name, version: component.Version})
	}
	sortNoticeRows(rows)
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

// shippedNoticeRows derives every shipped component from sources, keyed by the notices
// section that lists it. The Go toolchain row is absent when go.mod declares no Go version,
// the base-image row when the image builds from scratch.
func shippedNoticeRows(sources NoticeSources) (map[string][]noticeRow, error) {
	modules, err := goModuleRows(sources.GoMod)
	if err != nil {
		return nil, err
	}
	packages, err := lockRuntimePackages(sources.NPMLock)
	if err != nil {
		return nil, err
	}
	shipped := map[string][]noticeRow{noticesGoModules: modules, noticesNPM: packages}
	if version, declared := gomanifest.GoDirective(sources.GoMod); declared {
		shipped[noticesGoToolchain] = []noticeRow{{name: noticesGoToolchain, version: version, license: goToolchainLicense}}
	}
	image, based, err := finalBaseImage(sources.Dockerfile)
	if err != nil {
		return nil, err
	}
	if based {
		shipped[noticesBaseImage] = []noticeRow{image}
	}
	return shipped, nil
}
