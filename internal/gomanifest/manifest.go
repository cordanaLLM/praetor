// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package gomanifest

import "strings"

// maxManifestLines bounds the lines ParseManifest reads (HISS-02). A go.mod is read through a
// byte-bounded reader first; this bounds the walk over what that read returned.
const maxManifestLines = 1 << 16

// Manifest is what one go.mod declares about its module and the modules it builds against.
type Manifest struct {
	// Module is the module path, empty when the manifest declares none.
	Module string
	// Requires lists the require directives, single-line and block, in file order.
	Requires []Requirement
	// Replaces lists the replace directives, single-line and block, in file order.
	Replaces []ReplaceDirective
}

// ParseManifest reads a whole go.mod through the line readers of this package: ModulePath for
// the module line, RequirementLine and ParseRequirement for require directives, ReplaceLine and
// ParseReplaceDirective for replace directives. A leading byte-order mark is dropped (TrimBOM).
// A line none of them accepts is skipped, so a malformed directive contributes nothing rather
// than failing the read; a caller that needs a module path checks Module.
func ParseManifest(manifest []byte) Manifest {
	lines := strings.Split(string(TrimBOM(manifest)), "\n")
	var parsed Manifest
	inRequire, inReplace := false, false
	for i := 0; i < len(lines) && i < maxManifestLines; i++ {
		if module, declared := ModulePath(lines[i]); declared && parsed.Module == "" {
			parsed.Module = module
			continue
		}
		if line, required := RequirementLine(lines[i], &inRequire); required {
			if requirement, ok := ParseRequirement(line); ok {
				parsed.Requires = append(parsed.Requires, requirement)
			}
			continue
		}
		if line, replaced := ReplaceLine(lines[i], &inReplace); replaced {
			if directive, ok := ParseReplaceDirective(line); ok {
				parsed.Replaces = append(parsed.Replaces, directive)
			}
		}
	}
	return parsed
}
