// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package gomanifest

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

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

// maxReplacedManifestBytes bounds the go.mod of a replacement directory LocalReplaces reads.
const maxReplacedManifestBytes = 1 << 20

// LocalReplaces returns the module paths a whole go.mod replaces with a directory of the same
// checkout: a replacement that names no version, whose target directory resolves inside root and
// holds a go.mod declaring that very module path. A requirement on such a module is the module
// of the same checkout, not a third-party package, so the credits inventory and the needs scan
// skip it (HISS-19: both read it here). A fork under third_party/, a sibling checkout outside
// root, or a directory whose go.mod names another module is a third-party dependency and stays.
// modDir is the directory of the manifest, which relative targets resolve against. A trailing
// comment is ignored and a leading byte-order mark dropped; a versioned replacement ("old =>
// fork v1.2.3") is not local.
func LocalReplaces(manifest []byte, modDir, root string) map[string]bool {
	lines := strings.Split(string(TrimBOM(manifest)), "\n")
	local := map[string]bool{}
	inBlock := false
	for i := 0; i < len(lines) && i < maxManifestLines; i++ {
		line, replaced := ReplaceLine(lines[i], &inBlock)
		if !replaced {
			continue
		}
		code, _, _ := strings.Cut(line, "//")
		directive, ok := ParseReplaceDirective(code)
		if ok && directive.NewVersion == "" && ownModule(directive, modDir, root) {
			local[directive.OldPath] = true
		}
	}
	return local
}

// ownModule reports whether the directory a replacement names is inside root and declares the
// replaced module path.
func ownModule(directive ReplaceDirective, modDir, root string) bool {
	target := filepath.FromSlash(directive.NewPath)
	if !filepath.IsAbs(target) {
		target = filepath.Join(modDir, target)
	}
	if !insideRoot(root, target) {
		return false
	}
	// A symlink below root may point out of it; judge the resolved directory too.
	realRoot, rootErr := filepath.EvalSymlinks(root)
	realTarget, targetErr := filepath.EvalSymlinks(target)
	if rootErr != nil || targetErr != nil || !insideRoot(realRoot, realTarget) {
		return false
	}
	file, err := os.Open(filepath.Join(realTarget, "go.mod")) // #nosec G304 -- confined to root above
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxReplacedManifestBytes))
	if err != nil {
		return false
	}
	module := ParseManifest(data).Module
	return module != "" && module == directive.OldPath
}

// insideRoot reports whether target is root or below it.
func insideRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
