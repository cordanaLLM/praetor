package flavor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
)

// pinRemedy is the remedy every nothing-matched refusal names for a caller that has no
// --flavor flag: the manifest setting that replaces detection (#1103). It carries no flag
// name, because gate run and the generated pre-push hook take none.
const pinRemedy = "; pin the flavor with a flavors entry in .standards.yaml"

// ScopedPinRemedy is the remedy for a scoped or multiple pin: such pins get no scaffold, and the
// files that belong at the repository root differ from those that belong under the pin path. The
// root list is built from repositoryLevelPrefixes and repositoryLevelFiles, the lists the audit
// reads, so the text cannot drift from what the audit does.
func ScopedPinRemedy() string {
	root := append(slices.Clone(repositoryLevelPrefixes), repositoryLevelFiles...)
	return "pins scoped to a directory or several pins get no scaffold; keep the repository-level files (" +
		strings.Join(root, ", ") + ") at the repository root and the stack files " +
		"(for a Go service: go.mod, .golangci.yml, .gosec.json, Dockerfile) under each pinned path; " +
		"to scaffold one flavor, pin it once without a path (the repository root)"
}

// ErrPinNotScaffoldable refuses a scaffold that the manifest's flavors pins cannot describe as
// one flavor at the repository root: a pin scoped to a directory, or several pins. The audit
// measures every pin; a scaffold writes one flavor at the root, so it takes the choice from the
// operator (--flavor) instead of scaffolding a flavor the audit never measures.
var ErrPinNotScaffoldable = errors.New("flavor: the flavors pins in .standards.yaml are not one flavor at the repository root")

// Target is one flavor and the repository-relative directory it is audited against.
type Target struct {
	Flavor string `json:"flavor"`
	// Path is "." for the repository root.
	Path string `json:"path"`
}

// ResolveTargets names the flavors a repository is audited and scaffolded against, once, for
// every caller: `flavor audit`, `flavor apply`, adoption, `gate run`, the pre-push hook that
// runs the audit and the Hindsight distiller.
//
// A repository pinning flavors in .standards.yaml (config.FlavorPin) gets exactly those, each
// with its directory; an unknown flavor name or a missing directory is refused, never skipped.
// A repository without pins gets the one flavor Resolve detects, at its root. Detection errors
// pass through (ErrNoFlavorMatched, ErrFlavorNotApplicable) with the pin setting named as the
// remedy. An unreadable manifest is refused: dropping its pins would audit a different flavor
// than the one declared.
func ResolveTargets(repoPath string) ([]Target, error) {
	return resolveTargetsWith(repoPath, Resolve)
}

// ResolveTargetsForProfile is ResolveTargets for a caller that has already decided the profile,
// such as adoption, which records it in the manifest it writes: pins win, else the flavor of
// profile that matches the repository (ResolveForProfile).
func ResolveTargetsForProfile(repoPath, profile string) ([]Target, error) {
	return resolveTargetsWith(repoPath, func(path string) (string, error) {
		return ResolveForProfile(path, profile)
	})
}

// resolveTargetsWith is the one resolver: the manifest's pins, else the flavor detect names.
func resolveTargetsWith(repoPath string, detect func(string) (string, error)) ([]Target, error) {
	manifest, err := config.LoadManifest(filepath.Join(repoPath, ".standards.yaml"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("resolve flavor pins: %w", err)
	}
	if manifest != nil && len(manifest.Flavors) > 0 {
		return pinnedTargets(repoPath, manifest.Flavors)
	}
	name, err := detect(repoPath)
	if errors.Is(err, ErrNoFlavorMatched) {
		return nil, fmt.Errorf("%w%s", err, pinRemedy)
	}
	if err != nil {
		return nil, err
	}
	return []Target{{Flavor: name, Path: "."}}, nil
}

// SingleRootFlavor returns the one flavor targets scaffold at the repository root, or
// ErrPinNotScaffoldable when they are several or scoped to a directory.
func SingleRootFlavor(targets []Target) (string, error) {
	if len(targets) != 1 || targets[0].Path != "." {
		return "", fmt.Errorf("%w: %d target(s); %s", ErrPinNotScaffoldable, len(targets), ScopedPinRemedy())
	}
	return targets[0].Flavor, nil
}

// IsScaffoldSkip reports whether err means a scaffold has no flavor to write and says so, rather
// than failing: the audit's own not-applicable decision (IsNotApplicable), a repository no profile
// classifies (ErrNoProfile: nothing to scaffold, though the audit still refuses it) or a pin the
// root scaffold cannot express (ErrPinNotScaffoldable). Every other resolution error, such as a
// bad pin, is a failure for the caller to report.
func IsScaffoldSkip(err error) bool {
	return IsNotApplicable(err) || errors.Is(err, ErrNoProfile) || errors.Is(err, ErrPinNotScaffoldable)
}

// pinnedTargets validates each pin against the registry and the working tree.
func pinnedTargets(repoPath string, pins []config.FlavorPin) ([]Target, error) {
	targets := make([]Target, 0, len(pins))
	for i := 0; i < len(pins) && i < config.MaxFlavorPins; i++ {
		name := pins[i].FlavorName()
		if _, err := Get(name); err != nil {
			return nil, fmt.Errorf("flavors[%d] in .standards.yaml: %w (praetorctl flavor list names each)", i, err)
		}
		rel := pins[i].CleanPath()
		if err := pinDirectory(repoPath, rel); err != nil {
			return nil, fmt.Errorf("flavors[%d] in .standards.yaml: path %q for flavor %s: %w", i, rel, name, err)
		}
		targets = append(targets, Target{Flavor: name, Path: rel})
	}
	return targets, nil
}

// pinDirectory requires rel to be a directory of the repository after symlinks resolve: a
// link inside the repository that points outside it would otherwise audit a foreign tree.
func pinDirectory(repoPath, rel string) error {
	dir := filepath.Join(repoPath, filepath.FromSlash(rel))
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("not a directory of %s: %w", repoPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory of %s", repoPath)
	}
	root, err := filepath.EvalSymlinks(repoPath)
	if err != nil {
		return fmt.Errorf("resolve repository %s: %w", repoPath, err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", dir, err)
	}
	inside, err := filepath.Rel(root, real)
	if err != nil {
		return fmt.Errorf("relate %s to repository %s: %w", real, root, err)
	}
	if inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return fmt.Errorf("resolves outside the repository %s", repoPath)
	}
	return nil
}

// AuditTargetsContext audits a repository against every flavor ResolveTargets names and
// returns one report per target in order. A target scoped to a directory audits the flavor's
// stack templates and toolchains there, and the repository-level templates and settings
// (repositoryLevel) at the repository root, where the repository carries them. A cancelled
// context stops at the first target it reaches.
func AuditTargetsContext(ctx context.Context, repoPath string) ([]*FlavorAuditReport, error) {
	if ctx == nil {
		return nil, errors.New("audit flavor: context cannot be nil")
	}
	targets, err := ResolveTargets(repoPath)
	if err != nil {
		return nil, err
	}
	reports := make([]*FlavorAuditReport, 0, len(targets))
	for i := 0; i < len(targets) && i < config.MaxFlavorPins; i++ {
		report, err := auditScoped(ctx, repoPath, targets[i])
		if err != nil {
			return nil, fmt.Errorf("flavor %s at %s: %w", targets[i].Flavor, targets[i].Path, err)
		}
		reports = append(reports, report)
	}
	return reports, nil
}
