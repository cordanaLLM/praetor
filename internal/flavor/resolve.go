package flavor

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/classify"
	"github.com/cordanaLLM/praetor/internal/config"
)

// Resolve names the flavor a repository is scaffolded, audited and described under.
//
// A flavor is the second of two axes, never an answer of its own. The profile (archetype) says
// what governance applies, and internal/classify decides it once: the repository's declared
// profile, else its markers. The flavor says which templates, settings and toolchains that
// profile's stack needs, so it is picked only among the flavors implementing the profile
// (Flavor.HISSProfile), by their markers.
//
// Flavor detection used to scan the whole catalog on its own, and adoption, flavor apply and the
// Hindsight distiller acted on its answer (BUG-940). A repository declaring native-gpu-systems
// with a PyTorch dependency was scaffolded as python-ml, and one declaring gitops-infra received
// python-ml templates its profile never asked for. Only the flavor audit consulted the
// declaration, so audit and apply disagreed about the same checkout.
//
// Resolve returns ErrFlavorNotApplicable when the profile has no flavor at all and
// ErrNoFlavorMatched when nothing classifies the repository or none of the profile's flavors
// matches it. It never substitutes a flavor.
func Resolve(repoPath string) (string, error) {
	return ResolveForProfile(repoPath, ResolutionProfile(repoPath))
}

// ResolutionProfile is the profile Resolve picks flavors under: the repository's declared
// profile, else the one its markers classify, empty when neither names one. A caller that
// describes a resolved flavor reports this profile, not the flavor's own HISSProfile, which
// differs for a flavor that also implements the profile (MultiProfile) or is pinned.
func ResolutionProfile(repoPath string) string {
	return classify.Resolve(
		classify.FromDeclaration(declaredProfiles(repoPath)),
		classify.ByMarkers(repoPath),
	).Archetype
}

// ResolveForProfile names the flavor implementing profile that matches the repository, for a
// caller that has already decided the profile, such as adoption, which records it in the
// manifest it writes. An empty profile is ErrNoProfile (which also wraps ErrNoFlavorMatched), a profile no flavor implements is
// ErrFlavorNotApplicable, and a profile whose flavors all fail to match is ErrNoFlavorMatched.
func ResolveForProfile(repoPath, profile string) (string, error) {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return "", fmt.Errorf("%w: %w: %s", ErrNoProfile, ErrNoFlavorMatched, repoPath)
	}
	candidates := flavorsForProfile(profile)
	if len(candidates) == 0 {
		return "", fmt.Errorf("%w: profile %q", ErrFlavorNotApplicable, profile)
	}
	for i := 0; i < len(candidates) && i < maxDetectionCandidates; i++ {
		if candidates[i].Detect(repoPath) {
			return candidates[i].Name(), nil
		}
	}
	return "", fmt.Errorf("%w: profile %q has flavors (%s) but none match %s",
		ErrNoFlavorMatched, profile, flavorNames(candidates), repoPath)
}

// flavorNames joins the names of flavors, so a nothing-matched error says which flavors of the
// profile were tried and the operator can look up what each one detects.
func flavorNames(flavors []Flavor) string {
	names := make([]string, 0, len(flavors))
	for i := 0; i < len(flavors) && i < maxDetectionCandidates; i++ {
		names = append(names, flavors[i].Name())
	}
	return strings.Join(names, ", ")
}

// declaredProfiles returns the profiles the repository's manifest declares, or nil when it
// declares none. An unreadable manifest is not a declaration, so classification proceeds from
// the markers as though none were present.
func declaredProfiles(repoPath string) []string {
	manifest, err := config.LoadManifest(filepath.Join(repoPath, ".standards.yaml"))
	if err != nil || manifest == nil {
		return nil
	}
	return manifest.Profiles
}

// flavorsForProfile returns the registered flavors that implement one HISS profile, in
// detection precedence order.
func flavorsForProfile(profile string) []Flavor {
	all := List()
	matched := make([]Flavor, 0, len(all))
	for i := 0; i < len(all) && i < maxDetectionCandidates; i++ {
		if all[i].HISSProfile() == profile {
			matched = append(matched, all[i])
		}
	}
	for i := 0; i < len(all) && i < maxDetectionCandidates; i++ {
		if all[i].HISSProfile() != profile && alsoImplements(all[i], profile) {
			matched = append(matched, all[i])
		}
	}
	return matched
}

// alsoImplements reports whether f declares profile as a secondary profile (MultiProfile).
func alsoImplements(f Flavor, profile string) bool {
	multi, ok := f.(MultiProfile)
	if !ok {
		return false
	}
	return slices.Contains(multi.AlsoImplements(), profile)
}
