package needs

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// repositoryNameFixtures writes one repository per analyzer that names a repository after
// its directory, each in a directory named after the case.
func repositoryNameFixtures(t *testing.T) map[string]string {
	t.Helper()
	parent := t.TempDir()
	files := map[string][2]string{
		"rust-engine":  {"Cargo.toml", "[package]\nname = \"engine\"\n\n[dependencies]\nserde = \"1\"\n"},
		"py-service":   {"requirements.txt", "fastapi>=0.111\n"},
		"native-codec": {"meson.build", "dep = dependency('zlib')\n"},
		"go-nomodule":  {"go.mod", "go 1.24\n"},
		"ts-unnamed":   {"package.json", "{\"dependencies\":{\"zod\":\"3\"}}"},
	}
	repos := make(map[string]string, len(files))
	for name, file := range files {
		repo := filepath.Join(parent, name)
		makeCheckout(t, repo)
		writeRepoFile(t, filepath.Join(repo, file[0]), file[1])
		repos[name] = repo
	}
	return repos
}

// The default --path=. names a repository after its directory, as an absolute path does,
// in the scan row (.needs.yaml and scan output). Without the fix the Rust, Python and
// native rows, and the Go and Node fallbacks, were named ".".
func TestRepositoryNameFromRelativePath_3D(t *testing.T) {
	registry := acmeRegistry(t)
	for name, repo := range repositoryNameFixtures(t) {
		t.Run(name, func(t *testing.T) {
			// Positive: "." names the directory.
			t.Chdir(repo)
			if got := scanRowWith(t, registry, ".").Repository; got != name {
				t.Errorf("ScanRepo(.) Repository = %q, want %q", got, name)
			}
			// Negative: the absolute spelling (the control) gives the same name.
			if got := scanRowWith(t, registry, repo).Repository; got != name {
				t.Errorf("ScanRepo(%s) Repository = %q, want %q", repo, got, name)
			}
			// Boundary: a trailing separator and a path that leaves and re-enters the
			// directory name it too.
			for _, spelling := range []string{"." + string(filepath.Separator), filepath.Join("..", name)} {
				if got := scanRowWith(t, registry, spelling).Repository; got != name {
					t.Errorf("ScanRepo(%s) Repository = %q, want %q", spelling, got, name)
				}
			}
		})
	}
}

// The epic of a repository scanned through "." is titled after the directory, a manifest
// name still wins over the directory, and a root that is no project keeps its name.
func TestRepositoryNamePrecedenceAndEpic_3D(t *testing.T) {
	registry := acmeRegistry(t)
	contract, err := filepath.Abs(acmeTargets()["go"].Contract)
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()

	// Positive: the epic generated from "." carries the directory name in every title.
	rust := filepath.Join(parent, "rust-engine")
	writeRepoFile(t, filepath.Join(rust, "Cargo.toml"), "[dependencies]\nash = \"0.38\"\n")
	t.Chdir(rust)
	epic, err := GeneratePreMigrationEpic(context.Background(), ".", FrameworkSource{Contract: contract, Module: acmeKit}, registry)
	if err != nil {
		t.Fatalf("GeneratePreMigrationEpic(.) error = %v", err)
	}
	if epic.RepoName != "rust-engine" || !strings.HasSuffix(epic.ParentEpic.Title, ": rust-engine") ||
		!strings.HasSuffix(epic.ChildIssues[0].Title, ": rust-engine") {
		t.Errorf("epic = name %q title %q first task %q, want rust-engine throughout", epic.RepoName,
			epic.ParentEpic.Title, epic.ChildIssues[0].Title)
	}

	// Negative: a package.json name and a go.mod module path are not replaced by the
	// directory name.
	named := filepath.Join(parent, "web")
	writeRepoFile(t, filepath.Join(named, "package.json"), "{\"name\":\"@acme/web\"}")
	module := filepath.Join(parent, "svc")
	writeRepoFile(t, filepath.Join(module, "go.mod"), "module example.com/acme/svc\ngo 1.24\n")
	for dir, want := range map[string]string{named: "@acme/web", module: "example.com/acme/svc"} {
		t.Chdir(dir)
		if got := scanRowWith(t, registry, ".").Repository; got != want {
			t.Errorf("ScanRepo(.) in %s Repository = %q, want %q", dir, got, want)
		}
	}

	// Boundary: a checkout whose root is no project, scanned through ".", is named after
	// its directory by the sub-project fallback.
	outer := filepath.Join(parent, "outer")
	writeRepoFile(t, filepath.Join(outer, "core", "meson.build"), "dep = dependency('zlib')\n")
	makeCheckout(t, outer)
	t.Chdir(outer)
	if got := scanRowWith(t, registry, ".").Repository; got != "outer" {
		t.Errorf("ScanRepo(.) of a non-project root Repository = %q, want outer", got)
	}
}

// A manifest that names its project "unknown" leaves the epic to fall back to the
// directory. Scanned through ".", that fallback titled the epic "." before it used the
// shared directory-name helper.
func TestEpicRepositoryFallbackNamesDirectory_3D(t *testing.T) {
	registry := acmeRegistry(t)
	contract, err := filepath.Abs(acmeTargets()["go"].Contract)
	if err != nil {
		t.Fatal(err)
	}
	source := FrameworkSource{Contract: contract, Module: acmeKit}
	parent := t.TempDir()
	epicFrom := func(t *testing.T, dir, spelling string) *PreMigrationEpic {
		t.Helper()
		t.Chdir(dir)
		epic, err := GeneratePreMigrationEpic(context.Background(), spelling, source, registry)
		if err != nil {
			t.Fatalf("GeneratePreMigrationEpic(%s) in %s error = %v", spelling, dir, err)
		}
		return epic
	}
	fixtures := map[string][2]string{
		"go-unknown": {"go.mod", "module unknown\ngo 1.24\n"},
		"ts-unknown": {"package.json", "{\"name\":\"unknown\",\"dependencies\":{\"zod\":\"3\"}}"},
	}
	for name, file := range fixtures {
		t.Run(name, func(t *testing.T) {
			repo := filepath.Join(parent, name)
			writeRepoFile(t, filepath.Join(repo, file[0]), file[1])
			// Positive: "." names the epic after the directory; boundary: a trailing
			// separator and a path that leaves and re-enters the directory do too.
			spellings := []string{".", "." + string(filepath.Separator), filepath.Join("..", name)}
			for _, spelling := range spellings {
				epic := epicFrom(t, repo, spelling)
				if epic.RepoName != name || !strings.HasSuffix(epic.ParentEpic.Title, ": "+name) {
					t.Errorf("epic from %q = name %q title %q, want %s", spelling, epic.RepoName,
						epic.ParentEpic.Title, name)
				}
			}
		})
	}

	// Negative: a module path is kept, with only the github.com/ prefix trimmed.
	named := filepath.Join(parent, "svc")
	writeRepoFile(t, filepath.Join(named, "go.mod"), "module github.com/acme/unknown\ngo 1.24\n")
	if got := epicFrom(t, named, ".").RepoName; got != "acme/unknown" {
		t.Errorf("epic of module github.com/acme/unknown RepoName = %q, want acme/unknown", got)
	}
}
