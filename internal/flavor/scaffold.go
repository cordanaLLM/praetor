package flavor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
)

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
	// repository lacks what its body needs to work as written (TemplateItem.Requires), as
	// "<path>: <what is missing>". The audit still requires these files: supply what is
	// missing and apply again, or write a file that fits the repository.
	UnmetTemplates    []string `json:"unmet_templates,omitempty"`
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

	owner, repoName, err := util.ResolveRepoIdentity(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolve flavor repository identity: %w", err)
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

	return report, nil
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
	if !force && (TemplateSatisfied(repoPath, tmpl) || alternativePresent(repoPath, tmpl)) {
		return true, nil
	}
	return false, nil
}

// alternativePresent reports whether the repository carries a file under any of a
// template's AltPaths, whatever its content and wherever a symbolic link there resolves.
//
// TemplateSatisfied is the audit's question: does a valid file inside the repository cover
// the template. Apply asks a narrower one: is a configuration already in use under another
// name. A monorepo's tsconfig.base.json linked from a shared root, or one the validator
// rejects, is still the file the toolchain reads, and a canonical tsconfig.json written
// beside it is the contradictory second config AltPaths exists to prevent. The audit keeps
// reporting the alternative's content; apply only declines to add a rival.
func alternativePresent(repoPath string, tmpl TemplateItem) bool {
	for i := 0; i < len(tmpl.AltPaths) && i < maxTemplateCandidates; i++ {
		if util.FileExists(filepath.Join(repoPath, filepath.FromSlash(tmpl.AltPaths[i]))) {
			return true
		}
	}
	return false
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
	templateUnmet
)

func applySingleTemplate(ctx context.Context, repoPath string, tmpl TemplateItem, repoName, owner string, force bool, report *ApplyReport) {
	outcome, note, err := scaffoldTemplate(ctx, repoPath, tmpl, repoName, owner, force)
	switch {
	case err != nil:
		report.Errors = append(report.Errors, err.Error())
	case outcome == templateSkipped:
		report.SkippedTemplates = append(report.SkippedTemplates, tmpl.Path)
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
// producer of a deferred template and what an unmet one lacks.
func scaffoldTemplate(ctx context.Context, repoPath string, tmpl TemplateItem, repoName, owner string, force bool) (templateOutcome, string, error) {
	skip, err := templateDisposition(repoPath, tmpl, force)
	if err != nil || skip {
		return templateSkipped, "", err
	}
	if outcome, note := templateWithheld(ctx, repoPath, tmpl); outcome != templateCreated {
		return outcome, note, nil
	}
	destPath := filepath.Join(repoPath, tmpl.Path)
	target, err := readTemplateTarget(ctx, destPath, tmpl.Path, force)
	if err != nil || target.keep {
		return templateSkipped, "", err
	}
	content, err := templateContent(tmpl, repoName, owner)
	if err != nil {
		return templateSkipped, "", err
	}
	if err := contextopt.EnsureDirectory(ctx, filepath.Dir(destPath), 0o755); err != nil {
		return templateSkipped, "", fmt.Errorf("mkdir %s: %w", tmpl.Path, err)
	}
	options := contextopt.ReplaceOptions{Expected: target.before, Exists: target.exists, Mode: 0o644}
	if err := contextopt.ReplaceSnapshot(ctx, destPath, []byte(content), options); err != nil {
		return templateSkipped, "", fmt.Errorf("write %s: %w", tmpl.Path, err)
	}
	return templateCreated, "", nil
}

// templateWithheld reports why flavor apply writes no body for a template whatever --force
// says, or templateCreated when nothing withholds it. A producer-owned template has no body
// here, only a placeholder to lose the real file to. A template whose requirement the
// repository does not meet has a body that cannot work there, and writing it over an existing
// file would replace one that might.
func templateWithheld(ctx context.Context, repoPath string, tmpl TemplateItem) (templateOutcome, string) {
	if tmpl.Producer != "" {
		return templateDeferred, tmpl.Producer
	}
	if tmpl.Requires == nil {
		return templateCreated, ""
	}
	if missing := tmpl.Requires(ctx, repoPath); missing != "" {
		return templateUnmet, missing
	}
	return templateCreated, ""
}

// templateTarget is the file already at a template's destination.
type templateTarget struct {
	before []byte
	exists bool
	// keep reports that the existing file stays: present without --force, or a session
	// ledger file, which --force never replaces.
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
	keep := exists && (!force || strings.HasPrefix(rel, state.WorkingDirName+"/"))
	return templateTarget{before: before, exists: exists, keep: keep}, nil
}
