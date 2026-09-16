// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// Adoption writes files whose bytes it later compares, and until now it told the adopter's
// formatter nothing about them. A repository that runs Prettier, gofmt or dprint over its own
// tree could fail `praetorctl audit` by running its own formatter -- the devcontainer was
// re-indented from spaces to tabs, forty lines changed with no semantic difference, and the
// audit reported "contains unrecognized edits". That sentence is accurate from praetor's side
// and misleading from the adopter's: it reads as a hand edit, so the first instinct is to hunt
// for one rather than to look at the formatter that swept the tree (#116).
//
// The contract "these bytes are mine" existed only in praetor's head. Naming the set once, here,
// is what lets it be told to anything else.

const (
	// managedIgnoreBegin and managedIgnoreEnd delimit the block adoption owns inside an
	// adopter's ignore file. Everything outside the markers belongs to the adopter and is
	// preserved on every re-run.
	managedIgnoreBegin = "# BEGIN praetor-managed artifacts (praetorctl adopt)"
	managedIgnoreEnd   = "# END praetor-managed artifacts"
	// prettierIgnoreFile is the JS/TS ecosystem's ignore file. It is the first formatter to be
	// told, because it is the one that surfaced the defect; the set itself is language-neutral.
	prettierIgnoreFile = ".prettierignore"
	// maxIgnoreLines bounds the ignore file this rewrite reads (HISS-02).
	maxIgnoreLines = 4096
)

// managedArtifacts returns every path adoption writes and later compares byte for byte.
//
// This is the single source for that set. The paths were previously only constants scattered
// across the package, which is why nothing could enumerate them for any other purpose.
func managedArtifacts() []string {
	paths := []string{
		manifestFile,
		lockFile,
		baselineFile,
		agentsFile,
		devcontainerFile,
		lefthookFile,
		evasionHookFile,
		rulesetFile,
		labelsFile,
		paperclipFile,
		auditorAgentFile,
		gatekeeperFile,
	}
	sort.Strings(paths)
	return paths
}

// renderManagedIgnoreBlock builds the delimited block naming every managed artifact.
func renderManagedIgnoreBlock() string {
	var sb strings.Builder
	sb.WriteString(managedIgnoreBegin)
	sb.WriteString("\n# praetorctl audit compares these byte for byte. A formatter that rewrites\n")
	sb.WriteString("# them fails the gate with an error that reads like a hand edit.\n")
	for _, path := range managedArtifacts() {
		sb.WriteString(path)
		sb.WriteString("\n")
	}
	sb.WriteString(managedIgnoreEnd)
	sb.WriteString("\n")
	return sb.String()
}

// mergeManagedIgnore returns the ignore file content with the managed block present exactly
// once, preserving everything the adopter wrote around it.
//
// A re-run replaces the block rather than appending a second one, so the file converges instead
// of growing. An adopter's own entries are never touched: adoption owns the delimited region and
// nothing else.
func mergeManagedIgnore(existing string) (string, error) {
	block := renderManagedIgnoreBlock()
	lines := strings.Split(existing, "\n")
	if len(lines) > maxIgnoreLines {
		return "", fmt.Errorf("%s exceeds %d lines", prettierIgnoreFile, maxIgnoreLines)
	}
	kept, inBlock, closed := make([]string, 0, len(lines)), false, false
	for i := 0; i < len(lines) && i < maxIgnoreLines; i++ {
		switch {
		case strings.TrimSpace(lines[i]) == managedIgnoreBegin:
			inBlock = true
		case inBlock && strings.TrimSpace(lines[i]) == managedIgnoreEnd:
			inBlock, closed = false, true
		case !inBlock:
			kept = append(kept, lines[i])
		}
	}
	// An unterminated block -- truncated, or hand-edited mid-block -- needs no special case.
	// Every line after the opening marker is already treated as block content and dropped, and
	// what remains is only what the adopter wrote before it. Discarding that too would destroy
	// their entries to fix praetor's own marker, which is the wrong trade.
	_ = closed
	body := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if body == "" {
		return block, nil
	}
	return body + "\n\n" + block, nil
}

// reconcileFormatterIgnore declares the managed artifacts to the adopter's formatter.
//
// Only an existing .prettierignore is updated, and one is created only where the repository
// actually runs Prettier. Dropping the file into a Go or Rust repository that has never heard of
// it would be adoption leaving litter, which is its own complaint (#64).
func reconcileFormatterIgnore(ctx context.Context, s *adoptSession) error {
	full, err := repoFile(s.repoPath, prettierIgnoreFile)
	if err != nil {
		return err
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return err
	}
	if !exists && !s.usesPrettier(ctx) {
		s.report.recordReconciled(prettierIgnoreFile,
			"No Prettier configuration found; managed artifacts not declared to a formatter this repository does not run")
		return nil
	}
	merged, err := mergeManagedIgnore(string(data))
	if err != nil {
		return err
	}
	if merged == string(data) {
		s.report.recordReconciled(prettierIgnoreFile, "Managed artifacts already declared")
		return nil
	}
	if !s.opts.DryRun {
		if err := contextopt.ReplaceSnapshot(ctx, full, []byte(merged),
			contextopt.ReplaceOptions{Expected: data, Exists: exists, Mode: filePerm}); err != nil {
			return err
		}
	}
	if exists {
		s.report.recordReconciledAs(prettierIgnoreFile, actionAppend,
			"Preserved existing rules and declared the praetor-managed artifacts")
		return nil
	}
	s.report.recordCreated(prettierIgnoreFile, "Declared the praetor-managed artifacts to Prettier")
	return nil
}

// usesPrettier reports whether the repository runs Prettier, so adoption only writes an ignore
// file where one has an effect.
func (s *adoptSession) usesPrettier(ctx context.Context) bool {
	for _, name := range prettierConfigFiles {
		full, err := repoFile(s.repoPath, name)
		if err != nil {
			continue
		}
		if _, exists, err := contextopt.ObserveSnapshot(ctx, full); err == nil && exists {
			return true
		}
	}
	return false
}

// prettierConfigFiles are the configuration names whose presence means Prettier runs here.
var prettierConfigFiles = []string{
	".prettierrc",
	".prettierrc.json",
	".prettierrc.yaml",
	".prettierrc.yml",
	".prettierrc.js",
	"prettier.config.js",
	"prettier.config.mjs",
	"prettier.config.cjs",
}
