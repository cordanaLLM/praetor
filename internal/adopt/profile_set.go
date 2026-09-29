package adopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// ProfileSetOptions selects what `praetorctl profile set` declares for an adopted repository.
type ProfileSetOptions struct {
	// Path is the adopted repository's root.
	Path string
	// Profile replaces the declared profiles with this one id; empty keeps them as declared.
	Profile string
	// Facets replaces the declared facets when SetFacets is true, an empty list included, so
	// declaring no facet stays distinct from keeping the declared ones.
	Facets    []string
	SetFacets bool
	// LockSourceRoot is the verified Praetor source bundle the new pins and the vendored catalog
	// texts come from. It is required: every pin is rebuilt from it.
	LockSourceRoot string
	// DryRun writes nothing and previews each file that would change as a diff.
	DryRun bool
}

// ErrNotAdopted refuses profile set on a repository without a manifest: there is no
// declaration to change, and adoption writes the first one.
var ErrNotAdopted = errors.New("repository has no " + manifestFile + " to change; adopt it first with praetorctl adopt")

// profileSetPlan is everything profile set writes, prepared and checked before the first write.
type profileSetPlan struct {
	manifestPath   string
	manifestBefore []byte
	manifestAfter  []byte
	declared, next *config.Manifest
	lockPath       string
	lockBefore     lockSnapshot
	lock           []byte
	catalog        []catalogWrite
	// policy is the effective policy the planned declaration and lock resolve to, with the
	// catalog texts read from the source bundle; a dry run reports it (applyProfileSet).
	policy *config.EffectivePolicy
}

// SetProfile declares opts.Profile and opts.Facets in an adopted repository and re-pins it to
// opts.LockSourceRoot. It writes three things and nothing else: the profiles and facets lists of
// .standards.yaml, every other line kept as written; .standards.lock, rebuilt for that
// declaration; and the vendored catalog texts under .config/archetypes that lock pins (#123).
// With the declaration unchanged it re-pins the lock and the vendored texts to the selected
// source, a routine catalog update. A vendored text the new declaration no longer names is kept.
// Every other file stays as it is, including the files adoption derives from the declaration:
// the DevContainer, the branch protection ruleset, the README block and the documentation
// assets. After a profile or facet change those can fail praetorctl audit until adopt --force
// refreshes them; the returned report carries the effective policy, dry run included, that the
// caller checks them against. Every file is read, built and checked before the first is written,
// so a refusal leaves the repository as it was; a dry run writes nothing and previews each
// changed file. A replaced lock or catalog text is backed up and reported as an adoption reports
// it (replaceExisting).
func SetProfile(ctx context.Context, opts ProfileSetOptions) (*AdoptReport, error) {
	if ctx == nil {
		return nil, errors.New("profile set: context cannot be nil")
	}
	if strings.TrimSpace(opts.LockSourceRoot) == "" {
		return nil, fmt.Errorf("profile set: %w", ErrLockSourceRequired)
	}
	root, err := resolveTargetPath(AdoptOptions{Path: opts.Path})
	if err != nil {
		return nil, err
	}
	s := newProfileSetSession(root, opts)
	plan, err := planProfileSet(ctx, s, opts)
	if err == nil {
		err = applyProfileSet(ctx, s, plan)
	}
	if err != nil {
		s.report.addError("%s", err)
		return s.report, err
	}
	return s.report, nil
}

// newProfileSetSession is the adoption session profile set writes through, so a replaced file is
// backed up and reported exactly as adoption does it. It carries Force: profile set is the
// explicit request to replace the lock and the pinned catalog texts it names
// (mayReplaceCatalogText), and no other step runs in it.
func newProfileSetSession(root string, opts ProfileSetOptions) *adoptSession {
	report := &AdoptReport{
		State: DetectState(root), CreatedFiles: make([]string, 0), ReconciledFiles: make([]string, 0),
		ActionDetails: make([]ActionDetail, 0), DebtBreakdown: make(map[string]int), DryRun: opts.DryRun,
		BaselineStatus: "not_run", Errors: make([]string, 0), Warnings: make([]string, 0),
	}
	return &adoptSession{
		repoPath: root, report: report, backupStamp: newBackupStamp(),
		opts: AdoptOptions{Path: root, LockSourceRoot: opts.LockSourceRoot, DryRun: opts.DryRun, Force: true},
	}
}

// planProfileSet prepares the declaration, the lock and the catalog texts, and checks the backup
// root a replacement writes to, before anything is written.
func planProfileSet(ctx context.Context, s *adoptSession, opts ProfileSetOptions) (*profileSetPlan, error) {
	plan, err := planDeclaration(ctx, s, opts)
	if err != nil {
		return nil, err
	}
	if err := planPins(ctx, s, plan); err != nil {
		return nil, err
	}
	if err := preflightForceBackupRoot(ctx, s); err != nil {
		return nil, err
	}
	return plan, nil
}

