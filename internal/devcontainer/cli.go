// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// The pinned devcontainer CLI. cli/package.json and cli/package-lock.json pin @devcontainers/cli,
// with its integrity hash, and cli/node.json pins the Node release it runs on with one SHA-256 per
// platform. Renovate moves both pins (renovate.json); a Node bump needs its checksums refreshed
// from the release's SHASUMS256.txt, which TestPinnedNodeMatchesTheRelease checks and, with
// PRAETOR_UPDATE_NODE_PINS=1, rewrites. The bootstrap source capture admits exactly these assets
// under exactly this directive (bootstrapAssetFamilies), so it must stay "//go:embed " +
// cliEmbedPatterns.
//
//go:embed cli/package.json cli/package-lock.json cli/node.json
var cliAssets embed.FS

const (
	// cliSourceFile is the repository path of this file, the one Go file that embeds the pins.
	cliSourceFile = "internal/devcontainer/cli.go"
	// cliEmbedPatterns are the pin files cliAssets embeds, relative to this package.
	cliEmbedPatterns = "cli/package.json cli/package-lock.json cli/node.json"
)

// cliBootstrapFamily declares the pins as an embedded asset family of the bootstrap source
// capture, so a recorded bootstrap carries them and its go build finds them.
func cliBootstrapFamily() bootstrapAssetFamily {
	patterns := strings.Fields(cliEmbedPatterns)
	assets := make([]string, 0, len(patterns))
	for i := 0; i < len(patterns); i++ {
		assets = append(assets, filepath.ToSlash(filepath.Join(filepath.Dir(cliSourceFile), patterns[i])))
	}
	return bootstrapAssetFamily{name: "devcontainer CLI pins", source: cliSourceFile, directive: "//go:embed " + cliEmbedPatterns, assets: assets}
}

const (
	// CLIProvisionTimeout bounds fetching the pinned Node and installing the pinned devcontainer
	// CLI into the tool cache (HISS-02). It runs inside ImageBuildTimeout, and a later build finds
	// both in the cache.
	CLIProvisionTimeout = 10 * time.Minute
	// NodeDistURL is the official Node release server the pinned Node is fetched from.
	NodeDistURL = "https://nodejs.org/dist"
	// cliPackageName is the npm package of the reference devcontainer CLI.
	cliPackageName = "@devcontainers/cli"
	// featureLockfile is the file the CLI records resolved feature digests in, beside the
	// configuration.
	featureLockfile = "devcontainer-lock.json"
	// maxNodeArchiveBytes bounds one Node release download; a release archive is about 58 MB.
	maxNodeArchiveBytes = 256 << 20
	// maxNodeArchiveEntries bounds the entries of one Node release archive; one holds about 6,000.
	maxNodeArchiveEntries = 65536
	// maxPinFileBytes bounds the read of one installed package manifest.
	maxPinFileBytes = 64 << 10
	// maxCacheEntries bounds the tool cache entries one stale-stage sweep looks at (HISS-02).
	maxCacheEntries = 1024
	// stagePrefix names the directories a provisioning run assembles its install in.
	stagePrefix = ".stage-"
	// staleStageAge is how old a stage must be before a later run removes it: older than any
	// provisioning run may take, so only a stage whose run was killed qualifies.
	staleStageAge = 2 * CLIProvisionTimeout
)

// npmCIArgs installs exactly the locked tree without lifecycle scripts, the flags the Markdown
// gate's locked install uses (NPM_CI_ARGS in tools/markdownlint/verify.mjs).
var npmCIArgs = []string{"ci", "--ignore-scripts", "--no-audit", "--no-fund"}

// nodePlatforms maps a host's GOOS/GOARCH to the Node release platform the pins cover. Windows
// is absent because PlanImage refuses a Windows host before a CLI is planned.
var nodePlatforms = map[string]string{
	"darwin/amd64": "darwin-x64",
	"darwin/arm64": "darwin-arm64",
	"linux/amd64":  "linux-x64",
	"linux/arm64":  "linux-arm64",
}

