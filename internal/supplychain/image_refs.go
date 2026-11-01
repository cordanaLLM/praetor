// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ImageRefRuleID is the HISS rule identifier for unpinned container image references.
const ImageRefRuleID = "HISS-11"

// maxScannedFiles bounds how many repository paths one scan reads (HISS-02).
const maxScannedFiles = 8192

// maxImageRefFileBytes bounds one scanned file (HISS-02): a larger tracked file is refused by
// util.ReadConfinedLimited and fails the scan with its name, never read in full or skipped.
const maxImageRefFileBytes = 16 << 20

// maxFileLines bounds how many lines one file scan processes (HISS-02).
const maxFileLines = 16384

// maxLineLiterals bounds how many string literals one source line scan processes (HISS-02).
const maxLineLiterals = 64

// maxScopeBytes bounds the NUL-separated listing git returns (HISS-02).
const maxScopeBytes = 8 << 20

var (
	// taggedDigest matches "<repository>:<tag>@sha256:<64 hex>", the tag-plus-digest form
	// HISS-11 asks for.
	taggedDigest = regexp.MustCompile(`([A-Za-z0-9][A-Za-z0-9._/-]*):([A-Za-z0-9][A-Za-z0-9._-]*)@sha256:([0-9a-f]{64})`)
)

// pinnedFileNames are the formats that carry image pins compared across files.
var pinnedFileNames = map[string]bool{".go": true, ".json": true, ".yml": true, ".yaml": true, ".tmpl": true}

// ImagePin is one "<image>:<tag>@sha256:<digest>" occurrence and where it was read.
type ImagePin struct {
	File   string
	Image  string
	Digest string
}

// ImageRefFinding is one container image reference that lacks an immutable @sha256: digest pin.
type ImageRefFinding struct {
	File         string
	Line         int
	Reference    string
	RuleID       string
	Fingerprint  string
	Unresolvable bool
}

type numberedLine struct {
	line int
	text string
}

// ImageRefFingerprint returns the line-independent HISS-11 baseline fingerprint for file and reference.
func ImageRefFingerprint(file, reference string) string {
	return util.NormalizeSlashes(file) + ":" + ImageRefRuleID + ":image:" + strings.TrimSpace(reference)
}

// CollectImagePins extracts every tagged-digest reference from one file's content.
func CollectImagePins(file, content string) []ImagePin {
	matches := taggedDigest.FindAllStringSubmatch(content, -1)
	pins := make([]ImagePin, 0, len(matches))
	for i := 0; i < len(matches) && i < maxFileLines; i++ {
		match := matches[i]
		pins = append(pins, ImagePin{
			File:   file,
			Image:  NormalizeImage(match[1]) + ":" + match[2],
			Digest: match[3],
		})
	}
	return pins
}

// NormalizeImage strips the implicit Docker Hub prefixes so "docker.io/library/golang"
// and "golang" compare as one image.
func NormalizeImage(repository string) string {
	prefixes := []string{"docker.io/library/", "docker.io/", "index.docker.io/library/", "index.docker.io/"}
	for i := 0; i < len(prefixes); i++ {
		if strings.HasPrefix(repository, prefixes[i]) {
			return strings.TrimPrefix(repository, prefixes[i])
		}
	}
	return repository
}

// ConflictingPins groups pins by image:tag and returns only the groups carrying more than
// one digest.
func ConflictingPins(pins []ImagePin) map[string][]ImagePin {
	byImage := make(map[string][]ImagePin, len(pins))
	for i := 0; i < len(pins) && i < maxScannedFiles; i++ {
		pin := pins[i]
		byImage[pin.Image] = append(byImage[pin.Image], pin)
	}
	conflicts := make(map[string][]ImagePin)
	for image, group := range byImage {
		digests := make(map[string]bool, len(group))
		for i := 0; i < len(group) && i < maxScannedFiles; i++ {
			digests[group[i].Digest] = true
		}
		if len(digests) > 1 {
			conflicts[image] = group
		}
	}
	return conflicts
}

// ScannedFile reports whether a repository path is a format that carries image pins.
func ScannedFile(rel string) bool {
	slashed := filepath.ToSlash(rel)
	if isTestdataPath(slashed) {
		return false
	}
	base := filepath.Base(slashed)
	return pinnedFileNames[filepath.Ext(base)] || strings.HasPrefix(base, "Dockerfile")
}

// ScanRepositoryPins collects every tagged-digest reference the repository owns.
func ScanRepositoryPins(ctx context.Context, root string) ([]ImagePin, error) {
	files, err := RepositoryFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	var pins []ImagePin
	err = iterateScannedFiles(root, files, ScannedFile, func(rel string, content []byte, readErr error) error {
		if readErr != nil {
			return readErr
		}
		pins = append(pins, CollectImagePins(filepath.ToSlash(rel), string(content))...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return pins, nil
}

// isTestdataPath reports whether slashed is inside a testdata directory.
func isTestdataPath(slashed string) bool {
	return strings.HasPrefix(slashed, "testdata/") || strings.Contains(slashed, "/testdata/")
}

// ScannedImageRefFile reports whether a repository path is scanned for HISS-11 image references.
func ScannedImageRefFile(rel string) bool {
	slashed := filepath.ToSlash(rel)
	if isTestdataPath(slashed) || strings.HasSuffix(slashed, "_test.go") {
		return false
	}
	return isDockerfile(slashed) || isYAMLFile(slashed) || isDevcontainerJSON(slashed) || isSourceCodeFile(slashed)
}

// ScanImageRefs scans root for container image references lacking an @sha256: digest pin.
func ScanImageRefs(ctx context.Context, root string) ([]ImageRefFinding, error) {
	files, err := imageRefCandidateFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	var findings []ImageRefFinding
	err = iterateScannedFiles(root, files, ScannedImageRefFile, func(rel string, content []byte, readErr error) error {
		if errors.Is(readErr, util.ErrPathEscapesRoot) {
			// A link that leaves root is never followed (confinement); it is reported as not
			// verified rather than failing the scan or passing silently.
			findings = append(findings, newImageRefFinding(filepath.ToSlash(rel), 0, imageRefLinkOutside, true))
			return nil
		}
		if readErr != nil {
			return readErr
		}
		findings = append(findings, CollectImageRefFindings(filepath.ToSlash(rel), string(content))...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return findings, nil
}

// CollectImageRefFindings extracts unpinned container image references from one file's content.
func CollectImageRefFindings(file, content string) []ImageRefFinding {
	slashed := util.NormalizeSlashes(file)
	switch {
	case isDockerfile(slashed):
		return collectDockerfileFindings(slashed, content)
	case isYAMLFile(slashed):
		return collectYAMLFindings(slashed, content)
	case isDevcontainerJSON(slashed):
		return collectDevcontainerFindings(slashed, content)
	case isSourceCodeFile(slashed):
		return collectSourceLiteralFindings(slashed, content)
	default:
		return nil
	}
}
