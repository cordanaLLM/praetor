// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

// Every container image this repository pins is written down more than once: the
// distroless runtime appears in Dockerfile and templates/go/Dockerfile.distroless.tmpl (the
// body internal/flavor scaffolds); the Go builder in docker/dev/Dockerfile and the
// reviewed-default comment in internal/devcontainer/bootstrap.go (the generated
// .devcontainer files carry it digest-only, which this scan skips). Nothing held the
// copies equal, so a digest refresh that missed one left two digests behind one tag and
// the miss surfaced as an image that behaves differently from the one that was verified.
// HISS-19 names config formats explicitly, and a digest literal repeated across YAML, JSON,
// Dockerfiles and Go is the same defect as a duplicated function.
//
// This is a repository-wide scan rather than a list of file pairs, so a copy added in a
// file nobody thought of is covered too.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

func TestRepositoryPinsOneDigestPerImageTag(t *testing.T) {
	requireGit(t)
	pins, err := ScanRepositoryPins(t.Context(), filepath.Join("..", ".."))
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
	for image, group := range ConflictingPins(pins) {
		t.Errorf("%s is pinned to more than one digest:\n%s", image, describePins(group))
	}
}

// pinCopies maps each image:tag to the set of files that pin it.
func pinCopies(pins []ImagePin) map[string]map[string]bool {
	copies := make(map[string]map[string]bool)
	for _, pin := range pins {
		if copies[pin.Image] == nil {
			copies[pin.Image] = make(map[string]bool)
		}
		copies[pin.Image][pin.File] = true
	}
	return copies
}

