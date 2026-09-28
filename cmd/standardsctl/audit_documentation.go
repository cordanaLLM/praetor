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
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/util"
)

// auditExactManagedFile requires the managed file at rel to hold family's canonical text, one
// consistent checkout line-ending style allowed. An earlier Praetor text of rel fails as well,
// naming plain adoption, which refreshes it, instead of --force.
func auditExactManagedFile(ctx context.Context, rootDir string, family managedasset.Family, rel string) error {
	gate := familyGate(family)
	expected, _, err := family.Canonical(rel)
	if err != nil {
		return fmt.Errorf("[FAIL] Load canonical %s asset: %w", family.Kind, err)
	}
	path, err := util.ConfinePath(rootDir, rel)
	if err != nil {
		return fmt.Errorf("[FAIL] %s path %s is unsafe: %w", gate, rel, err)
	}
	actual, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return fmt.Errorf("[FAIL] %s asset %s is missing or unreadable: %w", gate, rel, err)
	}
	equivalent, compareErr := util.CanonicalTextEquivalent(actual, expected)
	if compareErr != nil {
		return fmt.Errorf("[FAIL] %s asset %s has invalid line endings: %w", gate, rel, compareErr)
	}
	if equivalent {
		return nil
	}
	if family.PriorText(rel, actual) {
		return fmt.Errorf("[FAIL] %s asset %s holds an earlier Praetor text; run 'praetorctl adopt' to refresh it", gate, rel)
	}
	return fmt.Errorf("[FAIL] %s asset %s differs from the locked Praetor asset; run 'praetorctl adopt --force'", gate, rel)
}

