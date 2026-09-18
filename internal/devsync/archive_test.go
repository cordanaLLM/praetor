package devsync

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func unitNames(units []unit) []string {
	names := make([]string, 0, len(units))
	for _, u := range units {
		names = append(names, filepath.ToSlash(u.rel))
	}
	return names
}

func TestDiscoverUnits(t *testing.T) {
	dev := makeDevTree(t)
	units, err := discoverUnits(context.Background(), dev)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"empty-org", "empty-org/x", "org", "org/app", "scratch", "solo"}
	if got := unitNames(units); !reflect.DeepEqual(got, want) {
		t.Fatalf("units = %v, want %v", got, want)
	}
	for _, u := range units {
		if u.rel == "org" && (!u.folder || !u.exclude["app"]) {
			t.Fatalf("folder unit must exclude its repository: %+v", u)
		}
		if u.rel == filepath.Join("org", "app") && u.folder {
			t.Fatal("repository marked as folder unit")
		}
	}
}

func TestDiscoverUnitsNegative(t *testing.T) {
	if _, err := discoverUnits(context.Background(), filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("missing dev folder accepted")
	}
	dev := makeDevTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := discoverUnits(ctx, dev); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled discovery = %v", err)
	}
}

func TestDiscoverUnitsDepthBoundary(t *testing.T) {
	dev := t.TempDir()
	// A repository maxRepoDepth levels below the top-level folder is found; one deeper is not
	// searched, and a repository inside a build cache is never searched.
	makeRepo(t, filepath.Join(dev, "top", "a", "b", "c", "found"))
	makeRepo(t, filepath.Join(dev, "top", "a", "b", "c", "d", "too-deep"))
	makeRepo(t, filepath.Join(dev, "top", "node_modules", "cached"))
	units, err := discoverUnits(context.Background(), dev)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"top", "top/a/b/c/found"}
	if got := unitNames(units); !reflect.DeepEqual(got, want) {
		t.Fatalf("units = %v, want %v", got, want)
	}
}

func TestWriteArchiveRoundTrip(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	makeRepo(t, repo)
	writeTestFile(t, filepath.Join(repo, "docs", "guide.md"), "guide\n")
	writeTestFile(t, filepath.Join(repo, "vendor", "lib.go"), "package lib\n")
	var buffer bytes.Buffer
	fp, err := writeArchive(context.Background(), unit{dir: repo}, &buffer)
	if err != nil {
		t.Fatal(err)
	}
	if fp.Files != 4 || fp.Bytes == 0 {
		t.Fatalf("fingerprint = %+v, want 4 files", fp)
	}
	target := t.TempDir()
	if err := extractArchive(context.Background(), &buffer, target); err != nil {
		t.Fatal(err)
	}
	if readTestFile(t, filepath.Join(target, ".git", "HEAD")) != "ref: refs/heads/main\n" ||
		readTestFile(t, filepath.Join(target, "vendor", "lib.go")) != "package lib\n" {
		t.Fatal(".git or vendor lost")
	}
	if _, err := os.Stat(filepath.Join(target, "node_modules")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("build cache archived")
	}
}

func TestWriteArchiveKeepsLinks(t *testing.T) {
	requireSymlinks(t)
	repo := t.TempDir()
	writeTestFile(t, filepath.Join(repo, "AGENTS.md"), "agents\n")
	if err := os.Symlink("AGENTS.md", filepath.Join(repo, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if _, err := writeArchive(context.Background(), unit{dir: repo}, &buffer); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := extractArchive(context.Background(), &buffer, target); err != nil {
		t.Fatal(err)
	}
	link, err := os.Readlink(filepath.Join(target, "CLAUDE.md"))
	if err != nil || link != "AGENTS.md" {
		t.Fatalf("link = %q, %v", link, err)
	}
}

func TestWriteArchiveFailureWritesNoTrailer(t *testing.T) {
	repo := t.TempDir()
	makeRepo(t, repo)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var buffer bytes.Buffer
	if _, err := writeArchive(ctx, unit{dir: repo}, &buffer); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled archive = %v", err)
	}
	if err := extractArchive(context.Background(), &buffer, t.TempDir()); err == nil {
		t.Fatal("a failed archive must not read back as complete")
	}
}

func TestFingerprintTracksChanges(t *testing.T) {
	repo := t.TempDir()
	makeRepo(t, repo)
	u := unit{dir: repo}
	first, err := fingerprintUnit(context.Background(), u)
	if err != nil {
		t.Fatal(err)
	}
	again, err := fingerprintUnit(context.Background(), u)
	if err != nil || again != first {
		t.Fatalf("unchanged tree changed fingerprint: %+v %+v %v", first, again, err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(repo, "main.go"), later, later); err != nil {
		t.Fatal(err)
	}
	touched, err := fingerprintUnit(context.Background(), u)
	if err != nil || touched == first {
		t.Fatalf("modified file kept fingerprint: %+v %v", touched, err)
	}
	// A change inside an excluded cache folder does not count.
	writeTestFile(t, filepath.Join(repo, "node_modules", "new.js"), "x")
	cached, err := fingerprintUnit(context.Background(), u)
	if err != nil || cached != touched {
		t.Fatalf("cache change altered fingerprint: %+v %v", cached, err)
	}
}
