// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/clientjson"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
)

// managedRenovatePaths adopts repo with opts and returns the paths its managed Renovate entry
// lists, none when the configuration holds no managed entry.
func managedRenovatePaths(t *testing.T, repo string, opts AdoptOptions) []string {
	t.Helper()
	opts.Path = repo
	if opts.LockSourceRoot == "" {
		opts.LockSourceRoot = newAdoptLockSource(t)
	}
	if _, err := Adopt(t.Context(), opts); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	_, managed, _ := renovateTestRules(t, mustRead(t, filepath.Join(repo, "renovate.json")))
	if len(managed) > 1 || (len(managed) == 1 && managed[0].Enabled) {
		t.Fatalf("managed entries %+v, want at most one disabled entry", managed)
	}
	if len(managed) == 0 {
		return nil
	}
	return managed[0].MatchFileNames
}

// bundleRenovateRepo is a repository with a Renovate configuration of no rule of its own and a
// tracked go.mod, so the default facets enable every managed family.
func bundleRenovateRepo(t *testing.T, name string) string {
	t.Helper()
	repo := newTestRepo(t, name)
	trackGoModule(t, repo, "go.mod")
	mustWrite(t, filepath.Join(repo, "renovate.json"), "{\"packageRules\": []}\n")
	return repo
}

// Positive (#323): adoption that generates the DevContainer keeps the adopter's Renovate off
// the bundle files its managers read. The files are named exactly as
// devcontainer.UpdateBotBundleFiles names them, a dry run plans the entry the real run
// writes, and forcing adoption over an operator's own DevContainer, which replaces it, lists
// them too.
func TestRenovateIgnorePositiveListsTheGeneratedBundle(t *testing.T) {
	if want := devcontainer.UpdateBotBundleFiles(devcontainerFile); !slices.Equal(devContainerBundleFixturePaths, want) {
		t.Fatalf("bundle files a bot reads = %v, want %v", want, devContainerBundleFixturePaths)
	}
	repo := bundleRenovateRepo(t, "renovate-bundle")
	source := newAdoptLockSource(t)
	dry, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: source, Path: repo, DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if got := mustRead(t, filepath.Join(repo, "renovate.json")); got != "{\"packageRules\": []}\n" {
		t.Fatalf("the dry run wrote renovate.json:\n%s", got)
	}
	want := withBundleFixturePaths(renovateManagedFixturePaths)
	if got := managedRenovatePaths(t, repo, AdoptOptions{LockSourceRoot: source}); !slices.Equal(got, want) {
		t.Fatalf("managed entry lists %v, want the family files and the bundle %v", got, want)
	}
	wet, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: source, Path: newRenovateTwin(t, repo)})
	if err != nil {
		t.Fatalf("adopt twin: %v", err)
	}
	if planned, written := findActionDetail(dry.ActionDetails, "renovate.json"), findActionDetail(wet.ActionDetails, "renovate.json"); planned == "" || planned != written {
		t.Fatalf("dry run planned %q, the real run reported %q", planned, written)
	}
	if !devcontainer.RecordsBootstrap([]byte(mustRead(t, filepath.Join(repo, filepath.FromSlash(devcontainerFile))))) {
		t.Fatal("adoption generated no Praetor DevContainer; the fixture has stopped exercising the generated case")
	}
	// A generated config a bot edited since is still generated: the rerun keeps the entry.
	generated := filepath.Join(repo, filepath.FromSlash(devcontainerFile))
	mustWrite(t, generated, strings.Replace(mustRead(t, generated), "{", "{\n  \"runArgs\": [\"--init\"],", 1))
	if got := managedRenovatePaths(t, repo, AdoptOptions{LockSourceRoot: source}); !slices.Equal(got, want) {
		t.Fatalf("rerun over an edited generated config lists %v, want %v", got, want)
	}
	forced := bundleRenovateRepo(t, "renovate-bundle-forced")
	mustWrite(t, filepath.Join(forced, filepath.FromSlash(devcontainerFile)), ownDevContainer)
	if got := managedRenovatePaths(t, forced, AdoptOptions{Force: true}); !slices.Equal(got, want) {
		t.Fatalf("forced adoption lists %v, want %v", got, want)
	}
}

