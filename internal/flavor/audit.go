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
//
// Score is the percentage of required templates and settings the repository carries, and
// nothing else. Toolchain counts are advisory: they describe the machine running the audit,
// so folding them into the score made the same commit pass on one host and fail on another.
type FlavorAuditReport struct {
	Flavor           string         `json:"flavor"`
	RepoPath         string         `json:"repo_path"`
	Score            float64        `json:"score"`
	Passed           bool           `json:"passed"`
	TemplatesTotal   int            `json:"templates_total"`
	TemplatesPresent int            `json:"templates_present"`
	MissingTemplates []TemplateItem `json:"missing_templates"`
	SettingsTotal    int            `json:"settings_total"`
	// SettingsValid counts settings that are present and, where the setting declares a
	// validator, parse. MissingSettings names the rest: absent and malformed alike, since
	// a file the reading tool rejects configures no more than a file that is not there.
	SettingsValid   int           `json:"settings_valid"`
	MissingSettings []SettingItem `json:"missing_settings"`
	// The toolchain fields are advisory and never enter Score or Passed.
	ToolchainsTotal     int             `json:"toolchains_total"`
	ToolchainsAvailable int             `json:"toolchains_available"`
	MissingToolchains   []ToolchainItem `json:"missing_toolchains"`
}

// maxReportedSettings bounds the setting paths a one-line verdict carries (HISS-02).
const maxReportedSettings = 64

// InvalidSettingPaths returns the repository-relative path of every setting the report
// counted against the score -- absent and present-but-unparsable alike, which is what
// MissingSettings holds.
//
// It exists for callers whose whole verdict is one line, such as the gate stage: a report
// that says "77.8%, 0 missing templates" names nothing an operator can act on, because the
// files that cost the score are settings. Callers with room for a block range over
// MissingSettings directly and print the name and description with the path.
func (r *FlavorAuditReport) InvalidSettingPaths() []string {
	if r == nil || len(r.MissingSettings) == 0 {
		return nil
	}
	paths := make([]string, 0, len(r.MissingSettings))
	for i := 0; i < len(r.MissingSettings) && i < maxReportedSettings; i++ {
		paths = append(paths, r.MissingSettings[i].Path)
	}
	return paths
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
	auditToolchains(repoPath, flv.RequiredToolchains(), report)

	report.Score = conformanceScore(report)
	report.Passed = report.Score >= passingScore && len(report.MissingTemplates) == 0
	return report, nil
}

// passingScore is the share of required templates and settings a repository must carry.
const passingScore = 80.0

// conformanceScore returns that share as a percentage.
//
// Toolchains are reported but never scored. They are resolved through exec.LookPath, so
// scoring them measures the machine running the audit rather than the repository: a
// conforming go-service repository (8 templates, 3 settings, 4 toolchains) scored
// 11/15 = 73.3% and failed the 80% bar on a host with none of its four tools installed,
// in an audit that runs in the generated pre-push hook. The repository had not changed.
// MissingToolchains stays in the report and in the CLI output as advice.
//
// A flavor that requires no templates and no settings scores 100: nothing can be missing.
func conformanceScore(report *FlavorAuditReport) float64 {
	total := report.TemplatesTotal + report.SettingsTotal
	if total == 0 {
		return 100.0
	}
	present := report.TemplatesPresent + report.SettingsValid
	return (float64(present) / float64(total)) * 100.0
}

func auditTemplates(repoPath string, templates []TemplateItem, report *FlavorAuditReport) {
	report.TemplatesTotal = len(templates)
	for _, t := range templates {
		if TemplateSatisfied(repoPath, t) {
			report.TemplatesPresent++
		} else {
			report.MissingTemplates = append(report.MissingTemplates, t)
		}
	}
}

// TemplateSatisfied reports whether the repository carries the template under its
// canonical path or any accepted alternative. Every template is a file, so a directory
// that happens to carry the name configures nothing and does not satisfy it.
func TemplateSatisfied(repoPath string, t TemplateItem) bool {
	if util.FileExists(filepath.Join(repoPath, t.Path)) {
		return true
	}
	for _, alt := range t.AltPaths {
		if util.FileExists(filepath.Join(repoPath, alt)) {
			return true
		}
	}
	return false
}

func auditSettings(repoPath string, settings []SettingItem, report *FlavorAuditReport) {
	report.SettingsTotal = len(settings)
	for _, s := range settings {
		if SettingSatisfied(repoPath, s) {
			report.SettingsValid++
		} else {
			report.MissingSettings = append(report.MissingSettings, s)
		}
	}
}

// maxSettingBytes bounds a setting file read (HISS-02). A configuration file larger than
// this is not one, and reading it to find out is how an audit becomes a memory fault.
//
// The bound is enforced by the read itself, through util.ReadConfinedLimited: the audit
// runs in the generated pre-push hook (internal/adopt/hooks.go) and in the gate pipeline,
// where a repository carrying a multi-GB generated artefact at a settings path would
// otherwise be allocated in full before the length was judged.
const maxSettingBytes = 1 << 20

// SettingSatisfied reports whether the repository carries the setting and, where the setting
// declares a validator, whether the content satisfies it.
//
// A file that is present but does not parse is not a satisfied setting: it is a file the tool
// reading that path will reject. Absent, unreadable and implausibly large are equally
// unsatisfied, because the audit can claim nothing about content it never read.
func SettingSatisfied(repoPath string, s SettingItem) bool {
	content, err := util.ReadConfinedLimited(repoPath, s.Path, maxSettingBytes)
	if err != nil {
		return false
	}
	if s.Validator == nil {
		return true
	}
	return s.Validator(content)
}

func auditToolchains(repoPath string, toolchains []ToolchainItem, report *FlavorAuditReport) {
	report.ToolchainsTotal = len(toolchains)
	for _, tc := range toolchains {
		if toolchainAvailable(repoPath, tc) {
			report.ToolchainsAvailable++
		} else {
			report.MissingToolchains = append(report.MissingToolchains, tc)
		}
	}
}

func toolchainAvailable(repoPath string, tc ToolchainItem) bool {
	for _, binary := range append([]string{tc.Binary}, tc.AltBinaries...) {
		if _, err := exec.LookPath(binary); err == nil {
			return true
		}
		if tc.ProjectLocal && util.PathExists(filepath.Join(repoPath, "node_modules", ".bin", binary)) {
			return true
		}
	}
	return false
}
