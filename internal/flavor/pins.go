package flavor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/config"
)

// pinRemedy is the remedy every nothing-matched refusal names for a caller that has no
// --flavor flag: the manifest setting that replaces detection (#1103). It carries no flag
// name, because gate run and the generated pre-push hook take none.
const pinRemedy = "; pin the flavor with a flavors entry in .standards.yaml"

// Target is one flavor and the repository-relative directory it is audited against.
type Target struct {
	Flavor string `json:"flavor"`
	// Path is "." for the repository root.
	Path string `json:"path"`
}

// ResolveTargets names the flavors a repository is audited against, once, for every caller:
// `flavor audit`, `gate run` and the pre-push hook that runs the audit.
//
// A repository pinning flavors in .standards.yaml (config.FlavorPin) gets exactly those, each
// with its directory; an unknown flavor name or a missing directory is refused, never skipped.
// A repository without pins gets the one flavor Resolve detects, at its root. Detection errors
// pass through (ErrNoFlavorMatched, ErrFlavorNotApplicable) with the pin setting named as the
// remedy.
func ResolveTargets(repoPath string) ([]Target, error) {
	manifest, err := config.LoadManifest(filepath.Join(repoPath, ".standards.yaml"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("resolve flavor pins: %w", err)
	}
	if manifest != nil && len(manifest.Flavors) > 0 {
		return pinnedTargets(repoPath, manifest.Flavors)
	}
	name, err := Resolve(repoPath)
	if errors.Is(err, ErrNoFlavorMatched) {
		return nil, fmt.Errorf("%w%s", err, pinRemedy)
	}
	if err != nil {
		return nil, err
	}
	return []Target{{Flavor: name, Path: "."}}, nil
}

// pinnedTargets validates each pin against the registry and the working tree.
func pinnedTargets(repoPath string, pins []config.FlavorPin) ([]Target, error) {
	targets := make([]Target, 0, len(pins))
	for i := 0; i < len(pins) && i < config.MaxFlavorPins; i++ {
		name := pins[i].Name
		if _, err := Get(name); err != nil {
			return nil, fmt.Errorf("flavors[%d] in .standards.yaml: %w (praetorctl flavor list names each)", i, err)
		}
		rel := pins[i].CleanPath()
		info, err := os.Stat(filepath.Join(repoPath, filepath.FromSlash(rel)))
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("flavors[%d] in .standards.yaml: path %q for flavor %s is not a directory of %s",
				i, rel, name, repoPath)
		}
		targets = append(targets, Target{Flavor: name, Path: rel})
	}
	return targets, nil
}

// AuditTargetsContext audits a repository against every flavor ResolveTargets names, each
// against its own directory only, and returns one report per target in order. A cancelled
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
		dir := filepath.Join(repoPath, filepath.FromSlash(targets[i].Path))
		report, err := AuditFlavorContext(ctx, dir, targets[i].Flavor)
		if err != nil {
			return nil, fmt.Errorf("flavor %s at %s: %w", targets[i].Flavor, targets[i].Path, err)
		}
		report.Path = targets[i].Path
		reports = append(reports, report)
	}
	return reports, nil
}
