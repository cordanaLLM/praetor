package adopt

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/managedasset"
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
	return config.DeclaresFacet(facets, managedasset.DocumentationFacet)
}

func documentationEnabledForSession(s *adoptSession) (bool, error) {
	return facetDeclaredForSession(s, managedasset.DocumentationFacet)
}

// DocumentationWorkflow renders the dedicated, required hosted documentation gate.
func DocumentationWorkflow() string {
	return markdownassets.Workflow
}

// DocumentationFamilies returns the managed asset families docs:seo-portal enables, in
// registry order.
func DocumentationFamilies() []managedasset.Family {
	return managedasset.ForFacet(managedasset.DocumentationFacet)
}

// reconcileDocumentationGate emits every documentation family, and the attribute block their
// hashed files need, while the facet is enabled, and removes both once it is disabled.
func reconcileDocumentationGate(ctx context.Context, s *adoptSession) error {
	enabled, err := documentationEnabledForSession(s)
	if err != nil {
		return fmt.Errorf("resolve documentation facet: %w", err)
	}
	if !enabled {
		return removeDocumentationGate(ctx, s)
	}
	families := DocumentationFamilies()
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		if err := reconcileManagedFamily(ctx, s, families[index]); err != nil {
			return err
		}
	}
	return reconcileGitAttributes(ctx, s, DocumentationAttributes())
}

// DocumentationAssetPaths returns every canonical text file owned only by the documentation facet.
func DocumentationAssetPaths() []string {
	return managedPathsOf(DocumentationFamilies())
}

// DocumentationAssetIsCanonical reports whether actual is Praetor's exact
// documentation asset, allowing one consistent checkout line-ending style.
func DocumentationAssetIsCanonical(rel string, actual []byte) (bool, error) {
	family, owned, err := managedFamilyOwning(DocumentationFamilies(), rel)
	if err != nil {
		return false, err
	}
	if !owned {
		return false, fmt.Errorf("unknown documentation asset %q", rel)
	}
	return ManagedFileIsCanonical(family, rel, actual)
}

func removeDocumentationGate(ctx context.Context, s *adoptSession) error {
	if err := preflightDocumentationDeprovision(ctx, s); err != nil {
		return err
	}
	if err := removeManagedFamilies(ctx, s, DocumentationFamilies()); err != nil {
		return err
	}
	return reconcileGitAttributes(ctx, s, nil)
}

func preflightDocumentationDeprovision(ctx context.Context, s *adoptSession) error {
	if err := preflightDocumentationRuleset(ctx, s); err != nil {
		return err
	}
	if err := preflightDocumentationMakefile(ctx, s); err != nil {
		return err
	}
	if err := preflightDocumentationAttributes(ctx, s); err != nil {
		return err
	}
	return preflightDocumentationFormatter(ctx, s)
}

// preflightDocumentationAttributes refuses a disable, before anything is removed, while
// .gitattributes carries ambiguous attribute-block markers or an edited attribute block.
func preflightDocumentationAttributes(ctx context.Context, s *adoptSession) error {
	full, err := repoFile(s.repoPath, gitAttributesFile)
	if err != nil {
		return err
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return fmt.Errorf("inspect %s before documentation disable: %w", gitAttributesFile, err)
	}
	if exists {
		if _, err := mergeGitAttributes(string(data), nil); err != nil {
			return fmt.Errorf("%s blocks documentation disable: %w", gitAttributesFile, err)
		}
	}
	return nil
}

func preflightDocumentationRuleset(ctx context.Context, s *adoptSession) error {
	return preflightContextRemoval(ctx, s, DocumentationFamilies())
}

// preflightContextRemoval refuses, before anything is removed, an unforced facet disable that
// would drop the hosted context of one of families while the branch ruleset still requires it.
func preflightContextRemoval(ctx context.Context, s *adoptSession, families []managedasset.Family) error {
	// A declined branch ruleset is operator-owned: adoption never rewrites it, so the facet
	// transition neither inspects it nor asks --force to rewrite it.
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
		return fmt.Errorf("inspect branch ruleset before a facet disable: %w", err)
	}
	if !rulesetExists {
		return nil
	}
	return refuseContextRemoval(rulesetData, s.opts.Force, families)
}

// refuseContextRemoval fails an unforced disable while the branch ruleset still requires the
// hosted context of one of families. The ruleset is parsed under --force too, so a malformed
// one is reported rather than rewritten blind.
func refuseContextRemoval(ruleset []byte, force bool, families []managedasset.Family) error {
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		statusContext := families[index].StatusContext
		if statusContext == "" {
			continue
		}
		required, err := forge.RulesetRequiresStatusContext(ruleset, statusContext)
		if err != nil {
			return fmt.Errorf("inspect branch ruleset status contexts: %w", err)
		}
		if required && !force {
			return fmt.Errorf("disabling %s removes hosted context %q; rerun adopt --force",
				families[index].Kind, statusContext)
		}
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
		if _, err := mergeManagedIgnore(string(data), nil); err != nil {
			return fmt.Errorf("formatter inventory blocks documentation disable: %w", err)
		}
	}
	return nil
}
