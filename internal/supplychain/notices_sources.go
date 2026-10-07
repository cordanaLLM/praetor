package supplychain

// The files that decide what the release ships -- go.mod, the root Dockerfile, the npm lock
// the binaries embed and write out for the Markdown gate, the figure engine the binaries
// embed (the interfig pin and the committed player, with the lock it is built from), and the
// npm lock of the devcontainer CLI the binaries embed and install -- and the
// readers that derive the shipped components from them. RenderNotices lists exactly these
// components in THIRD-PARTY-NOTICES.md.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/generated"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/semver"
	"github.com/cordanaLLM/praetor/internal/util"
	figureassets "github.com/cordanaLLM/praetor/tools/figures"
	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
)

// maxNoticeLines bounds the notices and Dockerfile scans (HISS-02).
const maxNoticeLines = 8192

// maxLockPackages bounds the npm lock entries one read takes (HISS-02). The lock holds 110.
const maxLockPackages = 4096

// noticesLockFile is the repository-relative npm lock of the Markdown gate: the file the
// binaries embed (tools/markdownlint) and write into an adopting repository.
const noticesLockFile = markdownassets.Directory + "/package-lock.json"

// devcontainerLockFile is the repository-relative npm lock of the pinned devcontainer CLI: the
// file the binaries embed (internal/devcontainer/cli.go) and install the CLI from into the tool
// cache before a devcontainer build.
const devcontainerLockFile = "internal/devcontainer/cli/package-lock.json"

// The figure engine the binaries embed (tools/figures/assets.go): the npm lock the committed
// player is built from, the player's license file, which names every package the bundle holds
// (thirdPartyLicenses in tools/figures/bundle.mjs), and the pin of the vendored interfig source.
const (
	figureLockFile     = figureassets.Directory + "/package-lock.json"
	figureLicensesFile = figureassets.Directory + "/dist/THIRD-PARTY-LICENSES.txt"
	interfigVendorFile = figureassets.Directory + "/third_party/interfig/vendor.json"
)

// interfigComponent names the vendored figure engine in the notices. Its part of the player's
// license file carries the same name, with the upstream URL where a package carries a version.
const interfigComponent = "interfig"

// figureLicenseRule is the line that opens each part of the player's license file (RULE in
// tools/figures/bundle.mjs).
var figureLicenseRule = strings.Repeat("-", 78)

// NoticeSources are the files that decide what the release archives and the image ship.
type NoticeSources struct {
	// GoMod is go.mod: the Go version the binaries link and every module they require.
	GoMod []byte
	// NPMLock is the Markdown gate's package-lock.json the binaries embed.
	NPMLock []byte
	// Dockerfile is the root Dockerfile, whose last stage names the image's base.
	Dockerfile string
	// FigureLock is the figure player's package-lock.json, from which the committed player the
	// binaries embed is built.
	FigureLock []byte
	// FigureLicenses is the committed player's THIRD-PARTY-LICENSES.txt the binaries embed.
	FigureLicenses []byte
	// InterfigVendor is the vendor.json that pins the interfig source the binaries embed.
	InterfigVendor []byte
	// DevContainerLock is the devcontainer CLI's package-lock.json the binaries embed.
	DevContainerLock []byte
}

// ReadNoticeSources reads go.mod, the root Dockerfile, the Markdown gate's npm lock, the
// figure engine's lock, player license file and interfig pin, and the devcontainer CLI's npm
// lock from the top of the Praetor checkout at root.
func ReadNoticeSources(ctx context.Context, root string) (NoticeSources, error) {
	rels := [...]string{"go.mod", "Dockerfile", noticesLockFile, figureLockFile, figureLicensesFile, interfigVendorFile, devcontainerLockFile}
	var data [len(rels)][]byte
	for index, rel := range rels {
		read, err := readNoticeSource(ctx, root, rel)
		if err != nil {
			return NoticeSources{}, err
		}
		data[index] = read
	}
	return NoticeSources{
		GoMod: data[0], Dockerfile: string(data[1]), NPMLock: data[2],
		FigureLock: data[3], FigureLicenses: data[4], InterfigVendor: data[5], DevContainerLock: data[6],
	}, nil
}