// familyGate names a family's gate in audit failures: "Documentation gate" for Kind
// "documentation".
func familyGate(family managedasset.Family) string {
	if family.Kind == "" {
		return "Managed gate"
	}
	return strings.ToUpper(family.Kind[:1]) + family.Kind[1:] + " gate"
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
	parts := []string{"local verify-all", "hosted required context", "private scratch ignores", "formatter inventory", "checkout attributes"}
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

// auditDocumentationGate verifies the locked documentation gate. branch is the effective
// branch protection the audit resolved, the policy adopt rendered the ruleset from.
func auditDocumentationGate(ctx context.Context, manifest *config.Manifest, rootDir string, branch config.BranchProtectionPolicy) error {
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
	if err := auditDocumentationHostedWiring(ctx, manifest, branch, rootDir, declines.ruleset); err != nil {
		return err
	}
	fmt.Printf("[PASS] Locked documentation gate verified (%d assets, %s).\n", count, declines.summary())
	auditVendoredLicenses(ctx, rootDir, adopt.DocumentationFamilies())
	if manifest.Documentation != nil {
		fmt.Printf("[PASS] Documentation gate settings from .standards.yaml: %s.\n", documentationSettingsSummary(manifest.Documentation))
	}
	return nil
}

// documentationSettingsSummary records the bounds and style exclusions a repository declares
// for the gate, which reads them from .standards.yaml at run time (#532, #534). LoadManifest has
// validated them against the gate's ranges and glob rules.
func documentationSettingsSummary(policy *config.DocumentationPolicy) string {
	exclusions := "no style exclusions"
	if len(policy.StyleExclude) > 0 {
		exclusions = fmt.Sprintf("%d style exclusions (%s)", len(policy.StyleExclude), strings.Join(policy.StyleExclude, ", "))
	}
	return fmt.Sprintf("max_files %d, max_file_bytes %d, %s", policy.EffectiveMaxFiles(), policy.EffectiveMaxFileBytes(), exclusions)
}

func auditDocumentationGateDisabled(ctx context.Context, rootDir string, declines documentationDeclines) error {
	if err := auditDisabledDocumentationAssets(ctx, rootDir); err != nil {
		return err
	}
	if err := auditDisabledDocumentationAttributes(ctx, rootDir); err != nil {
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
	families := adopt.DocumentationFamilies()
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		if err := auditDisabledManagedFamily(ctx, rootDir, families[index]); err != nil {
			return err
		}
	}
	return nil
}

// auditDisabledManagedFamily fails while a disabled family's managed path still holds the
// canonical text or an earlier Praetor text of that path; an operator's own file at the same
// path is not Praetor's and passes.
func auditDisabledManagedFamily(ctx context.Context, rootDir string, family managedasset.Family) error {
	paths := family.ManagedPaths()
	for index := 0; index < len(paths) && index <= family.MaxAssets; index++ {
		rel := paths[index]
		path, err := util.ConfinePath(rootDir, rel)
		if err != nil {
			return fmt.Errorf("[FAIL] Disabled %s path %s is unsafe: %w", family.Kind, rel, err)
		}
		actual, exists, err := contextopt.ObserveSnapshot(ctx, path)
		if err != nil {
			return fmt.Errorf("[FAIL] Inspect disabled %s asset %s: %w", family.Kind, rel, err)
		}
		if !exists {
			continue
		}
		praetors, err := adopt.ManagedFileIsPraetors(family, rel, actual)
		if err != nil {
			return fmt.Errorf("[FAIL] Classify disabled %s asset %s: %w", family.Kind, rel, err)
		}
		if praetors {
			return fmt.Errorf("[FAIL] Disabled %s facet retains Praetor asset %s", family.Kind, rel)
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
	if !exists {
		return nil
	}
	families := adopt.DocumentationFamilies()
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		family := families[index]
		if family.StatusContext == "" {
			continue
		}
		required, err := forge.RulesetRequiresStatusContext(ruleset, family.StatusContext)
		if err != nil {
			return fmt.Errorf("[FAIL] Inspect disabled %s ruleset contexts: %w", family.Kind, err)
		}
		if required {
			return fmt.Errorf("[FAIL] Disabled %s facet retains required context %q", family.Kind, family.StatusContext)
		}
	}
	return nil
}

// auditDocumentationAssets compares every enabled documentation family byte for byte and
// returns how many assets, workflows not counted, it verified.
func auditDocumentationAssets(ctx context.Context, rootDir string) (int, error) {
	families := adopt.DocumentationFamilies()
	count := 0
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		verified, err := auditManagedFamily(ctx, rootDir, families[index])
		if err != nil {
			return 0, err
		}
		count += verified
	}
	return count, nil
}

// auditManagedFamily compares every asset of family, then its workflow, with the canonical
// text and returns the number of assets verified.
func auditManagedFamily(ctx context.Context, rootDir string, family managedasset.Family) (int, error) {
	names := family.Names()
	for index := 0; index < len(names) && index < family.MaxAssets; index++ {
		if err := auditExactManagedFile(ctx, rootDir, family, family.AssetPath(names[index])); err != nil {
			return 0, err
		}
	}
	if family.WorkflowFile != "" {
		if err := auditExactManagedFile(ctx, rootDir, family, family.WorkflowFile); err != nil {
			return 0, err
		}
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
	if err := auditManagedGitAttributesBlock(ctx, rootDir); err != nil {
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
		return fmt.Errorf("[FAIL] Makefile does not attach the locked docs-lint and docs-figures targets to verify-all; run 'praetorctl adopt'")
	}
	return nil
}

// auditManagedGitAttributesBlock requires .gitattributes to end with the attribute block of the
// enabled documentation families, which keeps their hashed files unconverted on every platform.
// Nothing is required while no family declares a rule.
func auditManagedGitAttributesBlock(ctx context.Context, rootDir string) error {
	block := adopt.ManagedGitAttributesBlock(adopt.DocumentationAttributes())
	if block == "" {
		return nil
	}
	attributes, err := contextopt.ReadSnapshot(ctx, filepath.Join(rootDir, ".gitattributes"))
	if err != nil {
		return fmt.Errorf("[FAIL] Read .gitattributes documentation attributes (run 'praetorctl adopt'): %w", err)
	}
	normalized, _, lineErr := util.NormalizeLineEndingsStrict(string(attributes))
	if lineErr != nil {
		return fmt.Errorf("[FAIL] .gitattributes has invalid line endings: %w", lineErr)
	}
	// The markers are read as adoption reads them, so a second block or a stray marker fails
	// here as it fails adoption, even when the file still ends with the canonical block.
	if _, err := adopt.GitAttributesBlockPresent(normalized); err != nil {
		return fmt.Errorf("[FAIL] .gitattributes attribute block is ambiguous (repair it, then run 'praetorctl adopt'): %w", err)
	}
	if !strings.HasSuffix(normalized, block) {
		return fmt.Errorf("[FAIL] .gitattributes must end with the canonical Praetor attribute block; run 'praetorctl adopt'")
	}
	return nil
}

// auditDisabledDocumentationAttributes fails while a disabled facet's .gitattributes still carries
// the Praetor attribute block.
func auditDisabledDocumentationAttributes(ctx context.Context, rootDir string) error {
	attributes, exists, err := contextopt.ObserveSnapshot(ctx, filepath.Join(rootDir, ".gitattributes"))
	if err != nil {
		return fmt.Errorf("[FAIL] Inspect disabled documentation .gitattributes: %w", err)
	}
	if !exists {
		return nil
	}
	present, err := adopt.GitAttributesBlockPresent(string(attributes))
	if err != nil {
		return fmt.Errorf("[FAIL] Inspect disabled documentation .gitattributes block: %w", err)
	}
	if present {
		return fmt.Errorf("[FAIL] Disabled documentation facet retains the Praetor .gitattributes block")
	}
	return nil
}

// auditDocumentationScratchIgnores proves both private scratch roots are ignored. A declined
// git-ignore step leaves the rules' wording to the operator, never the privacy invariant the
// private-scratch link policy rests on, so the effective check runs either way.
//
// The answer comes from util.GitIgnoredPaths, which reads the repository's own ignore files
// with the global and system configuration isolated: an operator's personal excludes file
// hides scratch on one machine only, so it cannot prove the repository keeps it private.
func auditDocumentationScratchIgnores(ctx context.Context, rootDir string, declined bool) error {
	remedy := "run 'praetorctl adopt'"
	if declined {
		remedy = "git-ignore is declined by adoption.decline; add the rule to the operator-owned .gitignore"
	} else if err := auditManagedGitIgnoreBlock(ctx, rootDir); err != nil {
		return err
	}
	probes := []string{".workingdir/PRAETOR-AUDIT-PROBE", ".workingdir2/PRAETOR-AUDIT-PROBE"}
	ignored, err := util.GitIgnoredPaths(ctx, rootDir, probes, true)
	if err != nil {
		return fmt.Errorf("[FAIL] cannot prove .gitignore effectively excludes the private scratch roots (%s): %w", remedy, err)
	}
	for _, probe := range probes {
		if !slices.Contains(ignored, probe) {
			return fmt.Errorf("[FAIL] .gitignore does not effectively exclude %s (%s)", probe, remedy)
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

// auditDocumentationHostedWiring checks that the hosted documentation workflows report their
// contexts and, unless declined, that the ruleset is the one protection renders for the
// repository's default branch (forge.RepositoryDefaultBranch) and those contexts.
func auditDocumentationHostedWiring(
	ctx context.Context, manifest *config.Manifest, protection config.BranchProtectionPolicy, rootDir string, rulesetDeclined bool,
) error {
	contexts, err := forge.RequiredStatusContexts(ctx, rootDir)
	if err != nil {
		return fmt.Errorf("[FAIL] Discover hosted documentation context: %w", err)
	}
	families := adopt.DocumentationFamilies()
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		family := families[index]
		if family.StatusContext != "" && !slices.Contains(contexts, family.StatusContext) {
			return fmt.Errorf("[FAIL] Hosted %s workflow does not report required context %q",
				family.Kind, family.StatusContext)
		}
	}
	if rulesetDeclined {
		return nil
	}
	ruleset, err := contextopt.ReadSnapshot(ctx, filepath.Join(rootDir, ".github", "rulesets", "main.json"))
	if err != nil {
		return fmt.Errorf("[FAIL] Read documentation branch ruleset: %w", err)
	}
	defaultBranch, err := forge.RepositoryDefaultBranch(ctx, rootDir, manifest)
	if err != nil {
		return fmt.Errorf("[FAIL] Documentation branch ruleset audit failed: %w", err)
	}
	if err := forge.ValidateRepositoryRuleset(ruleset, defaultBranch, protection, contexts); err != nil {
		return fmt.Errorf("[FAIL] Documentation required status context is not reconciled in the branch ruleset: %w", err)
	}
	return nil
}
