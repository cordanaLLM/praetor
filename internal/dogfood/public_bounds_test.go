package dogfood

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPublicSourcesExactBoundsAndPins(t *testing.T) {
	opts := PublicLoopOptions{SourceRoot: "source", ArtifactDir: "artifacts", Repositories: append([]string(nil), PopularBenchmarks...), MaxAttempts: MaxPublicAttempts}
	if err := normalizePublicBounds(&opts); err != nil {
		t.Fatal(err)
	}
	sources, err := parsePublicSources(opts.Repositories)
	if err != nil || len(sources) != MaxPublicRepositories {
		t.Fatalf("exact curated bound: %v %v", sources, err)
	}
	for _, size := range []int{40, 64} {
		sha := strings.Repeat("a", size)
		pinned, err := parsePublicSources([]string{"https://github.com/spf13/cobra#" + sha})
		if err != nil || pinned[0].sha != sha {
			t.Fatalf("valid pin %d rejected: %v", size, err)
		}
	}
	for _, value := range []string{"", "https://github.com/spf13/cobra#", "https://github.com/spf13/cobra#main", "https://github.com/spf13/cobra?token=x", "file:///tmp/repo", strings.Repeat("x", 4097)} {
		if _, err := parsePublicSources([]string{value}); err == nil {
			t.Fatalf("invalid public URL accepted: %q", value)
		}
	}
	if _, err := parsePublicSources([]string{opts.Repositories[0], opts.Repositories[0]}); err == nil {
		t.Fatal("duplicate accepted")
	}
	opts.Repositories = append(opts.Repositories, opts.Repositories[0])
	if err := normalizePublicBounds(&opts); err == nil {
		t.Fatal("cap+1 accepted")
	}
}

func TestPublicSnapshotBoundsAndLinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data")
	publicWrite(t, path, "data", 0o600)
	before, err := snapshotPublicTree(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	publicWrite(t, path, "changed", 0o600)
	outside := filepath.Join(t.TempDir(), "secret")
	publicWrite(t, outside, "must not hash this content", 0o600)
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	after, err := snapshotPublicTree(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if before.digest() == after.digest() || !reflect.DeepEqual(after.changed(before), []string{"data", "link"}) {
		t.Fatalf("changed files missing: %v", after.changed(before))
	}
	if after["link"] != "symlink:"+outside {
		t.Fatal("snapshot followed outside symlink")
	}
	if err := os.Truncate(path, maxPublicFileBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotPublicTree(context.Background(), dir); err != nil {
		t.Fatalf("exact file bound failed: %v", err)
	}
	if err := os.Truncate(path, maxPublicFileBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotPublicTree(context.Background(), dir); err == nil {
		t.Fatal("cap+1 file accepted")
	}
}

func TestPublicManifestRefusesLinksAndOversize(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "manifest")
	publicWrite(t, outside, "version: 1\n", 0o600)
	path := filepath.Join(dir, ".standards.yaml")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := publicManifest(context.Background(), dir); err == nil {
		t.Fatal("manifest symlink followed")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	publicWrite(t, path, "version: 1\n", 0o600)
	if _, err := publicManifest(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, (1<<20)+1); err != nil {
		t.Fatal(err)
	}
	if _, err := publicManifest(context.Background(), dir); err == nil {
		t.Fatal("oversized manifest accepted")
	}
}

func TestRemoteModesDoNotResolveWorkstationHome(t *testing.T) {
	for _, opts := range []DogfoodOptions{{RemoteRepos: []string{"https://github.com/spf13/cobra"}, HomeDir: "/must-not-read"}, {BenchmarkPopular: true, HomeDir: "/must-not-read"}} {
		home, err := resolveAuditHome(opts)
		if err != nil || home != "" {
			t.Fatalf("remote run inspected workstation: %q %v", home, err)
		}
	}
}

func TestPublicSnapshotIncludesDirectoryModesAndEmptyDirectories(t *testing.T) {
	dir := t.TempDir()
	before, err := snapshotPublicTree(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	added, err := snapshotPublicTree(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if before.digest() == added.digest() {
		t.Fatal("empty directory omitted from tree digest")
	}
	if err := os.Chmod(empty, 0o500); err != nil {
		t.Fatal(err)
	}
	changed, err := snapshotPublicTree(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if added.digest() == changed.digest() {
		t.Fatal("directory permissions omitted from tree digest")
	}
}

func TestPublicSnapshotEntryBound(t *testing.T) {
	dir := t.TempDir()
	// The root directory itself consumes one entry.
	for i := 1; i < maxPublicTreeEntries; i++ {
		publicWrite(t, filepath.Join(dir, fmt.Sprintf("file-%05d", i)), "", 0o600)
	}
	if tree, err := snapshotPublicTree(context.Background(), dir); err != nil || len(tree) != maxPublicTreeEntries {
		t.Fatalf("exact entry bound: %d %v", len(tree), err)
	}
	publicWrite(t, filepath.Join(dir, "overflow"), "", 0o600)
	if _, err := snapshotPublicTree(context.Background(), dir); err == nil {
		t.Fatal("cap+1 entry accepted")
	}
}
