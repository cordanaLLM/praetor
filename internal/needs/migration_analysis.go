package needs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
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

// ErrSelfTargetMigration means the migration target is the selected framework's own module.
var ErrSelfTargetMigration = errors.New("needs: migration target is the selected framework module")

// SelfTargetMigrationError is returned before the target is scanned when the target and the
// selected framework resolve to one module: the same directory, directly or through a
// symlink, or a target whose go.mod declares the module the framework index names (another
// checkout of it, or a module-path selection of it). Scoring a framework against itself
// proposes replacing its own dependencies with its own packages: self-import cycles, and
// package paths nothing declares. A nested module that only shares the framework's path
// prefix is a different module and is analysed.
type SelfTargetMigrationError struct {
	// Target is the repository path the analysis was asked to score.
	Target string
	// Framework is the selected framework checkout, or the module named when none is checked out.
	Framework string
	// Module is the module path both declare; empty when the directory matched and the
	// framework names no module.
	Module string
	// SameDirectory reports that Target and Framework name one directory.
	SameDirectory bool
}

func (e *SelfTargetMigrationError) Error() string {
	if e.SameDirectory {
		return fmt.Sprintf("%s: target %s and framework %s are the same directory", ErrSelfTargetMigration, e.Target, e.Framework)
	}
	return fmt.Sprintf("%s: target %s and framework %s both declare module %s", ErrSelfTargetMigration, e.Target, e.Framework, e.Module)
}

func (*SelfTargetMigrationError) Unwrap() error { return ErrSelfTargetMigration }

type migrationAnalysis struct {
	framework *FrameworkIndex
	report    *RepoNeeds
}

func analyzeMigration(ctx context.Context, repoPath string, selected FrameworkSource, registry *AnalyzerRegistry) (*migrationAnalysis, error) {
	return analyzeMigrationWith(ctx, repoPath, selected, registry, func(framework *FrameworkIndex) (*RepoNeeds, error) {
		return ScanRepoWithFramework(ctx, repoPath, framework, registry)
	})
}

// analyzeMigrationWith inspects the selected framework, refuses a target at repoPath that is
// that framework's own module (SelfTargetMigrationError), and scores the target against it
// with scan. Fleet epic regeneration passes the fleet walk's repository, a
// single-repository epic or migration plan scans the path it was given. The analysis names
// the framework the row was scored against (RowFramework), so a host that configures only a
// non-go target plans against that target rather than against no framework.
func analyzeMigrationWith(ctx context.Context, repoPath string, selected FrameworkSource, registry *AnalyzerRegistry,
	scan func(framework *FrameworkIndex) (*RepoNeeds, error)) (*migrationAnalysis, error) {
	framework, err := inspectMigrationFramework(ctx, selected)
	if err != nil {
		return nil, fmt.Errorf("inspect migration framework: %w", err)
	}
	if err := refuseSelfTarget(repoPath, selected, framework); err != nil {
		return nil, err
	}
	report, err := scan(framework)
	if err != nil {
		return nil, fmt.Errorf("scan repository for migration: %w", err)
	}
	return &migrationAnalysis{framework: RowFramework(registry, report, framework), report: report}, nil
}

// refuseSelfTarget returns a SelfTargetMigrationError when the target at repoPath is the
// framework the index was inspected from: the checkout's own directory (util.SameDirectory
// decides by filesystem identity, so a symlink or another spelling of the path matches), or
// a root go.mod declaring exactly the index's module. The target's module is read the way
// the Go analyzer reads it (parseGoMod); a target without a root go.mod declares no module.
func refuseSelfTarget(repoPath string, selected FrameworkSource, framework *FrameworkIndex) error {
	named := selected.Checkout
	if named == "" {
		named = framework.Name
	}
	if framework.RootPath != "" && util.SameDirectory(repoPath, framework.RootPath) {
		return &SelfTargetMigrationError{Target: repoPath, Framework: named, Module: framework.Name, SameDirectory: true}
	}
	if framework.Name == "" {
		return nil
	}
	target, err := parseGoMod(filepath.Join(repoPath, "go.mod"))
	if errors.Is(err, ErrGoModMissing) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve migration target module: %w", err)
	}
	if target.modulePath != framework.Name {
		return nil
	}
	return &SelfTargetMigrationError{Target: repoPath, Framework: named, Module: target.modulePath}
}

// inspectMigrationFramework inspects the selected framework. A selected checkout value that
// is module-path shaped and not a directory names the framework by identity alone.
func inspectMigrationFramework(ctx context.Context, selected FrameworkSource) (*FrameworkIndex, error) {
	if ctx == nil {
		return nil, errors.New("needs: migration requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !config.IsModulePathShaped(selected.Checkout) || util.DirExists(selected.Checkout) {
		return InspectFramework(ctx, selected)
	}
	module, err := frameworkModuleIdentity(selected.Checkout)
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
	switch analysis.framework.Basis {
	case FrameworkSourceObserved:
	case FrameworkNotConfigured:
		blockers = append(blockers, nothingToRewrite+".")
	default:
		blockers = append(blockers, "Framework source availability is unobserved ("+analysis.framework.Basis+").")
	}
	if gaps := analysis.report.Readiness.GapDeps; gaps > 0 {
		blockers = append(blockers, fmt.Sprintf("%d dependencies have no selected framework mapping.", gaps))
	}
	if deep := analysis.report.UnscannedSubprojects; len(deep) > 0 {
		blockers = append(blockers, fmt.Sprintf("%d sub-projects sit more than %d directories below the repository root and were not scanned: %s.",
			len(deep), maxSubprojectDepth, strings.Join(deep, ", ")))
	}
	if failed := analysis.report.FailedSubprojects; len(failed) > 0 {
		blockers = append(blockers, fmt.Sprintf("%d sub-projects failed to scan and their demand is not counted: %s.",
			len(failed), formatSubprojectFailures(failed)))
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