// ReadCreditInventory lists every third-party item the manifests of the repository at root name,
// in path order: the direct requirements and tool directives of each go.mod (tools/go/go.mod
// holds the tool block), the direct dependencies of each package.json, the requirements of each
// pip requirements file that is no pip-compile lock (pipRequirementsFile), the remote actions of
// the workflows, composite actions and CI templates, the image of each Dockerfile FROM, and the
// image and features of each devcontainer.json. The files
// are the checkout's own, as git lists them (generated.DirTree: tracked files that exist and
// untracked ones git does not ignore), so root must be a git work tree; files below installed
// packages, test fixtures and hidden directories other than .config, .devcontainer and .github
// are not read (inventoryFile). The downloads docs/credits.yaml declares complete it in
// CheckUpstreamCredits.
func ReadCreditInventory(ctx context.Context, root string) ([]InventoryItem, error) {
	files, err := generated.DirTree(root).Files(ctx)
	if err != nil {
		return nil, fmt.Errorf("list %s for the credit inventory: %w", root, err)
	}
	listed := make(map[string]bool, len(files))
	for _, rel := range files {
		listed[rel] = true
	}
	var items []InventoryItem
	for _, rel := range files {
		if !inventoryFile(rel, listed) {
			continue
		}
		data, err := readNoticeSource(ctx, root, rel)
		if err != nil {
			return nil, err
		}
		for _, read := range inventoryReaders(rel, listed) {
			found, err := read(rel, string(data))
			if err != nil {
				return nil, err
			}
			items = append(items, found...)
		}
	}
	return items, nil
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

// lockPackages returns the "packages" entries of an npm lock, keyed by install path.
func lockPackages(lock []byte) (map[string]npmLockEntry, error) {
	var parsed struct {
		Packages map[string]npmLockEntry `json:"packages"`
	}
	if err := json.Unmarshal(lock, &parsed); err != nil {
		return nil, fmt.Errorf("parse npm lock: %w", err)
	}
	if len(parsed.Packages) > maxLockPackages {
		return nil, fmt.Errorf("npm lock holds %d packages, more than %d", len(parsed.Packages), maxLockPackages)
	}
	return parsed.Packages, nil
}

// lockRuntimePackages lists each distinct name@version an npm lock installs outside its dev
// dependencies, sorted. The root project, links and workspace folders are not packages
// someone else wrote and are skipped.
func lockRuntimePackages(lock []byte) ([]noticeRow, error) {
	packages, err := lockPackages(lock)
	if err != nil {
		return nil, err
	}
	const marker = "node_modules/"
	seen := make(map[noticeRow]bool, len(packages))
	rows := make([]noticeRow, 0, len(packages))
	for path, entry := range packages {
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

// bundledFigureParts returns the name and version of every part of the player's license file,
// in file order: each part opens with figureLicenseRule, then "<name> <version>" and
// "License: <id>". The interfig part carries its upstream URL as the version.
func bundledFigureParts(licenses []byte) ([]noticeRow, error) {
	lines, err := splitNoticeLines(string(licenses))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", figureLicensesFile, err)
	}
	var parts []noticeRow
	for index := 0; index < len(lines); index++ {
		if lines[index] != figureLicenseRule {
			continue
		}
		fields := []string{}
		license, labelled := "", false
		if index+2 < len(lines) {
			fields = strings.Fields(lines[index+1])
			license, labelled = strings.CutPrefix(lines[index+2], "License: ")
		}
		if len(fields) != 2 || !labelled {
			return nil, fmt.Errorf("parse %s line %d: a part opens with \"<name> <version>\" and \"License: <id>\"", figureLicensesFile, index+2)
		}
		parts = append(parts, noticeRow{name: fields[0], version: fields[1], license: license})
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("parse %s: it lists no bundled part", figureLicensesFile)
	}
	return parts, nil
}

// figurePlayerRows lists every npm package the committed player bundles, sorted: each part of
// its license file but interfig's, at the version and under the license the figure lock
// records. A part the lock does not install at the version the bundle names means the player
// was not rebuilt from the lock, and stops the render.
func figurePlayerRows(licenses, lock []byte) ([]noticeRow, error) {
	parts, err := bundledFigureParts(licenses)
	if err != nil {
		return nil, err
	}
	packages, err := lockPackages(lock)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", figureLockFile, err)
	}
	rows := make([]noticeRow, 0, len(parts))
	for _, part := range parts {
		if part.name == interfigComponent {
			continue
		}
		entry, installed := packages["node_modules/"+part.name]
		if !installed || entry.Version != part.version {
			return nil, fmt.Errorf("%s names %s %s, which %s does not install; rebuild the player with node tools/figures/bundle.mjs",
				figureLicensesFile, part.name, part.version, figureLockFile)
		}
		rows = append(rows, noticeRow{name: part.name, version: entry.Version, license: entry.License})
	}
	sortNoticeRows(rows)
	return rows, nil
}

// interfigRows returns the vendored interfig source at the upstream commit vendor.json pins.
// vendor.json records no license, so the row keeps the one the notices state.
func interfigRows(vendor []byte) ([]noticeRow, error) {
	var pin struct {
		Commit string `json:"commit"`
	}
	if err := json.Unmarshal(vendor, &pin); err != nil {
		return nil, fmt.Errorf("parse %s: %w", interfigVendorFile, err)
	}
	if strings.TrimSpace(pin.Commit) == "" {
		return nil, fmt.Errorf("parse %s: it pins no commit", interfigVendorFile)
	}
	return []noticeRow{{name: interfigComponent, version: pin.Commit}}, nil
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
		if from, ok := util.ParseDockerFrom(line); ok {
			ref = from.Image
		}
	}
	name, tag, _ := util.SplitImageReference(ref)
	if tag == "" && (name == "" || name == "scratch") {
		return noticeRow{}, false, nil
	}
	if tag == "" {
		tag = "latest"
	}
	return noticeRow{name: name, version: tag}, true, nil
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
	devcontainerCLI, err := lockRuntimePackages(sources.DevContainerLock)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", devcontainerLockFile, err)
	}
	shipped, err := figureNoticeRows(sources)
	if err != nil {
		return nil, err
	}
	shipped[noticesGoModules], shipped[noticesNPM], shipped[noticesDevContainerNPM] = modules, packages, devcontainerCLI
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

// figureNoticeRows derives the figure engine's components: the vendored interfig source and
// the npm packages the committed player bundles.
func figureNoticeRows(sources NoticeSources) (map[string][]noticeRow, error) {
	engine, err := interfigRows(sources.InterfigVendor)
	if err != nil {
		return nil, err
	}
	player, err := figurePlayerRows(sources.FigureLicenses, sources.FigureLock)
	if err != nil {
		return nil, err
	}
	return map[string][]noticeRow{noticesFigureEngine: engine, noticesFigureNPM: player}, nil
}
