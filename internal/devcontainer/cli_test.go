// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// updateNodePinsEnv rewrites cli/node.json's checksums from the pinned release's SHASUMS256.txt
// when set to 1 (TestPinnedNodeMatchesTheRelease).
const updateNodePinsEnv = "PRAETOR_UPDATE_NODE_PINS"

// writeFileAt writes body at path, creating its directory.
func writeFileAt(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// pinnedCLI is the pinned CLI for linux-x64 with an empty tool cache of its own.
func pinnedCLI(t *testing.T, fetch Fetcher) *PinnedCLI {
	t.Helper()
	pins, err := LoadCLIPins()
	if err != nil {
		t.Fatal(err)
	}
	return &PinnedCLI{Platform: "linux-x64", Pins: pins, Cache: ToolCache{Dir: filepath.Join(t.TempDir(), "tools"), Fetch: fetch}}
}

// provisionedCLI is a pinned CLI whose tool cache already holds what an earlier run published.
func provisionedCLI(t *testing.T) *PinnedCLI {
	t.Helper()
	c := pinnedCLI(t, noFetch)
	writeFileAt(t, nodeBinary(c.nodeDir()), "node")
	writeFileAt(t, cliScript(c.installDir()), "cli")
	return c
}

// archiveLinks reports whether the test process can unpack the bin/npm symbolic link a Node
// release carries. On Windows creating one needs Developer Mode or an elevated token, as the
// extractor's own tests note (util.requireArchiveSymlinks); the pinned CLI runs on Linux and
// macOS only (nodePlatforms), so the Windows leg replays the archive without the link.
func archiveLinks() bool {
	return runtime.GOOS != "windows"
}

// nodeArchive is a stand-in Node release archive for c: node, npm's CLI script and, where
// archiveLinks allows, the bin/npm link a real release carries. c's pinned checksum becomes the
// archive's.
func nodeArchive(t *testing.T, c *PinnedCLI) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	top := c.nodeRelease() + "/"
	entries := []tar.Header{
		{Name: top, Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: top + "bin/node", Typeflag: tar.TypeReg, Mode: 0o755, Size: 4},
		{Name: top + "lib/node_modules/npm/bin/npm-cli.js", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4},
	}
	if archiveLinks() {
		entries = append(entries, tar.Header{Name: top + "bin/npm", Typeflag: tar.TypeSymlink, Linkname: "../lib/node_modules/npm/bin/npm-cli.js"})
	}
	for i := range entries {
		if err := archive.WriteHeader(&entries[i]); err != nil {
			t.Fatal(err)
		}
		if entries[i].Size > 0 {
			if _, err := archive.Write([]byte("stub")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := errors.Join(archive.Close(), compressed.Close()); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buffer.Bytes())
	c.Pins.Node.SHA256[c.Platform] = hex.EncodeToString(sum[:])
	return buffer.Bytes()
}

// servedFetch serves body for every URL and records the URLs asked for.
type servedFetch struct {
	body []byte
	urls []string
	err  error
}

func (f *servedFetch) fetch(_ context.Context, url string, w io.Writer) error {
	f.urls = append(f.urls, url)
	if f.err != nil {
		return f.err
	}
	_, err := w.Write(f.body)
	return err
}

// fakeNPM stands in for `node npm-cli.js ci`: it installs the CLI at version into its working
// directory, or fails with err.
type fakeNPM struct {
	version  string
	err      error
	commands [][]string
}

func (n *fakeNPM) run(_ context.Context, dir, name string, args ...string) (string, error) {
	n.commands = append(n.commands, append([]string{dir, name}, args...))
	if n.err != nil {
		return "", n.err
	}
	pkg := filepath.Join(dir, "node_modules", "@devcontainers", "cli")
	manifest, err := json.Marshal(map[string]string{"name": cliPackageName, "version": n.version})
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), manifest, 0o600); err != nil {
		return "", err
	}
	return "", os.WriteFile(filepath.Join(pkg, "devcontainer.js"), []byte("cli"), 0o600)
}

// stages lists the stage directories left in the tool cache.
func stages(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), stagePrefix) {
			left = append(left, entry.Name())
		}
	}
	return left
}