// planDeclaration reads the declared manifest and renders the one profile set leaves
// (setManifestDeclaration); an unchanged declaration keeps the bytes as they are.
func planDeclaration(ctx context.Context, s *adoptSession, opts ProfileSetOptions) (*profileSetPlan, error) {
	path, err := repoFile(s.repoPath, manifestFile)
	if err != nil {
		return nil, err
	}
	before, exists, err := contextopt.ObserveSnapshot(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", manifestFile, err)
	}
	if !exists {
		return nil, ErrNotAdopted
	}
	declared, err := decodeTargetManifest(before)
	if err != nil {
		return nil, err
	}
	lists, err := declarationChanges(declared, opts)
	if err != nil {
		return nil, err
	}
	after := before
	if len(lists) > 0 {
		if after, err = setManifestDeclaration(ctx, before, lists); err != nil {
			return nil, fmt.Errorf("rewrite the declaration in %s: %w", manifestFile, err)
		}
	}
	next, err := decodeTargetManifest(after)
	if err != nil {
		return nil, err
	}
	return &profileSetPlan{manifestPath: path, manifestBefore: before, manifestAfter: after,
		declared: declared, next: next}, nil
}

// declarationChanges returns the lists profile set rewrites: the profiles when opts names one
// other than the declared list, and the facets when opts sets a list other than the declared one.
// The declaration it leaves must name a profile, and every id must be valid
// (validateDeclarationIDs).
func declarationChanges(declared *config.Manifest, opts ProfileSetOptions) ([]declarationList, error) {
	profiles := declared.Profiles
	if profile := strings.TrimSpace(opts.Profile); profile != "" {
		profiles = []string{profile}
	}
	facets := declared.Facets
	if opts.SetFacets {
		facets = opts.Facets
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("%s declares no profile: name one, praetorctl profile set <profile>", manifestFile)
	}
	next := []declarationList{{key: manifestProfilesKey, ids: profiles}, {key: manifestFacetsKey, ids: facets}}
	previous := [][]string{declared.Profiles, declared.Facets}
	changes := make([]declarationList, 0, maxDeclarationLists)
	for i := 0; i < len(next) && i < maxDeclarationLists; i++ {
		if err := validateDeclarationIDs(next[i]); err != nil {
			return nil, err
		}
		if !slices.Equal(next[i].ids, previous[i]) {
			changes = append(changes, next[i])
		}
	}
	return changes, nil
}

// validateDeclarationIDs refuses a list the lock cannot pin: more ids than a lock holds, an
// empty id, one holding white space, a control character or a comma, and a repeated id.
func validateDeclarationIDs(list declarationList) error {
	if len(list.ids) > config.MaxManifestEntriesPerKind {
		return fmt.Errorf("%s: %d ids exceed the %d a lock pins", list.key, len(list.ids), config.MaxManifestEntriesPerKind)
	}
	seen := make(map[string]bool, len(list.ids))
	for i := 0; i < len(list.ids) && i < config.MaxManifestEntriesPerKind; i++ {
		id := list.ids[i]
		if id == "" || strings.ContainsFunc(id, invalidIDRune) {
			return fmt.Errorf("%s: %q is not an id: ids hold no white space, control character or comma", list.key, id)
		}
		if seen[id] {
			return fmt.Errorf("%s: %q is listed twice", list.key, id)
		}
		seen[id] = true
	}
	return nil
}

// invalidIDRune reports a rune no profile or facet id holds.
func invalidIDRune(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r) || r == ','
}

// planPins builds the lock for the planned declaration from the source bundle, resolves the
// catalog texts it pins as the effective policy loader does, and observes every destination
// (prepareCatalogWrites), each before anything is written.
func planPins(ctx context.Context, s *adoptSession, plan *profileSetPlan) error {
	path, err := repoFile(s.repoPath, lockFile)
	if err != nil {
		return err
	}
	before, exists, err := observeLock(ctx, path)
	if err != nil {
		return err
	}
	plan.lockPath, plan.lockBefore = path, lockSnapshot{before: before, exists: exists}
	if plan.lock, err = buildLock(ctx, s, plan.next); err != nil {
		return err
	}
	policy, err := config.LoadEffectivePolicyInputsContext(ctx, config.EffectiveOptions{
		Root: s.repoPath, CatalogRoot: s.opts.LockSourceRoot,
	}, plan.manifestAfter, plan.lock)
	if err != nil {
		return fmt.Errorf("resolve the catalog the new pins select: %w", err)
	}
	if plan.catalog, err = prepareCatalogWrites(ctx, s, policy.CatalogArtifacts); err != nil {
		return err
	}
	plan.policy = policy
	if err := config.ValidateCatalogProjectionContext(ctx, s.repoPath, policy.CatalogArtifacts); err != nil {
		return fmt.Errorf("validate the prospective catalog: %w", err)
	}
	return nil
}

