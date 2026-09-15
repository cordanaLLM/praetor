package flavor

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/util"
)

// FlavorAuditReport contains the diagnostic result of auditing a repo against a flavor.
type FlavorAuditReport struct {
	Flavor              string          `json:"flavor"`
	RepoPath            string          `json:"repo_path"`
	Score               float64         `json:"score"`
	Passed              bool            `json:"passed"`
	TemplatesTotal      int             `json:"templates_total"`
	TemplatesPresent    int             `json:"templates_present"`
	MissingTemplates    []TemplateItem  `json:"missing_templates"`
	SettingsTotal       int             `json:"settings_total"`
	SettingsValid       int             `json:"settings_valid"`
	MissingSettings     []SettingItem   `json:"missing_settings"`
	ToolchainsTotal     int             `json:"toolchains_total"`
	ToolchainsAvailable int             `json:"toolchains_available"`
	MissingToolchains   []ToolchainItem `json:"missing_toolchains"`
}

// ErrNoFlavorMatched reports that nothing in the catalog fits the repository.
//
// This is returned rather than auditing against a guess. The audit drives which templates,
// settings and toolchains a repository is required to have, so auditing a repository against a
// flavor that does not describe it demands tooling it has no reason to install and reports a
// score that means nothing. Measured on cordanaLLM/imago, an OS image forge audited as a
// PyTorch pipeline and failed for lacking uv and ruff.
var ErrNoFlavorMatched = errors.New("flavor: no registered flavor matches this repository; pass an explicit --flavor")

// AuditFlavor audits a repository against a target flavor (or auto-detected if empty/"auto").
func AuditFlavor(repoPath string, targetFlavor string) (*FlavorAuditReport, error) {
	if targetFlavor == "" || targetFlavor == "auto" {
		detected, ok := Detect(repoPath)
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrNoFlavorMatched, repoPath)
		}
		targetFlavor = detected
	}

	flv, err := Get(targetFlavor)
	if err != nil {
		return nil, fmt.Errorf("audit flavor: %w", err)
	}

	report := &FlavorAuditReport{
		Flavor:   flv.Name(),
		RepoPath: repoPath,
	}

	auditTemplates(repoPath, flv.RequiredTemplates(), report)
	auditSettings(repoPath, flv.RequiredSettings(), report)
	auditToolchains(flv.RequiredToolchains(), report)

	total := report.TemplatesTotal + report.SettingsTotal + report.ToolchainsTotal
	present := report.TemplatesPresent + report.SettingsValid + report.ToolchainsAvailable
	if total > 0 {
		report.Score = (float64(present) / float64(total)) * 100.0
	} else {
		report.Score = 100.0
	}

	report.Passed = report.Score >= 80.0 && len(report.MissingTemplates) == 0
	return report, nil
}

func auditTemplates(repoPath string, templates []TemplateItem, report *FlavorAuditReport) {
	report.TemplatesTotal = len(templates)
	for _, t := range templates {
		fullPath := filepath.Join(repoPath, t.Path)
		if util.PathExists(fullPath) {
			report.TemplatesPresent++
		} else {
			report.MissingTemplates = append(report.MissingTemplates, t)
		}
	}
}

func auditSettings(repoPath string, settings []SettingItem, report *FlavorAuditReport) {
	report.SettingsTotal = len(settings)
	for _, s := range settings {
		fullPath := filepath.Join(repoPath, s.Path)
		if util.PathExists(fullPath) {
			report.SettingsValid++
		} else {
			report.MissingSettings = append(report.MissingSettings, s)
		}
	}
}

func auditToolchains(toolchains []ToolchainItem, report *FlavorAuditReport) {
	report.ToolchainsTotal = len(toolchains)
	for _, tc := range toolchains {
		if _, err := exec.LookPath(tc.Binary); err == nil {
			report.ToolchainsAvailable++
		} else {
			report.MissingToolchains = append(report.MissingToolchains, tc)
		}
	}
}