// Positive: the shipped pins are complete and exact. The lock resolves the CLI to the version
// package.json names with a sha512 integrity, and the Node release carries a SHA-256 for every
// platform the gate plans for.
func TestLoadCLIPinsAreCompleteAndExact(t *testing.T) {
	pins, err := LoadCLIPins()
	if err != nil {
		t.Fatalf("the shipped pins: %v", err)
	}
	if !nodeVersionForm.MatchString(pins.CLIVersion) || !strings.HasPrefix(pins.Node.Version, "24.") {
		t.Errorf("pins = CLI %q, Node %q; want an exact CLI version and Node 24", pins.CLIVersion, pins.Node.Version)
	}
	if want := []string{"darwin-arm64", "darwin-x64", "linux-arm64", "linux-x64"}; !slices.Equal(NodePlatforms(), want) {
		t.Errorf("platforms = %q, want %q", NodePlatforms(), want)
	}
	for _, platform := range NodePlatforms() {
		if !sha256Form.MatchString(pins.Node.SHA256[platform]) {
			t.Errorf("no SHA-256 pinned for %s", platform)
		}
	}
}

// Negative: a manifest range, a lock that resolves another version or carries no integrity, a
// Node version that is not x.y.z and a release missing a platform's checksum are each refused.
func TestLoadCLIPinsRefusesLoosePins(t *testing.T) {
	lock := func(version, integrity string) []byte {
		return []byte(`{"packages": {"node_modules/@devcontainers/cli": {"version": "` + version + `", "integrity": "` + integrity + `"}}}`)
	}
	manifest := func(spec string) []byte { return []byte(`{"dependencies": {"@devcontainers/cli": "` + spec + `"}}`) }
	for name, c := range map[string]struct{ manifest, lock []byte }{
		"range":        {manifest("^0.89.0"), lock("0.89.0", "sha512-x")},
		"other lock":   {manifest("0.89.0"), lock("0.88.0", "sha512-x")},
		"no integrity": {manifest("0.89.0"), lock("0.89.0", "")},
		"not json":     {[]byte("{"), lock("0.89.0", "sha512-x")},
	} {
		if _, err := lockedCLIVersion(c.manifest, c.lock); err == nil {
			t.Errorf("%s: a loose CLI pin must be refused", name)
		}
	}
	sums := `"darwin-arm64": "` + strings.Repeat("a", 64) + `", "darwin-x64": "` + strings.Repeat("a", 64) + `", "linux-arm64": "` + strings.Repeat("a", 64) + `"`
	for name, release := range map[string]string{
		"missing platform": `{"version": "24.21.0", "sha256": {` + sums + `}}`,
		"uppercase sum":    `{"version": "24.21.0", "sha256": {` + sums + `, "linux-x64": "` + strings.Repeat("A", 64) + `"}}`,
		"loose version":    `{"version": "24", "sha256": {` + sums + `, "linux-x64": "` + strings.Repeat("a", 64) + `"}}`,
	} {
		if _, err := parseNodeRelease([]byte(release)); err == nil {
			t.Errorf("%s: a loose Node pin must be refused", name)
		}
	}
}

