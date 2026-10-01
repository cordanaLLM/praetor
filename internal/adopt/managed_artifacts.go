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
	"github.com/cordanaLLM/praetor/internal/managedasset"
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

// managedArtifacts returns every path adoption writes and later compares as owned content: the
// fixed set and the managed paths of families, the managed asset families the active facets
// enable (EnabledManagedFamilies).
//
// This is the single source for that set. The paths were previously only constants scattered
// across the package, which is why nothing could enumerate them for any other purpose.
func managedArtifacts(families []managedasset.Family) []string {
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
	paths = append(paths, managedPathsOf(families)...)
	sort.Strings(paths)
	return paths
}

const (
	// managedFormatterHeader explains the block to whoever opens the ignore file.
	managedFormatterHeader = "# praetorctl audit owns this canonical content. A formatter rewrite fails;\n" +
		"# documentation text alone permits consistent LF/CRLF checkout conversion.\n"
	// historicalFormatterHeader is the comment adoption wrote before the documentation facet
	// joined the inventory. Only the comment changed, so audit still accepts it above the
	// pre-documentation inventory rather than failing every earlier adopter's ignore file as
	// stale; the next adopt run rewrites it to managedFormatterHeader.
	historicalFormatterHeader = "# praetorctl audit compares these byte for byte. A formatter that rewrites\n" +
		"# them fails the gate with an error that reads like a hand edit.\n"
)

// ManagedFormatterIgnoreBlock renders the exact formatter inventory for the managed asset
// families the active facets enable.
func ManagedFormatterIgnoreBlock(families []managedasset.Family) string {
	return renderFormatterIgnoreBlock(managedFormatterHeader, families)
}

// formatterTailBlock is the managed-artifact block adoption owns at the tail of the
// formatter's ignore file.
func formatterTailBlock() managedTailBlock {
	return managedTailBlock{
		file: prettierIgnoreFile, begin: managedIgnoreBegin, end: managedIgnoreEnd,
		maxLines: maxIgnoreLines, duplicate: "duplicate or nested managed blocks",
	}
}

func renderFormatterIgnoreBlock(header string, families []managedasset.Family) string {
	lines := strings.Split(strings.TrimSuffix(header, "\n"), "\n")
	return formatterTailBlock().render(append(lines, managedArtifacts(families)...))
}

// mergeManagedIgnore returns the ignore file content with the managed block present exactly
// once, preserving everything the adopter wrote around it.
//
// A re-run replaces the block rather than appending a second one, so the file converges instead
// of growing. An adopter's own entries are never touched: adoption owns the delimited region and
// nothing else.
func mergeManagedIgnore(existing string, families []managedasset.Family) (string, error) {
	return mergeFormatterIgnoreBlock(existing, ManagedFormatterIgnoreBlock(families))
}

func mergeFormatterIgnoreBlock(existing, block string) (string, error) {
	return formatterTailBlock().merge(existing, block)
}

// VerifyManagedFormatterIgnore accepts only the converged managed block for the enabled
// families, or, while no family is enabled, the historical rendering of the pre-documentation
// inventory that differs only in its comment.
func VerifyManagedFormatterIgnore(existing string, families []managedasset.Family) error {
	merged, err := mergeManagedIgnore(existing, families)
	if err != nil {
		return err
	}
	if merged == existing {
		return nil
	}
	if len(families) == 0 {
		historical, err := mergeFormatterIgnoreBlock(existing, renderFormatterIgnoreBlock(historicalFormatterHeader, nil))
		if err != nil {
			return err
		}
		if historical == existing {
			return nil
		}
	}
	return fmt.Errorf("%s managed artifact block is stale", prettierIgnoreFile)
}

// VerifyFormatterIgnore checks the facet-aware inventory when Prettier is configured or an
// ignore file already exists. A configured formatter without the managed ignore file is stale.
func VerifyFormatterIgnore(ctx context.Context, repoPath string, families []managedasset.Family) error {
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
	return VerifyManagedFormatterIgnore(string(data), families)
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
	families, err := enabledManagedFamiliesForSession(ctx, s)
	if err != nil {
		return fmt.Errorf("resolve the managed families for the formatter inventory: %w", err)
	}
	merged, err := mergeManagedIgnore(string(data), families)
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
