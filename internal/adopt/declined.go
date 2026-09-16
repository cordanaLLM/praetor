package adopt

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"gopkg.in/yaml.v3"
)

// maxDeclinedArtifacts bounds the declared list so a malformed manifest cannot make
// adoption iterate without limit.
const maxDeclinedArtifacts = 64

// namedStep pairs a reconciliation step with the name a repository uses to decline it.
type namedStep struct {
	name string
	run  adoptStep
}

// mandatoryArtifacts cannot be declined. Declining the manifest or the lockfile would leave
// a repository that claims adoption while carrying nothing that records what it adopted, and
// the baseline is what every later audit compares against.
var mandatoryArtifacts = map[string]string{
	"manifest": "the manifest is what records the declaration itself",
	"lockfile": "the lockfile is what pins the policies the manifest names",
	"baseline": "every later audit compares against the baseline",
}

// declinedArtifacts resolves the manifest's decline list into a lookup, rejecting names that
// match no artefact and names that may not be declined.
//
// An unknown name is an error rather than a no-op on purpose. A silently ignored typo reads
// exactly like a working declaration until the artefact it was meant to suppress reappears.
func declinedArtifacts(declared []string, known []string) (map[string]bool, error) {
	if len(declared) > maxDeclinedArtifacts {
		return nil, fmt.Errorf("adoption declines at most %d artefacts, got %d", maxDeclinedArtifacts, len(declared))
	}
	valid := make(map[string]bool, len(known))
	for i := 0; i < len(known) && i < maxAdoptSteps; i++ {
		valid[known[i]] = true
	}
	declined := make(map[string]bool, len(declared))
	for i := 0; i < len(declared) && i < maxDeclinedArtifacts; i++ {
		name := strings.ToLower(strings.TrimSpace(declared[i]))
		if name == "" {
			continue
		}
		if reason, mandatory := mandatoryArtifacts[name]; mandatory {
			return nil, fmt.Errorf("adoption cannot decline %q: %s", name, reason)
		}
		if !valid[name] {
			return nil, fmt.Errorf("adoption cannot decline unknown artefact %q; known artefacts are %s",
				name, strings.Join(sortedNames(known), ", "))
		}
		declined[name] = true
	}
	return declined, nil
}

func sortedNames(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// declaredDeclines reads adoption.decline from a repository's existing manifest.
//
// It is read before the chain runs, from the manifest already on disk, so a repository's
// recorded decision governs the run that follows it rather than the run after next. A
// repository with no manifest yet declines nothing, which is what a first adoption means.
func declaredDeclines(ctx context.Context, repoPath string) []string {
	full, err := repoFile(repoPath, manifestFile)
	if err != nil {
		return nil
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil || !exists {
		return nil
	}
	var manifest config.Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil || manifest.Adoption == nil {
		return nil
	}
	return manifest.Adoption.Decline
}

// adoptStepNames returns the name of every step in the adoption chain, so tests and error
// messages enumerate the real chain rather than a second list that can drift from it.
func adoptStepNames() []string {
	steps := adoptSteps()
	names := make([]string, 0, len(steps))
	for i := 0; i < len(steps) && i < maxAdoptSteps; i++ {
		names = append(names, steps[i].name)
	}
	return names
}
