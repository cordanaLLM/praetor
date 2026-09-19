package devsync

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
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

// gitRepo initialises a real repository with the given .gitignore and tracked paths.
func gitRepo(t *testing.T, gitignore string, files map[string]bool) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, ".gitignore"), gitignore)
	if out, err := util.RunGit(context.Background(), dir, "init", "-q"); err != nil {
		t.Fatal(err, out)
	}
	for name, tracked := range files {
		writeTestFile(t, filepath.Join(dir, filepath.FromSlash(name)), name)
		if !tracked {
			continue
		}
		if out, err := util.RunGit(context.Background(), dir, "add", "-f", "--", name); err != nil {
			t.Fatal(err, out)
		}
	}
	return dir
}

func archivedNames(t *testing.T, u unit) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	err := walkUnit(context.Background(), u, func(rel string, _ os.FileInfo) error {
		names[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestGitIgnoresDecideCacheFolders(t *testing.T) {
	// Tracked build/ is archived although build/ is ignored; untracked, ignored node_modules is not.
	repo := gitRepo(t, "node_modules/\nbuild/\n", map[string]bool{"build/package/Dockerfile": true, "node_modules/dep.js": false})
	u, note := withGitIgnores(context.Background(), unit{dir: repo})
	names := archivedNames(t, u)
	if note != "" || !names["build/package/Dockerfile"] || names["node_modules"] {
		t.Fatalf("note %q, archived %v", note, names)
	}
	// Boundary: an ignored build/ holding nothing tracked is left out, like any cache.
	repo = gitRepo(t, "build/\n", map[string]bool{"main.go": true, "build/out.bin": false, "dist/app.js": false})
	u, _ = withGitIgnores(context.Background(), unit{dir: repo})
	names = archivedNames(t, u)
	if names["build"] || !names["main.go"] || !names["dist/app.js"] {
		t.Fatalf("archived %v: ignored build/ must go, unignored dist/ must stay", names)
	}
	// Negative: without git's answer the plain cache list applies and the note says so.
	u, note = withGitIgnores(context.Background(), unit{dir: t.TempDir()})
	if u.ignored != nil || note == "" {
		t.Fatalf("non-repository kept ignores %v, note %q", u.ignored, note)
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
