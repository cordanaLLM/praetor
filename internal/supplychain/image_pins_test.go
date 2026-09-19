package supplychain

// Every container image this repository pins is written down more than once: the
// distroless runtime appears in Dockerfile, build/package/Dockerfile,
// templates/go/Dockerfile.distroless.tmpl and internal/flavor/scaffold.go; the Go builder
// in docker/dev/Dockerfile, .devcontainer/Dockerfile.praetor, build/package/Dockerfile,
// .devcontainer/devcontainer.json and internal/devcontainer/bootstrap.go. Nothing held the
// copies equal, so a digest refresh that missed one left two digests behind one tag and
// the miss surfaced as an image that behaves differently from the one that was verified.
// HISS-19 names config formats explicitly, and a digest literal repeated across YAML, JSON,
// Dockerfiles and Go is the same defect as a duplicated function.
//
// This is a repository-wide scan rather than a list of file pairs, so a copy added in a
// file nobody thought of is covered too.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// taggedDigest matches "<repository>:<tag>@sha256:<64 hex>", the tag-plus-digest form
// HISS-11 asks for. A bare "<repository>@sha256:..." carries no tag to disagree about and
// is deliberately not matched.
var taggedDigest = regexp.MustCompile(`([A-Za-z0-9][A-Za-z0-9._/-]*):([A-Za-z0-9][A-Za-z0-9._-]*)@sha256:([0-9a-f]{64})`)

// maxScannedFiles bounds how many repository paths one scan reads (HISS-02). Git reports
// roughly 1,800 paths for this tree.
const maxScannedFiles = 8192

// maxScopeBytes bounds the NUL-separated listing git returns (HISS-02).
const maxScopeBytes = 8 << 20

// pinnedFileNames are the formats that carry image references. Markdown is left out on
// purpose: prose quotes digests as examples, and an example is not a pin.
var pinnedFileNames = map[string]bool{".go": true, ".json": true, ".yml": true, ".yaml": true, ".tmpl": true}

// imagePin is one "<image>:<tag>@sha256:<digest>" occurrence and where it was read.
type imagePin struct {
	file   string
	image  string
	digest string
}

// collectImagePins extracts every tagged-digest reference from one file's content. Pure,
// so the conflict report below is replayable from synthetic content and not only from
// whatever the tree happens to hold today (HISS-20).
func collectImagePins(file string, content string) []imagePin {
	matches := taggedDigest.FindAllStringSubmatch(content, -1)
	pins := make([]imagePin, 0, len(matches))
	for _, match := range matches {
		pins = append(pins, imagePin{file: file, image: normalizeImage(match[1]) + ":" + match[2], digest: match[3]})
	}
	return pins
}

// normalizeImage strips the implicit Docker Hub prefixes, so "docker.io/library/golang"
// and "golang" are one image rather than two.
func normalizeImage(repository string) string {
	for _, prefix := range []string{"docker.io/library/", "docker.io/", "index.docker.io/library/", "index.docker.io/"} {
		if strings.HasPrefix(repository, prefix) {
			return strings.TrimPrefix(repository, prefix)
		}
	}
	return repository
}

// conflictingPins groups pins by image:tag and returns only the groups carrying more than
// one digest, which is the defect: one tag, two answers.
func conflictingPins(pins []imagePin) map[string][]imagePin {
	byImage := make(map[string][]imagePin, len(pins))
	for _, pin := range pins {
		byImage[pin.image] = append(byImage[pin.image], pin)
	}
	conflicts := make(map[string][]imagePin)
	for image, group := range byImage {
		digests := make(map[string]bool, len(group))
		for _, pin := range group {
			digests[pin.digest] = true
		}
		if len(digests) > 1 {
			conflicts[image] = group
		}
	}
	return conflicts
}

// repositoryFiles asks git which paths belong to this repository: tracked files plus
// untracked files git is not ignoring.
//
// A raw filesystem walk was wrong twice over. .claude/worktrees/ is gitignored
// (.gitignore:66) and holds one full checkout per agent -- 281,981 files on the maintainer's
// tree -- so the walk blew through maxScannedFiles and made `go test ./...` red in the
// primary checkout while staying green when run from inside one of those worktrees. Raising
// the bound is the worse fix: the sibling checkouts sit at older commits, so
// golang:1.27-alpine is pinned to two different digests across them and the scan then reports
// a CONFLICT for files this repository does not own.
//
// The listing is the seam this repository already uses for exactly that scoping question --
// same argv and the same bounded, configuration-isolated probe as internal/hiss/hiss.go's
// gitVisiblePaths and internal/dedupe/scope.go's gitSourceFiles -- rather than a third
// mechanism that would have to be kept in step with them (HISS-19).
func repositoryFiles(ctx context.Context, root string) ([]string, error) {
	out, err := util.RunGitProbe(ctx, root, maxScopeBytes,
		"ls-files", "--cached", "--others", "--exclude-standard", "--deduplicate", "-z", "--", ".")
	if err != nil {
		return nil, fmt.Errorf("enumerate image pin scope in %s: %w", root, err)
	}
	var files []string
	for _, rel := range strings.Split(string(out.Stdout), "\x00") {
		if rel == "" {
			continue
		}
		if len(files) >= maxScannedFiles {
			return nil, fmt.Errorf("image pin scope passed %d files; narrow the listing", maxScannedFiles)
		}
		files = append(files, rel)
	}
	return files, nil
}

