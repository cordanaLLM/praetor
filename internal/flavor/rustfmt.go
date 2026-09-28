// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"context"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
)

// cargoEditionKeys are the full keys of the root Cargo.toml the scaffolded rustfmt.toml takes
// its edition from, preferred first: the edition a workspace gives its members
// ([workspace.package], which a member inherits with edition.workspace = true), then the root
// package's own ([package]).
var cargoEditionKeys = []string{"workspace.package.edition", "package.edition"}

// rustEdition matches an edition as Cargo spells it, a four-digit year such as "2021". Only such
// a value is written into rustfmt.toml; any other is left to Cargo, which rejects it.
var rustEdition = regexp.MustCompile(`^[0-9]{4}$`)

// priorRustfmtDigests are the digests (util.CanonicalTextDigest) of every rustfmt.toml earlier
// releases scaffolded, keyed to what produced them. Both named one edition whatever the crates
// declared (#567), so an unedited copy is refreshed to the rendering this release derives from
// Cargo.toml (TemplateItem.Prior). testdata/rustfmt-prior holds each text
// (TestRustfmtPriorTextsAreEarlierRenderings).
var priorRustfmtDigests = map[string]string{
	"9349046a30c9737d5a2479b504989126faf87547ad1e4087863c800999927952": "edition 2021 for every crate (513804b2 to eed57331)",
	"97d4aa4c4ca05f55863c14f7efdcef46aaa4f7cc23a0874935f7fca78b300e99": "edition 2024 for every crate (eed57331 to #567)",
}

// rustfmtFacts resolves the edition the scaffolded rustfmt.toml declares (rustEditionOf). cargo
// fmt passes each crate's edition to rustfmt, and rustfmt run directly reads it from
// rustfmt.toml, so a scaffold naming any other edition makes the two formatters disagree. The
// file is never withheld: without an edition to copy it declares none.
func rustfmtFacts(_ context.Context, repoPath string) (templates.Context, string) {
	return templates.Context{RustEdition: rustEditionOf(repoPath)}, ""
}

// rustEditionOf returns the edition the root Cargo.toml gives [workspace.package], else the one
// it gives [package], else "". A manifest that cannot be read declares nothing to copy.
func rustEditionOf(repoPath string) string {
	data, err := util.ReadConfinedLimited(repoPath, "Cargo.toml", maxSettingBytes)
	if err != nil {
		return ""
	}
	editions := manifestEditions(string(data))
	for _, key := range cargoEditionKeys {
		if edition := editions[key]; edition != "" {
			return edition
		}
	}
	return ""
}

// manifestEditions maps each full key of manifest ending in "edition" that is assigned a
// well-formed edition string (rustEdition) to that edition: "package.edition" for an edition
// under [package], or for the dotted package.edition at the top level. It reads single-line
// keys under table headers, the shapes Cargo writes and its documentation shows; an inherited
// edition (edition.workspace = true) is not a string and is not recorded. TOML forbids a
// second assignment of a key, so the first one read is kept.
func manifestEditions(manifest string) map[string]string {
	editions := make(map[string]string, len(cargoEditionKeys))
	table := ""
	lines := strings.Split(manifest, "\n")
	for i := 0; i < len(lines) && i < maxValidatedLines; i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "[") {
			table = util.TOMLTableName(line)
			continue
		}
		key, value, ok := util.TOMLKeyValue(line)
		if !ok || strings.HasPrefix(line, "#") || (key != "edition" && !strings.HasSuffix(key, ".edition")) {
			continue
		}
		if table != "" {
			key = table + "." + key
		}
		edition, isString := util.TOMLStringValue(value)
		if _, seen := editions[key]; !seen && isString && rustEdition.MatchString(edition) {
			editions[key] = edition
		}
	}
	return editions
}
