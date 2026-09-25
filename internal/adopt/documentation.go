package adopt

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
)

const (
	// DocumentationWorkflowFile is the hosted gate emitted for documentation-enabled repositories.
	DocumentationWorkflowFile = markdownassets.WorkflowFile
	// DocumentationStatusContext is the check name reconciled into branch protection.
	DocumentationStatusContext = markdownassets.StatusContext
)

// DocumentationEnabled reports whether the validated manifest facet inventory
// declares documentation governance.
func DocumentationEnabled(facets []string) (bool, error) {
	return config.DeclaresFacet(facets, "docs:seo-portal")
}

func documentationEnabledForSession(s *adoptSession) (bool, error) {
	if s.policy != nil && s.policy.Manifest != nil {
		return DocumentationEnabled(s.policy.Manifest.Facets)
	}
	return DocumentationEnabled(s.facets)
}

// DocumentationWorkflow renders the dedicated, required hosted documentation gate.
func DocumentationWorkflow() string {
	return `name: Praetor Documentation Governance

on:
  pull_request:
  push:

permissions:
  contents: read

jobs:
  documentation:
    name: Documentation Governance
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - name: Checkout source
        uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - name: Setup Node.js
        uses: actions/setup-node@v4
        with:
          node-version: "24"
          cache: npm
          cache-dependency-path: tools/markdownlint/package-lock.json
      - name: Verify public Markdown
        run: node tools/markdownlint/verify.mjs
`
}

func reconcileDocumentationGate(ctx context.Context, s *adoptSession) error {
	enabled, err := documentationEnabledForSession(s)
	if err != nil {
		return fmt.Errorf("resolve documentation facet: %w", err)
	}
	if !enabled {
		return removeDocumentationGate(ctx, s)
	}
	names := markdownassets.Names()
	for index := 0; index < len(names) && index < markdownassets.MaxAssets; index++ {
		data, err := markdownassets.Read(names[index])
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(filepath.Join(markdownassets.Directory, names[index]))
		if _, err := reconcileDocumentationTextAsset(ctx, s, scaffold{
			rel: rel, perm: filePerm, content: data, force: true,
			created:  "Scaffolded locked Markdown governance asset",
			verified: "Existing Markdown governance asset preserved; audit verifies canonical text",
		}); err != nil {
			return fmt.Errorf("reconcile %s: %w", rel, err)
		}
	}
	_, err = reconcileDocumentationTextAsset(ctx, s, scaffold{
		rel: DocumentationWorkflowFile, perm: filePerm, content: []byte(DocumentationWorkflow()), force: true,
		created:  "Scaffolded required documentation governance workflow",
		verified: "Existing documentation governance workflow preserved; audit verifies canonical text",
	})
	return err
}

func reconcileDocumentationTextAsset(ctx context.Context, s *adoptSession, sc scaffold) (bool, error) {
	full, err := repoFile(s.repoPath, sc.rel)
	if err != nil {
		return false, err
	}
	actual, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return false, err
	}
	if exists {
		equivalent, compareErr := util.CanonicalTextEquivalent(actual, sc.content)
		if compareErr != nil {
			return false, fmt.Errorf("%s has invalid line endings: %w", sc.rel, compareErr)
		}
		if equivalent {
			s.report.recordReconciled(sc.rel, sc.verified)
			return false, nil
		}
	}
	return s.scaffoldFile(sc)
}

// DocumentationAssetPaths returns every canonical text file owned only by the documentation facet.
func DocumentationAssetPaths() []string {
	paths := []string{DocumentationWorkflowFile}
	names := markdownassets.Names()
	for index := 0; index < len(names) && index < markdownassets.MaxAssets; index++ {
		paths = append(paths, filepath.ToSlash(filepath.Join(markdownassets.Directory, names[index])))
	}
	return paths
}

func canonicalDocumentationAsset(rel string) ([]byte, error) {
	if rel == DocumentationWorkflowFile {
		return []byte(DocumentationWorkflow()), nil
	}
	if filepath.ToSlash(filepath.Dir(rel)) != markdownassets.Directory {
		return nil, fmt.Errorf("unknown documentation asset %q", rel)
	}
	return markdownassets.Read(filepath.Base(rel))
}

// DocumentationAssetIsCanonical reports whether actual is Praetor's exact
// documentation asset, allowing one consistent checkout line-ending style.
func DocumentationAssetIsCanonical(rel string, actual []byte) (bool, error) {
	expected, err := canonicalDocumentationAsset(rel)
	if err != nil {
		return false, err
	}
	actualLF, valid := classifiableDocumentationText(actual)
	if !valid {
		return false, nil
	}
	expectedLF, valid := classifiableDocumentationText(expected)
	if !valid {
		return false, fmt.Errorf("canonical documentation asset %s has invalid line endings", rel)
	}
	return actualLF == expectedLF, nil
}

