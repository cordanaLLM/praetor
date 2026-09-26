package adopt

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
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
// the baseline is what every later audit compares against. The documentation gate already
// has one off switch, the docs:seo-portal facet, whose removal converges every surface it
// owns; a decline would skip only the assets and leave verify-all calling a missing runner.
var mandatoryArtifacts = map[string]string{
	"manifest":           "the manifest is what records the declaration itself",
	"lockfile":           "the lockfile is what pins the policies the manifest names",
	"baseline":           "every later audit compares against the baseline",
	"documentation-gate": "remove the docs:seo-portal facet instead; it converges every documentation surface",
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

// ArtifactDeclined resolves one named adoption step through the same bounded policy used by
// Adopt. Auditors call this instead of maintaining a second decline parser that can drift.
func ArtifactDeclined(declared []string, artifact string) (bool, error) {
	known := adoptStepNames()
	declined, err := declinedArtifacts(declared, known)
	if err != nil {
		return false, err
	}
	name := strings.ToLower(strings.TrimSpace(artifact))
	for i := 0; i < len(known) && i < maxAdoptSteps; i++ {
		if known[i] == name {
			return declined[name], nil
		}
	}
	return false, fmt.Errorf("unknown adoption artefact %q", name)
}

// ManifestArtifactDeclined resolves one adoption step against the adoption.decline list the
// manifest records. A manifest without an adoption policy declines nothing; an invalid list
// fails closed through ArtifactDeclined, so every auditor reads declines one way.
func ManifestArtifactDeclined(manifest *config.Manifest, artifact string) (bool, error) {
	return ArtifactDeclined(manifestDeclines(manifest), artifact)
}

// manifestDeclines is the adoption.decline list a manifest records; nil declines nothing.
func manifestDeclines(manifest *config.Manifest) []string {
	if manifest == nil || manifest.Adoption == nil {
		return nil
	}
	return manifest.Adoption.Decline
}

// manifestProfiles is the profile list a manifest declares; nil declares none.
func manifestProfiles(manifest *config.Manifest) []string {
	if manifest == nil {
		return nil
	}
	return manifest.Profiles
}

func sortedNames(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// declaredDeclines reads adoption.decline from a repository's existing manifest.
func declaredDeclines(ctx context.Context, repoPath string) []string {
	return manifestDeclines(declaredManifest(ctx, repoPath))
}

// declaredManifest reads the repository's existing manifest once, before the chain runs, so
// its recorded decisions (adoption.decline, the declared profiles) govern the run that
// follows rather than the run after next. It returns nil when there is no manifest yet, which
// is what a first adoption means, and when the manifest cannot be read: the manifest step
// parses it strictly and fails the run with the reason.
func declaredManifest(ctx context.Context, repoPath string) *config.Manifest {
	full, err := repoFile(repoPath, manifestFile)
	if err != nil {
		return nil
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil || !exists {
		return nil
	}
	manifest, err := config.DecodeManifest(data)
	if err != nil {
		return nil
	}
	return manifest
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
