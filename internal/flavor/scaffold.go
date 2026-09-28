package flavor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
)

// ErrApplyIncomplete reports an apply that recorded at least one template failure.
//
// ApplyFlavor used to return (report, nil) whatever report.Errors held, so a caller that
// checked only the error treated a scaffold in which every write failed as a success. Adoption
// was that caller. The report still comes back beside this error, so a caller can say what was
// written before the failure.
var ErrApplyIncomplete = errors.New("flavor apply: one or more templates failed")

// ApplyReport contains the outcome of applying a flavor scaffold to a repository.
type ApplyReport struct {
	Flavor           string   `json:"flavor"`
	CreatedTemplates []string `json:"created_templates"`
	SkippedTemplates []string `json:"skipped_templates"`
	// DeferredTemplates names each required template flavor apply does not write because
	// another command produces it, as "<path> (<producer>)". The flavor audit still
	// requires these files, so a deferred template is work left for the named command.
	DeferredTemplates []string `json:"deferred_templates,omitempty"`
	// UnmetTemplates names each required template flavor apply does not write because the
	// repository lacks what its body needs to work as written (TemplateItem.Resolve), as
	// "<path>: <what is missing>". The audit still requires these files: supply what is
	// missing and apply again, or write a file that fits the repository.
	UnmetTemplates []string `json:"unmet_templates,omitempty"`
	// CoveredTemplates names each required template flavor apply does not write because the
	// repository already carries its configuration under another name the template accepts
	// (AltPaths or Search). A file under the canonical name beside it would be a second
	// configuration, one the tool ignores or one that contradicts the file in use.
	CoveredTemplates []CoveredTemplate `json:"covered_templates,omitempty"`
	// Settings records what apply did with each required setting, in the flavor's order
	// (SettingOutcome). An apply with ApplyOptions.TemplatesOnly records none.
	Settings          []SettingOutcome `json:"settings,omitempty"`
	WorkingDirCreated bool             `json:"working_dir_created"`
	Errors            []string         `json:"errors,omitempty"`
}

// ApplyOptions selects what one flavor apply may replace and which of its parts run.
type ApplyOptions struct {
	// Force replaces existing templates, and a branch ruleset that differs from the one the
	// effective policy renders. The ledger, the manifest and the lock are never replaced.
	Force bool
	// TemplatesOnly leaves every required setting alone and unreported. Adoption sets it: its
	// own branch-ruleset step renders the ruleset after every workflow of the run is written,
	// under the policy the run pins, and honours adoption.decline and its --force contract.
	TemplatesOnly bool
	// Declines resolves whether the repository's adoption.decline names an adoption step, such
	// as "branch-ruleset" for the ruleset. Adoption owns that list and its one parser
	// (adopt.RepositoryArtifactDeclined); this package cannot import it, so a caller passes it
	// in. Nil declines nothing.
	Declines func(ctx context.Context, step string) (bool, error)
}

// CoveredTemplate is a template flavor apply left unwritten because the repository configures
// it under another accepted name: InUse is that file, Path the name apply would have written.
type CoveredTemplate struct {
	Path  string `json:"path"`
	InUse string `json:"in_use"`
}

// ApplyFlavor scaffolds the missing templates and required settings of a target flavor, and
// replaces existing ones with force (ApplyOptions.Force). It is ApplyFlavorWith with nothing
// declined.
func ApplyFlavor(ctx context.Context, repoPath string, targetFlavor string, force bool) (*ApplyReport, error) {
	return ApplyFlavorWith(ctx, repoPath, targetFlavor, ApplyOptions{Force: force})
}

// ApplyFlavorWith scaffolds the missing templates of a target flavor, then renders its required
// settings (applySettings) unless opts.TemplatesOnly. Settings come after templates because the
// branch ruleset requires the status checks of the workflows present, the ones apply just wrote
// among them.
func ApplyFlavorWith(ctx context.Context, repoPath string, targetFlavor string, opts ApplyOptions) (*ApplyReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("apply flavor requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	flv, err := resolveApplyTarget(repoPath, targetFlavor)
	if err != nil {
		return nil, err
	}

	owner, repoName, err := flavorIdentity(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	report := &ApplyReport{
		Flavor: flv.Name(),
	}

	// 1. Initialize .workingdir/
	if err := state.InitWorkingDirContext(ctx, repoPath); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("workingdir init: %v", err))
		return report, fmt.Errorf("initialize flavor working directory: %w", err)
	} else {
		report.WorkingDirCreated = true
	}

	// 2. Scaffold required templates, after reading what the ruleset step compares against
	baseline := readRulesetBaseline(ctx, repoPath, opts)
	for _, tmpl := range flv.RequiredTemplates() {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		applySingleTemplate(ctx, repoPath, tmpl, repoName, owner, opts.Force, report)
	}

	// 3. Render required settings (none under opts.TemplatesOnly)
	if err := applySettings(ctx, repoPath, flv.RequiredSettings(), opts, baseline, report); err != nil {
		return report, err
	}

	if len(report.Errors) > 0 {
		return report, fmt.Errorf("%w: %d error(s): %s", ErrApplyIncomplete, len(report.Errors), strings.Join(report.Errors, "; "))
	}
	return report, nil
}