func classifiableDocumentationText(data []byte) (string, bool) {
	normalized, _, err := util.NormalizeLineEndingsStrict(string(data))
	return normalized, err == nil
}

type documentationRemoval struct {
	rel      string
	full     string
	expected []byte
}

func removeDocumentationGate(ctx context.Context, s *adoptSession) error {
	if err := preflightDocumentationDeprovision(ctx, s); err != nil {
		return err
	}
	removals, err := planDocumentationRemovals(ctx, s)
	if err != nil {
		return err
	}
	return applyDocumentationRemovals(ctx, s, removals)
}

func planDocumentationRemovals(ctx context.Context, s *adoptSession) ([]documentationRemoval, error) {
	paths := DocumentationAssetPaths()
	removals := make([]documentationRemoval, 0, len(paths))
	for index := 0; index < len(paths) && index <= markdownassets.MaxAssets; index++ {
		rel := paths[index]
		full, err := repoFile(s.repoPath, rel)
		if err != nil {
			return nil, err
		}
		expected, err := canonicalDocumentationAsset(rel)
		if err != nil {
			return nil, err
		}
		actual, exists, err := contextopt.ObserveSnapshot(ctx, full)
		if err != nil {
			return nil, fmt.Errorf("inspect disabled documentation asset %s: %w", rel, err)
		}
		if !exists {
			continue
		}
		equivalent, compareErr := util.CanonicalTextEquivalent(actual, expected)
		if compareErr != nil {
			return nil, fmt.Errorf("refusing to remove documentation asset %s with invalid line endings: %w",
				rel, compareErr)
		}
		if !equivalent {
			return nil, fmt.Errorf("refusing to remove drifted documentation asset %s", rel)
		}
		removals = append(removals, documentationRemoval{rel: rel, full: full, expected: actual})
	}
	return removals, nil
}

func applyDocumentationRemovals(ctx context.Context, s *adoptSession, removals []documentationRemoval) error {
	for index := 0; index < len(removals); index++ {
		removal := removals[index]
		if !s.opts.DryRun {
			if err := contextopt.RemoveSnapshot(ctx, removal.full, removal.expected); err != nil {
				return fmt.Errorf("remove disabled documentation asset %s: %w", removal.rel, err)
			}
		}
		s.report.recordReconciledAs(removal.rel, actionRemove,
			"Removed canonical documentation asset because docs:seo-portal is disabled")
	}
	return nil
}

func preflightDocumentationDeprovision(ctx context.Context, s *adoptSession) error {
	if err := preflightDocumentationRuleset(ctx, s); err != nil {
		return err
	}
	if err := preflightDocumentationMakefile(ctx, s); err != nil {
		return err
	}
	return preflightDocumentationFormatter(ctx, s)
}

func preflightDocumentationRuleset(ctx context.Context, s *adoptSession) error {
	// A declined branch ruleset is operator-owned: adoption never rewrites it, so the
	// documentation transition neither inspects it nor asks --force to rewrite it.
	rulesetDeclined, err := ArtifactDeclined(s.declined, "branch-ruleset")
	if err != nil || rulesetDeclined {
		return err
	}
	ruleset, err := repoFile(s.repoPath, rulesetFile)
	if err != nil {
		return err
	}
	rulesetData, rulesetExists, err := contextopt.ObserveSnapshot(ctx, ruleset)
	if err != nil {
		return fmt.Errorf("inspect branch ruleset before documentation disable: %w", err)
	}
	requiresDocumentation := false
	if rulesetExists {
		var rulesetErr error
		requiresDocumentation, rulesetErr = forge.RulesetRequiresStatusContext(rulesetData, DocumentationStatusContext)
		if rulesetErr != nil {
			return fmt.Errorf("inspect branch ruleset status contexts: %w", rulesetErr)
		}
	}
	if requiresDocumentation && !s.opts.Force {
		return fmt.Errorf("disabling documentation removes hosted context %q; rerun adopt --force",
			DocumentationStatusContext)
	}
	return nil
}

func preflightDocumentationMakefile(ctx context.Context, s *adoptSession) error {
	makefile, err := repoFile(s.repoPath, makefileName)
	if err != nil {
		return err
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, makefile)
	if err != nil {
		return fmt.Errorf("inspect documentation Makefile before disable: %w", err)
	}
	if exists {
		if _, _, err := removeDocumentationMakefileBlock(string(data)); err != nil {
			return err
		}
	}
	return nil
}

func preflightDocumentationFormatter(ctx context.Context, s *adoptSession) error {
	formatter, err := repoFile(s.repoPath, prettierIgnoreFile)
	if err != nil {
		return err
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, formatter)
	if err != nil {
		return fmt.Errorf("inspect formatter inventory before documentation disable: %w", err)
	}
	if exists {
		if _, err := mergeManagedIgnore(string(data), false); err != nil {
			return fmt.Errorf("formatter inventory blocks documentation disable: %w", err)
		}
	}
	return nil
}
