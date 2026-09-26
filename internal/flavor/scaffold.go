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
	WorkingDirCreated bool     `json:"working_dir_created"`
	Errors            []string `json:"errors,omitempty"`
}

// ApplyFlavor scaffolds the missing templates and configs for a target flavor.
func ApplyFlavor(ctx context.Context, repoPath string, targetFlavor string, force bool) (*ApplyReport, error) {
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

	// 2. Scaffold required templates
	for _, tmpl := range flv.RequiredTemplates() {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		applySingleTemplate(ctx, repoPath, tmpl, repoName, owner, force, report)
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

// resolveApplyTarget names the flavor an apply scaffolds: the explicit one, or the detected one
// for "" and "auto". A repository nothing matches is refused, never given a guessed flavor.
func resolveApplyTarget(repoPath, targetFlavor string) (Flavor, error) {
	if targetFlavor == "" || targetFlavor == "auto" {
		detected, ok := Detect(repoPath)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrNoFlavorMatched, repoPath)
		}
		targetFlavor = detected
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

// templateDisposition decides, before any filesystem mutation, whether a template is
// safe to write, already covered, or must be refused outright.
func templateDisposition(repoPath string, tmpl TemplateItem, force bool) (skip bool, err error) {
	// A template path is declared in slash form (".github/workflows/ci.yml"), so its
	// cleanliness is a slash-path property. filepath.Clean returns backslashes on Windows
	// and never equalled the declared value, so every template was refused and `flavor
	// apply` could scaffold nothing there. IsLocal still decides containment on the host.
	if !filepath.IsLocal(tmpl.Path) || path.Clean(tmpl.Path) != tmpl.Path {
		return false, fmt.Errorf("template path must remain within the repository: %s", tmpl.Path)
	}
	// An accepted alternative already covers this template, so scaffolding the canonical
	// name would add a second configuration file that contradicts the one in use.
	if !force && TemplateSatisfied(repoPath, tmpl) {
		return true, nil
	}
	return false, nil
}

// templateContent resolves a template's body from its generator or its embedded source.
//
// A template with neither has no content behind it, and that is an error. This used to
// fall back to a one-line "# <file> configuration for <owner>/<repo>" comment, which
// disabled every gitleaks rule (#410), scaffolded workflows that ran nothing, and was then
// scored by the audit as the file it stood in for (BUG-028, BUG-029).
func templateContent(tmpl TemplateItem, repoName, owner string) (string, error) {
	if tmpl.ContentFunc != nil {
		return tmpl.ContentFunc(repoName, owner), nil
	}
	if tmpl.Source == "" {
		return "", fmt.Errorf("template %s has no content source", tmpl.Path)
	}
	body, err := templates.RenderFile(tmpl.Source, templates.Context{RepoName: repoName, Owner: owner})
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
)

func applySingleTemplate(ctx context.Context, repoPath string, tmpl TemplateItem, repoName, owner string, force bool, report *ApplyReport) {
	outcome, err := scaffoldTemplate(ctx, repoPath, tmpl, repoName, owner, force)
	switch {
	case err != nil:
		report.Errors = append(report.Errors, err.Error())
	case outcome == templateSkipped:
		report.SkippedTemplates = append(report.SkippedTemplates, tmpl.Path)
	case outcome == templateDeferred:
		report.DeferredTemplates = append(report.DeferredTemplates, fmt.Sprintf("%s (%s)", tmpl.Path, tmpl.Producer))
	default:
		report.CreatedTemplates = append(report.CreatedTemplates, tmpl.Path)
	}
}

// scaffoldTemplate writes one template unless it is covered, owned by another command, or
// already present without --force. A producer-owned template is never written, --force
// included: flavor apply has no body for it, only a placeholder to lose the real file to.
func scaffoldTemplate(ctx context.Context, repoPath string, tmpl TemplateItem, repoName, owner string, force bool) (templateOutcome, error) {
	skip, err := templateDisposition(repoPath, tmpl, force)
	if err != nil || skip {
		return templateSkipped, err
	}
	if tmpl.Producer != "" {
		return templateDeferred, nil
	}
	destPath := filepath.Join(repoPath, tmpl.Path)
	target, err := readTemplateTarget(ctx, destPath, tmpl.Path, force)
	if err != nil || target.keep {
		return templateSkipped, err
	}
	content, err := templateContent(tmpl, repoName, owner)
	if err != nil {
		return templateSkipped, err
	}
	if err := contextopt.EnsureDirectory(ctx, filepath.Dir(destPath), 0o755); err != nil {
		return templateSkipped, fmt.Errorf("mkdir %s: %w", tmpl.Path, err)
	}
	options := contextopt.ReplaceOptions{Expected: target.before, Exists: target.exists, Mode: 0o644}
	if err := contextopt.ReplaceSnapshot(ctx, destPath, []byte(content), options); err != nil {
		return templateSkipped, fmt.Errorf("write %s: %w", tmpl.Path, err)
	}
	return templateCreated, nil
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
