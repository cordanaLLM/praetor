package needs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
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

// identityCheckout creates a real git checkout at dir holding a Python project, with the
// given origin remote ("" adds none) and .standards.yaml body ("" writes none), and returns
// the hermetic git runner used to build it.
func identityCheckout(t *testing.T, dir, remote, manifest string) func(string, ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	env := testsupport.HermeticGitEnv(t)
	run := func(in string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "init.defaultBranch=main", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir, cmd.Env = in, env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	writeRepoFile(t, filepath.Join(dir, "requirements.txt"), "fastapi>=0.111\n")
	if manifest != "" {
		writeRepoFile(t, filepath.Join(dir, ".standards.yaml"), manifest)
	}
	run(dir, "init", "-q")
	if remote != "" {
		run(dir, "remote", "add", "origin", remote)
	}
	return run
}

// Positive (#606): a non-Go checkout is named after the repository its .standards.yaml or
// origin remote names, not after its directory, in the scan row, .needs.yaml and every epic
// title, so `needs scan --check` passes for one commit under two directory names.
func TestRepositoryNameFollowsIdentity_Positive(t *testing.T) {
	registry := acmeRegistry(t)
	parent := t.TempDir()
	checkout := filepath.Join(parent, "kernel-forge-checkout")
	identityCheckout(t, checkout, "https://github.com/acme/other.git", "repository:\n  owner: acme\n  name: nucleus\n")
	row := scanRowWith(t, registry, checkout)
	if row.Repository != "nucleus" || row.RepositoryFallback != "" || FormatRepositoryFallback(row) != "" {
		t.Fatalf("row = %q (fallback %q), want nucleus from .standards.yaml", row.Repository, row.RepositoryFallback)
	}
	epic, err := GeneratePreMigrationEpic(t.Context(), checkout, acmeSource(""), registry)
	if err != nil {
		t.Fatalf("GeneratePreMigrationEpic error = %v", err)
	}
	titles := []string{epic.ParentEpic.Title}
	for _, child := range epic.ChildIssues {
		titles = append(titles, child.Title)
	}
	for _, omitted := range epic.OmittedTasks {
		titles = append(titles, omitted.Title)
	}
	for _, title := range titles {
		if !strings.HasSuffix(title, ": nucleus") {
			t.Errorf("epic title %q does not name nucleus", title)
		}
	}
	if !strings.HasPrefix(epic.ChecklistMarkdown, "# Pre-Migration Epic: nucleus\n") || strings.Contains(epic.ChecklistMarkdown, "Repository Name") {
		t.Errorf("checklist header:\n%s", epic.ChecklistMarkdown)
	}
	if err := WriteNeedsManifest(checkout, row); err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(parent, "nucleus")
	if err := os.Rename(checkout, renamed); err != nil {
		t.Fatal(err)
	}
	if drift, err := CheckNeedsManifest(t.Context(), renamed, scanRowWith(t, registry, renamed)); err != nil || drift != "" {
		t.Errorf("check under another directory name: drift %q, err %v", drift, err)
	}

	// The origin remote names the repository when .standards.yaml does not.
	remoteOnly := filepath.Join(parent, "ci-workspace")
	identityCheckout(t, remoteOnly, "git@github.com:acme/nucleus.git", "")
	if row := scanRowWith(t, registry, remoteOnly); row.Repository != "nucleus" || row.RepositoryFallback != "" {
		t.Errorf("remote-only row = %q (fallback %q), want nucleus", row.Repository, row.RepositoryFallback)
	}
}

// Negative (#606): a checkout neither .standards.yaml nor an origin remote names keeps its
// directory's name, and the scan, the report header and the epic say so.
func TestRepositoryNameFallbackIsNamed_Negative(t *testing.T) {
	registry := acmeRegistry(t)
	checkout := filepath.Join(t.TempDir(), "py-service")
	identityCheckout(t, checkout, "", "repository:\n  owner: acme\n")
	row := scanRowWith(t, registry, checkout)
	if row.Repository != "py-service" || row.RepositoryFallback != config.ErrRepositoryNameUnknown.Error() {
		t.Fatalf("row = %q (fallback %q), want py-service with the unknown-name reason", row.Repository, row.RepositoryFallback)
	}
	wantNote := "`py-service` is the repository directory's name, which differs between clones, worktrees and CI workspaces (" +
		config.ErrRepositoryNameUnknown.Error() + ")"
	if got := FormatRepositoryFallback(row); got != "Repository name: "+wantNote+"\n" {
		t.Errorf("FormatRepositoryFallback = %q", got)
	}
	if header := FormatReportHeader(row, &FrameworkIndex{Basis: FrameworkNotConfigured}); !strings.Contains(header, "Repository name: "+wantNote) {
		t.Errorf("report header lacks the fallback note:\n%s", header)
	}
	if err := WriteNeedsManifest(checkout, row); err != nil {
		t.Fatal(err)
	}
	if written, err := os.ReadFile(filepath.Join(checkout, NeedsManifestName)); err != nil || strings.Contains(string(written), "fallback") {
		t.Errorf(".needs.yaml carries the fallback reason (err %v):\n%s", err, written)
	}
	epic, err := GeneratePreMigrationEpic(t.Context(), checkout, FrameworkSource{}, registry)
	if err != nil {
		t.Fatalf("GeneratePreMigrationEpic error = %v", err)
	}
	if !strings.Contains(epic.ChecklistMarkdown, "- **Repository Name**: "+wantNote+"\n") || !strings.HasSuffix(epic.ParentEpic.Title, ": py-service") {
		t.Errorf("epic does not name the directory fallback:\n%s", epic.ChecklistMarkdown)
	}
	// A project manifest's own name still wins over .standards.yaml: a Go module keeps its
	// module path whatever the checkout's directory or repository.name says.
	module := filepath.Join(t.TempDir(), "svc-checkout")
	writeRepoFile(t, filepath.Join(module, "go.mod"), "module example.com/acme/svc\ngo 1.24\n")
	writeRepoFile(t, filepath.Join(module, ".standards.yaml"), "repository:\n  name: nucleus\n")
	if row := scanRowWith(t, registry, module); row.Repository != "example.com/acme/svc" || row.RepositoryFallback != "" {
		t.Errorf("Go module row = %q (fallback %q), want its module path", row.Repository, row.RepositoryFallback)
	}
}

// Boundary (#606): a linked worktree under another directory name and a clone whose origin
// ends in "/." (#407) take the manifest's name; without one, the "/." clone keeps its
// directory's name with the reason; and a directory outside every checkout is never named
// after the origin remote of the checkout around it.
func TestRepositoryNameIdentity_Boundary(t *testing.T) {
	registry := acmeRegistry(t)
	parent := t.TempDir()
	primary := filepath.Join(parent, "nucleus")
	run := identityCheckout(t, primary, "https://github.com/acme/nucleus.git", "repository:\n  name: nucleus\n")
	run(primary, "add", "requirements.txt", ".standards.yaml")
	run(primary, "commit", "-q", "-m", "init")
	worktree := filepath.Join(parent, "review-4711")
	run(primary, "worktree", "add", "-q", "-b", "review", worktree)
	if row := scanRowWith(t, registry, worktree); row.Repository != "nucleus" || row.RepositoryFallback != "" {
		t.Errorf("linked worktree row = %q (fallback %q), want nucleus", row.Repository, row.RepositoryFallback)
	}

	dotNamed := filepath.Join(parent, "dot-clone")
	identityCheckout(t, dotNamed, "https://github.com/acme/.", "repository:\n  name: nucleus\n")
	if row := scanRowWith(t, registry, dotNamed); row.Repository != "nucleus" {
		t.Errorf("clone with a /. origin = %q, want the manifest's nucleus", row.Repository)
	}
	dotOnly := filepath.Join(parent, "dot-only")
	identityCheckout(t, dotOnly, "https://github.com/acme/.", "")
	if row := scanRowWith(t, registry, dotOnly); row.Repository != "dot-only" || !strings.HasPrefix(row.RepositoryFallback, config.ErrRepositoryNameInvalid.Error()) {
		t.Errorf("clone with only a /. origin = %q (fallback %q), want dot-only with the invalid-name reason", row.Repository, row.RepositoryFallback)
	}

	// services/api scanned on its own is no checkout, so the remote of the checkout around it
	// names nothing, while its own .standards.yaml does.
	nested := filepath.Join(primary, "services", "api")
	writeRepoFile(t, filepath.Join(nested, "requirements.txt"), "click\n")
	if row := scanRowWith(t, registry, nested); row.Repository != "api" || row.RepositoryFallback != config.ErrRepositoryNameUnknown.Error() {
		t.Errorf("nested directory row = %q (fallback %q), want api named after its directory", row.Repository, row.RepositoryFallback)
	}
	writeRepoFile(t, filepath.Join(nested, ".standards.yaml"), "repository:\n  name: nucleus-api\n")
	if row := scanRowWith(t, registry, nested); row.Repository != "nucleus-api" || row.RepositoryFallback != "" {
		t.Errorf("nested directory with a manifest = %q (fallback %q), want nucleus-api", row.Repository, row.RepositoryFallback)
	}
}
