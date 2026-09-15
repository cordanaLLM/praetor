package flavor

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"

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

// ErrFlavorNotApplicable reports a repository whose declared profile has no flavors at all.
//
// This is not a failure and callers must not treat it as one. Profiles and flavors are two
// taxonomies over the same repositories: a profile says what governance applies, a flavor says
// which templates, settings and toolchains the language stack requires. Where a profile has no
// flavor -- os-image, for instance, which describes what a repository builds rather than what it
// is written in -- there is nothing for this audit to check, and holding the repository to an
// inferred language flavor demands files that do not follow from anything it declared.
//
// Measured on cordanaLLM/imago, an OS image forge declaring os-image, which was held to Go
// service templates and failed its own push gate for lacking a Dockerfile it has no use for.
var ErrFlavorNotApplicable = errors.New("flavor: the declared profile has no flavor to audit against")

// declaredProfile returns the repository's first declared profile, or "" when it declares none.
// An unreadable manifest is not a declaration, so detection proceeds as though none were present.
func declaredProfile(repoPath string) string {
	manifest, err := config.LoadManifest(filepath.Join(repoPath, ".standards.yaml"))
	if err != nil || manifest == nil || len(manifest.Profiles) == 0 {
		return ""
	}
	return strings.TrimSpace(manifest.Profiles[0])
}

// flavorsForProfile returns the registered flavors that implement one HISS profile.
func flavorsForProfile(profile string) []Flavor {
	all := List()
	matched := make([]Flavor, 0, len(all))
	for i := 0; i < len(all) && i < maxDetectionCandidates; i++ {
		if all[i].HISSProfile() == profile {
			matched = append(matched, all[i])
		}
	}
	return matched
}

// resolveAuditTarget decides which flavor to audit a repository against.
//
// The repository's own declaration comes first, and it narrows rather than replaces detection:
// a declared profile restricts the candidates to the flavors that implement it, and detection
// then picks among those. That keeps a Go framework repository detecting go-service rather than
// go-library while stopping an OS image forge from being measured as either.
func resolveAuditTarget(repoPath string) (string, error) {
	if profile := declaredProfile(repoPath); profile != "" {
		candidates := flavorsForProfile(profile)
		if len(candidates) == 0 {
			return "", fmt.Errorf("%w: profile %q", ErrFlavorNotApplicable, profile)
		}
		for i := 0; i < len(candidates) && i < maxDetectionCandidates; i++ {
			if candidates[i].Detect(repoPath) {
				return candidates[i].Name(), nil
			}
		}
		return "", fmt.Errorf("%w: profile %q declares flavors but none match %s", ErrNoFlavorMatched, profile, repoPath)
	}
	detected, ok := Detect(repoPath)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrNoFlavorMatched, repoPath)
	}
	return detected, nil
}

// AuditFlavor audits a repository against a target flavor (or auto-detected if empty/"auto").
func AuditFlavor(repoPath string, targetFlavor string) (*FlavorAuditReport, error) {
	if targetFlavor == "" || targetFlavor == "auto" {
		resolved, err := resolveAuditTarget(repoPath)
		if err != nil {
			return nil, err
		}
		targetFlavor = resolved
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