// describePins renders a conflict in file order so the failure names the copy to fix.
func describePins(group []ImagePin) string {
	lines := make([]string, 0, len(group))
	for _, pin := range group {
		lines = append(lines, fmt.Sprintf("  %s: sha256:%s", pin.File, pin.Digest))
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
			var pins []ImagePin
			for file, content := range tc.files {
				pins = append(pins, CollectImagePins(file, content)...)
			}
			if len(pins) != tc.wantPins {
				t.Fatalf("collected %d pins, want %d: %v", len(pins), tc.wantPins, pins)
			}
			conflicts := ConflictingPins(pins)
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
	// engine-agenthook-windows-flake-20261028: fixtures init git under fixture bound, not probe bound.
	testsupport.RunFixtureGit(t, root, []string{"init", "-q"})
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

	pins, err := ScanRepositoryPins(t.Context(), fixtureRepo(t, ignoring))
	if err != nil {
		t.Fatalf("scan the fixture with the nested checkout ignored: %v", err)
	}
	if len(pins) != 1 {
		t.Fatalf("an ignored checkout contributed pins: %v", pins)
	}
	if conflicts := ConflictingPins(pins); len(conflicts) != 0 {
		t.Errorf("an ignored checkout at an older digest was reported as a conflict: %v", conflicts)
	}

	tracked, err := ScanRepositoryPins(t.Context(), fixtureRepo(t, tree))
	if err != nil {
		t.Fatalf("scan the fixture without the ignore rule: %v", err)
	}
	if len(tracked) != 3 {
		t.Fatalf("collected %d pins from the unignored tree, want 3: %v", len(tracked), tracked)
	}
	if _, reported := ConflictingPins(tracked)["golang:1.27-alpine"]; !reported {
		t.Error("the scan no longer reports a real two-digest conflict, so the ignored case above proves nothing")
	}
}

// Boundary for the scope listing: an absent root is an error rather than an empty, passing
// scan, and a directory that is no git work tree is likewise refused rather than walked.
func TestScanRepositoryPinsRejectsARootGitCannotList(t *testing.T) {
	requireGit(t)
	if _, err := ScanRepositoryPins(t.Context(), filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("scanning an absent root returned no error")
	}
	if _, err := ScanRepositoryPins(t.Context(), t.TempDir()); err == nil {
		t.Error("scanning a directory that is no git work tree returned no error")
	}
}

// Boundary for the file filter: the formats that carry pins are read, testdata fixtures and
// prose are not.
func TestScannedFileSelectsPinFormatsOutsideTestdata(t *testing.T) {
	for _, rel := range []string{"Dockerfile", "build/package/Dockerfile", ".devcontainer/Dockerfile.praetor", "a.go", "b.yml", "c.yaml", "d.json", "e.tmpl"} {
		if !ScannedFile(rel) {
			t.Errorf("%s carries pins and was skipped", rel)
		}
	}
	for _, rel := range []string{"README.md", "docs/adoption.md", "testdata/Dockerfile", "internal/hiss/testdata/a.go", "LICENSE"} {
		if ScannedFile(rel) {
			t.Errorf("%s is not one of this repository's pins and was scanned", rel)
		}
	}
}

type imageRefSurfaceCase struct {
	name         string
	file         string
	content      string
	wantRefs     []string
	unresolvable bool
}

func runtimeImageRefCases(digest string) []imageRefSurfaceCase {
	return []imageRefSurfaceCase{
		{
			name:     "workflow container services and docker uses",
			file:     ".github/workflows/ci.yml",
			content:  "jobs:\n  test:\n    container: golang:1.27\n    services:\n      redis:\n        image: redis:7\n    steps:\n      - uses: docker://alpine:3.19\n      - uses: docker://alpine:3.19@sha256:" + digest + "\n",
			wantRefs: []string{"golang:1.27", "redis:7", "docker://alpine:3.19"},
		},
		{
			name:     "devcontainer image field",
			file:     ".devcontainer/devcontainer.json",
			content:  "{\n  \"image\": \"mcr.microsoft.com/devcontainers/go:1.27\"\n}\n",
			wantRefs: []string{"mcr.microsoft.com/devcontainers/go:1.27"},
		},
		{
			name:     "source string literals qualified references",
			file:     "runner.go",
			content:  "package p\nconst a = \"ghcr.io/org/runner:1.4.2\"\nconst b = \"localhost:5000/team/worker:v2\"\nconst c = \"ghcr.io/org/runner:1.4.2@sha256:" + digest + "\"\n",
			wantRefs: []string{"ghcr.io/org/runner:1.4.2", "localhost:5000/team/worker:v2"},
		},
		{
			name:    "source string literals plain host:port and URLs never match",
			file:    "net.go",
			content: "package p\nvar (a = \"localhost:8080\"; b = \"127.0.0.1:5432\"; c = \"db.internal:5432\"; d = \"https://ghcr.io/org/runner:1.4.2\"; e = \"redis:7.2\")\n",
		},
	}
}

func TestCollectImageRefFindingsSurfaces_3D(t *testing.T) {
	const digest = "4a7b9f0e1c2d3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b"
	cases := []imageRefSurfaceCase{
		{
			name:     "Dockerfile floating FROM and stage alias ignored",
			file:     "Dockerfile",
			content:  "FROM golang:1.27-alpine AS builder\nFROM scratch\nFROM builder\n",
			wantRefs: []string{"golang:1.27-alpine"},
		},
		{
			name:    "Dockerfile digest pinned FROM",
			file:    "Containerfile",
			content: "FROM golang:1.27-alpine@sha256:" + digest + " AS builder\nFROM builder\nFROM scratch\n",
		},
		{
			name:         "Dockerfile build arg without digest is reported unresolvable",
			file:         "Dockerfile",
			content:      "ARG BASE_IMAGE=alpine:3.19\nFROM ${BASE_IMAGE}\n",
			wantRefs:     []string{"${BASE_IMAGE}"},
			unresolvable: true,
		},
		{
			name:         "Helm printf digest template is reported unresolvable",
			file:         "deploy/helm/templates/deployment.yaml",
			content:      "spec:\n  containers:\n    - image: \"{{ printf \\\"%s@%s\\\" .Values.x.image.repository .Values.x.image.digest }}\"\n",
			wantRefs:     []string{`{{ printf \"%s@%s\" .Values.x.image.repository .Values.x.image.digest }}`},
			unresolvable: true,
		},
		{
			name:     "compose and k8s image field",
			file:     "docker-compose.yml",
			content:  "services:\n  db:\n    image: postgres:16-alpine\n  cache:\n    image: redis:7.2@sha256:" + digest + "\n",
			wantRefs: []string{"postgres:16-alpine"},
		},
	}
	cases = append(cases, runtimeImageRefCases(digest)...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CollectImageRefFindings(tc.file, tc.content)
			if len(got) != len(tc.wantRefs) {
				t.Fatalf("got %d findings (%+v), want %d (%v)", len(got), got, len(tc.wantRefs), tc.wantRefs)
			}
			for i, want := range tc.wantRefs {
				if got[i].Reference != want || got[i].RuleID != ImageRefRuleID {
					t.Errorf("finding[%d] = %+v, want reference %q rule %s", i, got[i], want, ImageRefRuleID)
				}
				if got[i].Fingerprint != ImageRefFingerprint(tc.file, want) {
					t.Errorf("finding[%d] fingerprint = %q, want %q", i, got[i].Fingerprint, ImageRefFingerprint(tc.file, want))
				}
				if tc.unresolvable && !got[i].Unresolvable {
					t.Errorf("finding[%d] expected Unresolvable=true, got %+v", i, got[i])
				}
			}
		})
	}
}