// Positive: a first run fetches the pinned Node from the official release server, checks it, and
// installs the CLI from the pinned lock with npm ci run by that Node, leaving no stage behind; a
// second run reuses both and fetches and installs nothing.
func TestProvisionFetchesVerifiesAndReuses(t *testing.T) {
	served := &servedFetch{}
	c := pinnedCLI(t, served.fetch)
	served.body = nodeArchive(t, c)
	npm := &fakeNPM{version: c.Pins.CLIVersion}

	cmd, err := c.provision(t.Context(), npm.run)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	wantURL := fmt.Sprintf("%s/v%s/node-v%s-linux-x64.tar.gz", NodeDistURL, c.Pins.Node.Version, c.Pins.Node.Version)
	if !slices.Equal(served.urls, []string{wantURL}) {
		t.Errorf("fetched %q, want %s", served.urls, wantURL)
	}
	if cmd.node != nodeBinary(c.nodeDir()) || cmd.script != cliScript(c.installDir()) {
		t.Errorf("command = %+v, want the published node and CLI script", cmd)
	}
	if len(npm.commands) != 1 {
		t.Fatalf("npm ran %d times, want once", len(npm.commands))
	}
	want := append([]string{nodeBinary(c.nodeDir()), filepath.Join(c.nodeDir(), "lib", "node_modules", "npm", "bin", "npm-cli.js")}, npmCIArgs...)
	if got := npm.commands[0]; !slices.Equal(got[1:], want) || !strings.HasPrefix(filepath.Base(got[0]), stagePrefix+"cli-") {
		t.Errorf("npm command = %q, want %q in a stage", got, want)
	}
	if !archiveLinks() {
		t.Log("the bin/npm link is not replayed on this platform (archiveLinks)")
	} else if link, err := os.Readlink(filepath.Join(c.nodeDir(), "bin", "npm")); err != nil || link != "../lib/node_modules/npm/bin/npm-cli.js" {
		t.Errorf("the release's bin/npm link = %q, %v", link, err)
	}
	if left := stages(t, c.Cache.Dir); len(left) != 0 {
		t.Errorf("stages left behind: %q", left)
	}

	again, err := c.provision(t.Context(), npm.run)
	if err != nil || again != cmd || len(served.urls) != 1 || len(npm.commands) != 1 {
		t.Errorf("a second run must reuse the cache: %+v %v, fetches %d, npm runs %d", again, err, len(served.urls), len(npm.commands))
	}
}

