package gomanifest

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// maxDirectiveLines bounds the scan for a go directive. A manifest declares it near the
// top, long before any plausible require block ends (HISS-02).
const maxDirectiveLines = 4096

// goDirectiveKeyword is the directive that fixes the language version.
const goDirectiveKeyword = "go"

// utf8BOM is the UTF-8 encoding of U+FEFF, the byte-order mark some editors write at the
// start of a file.
const utf8BOM = "\ufeff"

// TrimBOM returns manifest without a leading UTF-8 byte-order mark, and manifest unchanged
// otherwise. The go command refuses a go.mod that starts with one ("unexpected input
// character '\ufeff'"); an offline scanner drops it instead, because U+FEFF is not white
// space: left in place, it glues onto the first directive, so a module line reads as no
// module and the module's own imports would count as third-party demand. Call it on the
// whole manifest before splitting it into lines.
func TrimBOM(manifest []byte) []byte {
	return bytes.TrimPrefix(manifest, []byte(utf8BOM))
}

// GoDirective returns the Go language version a manifest requires, as written in its
// `go` line ("1.27", "1.27.1"), and whether the manifest declares one at all.
//
// The manifest's directive is the single source of truth for the toolchain: a CI
// workflow, a container image or a shipped template that names a different version
// builds the module with a compiler the module never declared. Callers compare against
// this value rather than repeating the number.
func GoDirective(manifest []byte) (string, bool) {
	lines := strings.Split(string(TrimBOM(manifest)), "\n")
	for i := 0; i < len(lines) && i < maxDirectiveLines; i++ {
		if version, declared := GoDirectiveLine(lines[i]); declared {
			return version, true
		}
	}
	return "", false
}

// GoDirectiveLine returns the version a single go.mod line declares when that line is
// the `go` directive, with any trailing comment dropped. Like the go.mod lexer of
// golang.org/x/mod/modfile, it accepts any white space, a tab included, between the
// keyword and the version. It reports false for every other line, a commented-out
// directive and a directive without a version. Line scanners that already walk a
// manifest use it instead of re-reading the whole file through GoDirective.
func GoDirectiveLine(line string) (string, bool) {
	value, found := directiveArgument(line, goDirectiveKeyword)
	if !found {
		return "", false
	}
	return strings.Fields(value)[0], true
}

// directiveArgument returns the argument of a single-line go.mod directive: the text
// after keyword, with a trailing // comment dropped and surrounding white space
// trimmed. It reports false when line is not that directive -- a commented-out line, a
// keyword glued to what follows ("modulefoo", "go.uber.org/zap") -- or when the
// directive carries no argument.
func directiveArgument(line, keyword string) (string, bool) {
	code, _, _ := strings.Cut(line, "//")
	rest, found := strings.CutPrefix(strings.TrimSpace(code), keyword)
	if !found {
		return "", false
	}
	argument := strings.TrimSpace(rest)
	// The keyword must end at white space and be followed by an argument.
	if argument == "" || len(argument) == len(rest) {
		return "", false
	}
	return argument, true
}

// ReplaceLine advances replace-block state and identifies a replace directive line, the
// same way RequirementLine does for require lines. Block delimiters are consumed; a
// single-line replace has its keyword removed.
func ReplaceLine(raw string, inBlock *bool) (string, bool) {
	return blockDirectiveLine(raw, "replace", inBlock)
}

// ReplaceDirective is one go.mod replace directive: "OldPath[ OldVersion] =>
// NewPath[ NewVersion]".
//
// OldVersion is empty when the directive replaces every version of OldPath, and
// otherwise limits it to that one version, as the go command applies it. NewVersion is
// empty for a local filesystem replacement ("old => ../local/dir"), which go.mod
// permits without a version; the caller decides what that means for its own output.
type ReplaceDirective struct {
	OldPath    string
	OldVersion string
	NewPath    string
	NewVersion string
}

// ParseReplaceDirective splits one already-unblocked replace line into the module being
// replaced and its replacement, keeping the version on each side of the arrow.
func ParseReplaceDirective(line string) (ReplaceDirective, bool) {
	left, right, found := strings.Cut(line, "=>")
	if !found {
		return ReplaceDirective{}, false
	}
	leftFields := strings.Fields(strings.TrimSpace(left))
	rightFields := strings.Fields(strings.TrimSpace(right))
	if len(leftFields) == 0 || len(rightFields) == 0 {
		return ReplaceDirective{}, false
	}
	directive := ReplaceDirective{OldPath: leftFields[0], NewPath: rightFields[0]}
	if len(leftFields) >= 2 {
		directive.OldVersion = leftFields[1]
	}
	if len(rightFields) >= 2 {
		directive.NewVersion = rightFields[1]
	}
	return directive, true
}

// moduleKeyword is the directive that names the module a manifest defines.
const moduleKeyword = "module"

// ModuleDirective returns the module path a whole manifest declares, as ModulePath reads its
// module line, and whether the manifest declares one within the first maxDirectiveLines
// lines. It is the manifest-level form of ModulePath, as GoDirective is of GoDirectiveLine.
func ModuleDirective(manifest []byte) (string, bool) {
	lines := strings.Split(string(manifest), "\n")
	for i := 0; i < len(lines) && i < maxDirectiveLines; i++ {
		if path, declared := ModulePath(lines[i]); declared {
			return path, true
		}
	}
	return "", false
}

// maxModuleManifestBytes bounds the go.mod ReadModulePath reads (HISS-02).
const maxModuleManifestBytes = 1 << 20

// ReadModulePath returns the module path root's go.mod declares, read through the bounded
// confined reader. The docs vocabulary tree and the archetype coverage scan resolve their
// module through it.
func ReadModulePath(root string) (string, error) {
	data, err := util.ReadConfinedLimited(root, "go.mod", maxModuleManifestBytes)
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	if module, ok := ModuleDirective(data); ok {
		return module, nil
	}
	return "", errors.New("go.mod declares no module path")
}

// ModulePath extracts the module directive's path from a single go.mod line, e.g.
// "module github.com/cordanaLLM/praetor" -> "github.com/cordanaLLM/praetor". It reads the
// line the way golang.org/x/mod/modfile.ModulePath does: a trailing // comment is dropped,
// any white space may separate the keyword from the path, and a quoted path ("..." or
// `...`) is unquoted. It reports false for any line that is not a module directive and
// for a quoted path that does not unquote.
func ModulePath(line string) (string, bool) {
	path, found := directiveArgument(line, moduleKeyword)
	if !found {
		return "", false
	}
	if path[0] != '"' && path[0] != '`' {
		return path, true
	}
	unquoted, err := strconv.Unquote(path)
	if err != nil || unquoted == "" {
		return "", false
	}
	return unquoted, true
}
