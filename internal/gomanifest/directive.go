package gomanifest

import (
	"strconv"
	"strings"
)

// maxDirectiveLines bounds the scan for a go directive. A manifest declares it near the
// top, long before any plausible require block ends (HISS-02).
const maxDirectiveLines = 4096

// goDirectiveKeyword is the manifest line that fixes the language version.
const goDirectiveKeyword = "go "

// GoDirective returns the Go language version a manifest requires, as written in its
// `go` line ("1.27", "1.27.1"), and whether the manifest declares one at all.
//
// The manifest's directive is the single source of truth for the toolchain: a CI
// workflow, a container image or a shipped template that names a different version
// builds the module with a compiler the module never declared. Callers compare against
// this value rather than repeating the number.
func GoDirective(manifest []byte) (string, bool) {
	lines := strings.Split(string(manifest), "\n")
	for i := 0; i < len(lines) && i < maxDirectiveLines; i++ {
		if version, declared := GoDirectiveLine(lines[i]); declared {
			return version, true
		}
	}
	return "", false
}

// GoDirectiveLine returns the version a single go.mod line declares when that line is
// the `go` directive, with any trailing comment dropped. It reports false for every
// other line, a commented-out directive and a directive without a version. Line
// scanners that already walk a manifest use it instead of re-reading the whole file
// through GoDirective.
func GoDirectiveLine(line string) (string, bool) {
	version, found := strings.CutPrefix(strings.TrimSpace(line), goDirectiveKeyword)
	if !found {
		return "", false
	}
	version = directiveVersion(version)
	return version, version != ""
}

// directiveVersion trims a directive value down to its version, dropping a trailing
// comment. An empty result means the line carried no version.
func directiveVersion(value string) string {
	if comment := strings.Index(value, "//"); comment >= 0 {
		value = value[:comment]
	}
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// ReplaceLine advances replace-block state and identifies a replace directive line, the
// same way RequirementLine does for require lines. Block delimiters are consumed; a
// single-line replace has its keyword removed.
func ReplaceLine(raw string, inBlock *bool) (string, bool) {
	if inBlock == nil {
		return "", false
	}
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "//") {
		return "", false
	}
	if strings.HasPrefix(line, "replace (") {
		*inBlock = true
		return "", false
	}
	if *inBlock && line == ")" {
		*inBlock = false
		return "", false
	}
	return strings.TrimPrefix(line, "replace "), *inBlock || strings.HasPrefix(line, "replace ")
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

// ModulePath extracts the module directive's path from a single go.mod line, e.g.
// "module github.com/cordanaLLM/praetor" -> "github.com/cordanaLLM/praetor". It reads the
// line the way golang.org/x/mod/modfile.ModulePath does: a trailing // comment is dropped,
// any white space may separate the keyword from the path, and a quoted path ("..." or
// `...`) is unquoted. It reports false for any line that is not a module directive and
// for a quoted path that does not unquote.
func ModulePath(line string) (string, bool) {
	code, _, _ := strings.Cut(line, "//")
	rest, found := strings.CutPrefix(strings.TrimSpace(code), moduleKeyword)
	if !found {
		return "", false
	}
	path := strings.TrimSpace(rest)
	// The keyword must end at white space: "modulefoo" is not a module directive.
	if path == "" || len(path) == len(rest) {
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