// flavorIdentity returns the owner and name templates render. Both come from the origin
// remote (util.ResolveRemoteIdentity), never from the checkout path: a parent directory names
// wherever the checkout sits, not its owner. Without a remote the owner stays empty and the
// name is the checkout directory's, which a template body may use as a label and is never
// written into an identity field. A remote read git did not answer is an error.
func flavorIdentity(ctx context.Context, repoPath string) (owner, repoName string, err error) {
	owner, repoName, err = util.ResolveRemoteIdentity(ctx, repoPath)
	if err == nil {
		return owner, repoName, nil
	}
	if !errors.Is(err, util.ErrRepoIdentityUnresolved) {
		return "", "", fmt.Errorf("resolve flavor repository identity: %w", err)
	}
	abs, absErr := filepath.Abs(repoPath)
	if absErr != nil {
		return "", "", fmt.Errorf("resolve flavor repository path %q: %w", repoPath, absErr)
	}
	return "", filepath.Base(abs), nil
}

// resolveApplyTarget names the flavor an apply scaffolds: the explicit one, or for "" and "auto"
// the one Resolve names, which is the flavor the audit then measures. It used to detect across
// the whole catalog while the audit narrowed to the declared profile, so apply could scaffold a
// flavor the audit never checked. A repository nothing matches is refused, never given a guess.
func resolveApplyTarget(repoPath, targetFlavor string) (Flavor, error) {
	if targetFlavor == "" || targetFlavor == "auto" {
		resolved, err := Resolve(repoPath)
		if err != nil {
			return nil, err
		}
		targetFlavor = resolved
	}
	flv, err := Get(targetFlavor)
	if err != nil {
		return nil, fmt.Errorf("apply flavor: %w", err)
	}
	return flv, nil
}

// forceProtected reports whether an existing file at rel survives --force.
//
// The ledger holds session history, and the manifest and lock hold the declared profile and
// pinned digests. A flavor has no body for any of them: the manifest and lock are deferred to
// their producer (TemplateItem.Producer), and a forced refresh used to replace operator data
// with a one-line stub. This guard holds even for a flavor registered from outside this
// package that lists one of these paths with a ContentFunc. --force refreshes scaffolds; it
// never rewrites what the repository declared.
func forceProtected(rel string) bool {
	return strings.HasPrefix(rel, state.WorkingDirName+"/") || rel == config.ManifestFileName || rel == config.LockFileName
}

// templateDisposition decides, before any filesystem mutation, whether a template is safe to
// write (templateCreated), already present (templateSkipped), configured under another name
// (templateCovered, with that name as the note), or must be refused outright.
func templateDisposition(repoPath string, tmpl TemplateItem, force bool) (templateOutcome, string, error) {
	// A template path is declared in slash form (".github/workflows/ci.yml"), so its
	// cleanliness is a slash-path property. filepath.Clean returns backslashes on Windows
	// and never equalled the declared value, so every template was refused and `flavor
	// apply` could scaffold nothing there. IsLocal still decides containment on the host.
	if !filepath.IsLocal(tmpl.Path) || path.Clean(tmpl.Path) != tmpl.Path {
		return templateSkipped, "", fmt.Errorf("template path must remain within the repository: %s", tmpl.Path)
	}
	if force {
		return templateCreated, "", nil
	}
	// TemplateSatisfied is the audit's question: does a valid file inside the repository
	// cover the template. Apply asks a narrower one: is a configuration already in use under
	// another name. A monorepo's tsconfig.base.json linked from a shared root, or one the
	// validator rejects, is still the file the toolchain reads, and a canonical tsconfig.json
	// written beside it is the contradictory second config AltPaths exists to prevent. The
	// same holds for a searched name: yamllint reads .yamllint.yaml ahead of .yamllint.yml, so
	// a scaffolded .yamllint.yml beside it would be configuration nothing reads. The audit
	// keeps reporting the file's content; apply only declines to add a rival.
	present := presentNames(repoPath, tmpl)
	if len(present) > 0 && present[0] != tmpl.Path {
		return templateCovered, present[0], nil
	}
	// Past this point any file present is the canonical one, first in lookup order; a second
	// entry is an alternative beside it, which blocks a rewrite the same way.
	if len(present) > 1 || TemplateSatisfied(repoPath, tmpl) {
		return templateSkipped, "", nil
	}
	return templateCreated, "", nil
}

// templateContent resolves a template's body from its generator or its embedded source,
// rendered against vars: the repository identity plus the facts TemplateItem.Resolve read.
//
// A template with neither has no content behind it, and that is an error. This used to
// fall back to a one-line "# <file> configuration for <owner>/<repo>" comment, which
// disabled every gitleaks rule (#410), scaffolded workflows that ran nothing, and was then
// scored by the audit as the file it stood in for (BUG-028, BUG-029).
func templateContent(tmpl TemplateItem, vars templates.Context) (string, error) {
	if tmpl.ContentFunc != nil {
		return tmpl.ContentFunc(vars.RepoName, vars.Owner), nil
	}
	if tmpl.Source == "" {
		return "", fmt.Errorf("template %s has no content source", tmpl.Path)
	}
	body, err := templates.RenderFile(tmpl.Source, vars)
	if err != nil {
		return "", fmt.Errorf("render template %s: %w", tmpl.Path, err)
	}
	return body, nil
}