// scannedFile reports whether a repository path is a format that carries image pins.
//
// testdata is dropped for the reason internal/dedupe/scope.go drops it: a fixture corpus is
// deliberately repetitive and the digests in it are inputs to some other rule, not this
// repository's own pins. Everything else the old walk skipped by name -- .git, .workingdir,
// vendor, node_modules -- is already outside the git listing, ignored or never tracked, so no
// second skip list has to be held in step with .gitignore.
func scannedFile(rel string) bool {
	slashed := filepath.ToSlash(rel)
	if strings.HasPrefix(slashed, "testdata/") || strings.Contains(slashed, "/testdata/") {
		return false
	}
	base := filepath.Base(slashed)
	return pinnedFileNames[filepath.Ext(base)] || strings.HasPrefix(base, "Dockerfile")
}

// scanRepositoryPins collects every tagged-digest reference the repository owns.
func scanRepositoryPins(ctx context.Context, root string) ([]imagePin, error) {
	files, err := repositoryFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	var pins []imagePin
	for _, rel := range files {
		if !scannedFile(rel) {
			continue
		}
		content, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if errors.Is(readErr, os.ErrNotExist) {
			continue // A tracked deletion is listed but absent from the working tree.
		}
		if readErr != nil {
			return nil, readErr
		}
		pins = append(pins, collectImagePins(filepath.ToSlash(rel), string(content))...)
	}
	return pins, nil
}

// requireGit skips with a stated reason where git is absent (HISS-21): this scan's scope is a
// git listing, and a gate that cannot run is not a gate that passed.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable, and the image pin scope is a git listing: %v", err)
	}
}

func TestRepositoryPinsOneDigestPerImageTag(t *testing.T) {
	requireGit(t)
	pins, err := scanRepositoryPins(t.Context(), filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("scan repository image pins: %v", err)
	}
	// A gate that found nothing to compare is not a gate that passed (HISS-21). The
	// repository pins at least the distroless runtime and the Go builder, each from
	// several files, so a scan that stops matching must fail rather than go quiet.
	shared := 0
	for _, files := range pinCopies(pins) {
		if len(files) > 1 {
			shared++
		}
	}
	if shared < 2 {
		t.Fatalf("expected at least two images pinned from several files, found %d in %d references: the scan has stopped matching this tree", shared, len(pins))
	}
	for image, group := range conflictingPins(pins) {
		t.Errorf("%s is pinned to more than one digest:\n%s", image, describePins(group))
	}
}

// pinCopies maps each image:tag to the set of files that pin it.
func pinCopies(pins []imagePin) map[string]map[string]bool {
	copies := make(map[string]map[string]bool)
	for _, pin := range pins {
		if copies[pin.image] == nil {
			copies[pin.image] = make(map[string]bool)
		}
		copies[pin.image][pin.file] = true
	}
	return copies
}

// describePins renders a conflict in file order so the failure names the copy to fix.
func describePins(group []imagePin) string {
	lines := make([]string, 0, len(group))
	for _, pin := range group {
		lines = append(lines, fmt.Sprintf("  %s: sha256:%s", pin.file, pin.digest))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// Negative and boundary for the pure half: a disagreement is reported, agreement across
// files is not, an untagged digest is not a pin, and the Docker Hub prefix does not make
// one image into two.
func TestConflictingPinsReportsOnlyDisagreement(t *testing.T) {
	const alpha = "1111111111111111111111111111111111111111111111111111111111111111"
	const beta = "2222222222222222222222222222222222222222222222222222222222222222"
	cases := []struct {
		name          string
		files         map[string]string
		wantPins      int
		wantConflicts []string
	}{
		{
			name:          "one tag, two digests",
			files:         map[string]string{"Dockerfile": "FROM golang:1.27-alpine@sha256:" + alpha, "a.go": `x = "docker.io/library/golang:1.27-alpine@sha256:` + beta + `"`},
			wantPins:      2,
			wantConflicts: []string{"golang:1.27-alpine"},
		},
		{
			name:     "same digest in four files",
			files:    map[string]string{"Dockerfile": "FROM golang:1.27-alpine@sha256:" + alpha, "b.yml": "image: golang:1.27-alpine@sha256:" + alpha, "c.json": `"i": "golang:1.27-alpine@sha256:` + alpha + `"`, "d.tmpl": "FROM golang:1.27-alpine@sha256:" + alpha},
			wantPins: 4,
		},
		{
			name:     "two tags of one repository are two images",
			files:    map[string]string{"Dockerfile": "FROM golang:1.27-alpine@sha256:" + alpha + "\nFROM golang:1.27-trixie@sha256:" + beta},
			wantPins: 2,
		},
		{
			name:     "an untagged digest is not a pin this gate compares",
			files:    map[string]string{"Dockerfile": "FROM redis@sha256:" + alpha, "e.go": `"redis@sha256:` + beta + `"`},
			wantPins: 0,
		},
		{
			name:     "no references at all",
			files:    map[string]string{"Dockerfile": "FROM scratch\n"},
			wantPins: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var pins []imagePin
			for file, content := range tc.files {
				pins = append(pins, collectImagePins(file, content)...)
			}
			if len(pins) != tc.wantPins {
				t.Fatalf("collected %d pins, want %d: %v", len(pins), tc.wantPins, pins)
			}
			conflicts := conflictingPins(pins)
			if len(conflicts) != len(tc.wantConflicts) {
				t.Fatalf("reported %d conflicts, want %d: %v", len(conflicts), len(tc.wantConflicts), conflicts)
			}
			for _, image := range tc.wantConflicts {
				if _, reported := conflicts[image]; !reported {
					t.Errorf("%s disagrees with itself and was not reported", image)
				}
			}
		})
	}
}

// fixtureRepo writes files into a fresh git work tree and returns its root. Nothing is
// committed: "--others --exclude-standard" lists untracked files that are not ignored, which
// is the half of the scope the gitignored-checkout case turns on.
func fixtureRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	requireGit(t)
	root := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("create %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	if _, err := util.RunGitProbe(t.Context(), root, maxScopeBytes, "init", "-q"); err != nil {
		t.Fatalf("git init the fixture repository: %v", err)
	}
	return root
}