// newRenovateTwin returns a fresh repository shaped as bundleRenovateRepo builds one, for a
// first real adoption to compare with a dry run of repo's.
func newRenovateTwin(t *testing.T, repo string) string {
	t.Helper()
	return bundleRenovateRepo(t, filepath.Base(repo))
}

// Negative (#323): a DevContainer of the operator's own is preserved and stays with the
// operator's Renovate, so the managed entry lists the family files alone, and with no family
// file either there is no entry.
func TestRenovateIgnoreNegativeLeavesAnOwnDevContainerToRenovate(t *testing.T) {
	repo := bundleRenovateRepo(t, "renovate-own-devcontainer")
	own := filepath.Join(repo, filepath.FromSlash(devcontainerFile))
	mustWrite(t, own, ownDevContainer)
	if got := managedRenovatePaths(t, repo, AdoptOptions{}); !slices.Equal(got, renovateManagedFixturePaths) {
		t.Fatalf("managed entry lists %v, want the family files only", got)
	}
	if mustRead(t, own) != ownDevContainer {
		t.Fatal("adoption replaced the operator's DevContainer")
	}
	paths, err := generatedDevContainerPaths(t.Context(), recordingSession(repo, repoIdentity{}))
	if err != nil || len(paths) != 0 {
		t.Fatalf("an own DevContainer contributes %v (%v)", paths, err)
	}
}

// Boundary (#323): an adopter rule that already disables Renovate below .devcontainer covers
// the bundle, so the managed entry lists the family files alone; Praetor's own renovate.json
// covers its bundle the same way, so adoption here adds no entry for it. A DevContainer the
// read contract refuses, a symbolic link, is preserved by adoption and contributes no path,
// and an ended context is an error, never an unlisted bundle.
func TestRenovateIgnoreBoundaryBundleCoverage(t *testing.T) {
	repo := bundleRenovateRepo(t, "renovate-bundle-covered")
	mustWrite(t, filepath.Join(repo, "renovate.json"), `{"packageRules": [{"matchFileNames": [".devcontainer/**"], "enabled": false}]}`+"\n")
	if got := managedRenovatePaths(t, repo, AdoptOptions{}); !slices.Equal(got, renovateManagedFixturePaths) {
		t.Fatalf("managed entry lists %v, want the family files only", got)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "renovate.json"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := clientjson.DecodeObject(data)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := root.Get("packageRules")
	var rules []jsontext.Value
	if err := json.Unmarshal(raw, &rules); err != nil {
		t.Fatal(err)
	}
	if uncovered := uncoveredRenovatePaths(rules, devContainerBundleFixturePaths); len(uncovered) != 0 {
		t.Fatalf("Praetor's renovate.json leaves Renovate on its generated %v", uncovered)
	}
	if uncovered := uncoveredRenovatePaths(nil, devContainerBundleFixturePaths); !slices.Equal(uncovered, devContainerBundleFixturePaths) {
		t.Fatalf("no rule covered %v", devContainerBundleFixturePaths)
	}
	linked := t.TempDir()
	mustWrite(t, filepath.Join(linked, "shared.json"), ownDevContainer)
	if err := os.MkdirAll(filepath.Join(linked, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "shared.json"), filepath.Join(linked, filepath.FromSlash(devcontainerFile))); err != nil {
		t.Logf("symlinks unavailable, case skipped: %v", err)
	} else if paths, err := generatedDevContainerPaths(t.Context(), recordingSession(linked, repoIdentity{})); err != nil || len(paths) != 0 {
		t.Fatalf("a symbolic link contributes %v (%v)", paths, err)
	}
	ended, cancel := context.WithCancel(t.Context())
	cancel()
	if paths, err := generatedDevContainerPaths(ended, recordingSession(repo, repoIdentity{})); !errors.Is(err, context.Canceled) || len(paths) != 0 {
		t.Fatalf("ended context returned %v (%v), want context.Canceled", paths, err)
	}
	absent, err := generatedDevContainerPaths(t.Context(), recordingSession(t.TempDir(), repoIdentity{}))
	if err != nil || !slices.Equal(absent, devContainerBundleFixturePaths) {
		t.Fatalf("a repository without a DevContainer, which adoption generates, contributes %v (%v)", absent, err)
	}
}
