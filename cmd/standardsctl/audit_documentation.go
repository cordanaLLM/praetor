package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/util"
	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
)

func auditExactDocumentationFile(ctx context.Context, rootDir, rel string, expected []byte) error {
	path, err := util.ConfinePath(rootDir, rel)
	if err != nil {
		return fmt.Errorf("[FAIL] Documentation gate path %s is unsafe: %w", rel, err)
	}
	actual, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return fmt.Errorf("[FAIL] Documentation gate asset %s is missing or unreadable: %w", rel, err)
	}
	equivalent, compareErr := util.CanonicalTextEquivalent(actual, expected)
	if compareErr != nil {
		return fmt.Errorf("[FAIL] Documentation gate asset %s has invalid line endings: %w", rel, compareErr)
	}
	if !equivalent {
		return fmt.Errorf("[FAIL] Documentation gate asset %s differs from the locked Praetor asset; run 'praetorctl adopt --force'", rel)
	}
	return nil
}

func auditDocumentationGate(ctx context.Context, manifest *config.Manifest, rootDir string) error {
	documentationEnabled, err := adopt.DocumentationEnabled(manifest.Facets)
	if err != nil {
		return fmt.Errorf("[FAIL] Resolve documentation facet: %w", err)
	}
	// A declined branch ruleset is operator-owned (#408): adoption never writes it, so the
	// documentation gate neither requires its context there nor claims a context found there.
	rulesetDeclined, err := adopt.ManifestArtifactDeclined(manifest, "branch-ruleset")
	if err != nil {
		return fmt.Errorf("[FAIL] Resolve branch-ruleset adoption decline: %w", err)
	}
	if !documentationEnabled {
		return auditDocumentationGateDisabled(ctx, rootDir, rulesetDeclined)
	}
	count, err := auditDocumentationAssets(ctx, rootDir)
	if err != nil {
		return err
	}
	if err := auditDocumentationLocalWiring(ctx, rootDir); err != nil {
		return err
	}
	if err := auditDocumentationHostedWiring(ctx, manifest, rootDir, rulesetDeclined); err != nil {
		return err
	}
	hosted := "hosted required context"
	if rulesetDeclined {
		hosted = "hosted context; branch ruleset declined by adoption.decline"
	}
	fmt.Printf("[PASS] Locked documentation gate verified (%d assets, local verify-all, %s, private scratch ignores).\n",
		count, hosted)
	return nil
}

func auditDocumentationGateDisabled(ctx context.Context, rootDir string, rulesetDeclined bool) error {
	if err := auditDisabledDocumentationAssets(ctx, rootDir); err != nil {
		return err
	}
	if err := auditDisabledDocumentationMakefile(ctx, rootDir); err != nil {
		return err
	}
	if !rulesetDeclined {
		if err := auditDisabledDocumentationRuleset(ctx, rootDir); err != nil {
			return err
		}
	}
	if err := adopt.VerifyFormatterIgnore(ctx, rootDir, false); err != nil {
		return fmt.Errorf("[FAIL] Disabled documentation formatter inventory is stale: %w", err)
	}
	return nil
}

func auditDisabledDocumentationAssets(ctx context.Context, rootDir string) error {
	paths := adopt.DocumentationAssetPaths()
	for index := 0; index < len(paths) && index <= markdownassets.MaxAssets; index++ {
		rel := paths[index]
		path, err := util.ConfinePath(rootDir, rel)
		if err != nil {
			return fmt.Errorf("[FAIL] Disabled documentation path %s is unsafe: %w", rel, err)
		}
		actual, exists, err := contextopt.ObserveSnapshot(ctx, path)
		if err != nil {
			return fmt.Errorf("[FAIL] Inspect disabled documentation asset %s: %w", rel, err)
		}
		canonical := false
		if exists {
			canonical, err = adopt.DocumentationAssetIsCanonical(rel, actual)
			if err != nil {
				return fmt.Errorf("[FAIL] Classify disabled documentation asset %s: %w", rel, err)
			}
		}
		if canonical {
			return fmt.Errorf("[FAIL] Disabled documentation facet retains Praetor asset %s", rel)
		}
	}
	return nil
}

func auditDisabledDocumentationMakefile(ctx context.Context, rootDir string) error {
	makefile, exists, err := contextopt.ObserveSnapshot(ctx, filepath.Join(rootDir, "Makefile"))
	if err != nil {
		return fmt.Errorf("[FAIL] Inspect disabled documentation Makefile: %w", err)
	}
	markersPresent := false
	if exists {
		markersPresent, err = adopt.DocumentationMakefileMarkersPresent(string(makefile))
		if err != nil {
			return fmt.Errorf("[FAIL] Inspect disabled documentation Makefile markers: %w", err)
		}
	}
	if markersPresent {
		return fmt.Errorf("[FAIL] Disabled documentation facet retains the Praetor Makefile block")
	}
	return nil
}

