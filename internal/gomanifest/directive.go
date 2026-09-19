package gomanifest

import "strings"

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
		line := strings.TrimSpace(strings.TrimSuffix(lines[i], "\r"))
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		version, found := strings.CutPrefix(line, goDirectiveKeyword)
		if !found {
			continue
		}
		if version = directiveVersion(version); version != "" {
			return version, true
		}
	}
	return "", false
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

// ParseReplaceDirective splits one already-unblocked replace line ("old[ vX] => new[ vY]")
// into the module path being replaced and its replacement. newVersion is empty for a local
// filesystem replacement ("old => ../local/dir"), which go.mod permits without a version;
// the caller decides what an empty replacement version means for its own output.
func ParseReplaceDirective(line string) (oldPath, newPath, newVersion string, ok bool) {
	left, right, found := strings.Cut(line, "=>")
	if !found {
		return "", "", "", false
	}
	leftFields := strings.Fields(strings.TrimSpace(left))
	rightFields := strings.Fields(strings.TrimSpace(right))
	if len(leftFields) == 0 || len(rightFields) == 0 {
		return "", "", "", false
	}
	oldPath = leftFields[0]
	newPath = rightFields[0]
	if len(rightFields) >= 2 {
		newVersion = rightFields[1]
	}
	return oldPath, newPath, newVersion, true
}

// ModulePath extracts the module directive's path from a single go.mod line, e.g.
// "module github.com/cordanaLLM/praetor" -> "github.com/cordanaLLM/praetor". It reports
// false for any line that is not a module directive.
func ModulePath(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "module ") {
		return "", false
	}
	path := strings.TrimSpace(strings.TrimPrefix(trimmed, "module"))
	if path == "" {
		return "", false
	}
	return path, true
}