// applyProfileSet writes the plan: the catalog texts first, so the lock never pins a text the
// repository lacks, then the lock, then the manifest. Each write is bound to the bytes planning
// observed (contextopt.ReplaceSnapshot), so a file changed since fails the run instead of losing
// the change. A dry run writes nothing, previews each file that would change and reports the
// planned effective policy; a real run reads the lock and the policy back (verifyProfileSet).
func applyProfileSet(ctx context.Context, s *adoptSession, plan *profileSetPlan) error {
	for i := 0; i < len(plan.catalog) && i < maxAdoptPolicyFiles; i++ {
		write := plan.catalog[i]
		if err := publishCatalogFile(ctx, s, write); err != nil {
			return err
		}
		s.previewProfileWrite(write.artifact.RelativePath, write.before, write.exists, write.artifact.Content)
	}
	if err := publishLock(ctx, s, plan.lockPath, plan.lockBefore, plan.lock); err != nil {
		return err
	}
	s.previewProfileWrite(lockFile, plan.lockBefore.before, plan.lockBefore.exists, plan.lock)
	if err := publishDeclaration(ctx, s, plan); err != nil {
		return err
	}
	s.previewProfileWrite(manifestFile, plan.manifestBefore, true, plan.manifestAfter)
	s.report.Archetype, s.report.Facets = plan.next.Profiles[0], plan.next.Facets
	if s.opts.DryRun {
		s.report.EffectivePolicy = plan.policy
		return nil
	}
	return verifyProfileSet(ctx, s, plan.next)
}

// publishDeclaration writes the planned manifest over the bytes planning observed and reports the
// declaration it leaves beside the one it replaced.
func publishDeclaration(ctx context.Context, s *adoptSession, plan *profileSetPlan) error {
	if bytes.Equal(plan.manifestBefore, plan.manifestAfter) {
		s.report.recordReconciled(manifestFile, "Declaration unchanged ("+declarationSummary(plan.next)+
			"); pins and vendored catalog texts follow the selected source bundle")
		return nil
	}
	if !s.opts.DryRun {
		if err := contextopt.ReplaceSnapshot(ctx, plan.manifestPath, plan.manifestAfter, contextopt.ReplaceOptions{
			Expected: plan.manifestBefore, Exists: true, Mode: filePerm,
		}); err != nil {
			return fmt.Errorf("write %s: %w", manifestFile, err)
		}
	}
	s.report.recordReconciled(manifestFile, "Declared "+declarationSummary(plan.next)+" (was "+
		declarationSummary(plan.declared)+"); every other line kept as written")
	return nil
}

// declarationSummary names the profiles and facets manifest declares.
func declarationSummary(manifest *config.Manifest) string {
	return fmt.Sprintf("profiles %v, facets %v", manifest.Profiles, manifest.Facets)
}

// previewProfileWrite records, in a dry run only, what rel comes to when it changes: its content
// for a file profile set creates, the diff from the bytes on disk otherwise (filePreview).
func (s *adoptSession) previewProfileWrite(rel string, before []byte, exists bool, after []byte) {
	if !s.opts.DryRun || (exists && bytes.Equal(before, after)) {
		return
	}
	s.report.Previews = append(s.report.Previews,
		filePreview(scaffold{rel: rel, content: after}, scaffoldWritten, before, exists, exists))
}

// verifyProfileSet reads the lock and the policy back as audit's manifest and lock gates do: the
// lock must verify against the catalog the repository now vendors, and the effective policy must
// resolve from the repository alone, without the source bundle. The gates that check files
// derived from the declaration are the caller's (runProfileSet).
func verifyProfileSet(ctx context.Context, s *adoptSession, manifest *config.Manifest) error {
	if _, err := config.ValidateLockfileWithOptions(ctx, config.LockValidationOptions{
		Root: s.repoPath, RequireSources: true,
	}, manifest); err != nil {
		return fmt.Errorf("read back %s: %w", lockFile, err)
	}
	policy, err := config.LoadEffectivePolicyContext(ctx, config.EffectiveOptions{Root: s.repoPath, Audit: true})
	if err != nil {
		return fmt.Errorf("read back repository-local pinned policy: %w", err)
	}
	s.report.EffectivePolicy = policy
	return nil
}
