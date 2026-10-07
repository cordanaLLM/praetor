// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
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
// the crates share no edition, or one of them cannot be read, the root manifest included, no
// rendering is known to agree with cargo fmt on every crate: the body is withheld, and an
// existing file is kept (withheldOverFile).
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
// it. A workspace with no crate to read gives the edition [workspace.package] declares. The
// second result says why no single edition holds: no root manifest to read (rootManifestProblem),
// crates on different editions, or a member that cannot be read.
func rustEditionOf(ctx context.Context, repoPath string) (edition, problem string) {
	data, err := util.ReadConfinedLimited(repoPath, "Cargo.toml", maxSettingBytes)
	if err != nil {
		return "", rootManifestProblem(err)
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

// rootManifestProblem says why err, the failed read of the root Cargo.toml, leaves the crate
// editions unknown. A repository whose crates sit in a subdirectory has no root manifest, and
// one too large, not a regular file, or reaching outside the repository cannot be read; none of
// them says that no crate declares an edition. Reading it that way wrote the edition-less
// config, rustfmt's 2015, over crates on a later edition, and refreshed an earlier 2024 text to
// it without --force.
func rootManifestProblem(err error) string {
	if errors.Is(err, fs.ErrNotExist) {
		return "the repository has no root Cargo.toml to read the crate editions from"
	}
	return fmt.Sprintf("the root Cargo.toml cannot be read (%v)", err)
}

// workspaceEditions returns the edition of every crate cargo fmt formats in the workspace root
// describes (workspaceCrates), "" for a crate that declares none.
func workspaceEditions(ctx context.Context, repoPath string, root cargoManifest) ([]string, string) {
	crates, problem := workspaceCrates(ctx, repoPath, root)
	if problem != "" {
		return nil, problem
	}
	editions := make([]string, 0, len(crates))
	for _, crate := range crates {
		edition, problem := crateEdition(crate.manifest, root.workspaceEdition, crate.name)
		if problem != "" {
			return nil, problem
		}
		editions = append(editions, edition)
	}
	return editions, ""
}

// cargoCrate is one crate of a workspace: its name for messages, "the root package" or its
// manifest's path, and what parseCargoManifest read from that manifest.
type cargoCrate struct {
	name     string
	manifest cargoManifest
}

// workspaceCrates returns every crate of the workspace root describes: the root package, when it
// is one, then each member (workspaceMemberDirs), or why they cannot all be read. cargo fmt,
// rustfmt.toml's edition and the warnings lint level of the build-warnings gate
// (CargoWarningsLints) read the crates through it.
func workspaceCrates(ctx context.Context, repoPath string, root cargoManifest) ([]cargoCrate, string) {
	if root.invalid != "" {
		return nil, root.invalid
	}
	var crates []cargoCrate
	if root.hasPackage {
		crates = append(crates, cargoCrate{name: "the root package", manifest: root})
	}
	dirs, problem := workspaceMemberDirs(repoPath, root)
	if problem != "" {
		return nil, problem
	}
	for _, dir := range dirs {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Sprintf("reading the workspace members stopped: %v", err)
		}
		crate, problem := memberManifest(repoPath, dir)
		if problem != "" {
			return nil, problem
		}
		crates = append(crates, crate)
	}
	return crates, ""
}

// memberManifest reads the manifest of the workspace member at dir, slash-separated and relative
// to repoPath.
func memberManifest(repoPath, dir string) (cargoCrate, string) {
	rel := path.Join(dir, "Cargo.toml")
	data, err := util.ReadConfinedLimited(repoPath, filepath.FromSlash(rel), maxSettingBytes)
	if err != nil {
		return cargoCrate{}, fmt.Sprintf("the workspace member %s cannot be read (%v)", rel, err)
	}
	manifest := parseCargoManifest(string(data))
	if !manifest.hasPackage {
		return cargoCrate{}, fmt.Sprintf("the workspace member %s declares no [package]", rel)
	}
	return cargoCrate{name: rel, manifest: manifest}, ""
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
// entry names or lies under; a file it matches is no member, as in Cargo. A pattern matching
// nothing at all is an error Cargo reports, since it then reads the pattern as a member path.
// Only the "*" and "?" patterns the Cargo reference names are expanded: a "**" or "[...]"
// pattern, one reaching outside the root, and more than maxCargoMembers members leave the
// members unknown.
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
// (workspaceMemberDirs). The pattern is matched below repoPath (fs.Glob over os.DirFS), so a
// glob metacharacter in the checkout's own path, such as a "ws[1]" directory, is no pattern.
func expandMember(repoPath, member string, exclude []string) ([]string, string) {
	clean := path.Clean(member)
	if !strings.ContainsAny(clean, "*?[") {
		return []string{clean}, ""
	}
	if !expandablePattern(clean) {
		return nil, fmt.Sprintf("the workspace member pattern %q is not expanded here: only \"*\" and \"?\" inside the workspace are", member)
	}
	matches, err := fs.Glob(os.DirFS(repoPath), clean)
	switch {
	case err != nil:
		return nil, fmt.Sprintf("the workspace member pattern %q does not expand: %v", member, err)
	case len(matches) == 0:
		return nil, fmt.Sprintf("the workspace member pattern %q matches nothing, which Cargo reads as a member path and fails to load", member)
	case len(matches) > maxMemberMatches:
		return nil, fmt.Sprintf("the workspace member pattern %q matches more than %d paths", member, maxMemberMatches)
	}
	return matchedMemberDirs(repoPath, matches, exclude), ""
}

// expandablePattern reports whether clean, a cleaned slash-separated members pattern, is one
// expandMember expands: "*" and "?" only, inside the root. Go's fs.Glob would read "**" as "*"
// and "[!...]" as a class holding "!", where Cargo's glob matches any depth and negates.
func expandablePattern(clean string) bool {
	outside := clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean)
	return !outside && !strings.Contains(clean, "**") && !strings.Contains(clean, "[")
}

// matchedMemberDirs returns each of matches, slash-separated paths a members pattern matched
// below repoPath, that is a directory no exclude entry names or contains. It stops once past
// maxCargoMembers directories, which workspaceMemberDirs reports.
func matchedMemberDirs(repoPath string, matches, exclude []string) []string {
	var dirs []string
	for i := 0; i < len(matches) && i < maxMemberMatches && len(dirs) <= maxCargoMembers; i++ {
		info, err := os.Stat(filepath.Join(repoPath, filepath.FromSlash(matches[i])))
		if err == nil && info.IsDir() && !excludedMember(matches[i], exclude) {
			dirs = append(dirs, matches[i])
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
