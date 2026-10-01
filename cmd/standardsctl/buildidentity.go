// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/cordanaLLM/praetor/internal/semver"
)

// buildIdentity is what a binary can prove about which praetor it is. At most one of release
// and revision is set; unknown says why neither is.
type buildIdentity struct {
	// release is reported and pinned verbatim: a version injected with -X main.version, or
	// the tag a `go install module@vX.Y.Z` build records as its module version.
	release string
	// revision is a commit shortened to shortRevisionLen, from Go's VCS stamp or from the
	// pseudo-version a `go install module@<commit>` build records.
	revision string
	// modified marks a VCS-stamped build of a tree with uncommitted changes.
	modified bool
	// unknown is why nothing identifies the build.
	unknown string
}

// runningBuildIdentity identifies the running binary from its injected version and the build
// information the Go runtime embeds.
func runningBuildIdentity() buildIdentity {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		info = nil
	}
	return identifyBuild(version, info)
}

// identifyBuild resolves what a build can prove, strongest source first: an injected release
// version, Go's VCS stamp, then the module version Go records in the build information.
//
// The module version is the only identity a `go install module@version` build carries. That
// build compiles from the module cache, where Go records no vcs.* settings, but Main.Version
// still names the tag or the pseudo-version of the commit (#642). A build with none of the
// three is "unknown" with a reason, never a plausible-looking version.
func identifyBuild(injected string, info *debug.BuildInfo) buildIdentity {
	if trimmed := strings.TrimSpace(injected); trimmed != "" {
		return buildIdentity{release: trimmed}
	}
	if info == nil {
		return buildIdentity{unknown: "no build information"}
	}
	if revision, modified := vcsStamp(info); revision != "" {
		return buildIdentity{revision: revision, modified: modified}
	}
	return moduleIdentity(info.Main.Version)
}

// moduleIdentity reads a module version as Go records it in Main.Version. A pseudo-version
// names its commit in its trailing revision, which is reported as a VCS stamp's revision is,
// so one commit reads the same however it was built. Any other valid SemVer version is a tag,
// a release. Go records "(devel)" for a checkout build without a VCS stamp; that and an empty
// version identify nothing.
func moduleIdentity(moduleVersion string) buildIdentity {
	moduleVersion = strings.TrimSpace(moduleVersion)
	if revision, ok := pseudoVersionRevision(moduleVersion); ok {
		return buildIdentity{revision: shortSHA(revision)}
	}
	if _, ok := semver.Parse(moduleVersion); ok {
		return buildIdentity{release: moduleVersion}
	}
	return buildIdentity{unknown: "untagged build, no VCS stamp"}
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

// display is the identity `praetorctl version` prints.
func (id buildIdentity) display() string {
	switch {
	case id.release != "":
		return id.release
	case id.revision != "" && id.modified:
		return id.revision + "-dirty"
	case id.revision != "":
		return id.revision
	}
	return "unknown (" + id.unknown + ")"
}

// lockPin is the .standards.lock pinned_version init writes for the identity. ok is false when
// the build identifies nothing and the pin is the zero version.
func (id buildIdentity) lockPin() (pinned string, ok bool) {
	switch {
	case id.release != "":
		return id.release, true
	case id.revision != "":
		return formatDevLockVersion(id.revision, id.modified), true
	}
	return unidentifiedLockVersion, false
}