// Both directions of the scope rule this gate turns on (HISS-20): a gitignored checkout
// nested in the tree contributes no pins, and the identical tree without the ignore rule does
// contribute them and is reported as the conflict it is. Without the second half the first
// would pass just as well against a scan that had quietly stopped reading files.
func TestScanRepositoryPinsSkipsGitignoredCheckouts(t *testing.T) {
	const own = "1111111111111111111111111111111111111111111111111111111111111111"
	const stale = "2222222222222222222222222222222222222222222222222222222222222222"
	tree := map[string]string{
		"Dockerfile":                      "FROM golang:1.27-alpine@sha256:" + own + "\n",
		"nested/checkout/Dockerfile":      "FROM golang:1.27-alpine@sha256:" + stale + "\n",
		"nested/checkout/internal/x.yaml": "image: golang:1.27-alpine@sha256:" + stale + "\n",
	}
	ignoring := map[string]string{".gitignore": "/nested/\n"}
	for rel, content := range tree {
		ignoring[rel] = content
	}

	pins, err := scanRepositoryPins(t.Context(), fixtureRepo(t, ignoring))
	if err != nil {
		t.Fatalf("scan the fixture with the nested checkout ignored: %v", err)
	}
	if len(pins) != 1 {
		t.Fatalf("an ignored checkout contributed pins: %v", pins)
	}
	if conflicts := conflictingPins(pins); len(conflicts) != 0 {
		t.Errorf("an ignored checkout at an older digest was reported as a conflict: %v", conflicts)
	}

	tracked, err := scanRepositoryPins(t.Context(), fixtureRepo(t, tree))
	if err != nil {
		t.Fatalf("scan the fixture without the ignore rule: %v", err)
	}
	if len(tracked) != 3 {
		t.Fatalf("collected %d pins from the unignored tree, want 3: %v", len(tracked), tracked)
	}
	if _, reported := conflictingPins(tracked)["golang:1.27-alpine"]; !reported {
		t.Error("the scan no longer reports a real two-digest conflict, so the ignored case above proves nothing")
	}
}

// Boundary for the scope listing: an absent root is an error rather than an empty, passing
// scan, and a directory that is no git work tree is likewise refused rather than walked.
func TestScanRepositoryPinsRejectsARootGitCannotList(t *testing.T) {
	requireGit(t)
	if _, err := scanRepositoryPins(t.Context(), filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("scanning an absent root returned no error")
	}
	if _, err := scanRepositoryPins(t.Context(), t.TempDir()); err == nil {
		t.Error("scanning a directory that is no git work tree returned no error")
	}
}

// Boundary for the file filter: the formats that carry pins are read, testdata fixtures and
// prose are not.
func TestScannedFileSelectsPinFormatsOutsideTestdata(t *testing.T) {
	for _, rel := range []string{"Dockerfile", "build/package/Dockerfile", ".devcontainer/Dockerfile.praetor", "a.go", "b.yml", "c.yaml", "d.json", "e.tmpl"} {
		if !scannedFile(rel) {
			t.Errorf("%s carries pins and was skipped", rel)
		}
	}
	for _, rel := range []string{"README.md", "docs/adoption.md", "testdata/Dockerfile", "internal/hiss/testdata/a.go", "LICENSE"} {
		if scannedFile(rel) {
			t.Errorf("%s is not one of this repository's pins and was scanned", rel)
		}
	}
}
