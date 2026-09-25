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

// documentationDeclines records which declinable adoption steps behind the documentation
// gate's surfaces the manifest declines. Adoption skips a declined step (#408), so its file is
// operator-owned: the gate neither requires the Praetor bytes adoption would have written there
// nor claims Praetor bytes the operator keeps there. Undeclined steps stay fail-closed.
type documentationDeclines struct {
	ruleset, makefile, gitIgnore, formatter bool
}

func resolveDocumentationDeclines(manifest *config.Manifest) (documentationDeclines, error) {
	var declines documentationDeclines
	for _, step := range []struct {
		name     string
		declined *bool
	}{
		{"branch-ruleset", &declines.ruleset},
		{"makefile", &declines.makefile},
		{"git-ignore", &declines.gitIgnore},
		{"formatter-ignore", &declines.formatter},
	} {
		declined, err := adopt.ManifestArtifactDeclined(manifest, step.name)
		if err != nil {
			return documentationDeclines{}, fmt.Errorf("[FAIL] Resolve %s adoption decline: %w", step.name, err)
		}
		*step.declined = declined
	}
	return declines, nil
}

// summary names what the enabled gate verified, and which surfaces it left to the operator.
func (declines documentationDeclines) summary() string {
	parts := []string{"local verify-all", "hosted required context", "private scratch ignores", "formatter inventory"}
	if declines.makefile {
		parts[0] = "Makefile declined by adoption.decline"
	}
	if declines.ruleset {
		parts[1] = "hosted context; branch ruleset declined by adoption.decline"
	}
	if declines.gitIgnore {
		parts[2] = "effective private scratch ignores; .gitignore declined by adoption.decline"
	}
	if declines.formatter {
		parts[3] = "formatter inventory declined by adoption.decline"
	}
	return strings.Join(parts, ", ")
}

func auditDocumentationGate(ctx context.Context, manifest *config.Manifest, rootDir string) error {
	documentationEnabled, err := adopt.DocumentationEnabled(manifest.Facets)
	if err != nil {
		return fmt.Errorf("[FAIL] Resolve documentation facet: %w", err)
	}
	declines, err := resolveDocumentationDeclines(manifest)
	if err != nil {
		return err
	}
	if !documentationEnabled {
		return auditDocumentationGateDisabled(ctx, rootDir, declines)
	}
	count, err := auditDocumentationAssets(ctx, rootDir)
	if err != nil {
		return err
	}
	if err := auditDocumentationLocalWiring(ctx, rootDir, declines); err != nil {
		return err
	}
	if err := auditDocumentationHostedWiring(ctx, manifest, rootDir, declines.ruleset); err != nil {
		return err
	}
	fmt.Printf("[PASS] Locked documentation gate verified (%d assets, %s).\n", count, declines.summary())
	return nil
}

func auditDocumentationGateDisabled(ctx context.Context, rootDir string, declines documentationDeclines) error {
	if err := auditDisabledDocumentationAssets(ctx, rootDir); err != nil {
		return err
	}
	if !declines.makefile {
		if err := auditDisabledDocumentationMakefile(ctx, rootDir); err != nil {
			return err
		}
	}
	if !declines.ruleset {
		if err := auditDisabledDocumentationRuleset(ctx, rootDir); err != nil {
			return err
		}
	}
	if declines.formatter {
		return nil
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

func auditDocumentationLocalWiring(ctx context.Context, rootDir string, declines documentationDeclines) error {
	if !declines.makefile {
		if err := auditDocumentationMakefileWiring(ctx, rootDir); err != nil {
			return err
		}
	}
	if err := auditDocumentationScratchIgnores(ctx, rootDir, declines.gitIgnore); err != nil {
		return err
	}
	if declines.formatter {
		return nil
	}
	if err := adopt.VerifyFormatterIgnore(ctx, rootDir, true); err != nil {
		return fmt.Errorf("[FAIL] Documentation formatter inventory is stale: %w", err)
	}
	return nil
}

func auditDocumentationMakefileWiring(ctx context.Context, rootDir string) error {
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
	return nil
}

// auditDocumentationScratchIgnores proves both private scratch roots are ignored. A declined
// git-ignore step leaves the rules' wording to the operator, never the privacy invariant the
// private-scratch link policy rests on, so the effective check runs either way.
func auditDocumentationScratchIgnores(ctx context.Context, rootDir string, declined bool) error {
	remedy := "run 'praetorctl adopt'"
	if declined {
		remedy = "git-ignore is declined by adoption.decline; add the rule to the operator-owned .gitignore"
	} else if err := auditManagedGitIgnoreBlock(ctx, rootDir); err != nil {
		return err
	}
	for _, probe := range []string{".workingdir/PRAETOR-AUDIT-PROBE", ".workingdir2/PRAETOR-AUDIT-PROBE"} {
		if _, err := util.RunGit(ctx, rootDir, "check-ignore", "--no-index", "--", probe); err != nil {
			return fmt.Errorf("[FAIL] .gitignore does not effectively exclude %s (%s): %w", probe, remedy, err)
		}
	}
	return nil
}

func auditManagedGitIgnoreBlock(ctx context.Context, rootDir string) error {
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
