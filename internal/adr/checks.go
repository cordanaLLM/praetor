package adr

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// exclusionSources are ignore files, where every non-comment line is an exclusion pattern by
// definition. That property is what makes the check decidable without parsing each tool's
// schema.
//
// Structured configuration -- .golangci.yml, .gosec.json, .clang-tidy -- is deliberately not
// read here. Those files mix exclusions with rule text, so matching their lines by substring
// reports a lint rule's own message as an exclusion when the message happens to name a
// directory. Measured on this repository that produced three findings, and all three were
// rule text rather than an exclusion. Deciding them needs key-aware parsing per tool; until
// that exists the check says so rather than guessing, because a checker that cannot tell
// naming a path from excluding one produces findings a reader learns to ignore.
var exclusionSources = []string{
	".gitignore", ".semgrepignore", ".eslintignore", ".prettierignore",
	".dockerignore", ".npmignore",
}

// checkConstraint replays one constraint against the repository's tracked paths.
func checkConstraint(constraint Constraint, tracked map[string]string) []Finding {
	switch constraint.Kind {
	case KindForbiddenPath:
		return checkForbiddenPath(constraint, tracked)
	case KindUniversalScope:
		return checkUniversalScope(constraint, tracked)
	}
	// Parse rejects every other kind, so reaching here means a kind was added to the type
	// without a check. Report it rather than returning clean.
	return []Finding{{Constraint: constraint,
		Detail: fmt.Sprintf("kind %q has no check in this engine", constraint.Kind)}}
}

// checkForbiddenPath fails when a path the decision removed is present again.
func checkForbiddenPath(constraint Constraint, tracked map[string]string) []Finding {
	var findings []Finding
	for i := 0; i < len(constraint.Forbids) && i < maxConstraintsPerRecord; i++ {
		pattern := constraint.Forbids[i]
		for file := range tracked {
			if matchesPattern(pattern, file) {
				findings = append(findings, Finding{Constraint: constraint,
					Detail: fmt.Sprintf("%s exists, but the record forbids %q", file, pattern)})
				break
			}
		}
	}
	return findings
}

// checkUniversalScope fails when an exclusion file carves out something the decision says
// is always in scope. The point is that "there is no upstream-code tier" stops being a
// sentence somebody has to remember and becomes a thing the repository is measured against.
func checkUniversalScope(constraint Constraint, tracked map[string]string) []Finding {
	var findings []Finding
	for _, source := range exclusionSources {
		body, present := tracked[source]
		if !present || body == "" {
			continue
		}
		findings = append(findings, scanExclusionFile(constraint, source, body)...)
	}
	return findings
}

func scanExclusionFile(constraint Constraint, source, body string) []Finding {
	var findings []Finding
	lines := strings.Split(body, "\n")
	for i := 0; i < len(lines) && i < maxScanFiles; i++ {
		entry := strings.TrimSpace(lines[i])
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}
		for k := 0; k < len(constraint.Forbids) && k < maxConstraintsPerRecord; k++ {
			if !strings.Contains(entry, constraint.Forbids[k]) {
				continue
			}
			findings = append(findings, Finding{Constraint: constraint,
				Detail: fmt.Sprintf("%s:%d excludes %q from the %s gate, which this record says has no exempt tier",
					source, i+1, entry, gateName(constraint))})
		}
	}
	return findings
}

func gateName(constraint Constraint) string {
	if strings.TrimSpace(constraint.Gate) == "" {
		return "declared"
	}
	return constraint.Gate
}

// matchesPattern compares in slash form on every host, so a record written on one platform
// decides the same way on another.
func matchesPattern(pattern, file string) bool {
	normalised := util.NormalizeSlashes(file)
	if ok, err := path.Match(util.NormalizeSlashes(pattern), normalised); err == nil && ok {
		return true
	}
	return strings.Contains(normalised, util.NormalizeSlashes(pattern))
}

// trackedPaths lists the repository's tracked files, reading the bodies of the exclusion
// sources so a scope check reads what the repository actually declares.
func trackedPaths(ctx context.Context, repoPath string) (map[string]string, error) {
	// RunGit is the single audited exec entry point: it carries the context deadline and the
	// hardened git environment, which a bare exec.CommandContext here would not.
	output, err := util.RunGit(ctx, repoPath, "ls-files", "-z")
	if err != nil {
		return nil, fmt.Errorf("listing tracked files: %w", err)
	}
	names := strings.Split(strings.TrimRight(output, "\x00"), "\x00")
	if len(names) > maxScanFiles {
		return nil, fmt.Errorf("repository exceeds %d tracked files", maxScanFiles)
	}
	tracked := make(map[string]string, len(names))
	for i := 0; i < len(names) && i < maxScanFiles; i++ {
		if names[i] != "" {
			tracked[util.NormalizeSlashes(names[i])] = ""
		}
	}
	for _, source := range exclusionSources {
		if _, present := tracked[source]; !present {
			continue
		}
		body, readErr := readTracked(ctx, repoPath, source)
		if readErr != nil && !errors.Is(readErr, context.Canceled) {
			return nil, readErr
		}
		tracked[source] = body
	}
	return tracked, nil
}
