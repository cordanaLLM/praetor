package util

import (
	"path/filepath"
	"strings"
)

// maxMarkerMatches bounds how many expansions of one glob marker are inspected (HISS-02).
// A repository with more matching files than this is already matched by the first of them.
const maxMarkerMatches = 256

// MarkerExists reports whether a repository carries one detection marker.
//
// Most markers are a fixed path: "go.mod" either exists or does not. Some cannot be written
// that way, because what identifies the repository is a file whose name nobody fixed. An OS
// image forge is known by holding some Packer template under packer/, not by holding any
// particular one, so the marker has to be "packer/*.pkr.hcl".
//
// A glob matches only regular files, which is what makes the negative cases work: an empty
// packer/ directory, a packer/ holding only a README, or a directory that happens to be named
// x.pkr.hcl are all non-matches rather than accidental detections. A malformed pattern matches
// nothing rather than erroring, because a marker table is a description of the world, not user
// input to validate.
func MarkerExists(repoPath, marker string) bool {
	native := filepath.FromSlash(marker)
	if !strings.ContainsAny(native, "*?[") {
		return PathExists(filepath.Join(repoPath, native))
	}
	matches, err := filepath.Glob(filepath.Join(repoPath, native))
	if err != nil {
		return false
	}
	for i := 0; i < len(matches) && i < maxMarkerMatches; i++ {
		if FileExists(matches[i]) {
			return true
		}
	}
	return false
}

// maxMarkerList bounds how many markers one AnyMarkerExists call inspects (HISS-02). Every
// list in the tree is a handful of entries; a longer one is a table to split, not to scan.
const maxMarkerList = 32

// AnyMarkerExists reports whether a repository carries at least one of markers, each matched
// as MarkerExists matches it. An empty list matches nothing. It is the one "any of these
// markers" check: the classification table (internal/classify) and a flavor's toolchain
// applicability (internal/flavor) both call it.
func AnyMarkerExists(repoPath string, markers []string) bool {
	for i := 0; i < len(markers) && i < maxMarkerList; i++ {
		if MarkerExists(repoPath, markers[i]) {
			return true
		}
	}
	return false
}
