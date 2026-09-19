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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// taggedDigest matches "<repository>:<tag>@sha256:<64 hex>", the tag-plus-digest form
// HISS-11 asks for. A bare "<repository>@sha256:..." carries no tag to disagree about and
// is deliberately not matched.
var taggedDigest = regexp.MustCompile(`([A-Za-z0-9][A-Za-z0-9._/-]*):([A-Za-z0-9][A-Za-z0-9._-]*)@sha256:([0-9a-f]{64})`)

// maxScannedFiles bounds the walk so a pathological tree cannot make this test unbounded
// (HISS-02). The repository holds roughly a thousand tracked files.
const maxScannedFiles = 8192

// pinnedFileExtensions are the formats that carry image references. Markdown is left out
// on purpose: prose quotes digests as examples, and an example is not a pin.
var pinnedFileNames = map[string]bool{".go": true, ".json": true, ".yml": true, ".yaml": true, ".tmpl": true}

// skippedDirectories are trees whose contents are not this repository's own pins.
var skippedDirectories = map[string]bool{".git": true, ".workingdir": true, "node_modules": true, "testdata": true, "vendor": true}

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

// scanRepositoryPins walks the repository and collects every tagged-digest reference.
func scanRepositoryPins(root string) ([]imagePin, error) {
	var pins []imagePin
	seen := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skippedDirectories[entry.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if seen++; seen > maxScannedFiles {
			return fmt.Errorf("image pin scan passed %d files; raise maxScannedFiles or narrow the walk", maxScannedFiles)
		}
		if !pinnedFileNames[filepath.Ext(path)] && !strings.HasPrefix(entry.Name(), "Dockerfile") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		pins = append(pins, collectImagePins(filepath.ToSlash(rel), string(content))...)
		return nil
	})
	return pins, err
}

func TestRepositoryPinsOneDigestPerImageTag(t *testing.T) {
	pins, err := scanRepositoryPins(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("scan repository image pins: %v", err)
	}
	// A gate that found nothing to compare is not a gate that passed (HISS-21). The
	// repository pins at least the distroless runtime and the Go builder, each from
	// several files, so a scan that stops matching must fail rather than go quiet.
	copies := make(map[string]map[string]bool)
	for _, pin := range pins {
		if copies[pin.image] == nil {
			copies[pin.image] = make(map[string]bool)
		}
		copies[pin.image][pin.file] = true
	}
	shared := 0
	for _, files := range copies {
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

// Boundary for the walk: an absent root is an error rather than an empty, passing scan.
func TestScanRepositoryPinsRejectsAnAbsentRoot(t *testing.T) {
	if _, err := scanRepositoryPins(filepath.Join(t.TempDir(), "absent")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("scanning an absent root returned %v, want a not-exist error", err)
	}
}
