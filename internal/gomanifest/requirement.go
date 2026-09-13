// Package gomanifest shares lexical Go manifest handling across offline scanners.
// It extracts require-block lines; callers retain their version and policy validation.
package gomanifest

import "strings"

// RequirementLine advances require-block state and identifies a dependency line.
// Block delimiters are consumed; a single-line require has its keyword removed.
func RequirementLine(raw string, inBlock *bool) (string, bool) {
	if inBlock == nil {
		return "", false
	}
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "//") {
		return "", false
	}
	if strings.HasPrefix(line, "require (") {
		*inBlock = true
		return "", false
	}
	if *inBlock && line == ")" {
		*inBlock = false
		return "", false
	}
	return strings.TrimPrefix(line, "require "), *inBlock || strings.HasPrefix(line, "require ")
}