// templateOutcome is what applying one template did.
type templateOutcome int

const (
	templateCreated templateOutcome = iota
	templateSkipped
	templateDeferred
	templateUnmet
	templateCovered
)

func applySingleTemplate(ctx context.Context, repoPath string, tmpl TemplateItem, repoName, owner string, force bool, report *ApplyReport) {
	outcome, note, err := scaffoldTemplate(ctx, repoPath, tmpl, repoName, owner, force)
	switch {
	case err != nil:
		report.Errors = append(report.Errors, err.Error())
	case outcome == templateSkipped:
		report.SkippedTemplates = append(report.SkippedTemplates, tmpl.Path)
	case outcome == templateCovered:
		report.CoveredTemplates = append(report.CoveredTemplates, CoveredTemplate{Path: tmpl.Path, InUse: note})
	case outcome == templateDeferred:
		report.DeferredTemplates = append(report.DeferredTemplates, fmt.Sprintf("%s (%s)", tmpl.Path, note))
	case outcome == templateUnmet:
		report.UnmetTemplates = append(report.UnmetTemplates, fmt.Sprintf("%s: %s", tmpl.Path, note))
	default:
		report.CreatedTemplates = append(report.CreatedTemplates, tmpl.Path)
	}
}

// scaffoldTemplate writes one template unless it is covered, owned by another command,
// unable to work in this repository, or already present without --force. The note names the
// file covering a covered template, the producer of a deferred one and what an unmet one lacks.
func scaffoldTemplate(ctx context.Context, repoPath string, tmpl TemplateItem, repoName, owner string, force bool) (templateOutcome, string, error) {
	outcome, note, err := templateDisposition(repoPath, tmpl, force)
	if err != nil || outcome != templateCreated {
		return outcome, note, err
	}
	outcome, note, vars := templateWithheld(ctx, repoPath, tmpl)
	if outcome != templateCreated {
		return outcome, note, nil
	}
	destPath := filepath.Join(repoPath, tmpl.Path)
	target, err := readTemplateTarget(ctx, destPath, tmpl.Path, force)
	if err != nil || target.keep {
		return templateSkipped, "", err
	}
	vars.RepoName, vars.Owner = repoName, owner
	content, err := templateContent(tmpl, vars)
	if err != nil {
		return templateSkipped, "", err
	}
	if err := writeTarget(ctx, destPath, tmpl.Path, []byte(content), target); err != nil {
		return templateSkipped, "", err
	}
	return templateCreated, "", nil
}

// writeTarget writes content at destPath, creating its directory, bound to the bytes
// readTemplateTarget observed there, so a file changed in between is not overwritten. Templates
// and the rendered settings write through it.
func writeTarget(ctx context.Context, destPath, rel string, content []byte, target templateTarget) error {
	if err := contextopt.EnsureDirectory(ctx, filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", rel, err)
	}
	options := contextopt.ReplaceOptions{Expected: target.before, Exists: target.exists, Mode: 0o644}
	if err := contextopt.ReplaceSnapshot(ctx, destPath, content, options); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	return nil
}

// templateWithheld reports why flavor apply writes no body for a template whatever --force
// says, or templateCreated together with the facts the body renders against when nothing
// withholds it. A producer-owned template has no body here, only a placeholder to lose the
// real file to. A template whose requirement the repository does not meet has a body that
// cannot work there, and writing it over an existing file would replace one that might.
func templateWithheld(ctx context.Context, repoPath string, tmpl TemplateItem) (templateOutcome, string, templates.Context) {
	if tmpl.Producer != "" {
		return templateDeferred, tmpl.Producer, templates.Context{}
	}
	if tmpl.Resolve == nil {
		return templateCreated, "", templates.Context{}
	}
	facts, missing := tmpl.Resolve(ctx, repoPath)
	if missing != "" {
		return templateUnmet, missing, templates.Context{}
	}
	return templateCreated, "", facts
}

// templateTarget is the file already at a template's destination.
type templateTarget struct {
	before []byte
	exists bool
	// keep reports that the existing file stays: present without --force, or a file
	// forceProtected names (the session ledger, the manifest, the lock), which --force
	// never replaces.
	keep bool
}

// readTemplateTarget snapshots a template's destination, so the later write replaces
// exactly the bytes that were read.
func readTemplateTarget(ctx context.Context, destPath, rel string, force bool) (templateTarget, error) {
	before, err := contextopt.ReadSnapshot(ctx, destPath)
	exists := !errors.Is(err, os.ErrNotExist)
	if err != nil && exists {
		return templateTarget{}, fmt.Errorf("read %s: %w", rel, err)
	}
	keep := exists && (!force || forceProtected(rel))
	return templateTarget{before: before, exists: exists, keep: keep}, nil
}