// Negative: a download whose SHA-256 is not the pinned one, a failed download, a failed npm ci and
// an install of another version each fail closed, and nothing a later run would reuse is left.
func TestProvisionFailsClosed(t *testing.T) {
	t.Run("checksum", func(t *testing.T) {
		served := &servedFetch{}
		c := pinnedCLI(t, served.fetch)
		served.body = nodeArchive(t, c)
		pinned := strings.Repeat("0", 64)
		c.Pins.Node.SHA256[c.Platform] = pinned
		npm := &fakeNPM{version: c.Pins.CLIVersion}
		_, err := c.provision(t.Context(), npm.run)
		if !errors.Is(err, ErrChecksumMismatch) || !strings.Contains(err.Error(), "the pinned value is "+pinned) {
			t.Fatalf("a download that is not the pinned release must be refused, got %v", err)
		}
		if _, statErr := os.Stat(c.nodeDir()); !errors.Is(statErr, os.ErrNotExist) || len(npm.commands) != 0 || len(stages(t, c.Cache.Dir)) != 0 {
			t.Errorf("a refused download must publish nothing and run nothing: %v, npm %q, stages %q", statErr, npm.commands, stages(t, c.Cache.Dir))
		}
	})
	cases := map[string]struct {
		fetchErr error
		npm      *fakeNPM
		want     string
	}{
		"download": {fetchErr: errors.New("connection refused"), npm: &fakeNPM{}, want: "connection refused"},
		"npm ci":   {npm: &fakeNPM{err: errors.New("E403 forbidden")}, want: "npm ci: E403 forbidden"},
		"version":  {npm: &fakeNPM{version: "0.1.0"}, want: `installed @devcontainers/cli "0.1.0"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			served := &servedFetch{err: tc.fetchErr}
			c := pinnedCLI(t, served.fetch)
			served.body = nodeArchive(t, c)
			if _, err := c.provision(t.Context(), tc.npm.run); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("provision = %v, want an error naming %q", err, tc.want)
			}
			if _, err := os.Stat(c.installDir()); !errors.Is(err, os.ErrNotExist) || len(stages(t, c.Cache.Dir)) != 0 {
				t.Errorf("a failed install must publish nothing and leave no stage: %v %q", err, stages(t, c.Cache.Dir))
			}
		})
	}
}

// Boundary: a download past its cap is cut off, a run that lost the publish race uses the
// winner's install, and only stages older than any run may take are swept.
func TestProvisionBoundaries(t *testing.T) {
	capped := &cappedWriter{w: io.Discard, left: 4}
	if n, err := capped.Write([]byte("four")); n != 4 || err != nil {
		t.Errorf("a write up to the cap must pass: %d %v", n, err)
	}
	if _, err := capped.Write([]byte("x")); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("a write past the cap must be refused, got %v", err)
	}

	dir := t.TempDir()
	winner := filepath.Join(dir, "final")
	writeFileAt(t, filepath.Join(winner, "mark"), "winner")
	loser := filepath.Join(dir, "stage")
	writeFileAt(t, filepath.Join(loser, "mark"), "loser")
	if err := publish(loser, winner); err != nil {
		t.Fatalf("a lost publish race must use the published install: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(winner, "mark")); err != nil || string(data) != "winner" {
		t.Errorf("the published install must stay the winner's: %q %v", data, err)
	}
	if err := publish(filepath.Join(dir, "absent"), filepath.Join(dir, "elsewhere")); err == nil {
		t.Error("a publish that fails with nothing published must fail")
	}

	now := time.Now()
	stale, young := filepath.Join(dir, stagePrefix+"node-old"), filepath.Join(dir, stagePrefix+"cli-new")
	writeFileAt(t, filepath.Join(stale, "x"), "x")
	writeFileAt(t, filepath.Join(young, "x"), "x")
	if err := os.Chtimes(stale, now.Add(-staleStageAge-time.Minute), now.Add(-staleStageAge-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := removeStaleStages(dir, now); err != nil {
		t.Fatal(err)
	}
	if left := stages(t, dir); !slices.Equal(left, []string{filepath.Base(young)}) {
		t.Errorf("only the stale stage may be swept, left %q", left)
	}
}

// HTTPFetch streams a 200 response (positive), refuses another status naming it (negative), and
// refuses a context without a deadline before any request (boundary, HISS-02).
func TestHTTPFetch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.Error(w, "no such release", http.StatusNotFound)
			return
		}
		if _, err := w.Write([]byte("release")); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	var got bytes.Buffer
	if err := HTTPFetch(ctx, server.URL+"/node.tar.gz", &got); err != nil || got.String() != "release" {
		t.Errorf("fetch = %q, %v", got.String(), err)
	}
	if err := HTTPFetch(ctx, server.URL+"/missing", io.Discard); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("a 404 must fail naming the status, got %v", err)
	}
	if err := HTTPFetch(context.Background(), server.URL, io.Discard); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Errorf("a fetch without a deadline must be refused, got %v", err)
	}
}

// The pinned Node checksums are the release's own: each equals its line in the release's
// SHASUMS256.txt on the official server. Renovate moves the version alone, so this fails on its
// branch until the checksums are refreshed:
//
//	PRAETOR_UPDATE_NODE_PINS=1 go test ./internal/devcontainer -run TestPinnedNodeMatchesTheRelease
//
// It skips under -short and when the server cannot be reached; a reached server that disagrees
// fails.
func TestPinnedNodeMatchesTheRelease(t *testing.T) {
	if testing.Short() {
		t.Skip("-short: this test reads the pinned release's SHASUMS256.txt from " + NodeDistURL)
	}
	pins, err := LoadCLIPins()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	var sums bytes.Buffer
	url := fmt.Sprintf("%s/v%s/SHASUMS256.txt", NodeDistURL, pins.Node.Version)
	if err := HTTPFetch(ctx, url, &cappedWriter{w: &sums, left: 1 << 20}); err != nil {
		t.Skipf("%s cannot be reached here: %v", url, err)
	}
	upstream := map[string]string{}
	for _, line := range strings.Split(sums.String(), "\n") {
		if sum, name, ok := strings.Cut(strings.TrimSpace(line), "  "); ok {
			upstream[name] = sum
		}
	}
	release := NodeRelease{Version: pins.Node.Version, SHA256: map[string]string{}}
	for _, platform := range NodePlatforms() {
		release.SHA256[platform] = upstream[fmt.Sprintf("node-v%s-%s.tar.gz", pins.Node.Version, platform)]
	}
	if os.Getenv(updateNodePinsEnv) == "1" {
		rewriteNodePins(t, release)
		return
	}
	for _, platform := range NodePlatforms() {
		if pins.Node.SHA256[platform] != release.SHA256[platform] {
			t.Errorf("cli/node.json pins %s at %q; %s says %q (refresh with %s=1)", platform, pins.Node.SHA256[platform], url,
				release.SHA256[platform], updateNodePinsEnv)
		}
	}
}

// rewriteNodePins writes release to cli/node.json in the form the file ships in.
func rewriteNodePins(t *testing.T, release NodeRelease) {
	t.Helper()
	data, err := json.MarshalIndent(release, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseNodeRelease(data); err != nil {
		t.Fatalf("the release does not publish every pinned platform: %v", err)
	}
	if err := os.WriteFile(filepath.Join("cli", "node.json"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// renovateRules is the part of renovate.json that moves the CLI pins.
type renovateRules struct {
	CustomManagers []struct {
		CustomType          string   `json:"customType"`
		ManagerFilePatterns []string `json:"managerFilePatterns"`
		MatchStrings        []string `json:"matchStrings"`
		DepNameTemplate     string   `json:"depNameTemplate"`
		DatasourceTemplate  string   `json:"datasourceTemplate"`
	} `json:"customManagers"`
	PackageRules []struct {
		MatchManagers   []string `json:"matchManagers"`
		MatchFileNames  []string `json:"matchFileNames"`
		MatchDepNames   []string `json:"matchDepNames"`
		AllowedVersions string   `json:"allowedVersions"`
		RangeStrategy   string   `json:"rangeStrategy"`
	} `json:"packageRules"`
}

// Renovate moves both pins: a regex manager reads the Node version out of cli/node.json from the
// node-version datasource, held to Node 24, and the npm manager keeps the CLI's exact version and
// lock together.
func TestRenovateMovesThePins(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "renovate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config renovateRules
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	pins, err := LoadCLIPins()
	if err != nil {
		t.Fatal(err)
	}
	node, err := cliAssets.ReadFile("cli/node.json")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, manager := range config.CustomManagers {
		if manager.CustomType == "regex" && manager.DatasourceTemplate == "node-version" &&
			renovatePatternsMatch(t, manager.ManagerFilePatterns, "internal/devcontainer/cli/node.json") {
			found = capturesVersion(t, manager.MatchStrings, string(node), pins.Node.Version)
		}
	}
	if !found {
		t.Error("renovate.json has no regex manager reading the Node version out of internal/devcontainer/cli/node.json")
	}
	var heldTo24, cliPinned bool
	for _, rule := range config.PackageRules {
		heldTo24 = heldTo24 || (slices.Contains(rule.MatchDepNames, "node") && slices.Contains(rule.MatchManagers, "custom.regex") &&
			rule.AllowedVersions == "<25")
		cliPinned = cliPinned || (slices.Contains(rule.MatchManagers, "npm") && slices.Contains(rule.MatchFileNames, "internal/devcontainer/cli/**") &&
			rule.RangeStrategy == "pin")
	}
	if !heldTo24 || !cliPinned {
		t.Errorf("renovate.json must hold the pinned Node to 24 (%v) and keep the CLI pinned with its lock (%v)", heldTo24, cliPinned)
	}
}

// renovatePatternsMatch reports whether one of Renovate's /regex/ file patterns matches path.
func renovatePatternsMatch(t *testing.T, patterns []string, path string) bool {
	t.Helper()
	for _, pattern := range patterns {
		expr, err := regexp.Compile(strings.TrimSuffix(strings.TrimPrefix(pattern, "/"), "/"))
		if err != nil {
			t.Fatalf("pattern %q: %v", pattern, err)
		}
		if expr.MatchString(path) {
			return true
		}
	}
	return false
}

// capturesVersion reports whether one of the match strings captures want as currentValue in text.
func capturesVersion(t *testing.T, matchStrings []string, text, want string) bool {
	t.Helper()
	for _, match := range matchStrings {
		expr, err := regexp.Compile(match)
		if err != nil {
			t.Fatalf("match string %q: %v", match, err)
		}
		groups := expr.FindStringSubmatch(text)
		if at := expr.SubexpIndex("currentValue"); at >= 0 && groups != nil && groups[at] == want {
			return true
		}
	}
	return false
}