// Boundaries of the file read (HISS-02, HISS-21): a tracked file deleted from the working tree is
// skipped, and a tracked file above maxImageRefFileBytes fails the scan with its name instead of
// being read in full or skipped.
func TestScanImageRefsReadBoundaries(t *testing.T) {
	root := fixtureRepo(t, map[string]string{
		"Dockerfile":     "FROM docker.io/library/alpine:3.20\n",
		"big/Dockerfile": "FROM docker.io/library/alpine:3.20\n",
	})
	testsupport.RunFixtureGit(t, root, []string{"add", "-A"})
	if err := os.Remove(filepath.Join(root, "Dockerfile")); err != nil {
		t.Fatalf("remove tracked Dockerfile: %v", err)
	}
	if err := os.Truncate(filepath.Join(root, "big", "Dockerfile"), maxImageRefFileBytes+1); err != nil {
		t.Fatalf("grow big/Dockerfile: %v", err)
	}
	_, err := ScanImageRefs(t.Context(), root)
	if err == nil || !strings.Contains(err.Error(), "big/Dockerfile") {
		t.Fatalf("oversized tracked file: got error %v, want one naming big/Dockerfile", err)
	}
	if err := os.Remove(filepath.Join(root, "big", "Dockerfile")); err != nil {
		t.Fatalf("remove big/Dockerfile: %v", err)
	}
	findings, err := ScanImageRefs(t.Context(), root)
	if err != nil {
		t.Fatalf("tracked files deleted from the working tree: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("deleted files produced findings: %+v", findings)
	}
	assertImageRefOutsideLink(t, root)
}

func assertImageRefOutsideLink(t *testing.T, root string) {
	t.Helper()
	outside := filepath.Join(t.TempDir(), "compose.yml")
	if err := os.WriteFile(outside, []byte("services:\n  app:\n    image: docker.io/library/redis:latest\n"), 0o600); err != nil {
		t.Fatalf("write outside compose file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "docker-compose.yml")); err != nil {
		t.Skipf("symlink unavailable on this platform, link case not run: %v", err)
	}
	testsupport.RunFixtureGit(t, root, []string{"add", "-A"})
	findings, err := ScanImageRefs(t.Context(), root)
	if err != nil {
		t.Fatalf("link leaving the root failed the scan: %v", err)
	}
	if len(findings) != 1 || !findings[0].Unresolvable || findings[0].Reference != imageRefLinkOutside || findings[0].File != "docker-compose.yml" {
		t.Fatalf("link leaving the root: got %+v, want one unresolvable finding for docker-compose.yml", findings)
	}
}
