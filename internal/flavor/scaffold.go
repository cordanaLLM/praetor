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
		return "run:\n  timeout: 5m\nlinters:\n  enable:\n    - govet\n    - staticcheck\n    - errcheck\n"
	case ".gosec.json":
		return "{\n  \"global\": {\n    \"exclude\": \"G104,G301,G302,G304,G306,G204,G703\"\n  }\n}\n"
	case "Dockerfile":
		return "FROM gcr.io/distroless/static:nonroot\nWORKDIR /\nCOPY " + repoName + " /\nUSER 65532:65532\nENTRYPOINT [\"/" + repoName + "\"]\n"
	default:
		return fmt.Sprintf("# %s configuration for %s/%s\n", filepath.Base(path), owner, repoName)
	}
}
