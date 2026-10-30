// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package pymanifest

import "strings"

// maxManifestLines bounds the line scans of a requirements.in and of its lock; the largest lock
// in this repository holds about 1,000 lines.
const maxManifestLines = 100_000

// NormalizeName applies PEP 503 name normalisation: every run of "-", "_" and "." becomes a
// single "-", and the result is lower-cased. A requirements.in may spell mkdocs_material where
// uv writes mkdocs-material into the lock, and typing_extensions and Typing.Extensions are one
// package to the needs analyzer.
func NormalizeName(name string) string {
	var sb strings.Builder
	sb.Grow(len(name))
	inRun := false
	for _, r := range strings.ToLower(name) {
		if r == '-' || r == '_' || r == '.' {
			inRun = true
			continue
		}
		if inRun {
			sb.WriteByte('-')
			inRun = false
		}
		sb.WriteRune(r)
	}
	if inRun {
		sb.WriteByte('-')
	}
	return sb.String()
}

// UnlockedPins returns "name==version" for each exact pin of a requirements.in that the lock
// compiled from it does not hold at that version, in the order requirements.in lists them. A
// lock compiled with environment markers may hold several versions of one name; the pin is
// held when one of them matches. Ranges and unpinned names are not compared: only == says which
// version the lock must hold (#1697).
func UnlockedPins(in, lock string) []string {
	held := lockedVersions(lock)
	var missing []string
	lines := strings.Split(in, "\n")
	for i := 0; i < len(lines) && i < maxManifestLines; i++ {
		pin, ok := exactPin(lines[i])
		if ok && !held[pin] {
			missing = append(missing, pin)
		}
	}
	return missing
}

// exactPin reads one requirement line as an exact pin and returns it as "name==version" with
// the name normalised; extras, a marker and a comment are allowed and dropped. "===" (arbitrary
// equality), every range and a bare name report false.
func exactPin(line string) (string, bool) {
	requirement, ok := ParseRequirement(line)
	if !ok || requirement.Version == "" {
		return "", false
	}
	rest := strings.TrimSpace(stripDecorations(line)[len(requirement.Name):])
	if strings.HasPrefix(rest, "[") {
		_, rest, _ = strings.Cut(rest, "]")
		rest = strings.TrimSpace(rest)
	}
	if !strings.HasPrefix(rest, "==") || strings.HasPrefix(rest, "===") {
		return "", false
	}
	return NormalizeName(requirement.Name) + "==" + requirement.Version, true
}

// lockedVersions returns the set of "name==version" a hashed lock pins. Each entry line ends
// with the continuation of its hash list, which is cut before the line is read.
func lockedVersions(lock string) map[string]bool {
	held := map[string]bool{}
	lines := strings.Split(lock, "\n")
	for i := 0; i < len(lines) && i < maxManifestLines; i++ {
		if pin, ok := exactPin(strings.TrimSuffix(strings.TrimSpace(lines[i]), `\`)); ok {
			held[pin] = true
		}
	}
	return held
}
