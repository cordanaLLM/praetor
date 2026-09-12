package flavor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/util"
)

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
	if targetFlavor == "" || targetFlavor == "auto" {
		targetFlavor = DetectFlavor(repoPath)
	}

	flv, err := Get(targetFlavor)
	if err != nil {
		return nil, fmt.Errorf("apply flavor: %w", err)
	}

	owner, repoName, _ := util.ResolveRepoIdentity(ctx, repoPath)
	report := &ApplyReport{
		Flavor: flv.Name(),
	}

	// 1. Initialize .workingdir/
	if err := state.InitWorkingDir(repoPath); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("workingdir init: %v", err))
	} else {
		report.WorkingDirCreated = true
	}

	// 2. Scaffold required templates
	for _, tmpl := range flv.RequiredTemplates() {
		applySingleTemplate(repoPath, tmpl, repoName, owner, force, report)
	}

	return report, nil
}

func applySingleTemplate(repoPath string, tmpl TemplateItem, repoName, owner string, force bool, report *ApplyReport) {
	destPath := filepath.Join(repoPath, tmpl.Path)
	if util.PathExists(destPath) && !force {
		report.SkippedTemplates = append(report.SkippedTemplates, tmpl.Path)
		return
	}

	content := ""
	if tmpl.ContentFunc != nil {
		content = tmpl.ContentFunc(repoName, owner)
	} else {
		content = defaultTemplateContent(tmpl.Path, repoName, owner)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("mkdir %s: %v", tmpl.Path, err))
		return
	}

	if err := os.WriteFile(destPath, []byte(content), 0644); err != nil {
		report.Errors = append(report.Errors, fmt.Sprintf("write %s: %v", tmpl.Path, err))
		return
	}

	report.CreatedTemplates = append(report.CreatedTemplates, tmpl.Path)
}

func defaultTemplateContent(path, repoName, owner string) string {
	switch filepath.Base(path) {
	case ".golangci.yml":
		// golangci-lint v2 schema; the enabled set mirrors praetor's own HISS-10 gate.
		return "version: \"2\"\nrun:\n  timeout: 10m\nlinters:\n  default: none\n  enable:\n" +
			"    - govet\n    - staticcheck\n    - errcheck\n    - errorlint\n    - nilerr\n" +
			"    - unused\n    - ineffassign\n    - bodyclose\n    - noctx\n" +
			"  settings:\n    errcheck:\n      check-type-assertions: true\n      check-blank: true\n"
	case ".gosec.json":
		// Zero exclusions: every finding is fixed or carries a per-line
		// "#nosec Gxxx -- <reason>" justification.
		return "{\n  \"global\": {\n    \"exclude\": \"\"\n  }\n}\n"
	case "Dockerfile":
		return "FROM gcr.io/distroless/static:nonroot\nWORKDIR /\nCOPY " + repoName + " /\nUSER 65532:65532\nENTRYPOINT [\"/" + repoName + "\"]\n"
	case "rustfmt.toml":
		return "edition = \"2021\"\nmax_width = 100\nnewline_style = \"Unix\"\nuse_small_heuristics = \"Default\"\n"
	case "clippy.toml":
		return "# Clippy linting configuration\navoid-breaking-exported-api = true\n"
	case "tsconfig.json":
		return "{\n  \"compilerOptions\": {\n    \"target\": \"es2022\",\n    \"module\": \"commonjs\",\n    \"strict\": true,\n    \"esModuleInterop\": true,\n    \"skipLibCheck\": true,\n    \"forceConsistentCasingInFileNames\": true,\n    \"outDir\": \"./dist\"\n  },\n  \"include\": [\"src/**/*\"]\n}\n"
	case ".eslintrc.json":
		return "{\n  \"env\": {\n    \"node\": true,\n    \"es2022\": true\n  },\n  \"extends\": [\"eslint:recommended\"],\n  \"parserOptions\": {\n    \"ecmaVersion\": \"latest\",\n    \"sourceType\": \"module\"\n  }\n}\n"
	case "checkstyle.xml":
		return "<?xml version=\"1.0\"?>\n<!DOCTYPE module PUBLIC\n  \"-//Checkstyle//DTD Checkstyle Configuration 1.3//EN\"\n  \"https://checkstyle.org/dtds/configuration_1_3.dtd\">\n<module name=\"Checker\">\n  <module name=\"TreeWalker\">\n    <module name=\"AvoidStarImport\"/>\n    <module name=\"NeedBraces\"/>\n  </module>\n</module>\n"
	case "analysis_options.yaml":
		return "include: package:lints/recommended.yaml\n\nlinter:\n  rules:\n    - prefer_const_constructors\n    - prefer_final_fields\n    - unawaited_futures\n"
	default:
		return fmt.Sprintf("# %s configuration for %s/%s\n", filepath.Base(path), owner, repoName)
	}
}
