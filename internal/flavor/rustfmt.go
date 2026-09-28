// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
)

// defaultRustEdition is the edition Cargo gives a crate whose manifest declares none, and the
// one rustfmt formats under when neither its command line nor rustfmt.toml names one.
const defaultRustEdition = "2015"

// maxCargoMembers bounds the workspace members whose manifests are read (HISS-02).
const maxCargoMembers = 256

// maxMemberMatches bounds the paths one members pattern may match, files and excluded
// directories included, before its members count as unknown (HISS-02).
const maxMemberMatches = 16 * maxCargoMembers

// rustEdition matches an edition as Cargo spells it, a four-digit year such as "2021". Only such
// a value is written into rustfmt.toml; any other is left to Cargo, which rejects it.
var rustEdition = regexp.MustCompile(`^[0-9]{4}$`)

// priorRustfmtDigests are the digests (util.CanonicalTextDigest) of every rustfmt.toml earlier
// releases scaffolded, keyed to what produced them. Both named one edition whatever the crates
// declared (#567), so an unedited copy is refreshed to the rendering this release derives from
// the workspace's crates (TemplateItem.Prior). testdata/rustfmt-prior holds each text
// (TestRustfmtPriorTextsAreEarlierRenderings).
var priorRustfmtDigests = map[string]string{
	"9349046a30c9737d5a2479b504989126faf87547ad1e4087863c800999927952": "edition 2021 for every crate (513804b2 to eed57331)",
	"97d4aa4c4ca05f55863c14f7efdcef46aaa4f7cc23a0874935f7fca78b300e99": "edition 2024 for every crate (eed57331 to #567)",
}

// rustfmtFacts resolves the edition the scaffolded rustfmt.toml declares (rustEditionOf). cargo
// fmt passes each crate's edition to rustfmt, and rustfmt run directly reads it from
// rustfmt.toml, so a scaffold naming any other edition makes the two formatters disagree. When
// the crates share no edition, or one of them cannot be read, no rendering agrees with cargo fmt
// on every crate: the body is withheld, and an existing file is kept (scaffoldTemplate).
func rustfmtFacts(ctx context.Context, repoPath string) (templates.Context, string) {
	edition, problem := rustEditionOf(ctx, repoPath)
	if problem != "" {
		return templates.Context{}, problem + "; rustfmt run directly formats every crate under the one edition rustfmt.toml names, so write rustfmt.toml for the edition to format under"
	}
	return templates.Context{RustEdition: edition}, ""
}

// rustEditionOf returns the edition every crate of the root Cargo.toml's workspace is on: the
// root package and each workspace member (workspaceEditions). A crate that declares none is on
// Cargo's default, 2015, which rustfmt also formats under without an edition, so "" stands for
// it. A workspace with no crate to read gives the edition [workspace.package] declares, and a
// root manifest that cannot be read declares nothing to copy. The second result says why no
// single edition holds: crates on different editions, or a member that cannot be read.
func rustEditionOf(ctx context.Context, repoPath string) (edition, problem string) {
	data, err := util.ReadConfinedLimited(repoPath, "Cargo.toml", maxSettingBytes)
	if err != nil {
		return "", ""
	}
	root := parseCargoManifest(string(data))
	editions, problem := workspaceEditions(ctx, repoPath, root)
	switch {
	case problem != "":
		return "", problem
	case len(editions) == 0:
		return root.workspaceEdition, ""
	}
	return commonEdition(editions)
}

// workspaceEditions returns the edition of every crate cargo fmt formats in the workspace root
// describes: the root package's, when it is one, then each member's (workspaceMemberDirs), ""
// for a crate that declares none.
func workspaceEditions(ctx context.Context, repoPath string, root cargoManifest) ([]string, string) {
	if root.invalid != "" {
		return nil, root.invalid
	}
	var editions []string
	if root.hasPackage {
		edition, problem := crateEdition(root, root.workspaceEdition, "the root package")
		if problem != "" {
			return nil, problem
		}
		editions = append(editions, edition)
	}
	dirs, problem := workspaceMemberDirs(repoPath, root)
	if problem != "" {
		return nil, problem
	}
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Sprintf("reading the workspace members stopped: %v", err)
		}
		edition, problem := memberEdition(repoPath, dir, root.workspaceEdition)
		if problem != "" {
			return nil, problem
		}
		editions = append(editions, edition)
	}
	return editions, ""
}

// memberEdition returns the edition of the workspace member at dir, slash-separated and
// relative to repoPath.
func memberEdition(repoPath, dir, workspaceEdition string) (string, string) {
	rel := path.Join(dir, "Cargo.toml")
	data, err := util.ReadConfinedLimited(repoPath, filepath.FromSlash(rel), maxSettingBytes)
	if err != nil {
		return "", fmt.Sprintf("the workspace member %s cannot be read (%v)", rel, err)
	}
	manifest := parseCargoManifest(string(data))
	if !manifest.hasPackage {
		return "", fmt.Sprintf("the workspace member %s declares no [package]", rel)
	}
	return crateEdition(manifest, workspaceEdition, rel)
}

