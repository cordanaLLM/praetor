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
	"github.com/cordanaLLM/praetor/internal/util"
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

// FormatterIgnoreFile is the formatter inventory reconciled by adoption when present.
const FormatterIgnoreFile = prettierIgnoreFile

// managedArtifacts returns every path adoption writes and later compares as owned content.
//
// This is the single source for that set. The paths were previously only constants scattered
// across the package, which is why nothing could enumerate them for any other purpose.
func managedArtifacts(documentationEnabled bool) []string {
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
	if documentationEnabled {
		paths = append(paths, DocumentationAssetPaths()...)
	}
	sort.Strings(paths)
	return paths
}

// ManagedFormatterIgnoreBlock renders the exact formatter inventory for the active facets.
func ManagedFormatterIgnoreBlock(documentationEnabled bool) string {
	var sb strings.Builder
	sb.WriteString(managedIgnoreBegin)
	sb.WriteString("\n# praetorctl audit owns this canonical content. A formatter rewrite fails;\n")
	sb.WriteString("# documentation text alone permits consistent LF/CRLF checkout conversion.\n")
	for _, path := range managedArtifacts(documentationEnabled) {
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
func mergeManagedIgnore(existing string, documentationEnabled bool) (string, error) {
	normalized, crlf, err := util.NormalizeLineEndingsStrict(existing)
	if err != nil {
		return "", fmt.Errorf("%s line endings are inconsistent: %w", prettierIgnoreFile, err)
	}
	lines := strings.Split(normalized, "\n")
	if len(lines) > maxIgnoreLines {
		return "", fmt.Errorf("%s exceeds %d lines", prettierIgnoreFile, maxIgnoreLines)
	}
	kept, err := filterManagedFormatterLines(lines)
	if err != nil {
		return "", err
	}
	block := ManagedFormatterIgnoreBlock(documentationEnabled)
	body := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	merged := block
	if body == "" {
		return util.RestoreLineEndings(merged, crlf), nil
	}
	merged = body + "\n\n" + block
	return util.RestoreLineEndings(merged, crlf), nil
}

type managedFormatterScan struct {
	kept               []string
	inBlock, seenBlock bool
}

func filterManagedFormatterLines(lines []string) ([]string, error) {
	scan := managedFormatterScan{kept: make([]string, 0, len(lines))}
	for index := 0; index < len(lines) && index < maxIgnoreLines; index++ {
		if err := scan.consume(lines[index], index); err != nil {
			return nil, err
		}
	}
	if scan.inBlock {
		return nil, fmt.Errorf("%s contains an unterminated managed block", prettierIgnoreFile)
	}
	return scan.kept, nil
}

func (scan *managedFormatterScan) consume(line string, index int) error {
	trimmed := strings.TrimSpace(line)
	if (trimmed == managedIgnoreBegin || trimmed == managedIgnoreEnd) && trimmed != line {
		return fmt.Errorf("%s contains a non-canonical managed marker at line %d", prettierIgnoreFile, index+1)
	}
	switch line {
	case managedIgnoreBegin:
		if scan.inBlock || scan.seenBlock {
			return fmt.Errorf("%s contains duplicate or nested managed blocks", prettierIgnoreFile)
		}
		scan.inBlock = true
	case managedIgnoreEnd:
		if !scan.inBlock {
			return fmt.Errorf("%s contains an unmatched managed end marker", prettierIgnoreFile)
		}
		scan.inBlock, scan.seenBlock = false, true
	default:
		if !scan.inBlock {
			scan.kept = append(scan.kept, line)
		}
	}
	return nil
}

// VerifyManagedFormatterIgnore accepts only the converged managed block for active facets.
func VerifyManagedFormatterIgnore(existing string, documentationEnabled bool) error {
	merged, err := mergeManagedIgnore(existing, documentationEnabled)
	if err != nil {
		return err
	}
	if merged != existing {
		return fmt.Errorf("%s managed artifact block is stale", prettierIgnoreFile)
	}
	return nil
}

// VerifyFormatterIgnore checks the facet-aware inventory when Prettier is configured or an
// ignore file already exists. A configured formatter without the managed ignore file is stale.
func VerifyFormatterIgnore(ctx context.Context, repoPath string, documentationEnabled bool) error {
	full, err := repoFile(repoPath, prettierIgnoreFile)
	if err != nil {
		return err
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return err
	}
	required, err := formatterIgnoreRequired(ctx, repoPath)
	if err != nil {
		return err
	}
	if !exists {
		if required {
			return fmt.Errorf("%s is required when Prettier is configured", prettierIgnoreFile)
		}
		return nil
	}
	return VerifyManagedFormatterIgnore(string(data), documentationEnabled)
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
	applicable, err := formatterIgnoreApplicable(ctx, s, exists)
	if err != nil || !applicable {
		return err
	}
	documentationEnabled, err := documentationEnabledForSession(s)
	if err != nil {
		return fmt.Errorf("resolve documentation facet for formatter inventory: %w", err)
	}
	merged, err := mergeManagedIgnore(string(data), documentationEnabled)
	if err != nil {
		return err
	}
	return publishFormatterIgnore(ctx, s, full, data, merged, exists)
}

func formatterIgnoreApplicable(ctx context.Context, s *adoptSession, exists bool) (bool, error) {
	if exists {
		return true, nil
	}
	required, err := formatterIgnoreRequired(ctx, s.repoPath)
	if err != nil || required {
		return required, err
	}
	s.report.recordReconciled(prettierIgnoreFile,
		"No Prettier configuration found; managed artifacts not declared to a formatter this repository does not run")
	return false, nil
}

func publishFormatterIgnore(
	ctx context.Context, s *adoptSession, full string, data []byte, merged string, exists bool,
) error {
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

func formatterIgnoreRequired(ctx context.Context, repoPath string) (bool, error) {
	for index := 0; index < len(prettierConfigFiles); index++ {
		full, err := repoFile(repoPath, prettierConfigFiles[index])
		if err != nil {
			return false, err
		}
		_, exists, err := contextopt.ObserveSnapshot(ctx, full)
		if err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
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
