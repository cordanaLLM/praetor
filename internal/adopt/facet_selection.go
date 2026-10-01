package adopt

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
)

// FacetOrigin says where the facets of an adoption report come from (adoptionFacets).
type FacetOrigin string

const (
	// FacetsDeclared are the facets the existing .standards.yaml declares, an empty list
	// included. Adoption applies and keeps them, and ignores a --facets that names others with a
	// warning (warnIgnoredFacets).
	FacetsDeclared FacetOrigin = "declared"
	// FacetsRequested are the facets --facets named, which adoption declares in the
	// .standards.yaml it creates.
	FacetsRequested FacetOrigin = "requested"
	// FacetsDefaulted are config.DefaultFacets, which adoption declares in the .standards.yaml it
	// creates when --facets names none, and says so (defaultFacetNotes, #596).
	FacetsDefaulted FacetOrigin = "default"
)

// adoptionFacets returns the facets an adoption applies and where they come from. An existing
// manifest's declaration stands, because adoption never rewrites it: the lock, the documentation
// gate and the DevContainer all read it (manifestForLock, documentationEnabledForSession), so
// the report shows it too rather than requested, the --facets value (#592). Without a manifest,
// requested names them, and the defaults stand in when it names none. A manifest that exists but
// cannot be read resolves none: the manifest step fails the run with the reason.
func adoptionFacets(declared *config.Manifest, unreadable bool, requested []string) ([]string, FacetOrigin) {
	switch {
	case unreadable:
		return nil, ""
	case declared != nil:
		return declared.Facets, FacetsDeclared
	case len(requested) > 0:
		return requested, FacetsRequested
	default:
		return config.DefaultFacets(), FacetsDefaulted
	}
}

// warnIgnoredFacets warns that --facets is ignored when the existing manifest declares other
// facets, naming both lists and the command that changes them, as an overridden --profile is
// warned about (newAdoptionReport). Adoption never rewrites a declaration; profile set does.
// An omitted flag, and one naming the declared list in its order, warn nothing; an explicit
// empty list (opts.SetFacets) against declared facets does.
func warnIgnoredFacets(report *AdoptReport, opts AdoptOptions, declared *config.Manifest) {
	if declared == nil || (len(opts.Facets) == 0 && !opts.SetFacets) || slices.Equal(opts.Facets, declared.Facets) {
		return
	}
	requested := "--facets=" + strings.Join(opts.Facets, ",")
	report.addWarning("%s ignored: %s declares facets %v, and adoption never rewrites declared facets; change them with %s",
		requested, manifestFile, declared.Facets, profileSetCommand(requested, opts.LockSourceRoot))
}

// profileSetCommand is the praetorctl profile set command line that applies args with the
// source bundle this run selected, or a placeholder naming one when it selected none.
func profileSetCommand(args, lockSource string) string {
	if lockSource == "" {
		lockSource = "<praetor checkout>"
	}
	command := "praetorctl profile set"
	if args != "" {
		command += " " + args
	}
	return command + " --lock-source-root=" + lockSource
}

// defaultFacetNotes says, for default facets only, that adoption chose them and why, what each
// one raises (facetEffectNotes), and how to choose others before and after the first adoption
// (#596). It returns nothing for declared or requested facets.
func defaultFacetNotes(ctx context.Context, s *adoptSession) []string {
	if s.report.FacetOrigin != FacetsDefaulted {
		return nil
	}
	reason := "--facets was omitted"
	if s.opts.SetFacets {
		reason = "--facets named no facet"
	}
	notes := []string{fmt.Sprintf("default facets: %s, so adoption declares %s in the %s it creates",
		reason, strings.Join(s.report.Facets, ", "), manifestFile)}
	notes = append(notes, facetEffectNotes(ctx, s)...)
	return append(notes, "choose other facets with --facets=<id>,... on a first adoption, or afterwards with "+
		profileSetCommand("--facets=<id>,...", s.opts.LockSourceRoot)+" (--facets= declares none)")
}

// facetEffectNotes names, one line per declared facet, the branch-protection and supply-chain
// settings it raises over the profile alone, read from the pinned policy the run resolved
// (config.EffectivePolicy.FacetEffects). Without that policy, or when it cannot pair a facet
// with its catalog text, one line says why the effects are not listed.
func facetEffectNotes(ctx context.Context, s *adoptSession) []string {
	if s.policy == nil {
		return []string{"what each facet raises is not listed: the run resolved no pinned policy to read it from"}
	}
	effects, err := s.policy.FacetEffects(ctx)
	if err != nil {
		return []string{fmt.Sprintf("what each facet raises is not listed: %v", err)}
	}
	notes := make([]string, 0, len(effects))
	for i := 0; i < len(effects) && i < config.MaxManifestEntriesPerKind; i++ {
		raises := "no branch-protection or supply-chain setting"
		if len(effects[i].Raises) > 0 {
			raises = strings.Join(effects[i].Raises, ", ")
		}
		notes = append(notes, fmt.Sprintf("%s raises over %s alone: %s", effects[i].Facet, s.arch, raises))
	}
	return notes
}