// crateEdition returns the edition the crate manifest describes is on: its own, or
// workspaceEdition when it inherits that (edition.workspace = true), "" when it declares none.
// Inheriting an edition the workspace does not declare is an error Cargo reports, so no edition
// holds.
func crateEdition(manifest cargoManifest, workspaceEdition, name string) (string, string) {
	if !manifest.inherits {
		return manifest.edition, ""
	}
	if workspaceEdition == "" {
		return "", fmt.Sprintf("%s inherits an edition [workspace.package] does not declare", name)
	}
	return workspaceEdition, ""
}

// commonEdition returns the edition every one of editions names, "" counting as
// defaultRustEdition, or why there is none.
func commonEdition(editions []string) (string, string) {
	distinct := make(map[string]bool, len(editions))
	for _, edition := range editions {
		distinct[cmp.Or(edition, defaultRustEdition)] = true
	}
	if len(distinct) > 1 {
		return "", fmt.Sprintf("the workspace crates are on different editions (%s)",
			strings.Join(slices.Sorted(maps.Keys(distinct)), ", "))
	}
	return editions[0], ""
}

// workspaceMemberDirs returns the directory of every member root lists under [workspace]
// members, slash-separated and relative to the root, in the order Cargo reads them. A path is a
// member as written. A pattern is every directory it matches, sorted, except one an exclude
// entry names or lies under; a file it matches is no member, as in Cargo. Only the "*" and "?"
// patterns the Cargo reference names are expanded: a "**" or "[...]" pattern, one reaching
// outside the root, and more than maxCargoMembers members leave the members unknown.
func workspaceMemberDirs(repoPath string, root cargoManifest) ([]string, string) {
	var dirs []string
	for i := 0; i < len(root.members) && len(dirs) <= maxCargoMembers; i++ {
		found, problem := expandMember(repoPath, root.members[i], root.exclude)
		if problem != "" {
			return nil, problem
		}
		dirs = append(dirs, found...)
	}
	if len(dirs) > maxCargoMembers {
		return nil, fmt.Sprintf("the workspace has more than %d members", maxCargoMembers)
	}
	return dirs, ""
}

// expandMember returns the member directories one [workspace] members entry names
// (workspaceMemberDirs).
func expandMember(repoPath, member string, exclude []string) ([]string, string) {
	clean := path.Clean(member)
	if !strings.ContainsAny(clean, "*?[") {
		return []string{clean}, ""
	}
	if !expandablePattern(clean) {
		return nil, fmt.Sprintf("the workspace member pattern %q is not expanded here: only \"*\" and \"?\" inside the workspace are", member)
	}
	matches, err := filepath.Glob(filepath.Join(repoPath, filepath.FromSlash(clean)))
	if err != nil {
		return nil, fmt.Sprintf("the workspace member pattern %q does not expand: %v", member, err)
	}
	if len(matches) > maxMemberMatches {
		return nil, fmt.Sprintf("the workspace member pattern %q matches more than %d paths", member, maxMemberMatches)
	}
	return matchedMemberDirs(repoPath, matches, exclude), ""
}

// expandablePattern reports whether clean, a cleaned slash-separated members pattern, is one
// expandMember expands: "*" and "?" only, inside the root. Go's filepath.Glob would read "**"
// as "*" and "[!...]" as a class holding "!", where Cargo's glob matches any depth and negates.
func expandablePattern(clean string) bool {
	outside := clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean)
	return !outside && !strings.Contains(clean, "**") && !strings.Contains(clean, "[")
}

// matchedMemberDirs returns each of matches, paths a members pattern matched below repoPath,
// that is a directory no exclude entry names or contains, slash-separated and relative to
// repoPath. It stops once past maxCargoMembers directories, which workspaceMemberDirs reports.
func matchedMemberDirs(repoPath string, matches, exclude []string) []string {
	var dirs []string
	for i := 0; i < len(matches) && i < maxMemberMatches && len(dirs) <= maxCargoMembers; i++ {
		rel, err := filepath.Rel(repoPath, matches[i])
		info, statErr := os.Stat(matches[i])
		if err == nil && statErr == nil && info.IsDir() && !excludedMember(filepath.ToSlash(rel), exclude) {
			dirs = append(dirs, filepath.ToSlash(rel))
		}
	}
	return dirs
}

// excludedMember reports whether an exclude entry names dir or a directory above it.
func excludedMember(dir string, exclude []string) bool {
	return slices.ContainsFunc(exclude, func(entry string) bool {
		entry = path.Clean(entry)
		return dir == entry || strings.HasPrefix(dir, entry+"/")
	})
}
