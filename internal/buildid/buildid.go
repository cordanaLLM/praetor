// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package buildid works out which praetor build a binary is. It is the one implementation
// every surface reads (#666): standardsctl, standards-mcp and standards-lsp each declare
// `var version = ""` in package main, which the release config (.goreleaser.yaml) writes with
// -X main.version, and resolve their identity through Running. The dogfood reports and the
// workstation engine-build check read the VCS stamp through Stamp, and build commits shown in
// output are shortened through Short, so no two surfaces report a different build identity for
// one binary. Worktree heads keep their own 8-character form (worktree.go shortHead).
package buildid

import (
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/cordanaLLM/praetor/internal/semver"
)

// ShortRevisionLen is how many characters of a commit identify it in output.
const ShortRevisionLen = 12

// maxBuildSettings bounds the scan of the build settings Go embeds (HISS-02).
const maxBuildSettings = 256

// Identity is what a binary can prove about which praetor it is. At most one of Release and
// Revision is set; Unknown says why neither is.
type Identity struct {
	// Release is reported verbatim: a version injected with -X main.version, or the tag a
	// `go install module@vX.Y.Z` build records as its module version.
	Release string
	// Revision is a commit shortened to ShortRevisionLen, from Go's VCS stamp or from the
	// pseudo-version a `go install module@<commit>` build records.
	Revision string
	// Modified marks a VCS-stamped build of a tree with uncommitted changes.
	Modified bool
	// Unknown is why nothing identifies the build.
	Unknown string
}

// Running identifies the running binary from the version its main package was given with
// -X main.version (empty for any build but a release) and the build information the Go
// runtime embeds.
func Running(injected string) Identity {
	return Identify(injected, RunningInfo())
}

// RunningInfo is the build information the Go runtime embeds in the running binary, or nil
// for a binary built without module support. Every reader of the running build starts here.
func RunningInfo() *debug.BuildInfo {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	return info
}

// Identify resolves what a build can prove, strongest source first: an injected release
// version, Go's VCS stamp, then the module version Go records in the build information.
//
// The module version is the only identity a `go install module@version` build carries. That
// build compiles from the module cache, where Go records no vcs.* settings, but Main.Version
// still names the tag or the pseudo-version of the commit (#642). A build with none of the
// three is unknown with a reason, never a plausible-looking version.
func Identify(injected string, info *debug.BuildInfo) Identity {
	if trimmed := strings.TrimSpace(injected); trimmed != "" {
		return Identity{Release: trimmed}
	}
	if info == nil {
		return Identity{Unknown: "no build information"}
	}
	if revision, modified := Stamp(info); Short(revision) != "" {
		return FromStamp(revision, modified)
	}
	return moduleIdentity(info.Main.Version)
}

// Stamp extracts the full revision and the dirty flag Go embeds at build time. Both are zero
// for a build that carries no VCS stamp (go run, go test, a module-cache build).
func Stamp(info *debug.BuildInfo) (revision string, modified bool) {
	if info == nil {
		return "", false
	}
	for i := 0; i < len(info.Settings) && i < maxBuildSettings; i++ {
		switch info.Settings[i].Key {
		case "vcs.revision":
			revision = info.Settings[i].Value
		case "vcs.modified":
			modified = info.Settings[i].Value == "true"
		}
	}
	return revision, modified
}

// FromStamp is the identity a VCS stamp proves: its revision, shortened, and the dirty flag.
// An empty revision proves nothing.
func FromStamp(revision string, modified bool) Identity {
	if short := Short(revision); short != "" {
		return Identity{Revision: short, Modified: modified}
	}
	return Identity{Unknown: "no VCS stamp"}
}

// Short cuts a commit to the ShortRevisionLen characters that identify it in output, after
// trimming surrounding whitespace. A shorter commit is returned whole.
func Short(revision string) string {
	trimmed := strings.TrimSpace(revision)
	if len(trimmed) > ShortRevisionLen {
		return trimmed[:ShortRevisionLen]
	}
	return trimmed
}

// String is the identity every binary reports: the release, the short revision suffixed
// -dirty for a modified tree, or "unknown (<reason>)".
func (id Identity) String() string {
	switch {
	case id.Release != "":
		return id.Release
	case id.Revision != "" && id.Modified:
		return id.Revision + "-dirty"
	case id.Revision != "":
		return id.Revision
	}
	return "unknown (" + id.Unknown + ")"
}

// moduleIdentity reads a module version as Go records it in Main.Version. A pseudo-version
// names its commit in its trailing revision, which is reported as a VCS stamp's revision is,
// so one commit reads the same however it was built. Any other valid SemVer version is a tag,
// a release. Go records "(devel)" for a checkout build without a VCS stamp; that and an empty
// version identify nothing.
func moduleIdentity(moduleVersion string) Identity {
	moduleVersion = strings.TrimSpace(moduleVersion)
	if revision, ok := pseudoVersionRevision(moduleVersion); ok {
		return Identity{Revision: Short(revision)}
	}
	if _, ok := semver.Parse(moduleVersion); ok {
		return Identity{Release: moduleVersion}
	}
	return Identity{Unknown: "untagged build, no VCS stamp"}
}

// pseudoVersionPattern is Go's pseudo-version grammar (pseudoVersionRE in
// golang.org/x/mod/module) with the revision captured. It covers the three forms Go writes,
// vX.0.0-yyyymmddhhmmss-rev, vX.Y.Z-pre.0.yyyymmddhhmmss-rev and
// vX.Y.(Z+1)-0.yyyymmddhhmmss-rev, each with optional build metadata such as +incompatible.
var pseudoVersionPattern = regexp.MustCompile(
	`^v[0-9]+\.(?:0\.0-|\d+\.\d+-(?:[^+]*\.)?0\.)\d{14}-([A-Za-z0-9]+)(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// pseudoVersionRevision returns the revision a Go pseudo-version ends with. Like Go's own
// module.IsPseudoVersion, it requires the version to be valid SemVer as well as to match the
// pseudo-version shape.
func pseudoVersionRevision(moduleVersion string) (string, bool) {
	if _, ok := semver.Parse(moduleVersion); !ok {
		return "", false
	}
	match := pseudoVersionPattern.FindStringSubmatch(moduleVersion)
	if match == nil {
		return "", false
	}
	return match[1], true
}