func auditDisabledDocumentationRuleset(ctx context.Context, rootDir string) error {
	ruleset, exists, err := contextopt.ObserveSnapshot(ctx, filepath.Join(rootDir, ".github", "rulesets", "main.json"))
	if err != nil {
		return fmt.Errorf("[FAIL] Inspect disabled documentation ruleset: %w", err)
	}
	requiresDocumentation := false
	if exists {
		requiresDocumentation, err = forge.RulesetRequiresStatusContext(ruleset, adopt.DocumentationStatusContext)
		if err != nil {
			return fmt.Errorf("[FAIL] Inspect disabled documentation ruleset contexts: %w", err)
		}
	}
	if requiresDocumentation {
		return fmt.Errorf("[FAIL] Disabled documentation facet retains required context %q", adopt.DocumentationStatusContext)
	}
	return nil
}

func auditDocumentationAssets(ctx context.Context, rootDir string) (int, error) {
	names := markdownassets.Names()
	for index := 0; index < len(names) && index < markdownassets.MaxAssets; index++ {
		expected, err := markdownassets.Read(names[index])
		if err != nil {
			return 0, fmt.Errorf("[FAIL] Load canonical documentation asset: %w", err)
		}
		rel := filepath.ToSlash(filepath.Join(markdownassets.Directory, names[index]))
		if err := auditExactDocumentationFile(ctx, rootDir, rel, expected); err != nil {
			return 0, err
		}
	}
	if err := auditExactDocumentationFile(ctx, rootDir, adopt.DocumentationWorkflowFile,
		[]byte(adopt.DocumentationWorkflow())); err != nil {
		return 0, err
	}
	return len(names), nil
}

func auditDocumentationLocalWiring(ctx context.Context, rootDir string) error {
	makefile, err := contextopt.ReadSnapshot(ctx, filepath.Join(rootDir, "Makefile"))
	if err != nil {
		return fmt.Errorf("[FAIL] Read Makefile documentation wiring: %w", err)
	}
	normalizedMakefile, _, lineErr := util.NormalizeLineEndingsStrict(string(makefile))
	if lineErr != nil {
		return fmt.Errorf("[FAIL] Makefile documentation wiring has invalid line endings: %w", lineErr)
	}
	if strings.Count(normalizedMakefile, adopt.DocumentationMakefileBlock()) != 1 {
		return fmt.Errorf("[FAIL] Makefile does not attach the locked docs-lint target to verify-all")
	}
	ignore, err := contextopt.ReadSnapshot(ctx, filepath.Join(rootDir, ".gitignore"))
	if err != nil {
		return fmt.Errorf("[FAIL] Read .gitignore documentation privacy rules: %w", err)
	}
	normalized, _, ignoreLineErr := util.NormalizeLineEndingsStrict(string(ignore))
	if ignoreLineErr != nil {
		return fmt.Errorf("[FAIL] .gitignore documentation privacy rules have invalid line endings: %w", ignoreLineErr)
	}
	if !strings.HasSuffix(normalized, adopt.ManagedGitIgnoreBlock()) {
		return fmt.Errorf("[FAIL] .gitignore must end with the canonical Praetor private-artifact block")
	}
	for _, probe := range []string{".workingdir/PRAETOR-AUDIT-PROBE", ".workingdir2/PRAETOR-AUDIT-PROBE"} {
		if _, err := util.RunGit(ctx, rootDir, "check-ignore", "--no-index", "--", probe); err != nil {
			return fmt.Errorf("[FAIL] .gitignore does not effectively exclude %s: %w", probe, err)
		}
	}
	if err := adopt.VerifyFormatterIgnore(ctx, rootDir, true); err != nil {
		return fmt.Errorf("[FAIL] Documentation formatter inventory is stale: %w", err)
	}
	return nil
}

func auditDocumentationHostedWiring(
	ctx context.Context, manifest *config.Manifest, rootDir string, rulesetDeclined bool,
) error {
	contexts, err := forge.RequiredStatusContexts(ctx, rootDir)
	if err != nil {
		return fmt.Errorf("[FAIL] Discover hosted documentation context: %w", err)
	}
	if !slices.Contains(contexts, adopt.DocumentationStatusContext) {
		return fmt.Errorf("[FAIL] Hosted documentation workflow does not report required context %q",
			adopt.DocumentationStatusContext)
	}
	if rulesetDeclined {
		return nil
	}
	ruleset, err := contextopt.ReadSnapshot(ctx, filepath.Join(rootDir, ".github", "rulesets", "main.json"))
	if err != nil {
		return fmt.Errorf("[FAIL] Read documentation branch ruleset: %w", err)
	}
	policy := config.DefaultPolicy()
	policy.ApplyOverrides(manifest.Overrides)
	if err := validateSyncRuleset(ruleset, policy.BranchProtection, contexts); err != nil {
		return fmt.Errorf("[FAIL] Documentation required status context is not reconciled in the branch ruleset: %w", err)
	}
	return nil
}
