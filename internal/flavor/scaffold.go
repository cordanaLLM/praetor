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
)

// distrolessRuntimeImage is the runtime stage every scaffolded Dockerfile receives.
//
// It names static-debian13 and a digest rather than the gcr.io/distroless/static
// alias. The alias resolves to this same digest today, which is exactly the trap:
// it is bound to whichever Debian release distroless currently promotes, so an
// adopter's Dockerfile silently retargets on the next promotion. HISS-11 reads
// "zero floating tags in container deployments", and .devcontainer/Dockerfile.praetor
// already uses the tag-plus-digest form this repository treats as the standard.
//
// Measured 2026-09-19: crane digest gcr.io/distroless/static-debian13:nonroot.
const distrolessRuntimeImage = "gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3"

// ApplyReport contains the outcome of applying a flavor scaffold to a repository.
type ApplyReport struct {
	Flavor            string   `json:"flavor"`
	CreatedTemplates  []string `json:"created_templates"`
	SkippedTemplates  []string `json:"skipped_templates"`
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
	if !force && TemplateSatisfied(repoPath, tmpl) {
		return true, nil
	}
	return false, nil
}

// templateContent resolves a template's body from its generator, falling back to the
// built-in default for that filename.
func templateContent(tmpl TemplateItem, repoName, owner string) string {
	if tmpl.ContentFunc != nil {
		return tmpl.ContentFunc(repoName, owner)
	}
	return defaultTemplateContent(tmpl.Path, repoName, owner)
}

func applySingleTemplate(ctx context.Context, repoPath string, tmpl TemplateItem, repoName, owner string, force bool, report *ApplyReport) {
	skip, dispErr := templateDisposition(repoPath, tmpl, force)
	if dispErr != nil {
		report.Errors = append(report.Errors, dispErr.Error())
		return
	}
	if skip {
		report.SkippedTemplates = append(report.SkippedTemplates, tmpl.Path)
		return
	}
	destPath := filepath.Join(repoPath, tmpl.Path)
	before, err := contextopt.ReadSnapshot(ctx, destPath)
	exists := !errors.Is(err, os.ErrNotExist)
	if err != nil && exists {
		report.Errors = append(report.Errors, fmt.Sprintf("read %s: %v", tmpl.Path, err))
		return
	}
	if exists && (!force || strings.HasPrefix(tmpl.Path, state.WorkingDirName+"/")) {
		report.SkippedTemplates = append(report.SkippedTemplates, tmpl.Path)
		return
	}

	content := templateContent(tmpl, repoName, owner)

	if err := contextopt.EnsureDirectory(ctx, filepath.Dir(destPath), 0o755); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("mkdir %s: %v", tmpl.Path, err))
		return
	}

	if err := contextopt.ReplaceSnapshot(ctx, destPath, []byte(content), contextopt.ReplaceOptions{Expected: before, Exists: exists, Mode: 0o644}); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("write %s: %v", tmpl.Path, err))
		return
	}

	report.CreatedTemplates = append(report.CreatedTemplates, tmpl.Path)
}

func defaultTemplateContent(path, repoName, owner string) string {
	switch filepath.Base(path) {
	case ".golangci.yml":
		// golangci-lint v2 schema. The enabled set is the correctness subset of
		// praetor's own gate, not a mirror of it: the repository's .golangci.yml
		// also enables gocyclo, gocognit and funlen (the three linters that carry
		// the HISS-04 caps) plus nolintlint, forbidigo, contextcheck, wastedassign,
		// copyloopvar and gochecknoinits. An adopter therefore receives HISS-07 and
		// HISS-10 enforcement here and not the HISS-04 caps: only the function-LOC
		// cap reaches them, through praetor's own scanner (internal/hiss/rules.go),
		// and the cyclomatic and cognitive caps have no scanner outside this lint
		// configuration at all.
		return "version: \"2\"\nrun:\n  timeout: 10m\nlinters:\n  default: none\n  enable:\n" +
			"    - govet\n    - staticcheck\n    - errcheck\n    - errorlint\n    - nilerr\n" +
			"    - unused\n    - ineffassign\n    - bodyclose\n    - noctx\n" +
			"  settings:\n    errcheck:\n      check-type-assertions: true\n      check-blank: true\n"
	case ".gosec.json":
		// Zero exclusions: every finding is fixed or carries a per-line
		// "#nosec Gxxx -- <reason>" justification.
		return "{\n  \"global\": {\n    \"exclude\": \"\"\n  }\n}\n"
	case "Dockerfile":
		return "FROM " + distrolessRuntimeImage + "\nWORKDIR /\nCOPY " + repoName + " /\nUSER 65532:65532\nENTRYPOINT [\"/" + repoName + "\"]\n"
	case "rustfmt.toml":
		return "edition = \"2024\"\nmax_width = 100\nnewline_style = \"Unix\"\nuse_small_heuristics = \"Default\"\n"
	case "clippy.toml":
		return "# Clippy linting configuration\navoid-breaking-exported-api = true\n"
	case "tsconfig.json":
		return "{\n  \"compilerOptions\": {\n    \"target\": \"es2022\",\n    \"module\": \"commonjs\",\n    \"strict\": true,\n    \"esModuleInterop\": true,\n    \"skipLibCheck\": true,\n    \"forceConsistentCasingInFileNames\": true,\n    \"outDir\": \"./dist\"\n  },\n  \"include\": [\"src/**/*\"]\n}\n"
	case eslintConfigPath:
		return eslintFlatConfig
	case "playwright.config.ts":
		return playwrightConfig
	case "checkstyle.xml":
		return "<?xml version=\"1.0\"?>\n<!DOCTYPE module PUBLIC\n  \"-//Checkstyle//DTD Checkstyle Configuration 1.3//EN\"\n  \"https://checkstyle.org/dtds/configuration_1_3.dtd\">\n<module name=\"Checker\">\n  <module name=\"TreeWalker\">\n    <module name=\"AvoidStarImport\"/>\n    <module name=\"NeedBraces\"/>\n  </module>\n</module>\n"
	case "analysis_options.yaml":
		return "include: package:lints/recommended.yaml\n\nlinter:\n  rules:\n    - prefer_const_constructors\n    - prefer_final_fields\n    - unawaited_futures\n"
	default:
		return fmt.Sprintf("# %s configuration for %s/%s\n", filepath.Base(path), owner, repoName)
	}
}
