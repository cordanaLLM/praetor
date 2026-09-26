package needs

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// FrameworkIdentityDeclared names a module without claiming source/catalog coverage.
const FrameworkIdentityDeclared = "identity-declared"

// ErrUnverifiedMigration means no trusted version/compatibility evidence admits a rewrite.
var ErrUnverifiedMigration = errors.New("needs: migration requires verified module version and API compatibility evidence")

// UnverifiedMigrationError is returned before any application commands or writes.
// Caller-supplied plan metadata cannot substitute for an evidence validator.
type UnverifiedMigrationError struct{}

func (*UnverifiedMigrationError) Error() string { return ErrUnverifiedMigration.Error() }
func (*UnverifiedMigrationError) Unwrap() error { return ErrUnverifiedMigration }

type migrationAnalysis struct {
	framework *FrameworkIndex
	report    *RepoNeeds
}

func analyzeMigration(ctx context.Context, repoPath, selected string) (*migrationAnalysis, error) {
	return analyzeMigrationWith(ctx, selected, func(framework *FrameworkIndex) (*RepoNeeds, error) {
		return ScanRepoWithFramework(ctx, repoPath, framework)
	})
}

// analyzeMigrationWith inspects the selected framework and scores a repository against it
// with scan. Fleet epic regeneration passes the fleet walk's repository, a
// single-repository epic or migration plan scans the path it was given.
func analyzeMigrationWith(ctx context.Context, selected string,
	scan func(framework *FrameworkIndex) (*RepoNeeds, error)) (*migrationAnalysis, error) {
	framework, err := inspectMigrationFramework(ctx, selected)
	if err != nil {
		return nil, fmt.Errorf("inspect migration framework: %w", err)
	}
	report, err := scan(framework)
	if err != nil {
		return nil, fmt.Errorf("scan repository for migration: %w", err)
	}
	return &migrationAnalysis{framework: framework, report: report}, nil
}

func inspectMigrationFramework(ctx context.Context, selected string) (*FrameworkIndex, error) {
	if ctx == nil {
		return nil, errors.New("needs: migration requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !isModulePathShaped(selected) || util.DirExists(selected) {
		return InspectFramework(ctx, selected)
	}
	module, err := frameworkModuleIdentity(selected)
	if err != nil {
		return nil, err
	}
	return &FrameworkIndex{
		Name:         module,
		Version:      "unverified",
		Basis:        FrameworkIdentityDeclared,
		Packages:     map[string]FrameworkPackage{},
		Capabilities: map[CapabilityKey][]string{},
	}, nil
}

func migrationBlockers(analysis *migrationAnalysis) []string {
	blockers := []string{
		"No verified module version is bound to the selected framework source.",
		"Replacement API compatibility and consumer compilation/tests are unverified.",
		"Executable migration admission is unavailable until a real evidence validator exists.",
	}
	if analysis.framework.Basis != FrameworkSourceObserved {
		blockers = append(blockers, "Framework source availability is unobserved ("+analysis.framework.Basis+").")
	}
	if gaps := analysis.report.Readiness.GapDeps; gaps > 0 {
		blockers = append(blockers, fmt.Sprintf("%d dependencies have no selected framework mapping.", gaps))
	}
	if deep := analysis.report.UnscannedSubprojects; len(deep) > 0 {
		blockers = append(blockers, fmt.Sprintf("%d sub-projects sit more than %d directories below the repository root and were not scanned: %s.",
			len(deep), maxSubprojectDepth, strings.Join(deep, ", ")))
	}
	return blockers
}

func writeMigrationEvidence(sb *strings.Builder, plan *MigrationPlan) {
	writef(sb, "- **Status**: %s; application blocked\n", plan.Status)
	writef(sb, "- **Framework version**: %s\n", plan.FrameworkVersion)
	writef(sb, "- **Coverage basis**: %s; builds and tests not run\n", plan.CoverageBasis)
	for _, blocker := range plan.Blockers {
		writef(sb, "- **Blocker**: %s\n", blocker)
	}
	sb.WriteString("\n")
}