var (
	nodeVersionForm = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	sha256Form      = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// fetchClient is the production fetcher's client. Its timeout is the provisioning bound, a backstop
// behind the caller's context deadline (HISS-02); the environment's proxy settings apply.
var fetchClient = &http.Client{Timeout: CLIProvisionTimeout}

// ErrChecksumMismatch reports a download whose SHA-256 is not the pinned value.
var ErrChecksumMismatch = errors.New("checksum mismatch")

// Fetcher streams the body of a GET of url into w. HTTPFetch is the production fetcher.
type Fetcher func(ctx context.Context, url string, w io.Writer) error

// ToolCache is where the gate keeps the pinned Node and devcontainer CLI across runs and
// checkouts, and how it fetches them the first time.
type ToolCache struct {
	Dir   string
	Fetch Fetcher
}

// NodeRelease is the pinned Node release: its version and the SHA-256 of its .tar.gz archive per
// platform.
type NodeRelease struct {
	Version string            `json:"version"`
	SHA256  map[string]string `json:"sha256"`
}

// CLIPins are the pinned CLI version, the Node release it runs on, and the package files the
// locked install is made from.
type CLIPins struct {
	CLIVersion  string
	Node        NodeRelease
	packageJSON []byte
	lock        []byte
}

// PinnedCLI is the devcontainer CLI a plan with features builds through: the pinned
// @devcontainers/cli, run by the pinned Node release for Platform, both kept in Cache.
type PinnedCLI struct {
	Platform string
	Pins     CLIPins
	Cache    ToolCache
}

// cliCommand is how a provisioned CLI starts: the pinned node binary running the CLI's script.
type cliCommand struct{ node, script string }

// LoadCLIPins reads and checks the embedded pins: a Node version of the form x.y.z with a
// SHA-256 for every platform in nodePlatforms, and a lock that resolves @devcontainers/cli to the
// exact version package.json names, with its integrity hash.
func LoadCLIPins() (CLIPins, error) {
	var pins CLIPins
	var err error
	if pins.packageJSON, err = cliAssets.ReadFile("cli/package.json"); err != nil {
		return CLIPins{}, fmt.Errorf("read the pinned CLI manifest: %w", err)
	}
	if pins.lock, err = cliAssets.ReadFile("cli/package-lock.json"); err != nil {
		return CLIPins{}, fmt.Errorf("read the pinned CLI lock: %w", err)
	}
	if pins.CLIVersion, err = lockedCLIVersion(pins.packageJSON, pins.lock); err != nil {
		return CLIPins{}, err
	}
	node, err := cliAssets.ReadFile("cli/node.json")
	if err != nil {
		return CLIPins{}, fmt.Errorf("read the pinned Node release: %w", err)
	}
	if pins.Node, err = parseNodeRelease(node); err != nil {
		return CLIPins{}, err
	}
	return pins, nil
}

// lockedCLIVersion returns the CLI version the manifest pins, once the lock resolves exactly that
// version with an integrity hash.
func lockedCLIVersion(manifest, lock []byte) (string, error) {
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(manifest, &pkg); err != nil {
		return "", fmt.Errorf("parse the pinned CLI manifest: %w", err)
	}
	var locked struct {
		Packages map[string]struct {
			Version   string `json:"version"`
			Integrity string `json:"integrity"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(lock, &locked); err != nil {
		return "", fmt.Errorf("parse the pinned CLI lock: %w", err)
	}
	want := pkg.Dependencies[cliPackageName]
	entry := locked.Packages["node_modules/"+cliPackageName]
	if !nodeVersionForm.MatchString(want) || entry.Version != want || !strings.HasPrefix(entry.Integrity, "sha512-") {
		return "", fmt.Errorf("the pinned CLI lock must resolve %s to the exact version package.json names (%q) with a sha512 integrity, got %q %q",
			cliPackageName, want, entry.Version, entry.Integrity)
	}
	return want, nil
}

// parseNodeRelease decodes cli/node.json and checks every pinned platform has a SHA-256.
func parseNodeRelease(data []byte) (NodeRelease, error) {
	var release NodeRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return NodeRelease{}, fmt.Errorf("parse the pinned Node release: %w", err)
	}
	if !nodeVersionForm.MatchString(release.Version) {
		return NodeRelease{}, fmt.Errorf("the pinned Node version %q is not of the form x.y.z", release.Version)
	}
	platforms := NodePlatforms()
	for i := 0; i < len(platforms); i++ {
		if !sha256Form.MatchString(release.SHA256[platforms[i]]) {
			return NodeRelease{}, fmt.Errorf("the pinned Node release has no lowercase SHA-256 for %s", platforms[i])
		}
	}
	return release, nil
}

// NodePlatforms lists the Node release platforms the pins cover, sorted.
func NodePlatforms() []string {
	platforms := make([]string, 0, len(nodePlatforms))
	for _, platform := range nodePlatforms {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	return platforms
}

// planCLI plans the pinned CLI for host, fetching nothing. It refuses a platform the Node pins do
// not cover and a host without a tool cache.
func planCLI(host Host) (*PinnedCLI, error) {
	platform, ok := nodePlatforms[host.GOOS+"/"+host.GOARCH]
	if !ok {
		return nil, fmt.Errorf("the gate pins Node for %s only, not %s/%s", strings.Join(NodePlatforms(), ", "), host.GOOS, host.GOARCH)
	}
	if host.Tools.Dir == "" || host.Tools.Fetch == nil {
		return nil, errors.New("no tool cache to keep the pinned Node and devcontainer CLI in")
	}
	pins, err := LoadCLIPins()
	if err != nil {
		return nil, err
	}
	return &PinnedCLI{Platform: platform, Pins: pins, Cache: host.Tools}, nil
}

// String names the CLI and the Node it runs on: the builder a receipt records.
func (c *PinnedCLI) String() string {
	return fmt.Sprintf("%s build (%s %s, Node %s)", CLIName, cliPackageName, c.Pins.CLIVersion, c.Pins.Node.Version)
}

// provision makes the pinned Node and CLI ready in the tool cache under CLIProvisionTimeout and
// returns how to start the CLI. What a previous run published is reused; otherwise the Node
// archive is fetched from NodeDistURL and refused unless its SHA-256 is the pinned one, and the CLI
// is installed from the pinned lock with npm ci, run by that Node. Each is assembled in a stage
// directory and published by rename, so an interrupted run never leaves a half-made install where
// a later run would reuse it.
func (c *PinnedCLI) provision(ctx context.Context, run CommandRunner) (cliCommand, error) {
	pCtx, cancel := context.WithTimeout(ctx, CLIProvisionTimeout)
	defer cancel()
	if err := util.MkdirSecure(c.Cache.Dir, util.SecureDirPerm); err != nil {
		return cliCommand{}, fmt.Errorf("create the tool cache: %w", err)
	}
	if err := removeStaleStages(c.Cache.Dir, time.Now()); err != nil {
		return cliCommand{}, fmt.Errorf("clear abandoned stages in %s: %w", c.Cache.Dir, err)
	}
	nodeDir, err := c.provisionNode(pCtx)
	if err != nil {
		return cliCommand{}, fmt.Errorf("fetch the pinned Node %s for %s: %w", c.Pins.Node.Version, c.Platform, err)
	}
	script, err := c.provisionPackage(pCtx, run, nodeDir)
	if err != nil {
		return cliCommand{}, fmt.Errorf("install the pinned %s %s: %w", cliPackageName, c.Pins.CLIVersion, err)
	}
	return cliCommand{node: nodeBinary(nodeDir), script: script}, nil
}

// provisionNode returns the directory of the pinned Node release in the tool cache, fetching,
// verifying and unpacking it first when no run has published it yet.
func (c *PinnedCLI) provisionNode(ctx context.Context) (dir string, err error) {
	name := c.nodeRelease()
	final := c.nodeDir()
	if util.FileExists(nodeBinary(final)) {
		return final, nil
	}
	stage, err := os.MkdirTemp(c.Cache.Dir, stagePrefix+"node-")
	if err != nil {
		return "", fmt.Errorf("create a stage: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(stage)) }()
	url := fmt.Sprintf("%s/v%s/%s.tar.gz", NodeDistURL, c.Pins.Node.Version, name)
	archive := filepath.Join(stage, name+".tar.gz")
	if err := fetchVerified(ctx, c.Cache.Fetch, url, archive, c.Pins.Node.SHA256[c.Platform]); err != nil {
		return "", err
	}
	tree := filepath.Join(stage, "tree")
	if err := extractFile(ctx, archive, tree); err != nil {
		return "", fmt.Errorf("unpack %s: %w", url, err)
	}
	if !util.FileExists(nodeBinary(filepath.Join(tree, name))) {
		return "", fmt.Errorf("%s holds no %s/bin/node", url, name)
	}
	if err := publish(filepath.Join(tree, name), final); err != nil {
		return "", err
	}
	return final, nil
}

// provisionPackage returns the pinned CLI's script in the tool cache, installing it first from the
// pinned lock when no run has published this lock's install yet.
func (c *PinnedCLI) provisionPackage(ctx context.Context, run CommandRunner, nodeDir string) (script string, err error) {
	final := c.installDir()
	if util.FileExists(cliScript(final)) {
		return cliScript(final), nil
	}
	stage, err := os.MkdirTemp(c.Cache.Dir, stagePrefix+"cli-")
	if err != nil {
		return "", fmt.Errorf("create a stage: %w", err)
	}
	defer func() { err = errors.Join(err, os.RemoveAll(stage)) }()
	files := [...]struct {
		name string
		data []byte
	}{{"package.json", c.Pins.packageJSON}, {"package-lock.json", c.Pins.lock}}
	for i := 0; i < len(files); i++ {
		if err := util.WriteFileSecure(filepath.Join(stage, files[i].name), files[i].data, util.SecureFilePerm); err != nil {
			return "", err
		}
	}
	npm := filepath.Join(nodeDir, "lib", "node_modules", "npm", "bin", "npm-cli.js")
	if _, err := run(ctx, stage, nodeBinary(nodeDir), append([]string{npm}, npmCIArgs...)...); err != nil {
		return "", fmt.Errorf("npm ci: %w", err)
	}
	if err := checkInstalledCLI(stage, c.Pins.CLIVersion); err != nil {
		return "", err
	}
	if err := publish(stage, final); err != nil {
		return "", err
	}
	return cliScript(final), nil
}

// checkInstalledCLI confirms npm ci installed the pinned CLI version and its script.
func checkInstalledCLI(dir, version string) error {
	pkg := filepath.Join(dir, "node_modules", filepath.FromSlash(cliPackageName))
	data, err := util.ReadFileLimited(filepath.Join(pkg, "package.json"), maxPinFileBytes)
	if err != nil {
		return fmt.Errorf("npm ci left no %s: %w", cliPackageName, err)
	}
	var installed struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &installed); err != nil {
		return fmt.Errorf("parse the installed %s manifest: %w", cliPackageName, err)
	}
	if installed.Version != version || !util.FileExists(cliScript(dir)) {
		return fmt.Errorf("npm ci installed %s %q, want %q with its devcontainer.js", cliPackageName, installed.Version, version)
	}
	return nil
}

// nodeRelease names the pinned Node release archive and the directory it unpacks to.
func (c *PinnedCLI) nodeRelease() string {
	return fmt.Sprintf("node-v%s-%s", c.Pins.Node.Version, c.Platform)
}

// nodeDir is where the pinned Node release is published in the tool cache.
func (c *PinnedCLI) nodeDir() string {
	return filepath.Join(c.Cache.Dir, c.nodeRelease())
}

// installDir is where the CLI installed from the pinned lock is published in the tool cache: keyed
// by the CLI version and a digest of the package files, so a changed lock installs afresh.
func (c *PinnedCLI) installDir() string {
	sum := sha256.Sum256(append(append([]byte{}, c.Pins.packageJSON...), c.Pins.lock...))
	return filepath.Join(c.Cache.Dir, "devcontainer-cli-"+c.Pins.CLIVersion+"-"+hex.EncodeToString(sum[:6]))
}

// nodeBinary is the node executable of a Node release directory.
func nodeBinary(dir string) string {
	return filepath.Join(dir, "bin", "node")
}

// cliScript is the CLI's entry script in an install directory.
func cliScript(dir string) string {
	return filepath.Join(dir, "node_modules", filepath.FromSlash(cliPackageName), "devcontainer.js")
}

// fetchVerified downloads url into path, at most maxNodeArchiveBytes, and refuses it with
// ErrChecksumMismatch unless its SHA-256 is want.
func fetchVerified(ctx context.Context, fetch Fetcher, url, path, want string) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, util.SecureFilePerm)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	sum := sha256.New()
	if err := fetch(ctx, url, &cappedWriter{w: io.MultiWriter(file, sum), left: maxNodeArchiveBytes}); err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != want {
		return fmt.Errorf("%w: %s has SHA-256 %s, the pinned value is %s", ErrChecksumMismatch, url, got, want)
	}
	return nil
}

// cappedWriter refuses a write that would take it past left bytes.
type cappedWriter struct {
	w    io.Writer
	left int64
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > c.left {
		return 0, fmt.Errorf("the download exceeds %d bytes", maxNodeArchiveBytes)
	}
	c.left -= int64(len(p))
	return c.w.Write(p)
}

// extractFile unpacks the tar.gz archive at path into dir, which it creates.
func extractFile(ctx context.Context, path, dir string) (err error) {
	if err := os.Mkdir(dir, util.SecureDirPerm); err != nil {
		return err
	}
	file, err := os.Open(path) // #nosec G304 -- path is the stage's own download.
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	return util.ExtractTarGz(ctx, file, dir, maxNodeArchiveEntries)
}

// publish moves a finished stage to final. When another run published final first, the rename
// fails and its install is used instead.
func publish(stage, final string) error {
	if err := os.Rename(stage, final); err != nil {
		if util.DirExists(final) {
			return nil
		}
		return fmt.Errorf("publish %s: %w", final, err)
	}
	return nil
}

// removeStaleStages removes the stage directories in dir older than staleStageAge: those of a run
// killed before it published or removed its stage. A younger stage may belong to a run in progress
// and is left alone.
func removeStaleStages(dir string, now time.Time) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	for i := 0; i < len(entries) && i < maxCacheEntries; i++ {
		if !strings.HasPrefix(entries[i].Name(), stagePrefix) {
			continue
		}
		info, err := entries[i].Info()
		if err != nil || now.Sub(info.ModTime()) < staleStageAge {
			continue
		}
		errs = append(errs, os.RemoveAll(filepath.Join(dir, entries[i].Name())))
	}
	return errors.Join(errs...)
}

// lockfileFlag keeps the CLI from writing into the checkout, which would leave the tree the
// receipt certifies dirty: the CLI writes devcontainer-lock.json beside the configuration unless
// told not to. A committed lockfile is enforced as it stands (--frozen-lockfile); without one the
// CLI writes none (--no-lockfile).
func lockfileFlag(configPath string) string {
	if util.FileExists(filepath.Join(filepath.Dir(configPath), featureLockfile)) {
		return "--frozen-lockfile"
	}
	return "--no-lockfile"
}

// HTTPFetch is the production Fetcher: a GET of url under ctx, which must carry a deadline
// (HISS-02), streaming a 200 response into w. The environment's proxy settings apply.
func HTTPFetch(ctx context.Context, url string, w io.Writer) (err error) {
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("a fetch needs a context with a deadline")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := fetchClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, resp.Body.Close()) }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s answered %s: %s", url, resp.Status, util.ReadErrorBody(resp.Body))
	}
	_, err = io.Copy(w, resp.Body)
	return err
}
