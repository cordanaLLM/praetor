package flavor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// FlavorAuditReport contains the diagnostic result of auditing a repo against a flavor.
//
// Score is the percentage of required templates and settings the repository carries, and
// nothing else. Toolchain counts are advisory: they describe the machine running the audit,
// so folding them into the score made the same commit pass on one host and fail on another.
type FlavorAuditReport struct {
	Flavor   string `json:"flavor"`
	RepoPath string `json:"repo_path"`
	// Path is the repository-relative directory a pinned flavor was audited against, "." for
	// the root; it is set by AuditTargetsContext.
	Path             string         `json:"path,omitempty"`
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
	// ShadowedTemplates is advisory and never enters Score or Passed. It names each template
	// the repository carries under more than one of the names its tool searches.
	ShadowedTemplates []ShadowedTemplate `json:"shadowed_templates,omitempty"`
}

// ShadowedTemplate is a template carried under more than one of the names its tool searches
// (TemplateItem.Search). The tool reads InUse and never reads Ignored, so an edit to an ignored
// file changes nothing although the file looks like the configuration.
type ShadowedTemplate struct {
	Path    string   `json:"path"`
	Tool    string   `json:"tool"`
	InUse   string   `json:"in_use"`
	Ignored []string `json:"ignored"`
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
// score that means nothing. Measured on an adopter's OS image forge audited as a
// PyTorch pipeline and failed for lacking uv and ruff.
//
// The text names no remedy, because the remedy depends on the caller: `flavor audit` and
// `flavor apply` take --flavor and say so (cmd/standardsctl/flavor.go), while `gate run` has no
// such flag, and a sentinel telling its operator to pass one sent them to a flag that does not
// exist (#615).
var ErrNoFlavorMatched = errors.New("flavor: no registered flavor matches this repository")

// ErrFlavorNotApplicable reports a repository whose declared profile has no flavors at all.
//
// This is not a failure and callers must not treat it as one. Profiles and flavors are two
// taxonomies over the same repositories: a profile says what governance applies, a flavor says
// which templates, settings and toolchains the stack requires. Where a profile has no flavor --
// gitops-infra or pages-site, for instance -- there is nothing for this audit to check, and
// holding the repository to an inferred language flavor demands files that do not follow from
// anything it declared.
//
// Measured on an adopter's OS image forge declaring os-image before that profile had a flavor:
// it was held to Go service templates and failed its own push gate for lacking a Dockerfile it
// has no use for. os-image has had its own flavor since, so a forge declaring it that none of
// the flavor's markers match is ErrNoFlavorMatched, not this.
var ErrFlavorNotApplicable = errors.New("flavor: the declared profile has no flavor to audit or scaffold against")

// IsNotApplicable reports whether err means no flavor applies to the repository: its profile has
// none (ErrFlavorNotApplicable) or none of the profile's flavors matches it (ErrNoFlavorMatched).
// It is the one decision every caller that audits without a named flavor makes -- `flavor audit`,
// the gate's Flavor Conformance stage, the generated pre-push hook that runs the audit, adoption
// and the Hindsight distiller -- so they reach the same verdict on one checkout (#1111). A
// repository whose markers match a flavor is never skipped, and a pin (ResolveTargets) is read
// before detection, so it never reaches this decision.
func IsNotApplicable(err error) bool {
	return errors.Is(err, ErrFlavorNotApplicable) || errors.Is(err, ErrNoFlavorMatched)
}

// DefaultAuditTimeout bounds AuditFlavor, whose callers bring no deadline of their own
// (HISS-02). An audit reads a few dozen small files and resolves a handful of binaries.
const DefaultAuditTimeout = 60 * time.Second

// AuditFlavor audits a repository against a target flavor (for "" and "auto", the one root flavor
// ResolveTargets names: a pin, else the detected flavor; several or scoped pins are refused),
// bounded by DefaultAuditTimeout. Callers that carry a context use AuditFlavorContext.
func AuditFlavor(repoPath string, targetFlavor string) (*FlavorAuditReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultAuditTimeout)
	defer cancel()
	return AuditFlavorContext(ctx, repoPath, targetFlavor)
}

// AuditFlavorContext audits a repository against a target flavor under ctx. Every template
// and setting read and every toolchain lookup is preceded by a ctx check, so a cancelled or
// expired context stops the audit with ctx's error instead of finishing a verdict nobody is
// waiting for; the gate's flavor stage used to check ctx only before and after the whole audit.
func AuditFlavorContext(ctx context.Context, repoPath string, targetFlavor string) (*FlavorAuditReport, error) {
	if ctx == nil {
		return nil, errors.New("audit flavor: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("audit flavor cancelled: %w", err)
	}
	if targetFlavor == "" || targetFlavor == "auto" {
		resolved, err := resolveAutoTarget(repoPath)
		if err != nil {
			return nil, err
		}
		targetFlavor = resolved
	}

	flv, err := Get(targetFlavor)
	if err != nil {
		return nil, fmt.Errorf("audit flavor: %w", err)
	}
	return auditFlavorAt(ctx, repoPath, repoPath, flv)
}

// auditScoped audits one target of ResolveTargets. A target at the root is AuditFlavorContext's
// audit. One scoped to a directory audits the stack there and the repository-level items at the
// root, because a workflow, a ruleset or the editor settings sit at the root whichever
// directory holds the stack: a pin scoped to api/ never passed while they were required below it.
func auditScoped(ctx context.Context, repoPath string, target Target) (*FlavorAuditReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("audit flavor cancelled: %w", err)
	}
	flv, err := Get(target.Flavor)
	if err != nil {
		return nil, fmt.Errorf("audit flavor: %w", err)
	}
	report, err := auditFlavorAt(ctx, repoPath, filepath.Join(repoPath, filepath.FromSlash(target.Path)), flv)
	if err != nil {
		return nil, err
	}
	report.Path = target.Path
	return report, nil
}

// auditFlavorAt audits rootDir against flv, reading the stack's own items from stackDir; the two
// are one directory for an unscoped audit.
func auditFlavorAt(ctx context.Context, rootDir, stackDir string, flv Flavor) (*FlavorAuditReport, error) {
	report := &FlavorAuditReport{
		Flavor:   flv.Name(),
		RepoPath: stackDir,
	}
	if err := auditRequiredItems(ctx, rootDir, stackDir, flv, report); err != nil {
		return nil, err
	}

	report.Score = conformanceScore(report)
	report.Passed = report.Score >= passingScore && len(report.MissingTemplates) == 0
	return report, nil
}

// resolveAutoTarget names the one flavor a repository without an explicit target audits
// against; a repository with several pin targets is refused, since one report cannot cover them.
func resolveAutoTarget(repoPath string) (string, error) {
	targets, err := ResolveTargets(repoPath)
	if err != nil {
		return "", err
	}
	if len(targets) != 1 || targets[0].Path != "." {
		return "", fmt.Errorf("audit flavor: %s pins %d flavor targets; audit them with AuditTargetsContext", repoPath, len(targets))
	}
	return targets[0].Flavor, nil
}

// auditRequiredItems records flv's required templates, settings and toolchains in report,
// stopping at the first cancellation. Repository-level items are read from rootDir and the
// rest from stackDir (repositoryLevel); when the two are one directory the order is flv's own.
func auditRequiredItems(ctx context.Context, rootDir, stackDir string, flv Flavor, report *FlavorAuditReport) error {
	rootTemplates, stackTemplates := splitTemplates(flv.RequiredTemplates(), rootDir != stackDir)
	rootSettings, stackSettings := splitSettings(flv.RequiredSettings(), rootDir != stackDir)
	if err := auditTemplates(ctx, rootDir, rootTemplates, report); err != nil {
		return err
	}
	if err := auditTemplates(ctx, stackDir, stackTemplates, report); err != nil {
		return err
	}
	if err := auditSettings(ctx, rootDir, rootSettings, report); err != nil {
		return err
	}
	if err := auditSettings(ctx, stackDir, stackSettings, report); err != nil {
		return err
	}
	return auditToolchains(ctx, stackDir, flv.RequiredToolchains(), report)
}

// repositoryLevelPrefixes and repositoryLevelFiles name the items that belong to the
// repository, not to one stack directory: CI workflows, the branch ruleset, editor settings and
// the agent harness directories, the git hook manifest, the standards declaration and lock, and
// the agent context files.
var (
	repositoryLevelPrefixes = []string{".github/", ".vscode/", ".paperclip/"}
	repositoryLevelFiles    = []string{"lefthook.yml", ".gitleaks.toml", ".standards.yaml", ".standards.lock", "AGENTS.md", "CLAUDE.md"}
)

// repositoryLevel reports whether a template or setting path is read at the repository root
// when a flavor is pinned to a directory.
func repositoryLevel(path string) bool {
	if slices.Contains(repositoryLevelFiles, path) {
		return true
	}
	for _, prefix := range repositoryLevelPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// splitTemplates divides templates into repository-level and stack ones when scoped; unscoped,
// every template is a stack one, which keeps the flavor's order.
func splitTemplates(items []TemplateItem, scoped bool) (root, stack []TemplateItem) {
	for _, item := range items {
		if scoped && repositoryLevel(item.Path) {
			root = append(root, item)
		} else {
			stack = append(stack, item)
		}
	}
	return root, stack
}

// splitSettings is splitTemplates for settings.
func splitSettings(items []SettingItem, scoped bool) (root, stack []SettingItem) {
	for _, item := range items {
		if scoped && repositoryLevel(item.Path) {
			root = append(root, item)
		} else {
			stack = append(stack, item)
		}
	}
	return root, stack
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

// auditCancelled reports ctx's error, naming what the audit was checking when it stopped.
func auditCancelled(ctx context.Context, what, path string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("audit flavor cancelled before %s %s: %w", what, path, err)
	}
	return nil
}

func auditTemplates(ctx context.Context, repoPath string, templates []TemplateItem, report *FlavorAuditReport) error {
	report.TemplatesTotal += len(templates)
	for _, t := range templates {
		if err := auditCancelled(ctx, "template", t.Path); err != nil {
			return err
		}
		if TemplateSatisfied(repoPath, t) {
			report.TemplatesPresent++
		} else {
			report.MissingTemplates = append(report.MissingTemplates, t)
		}
		if shadowed, ok := shadowedTemplate(repoPath, t); ok {
			report.ShadowedTemplates = append(report.ShadowedTemplates, shadowed)
		}
	}
	return nil
}

// shadowedTemplate reports the searched names beyond the first that the repository carries
// for t, which t's tool never reads. A template without a search order shadows nothing.
func shadowedTemplate(repoPath string, t TemplateItem) (ShadowedTemplate, bool) {
	if t.Search == nil {
		return ShadowedTemplate{}, false
	}
	present := presentNames(repoPath, t)
	if len(present) < 2 {
		return ShadowedTemplate{}, false
	}
	return ShadowedTemplate{Path: t.Path, Tool: t.Search.Tool, InUse: present[0], Ignored: present[1:]}, true
}

// maxTemplateCandidates bounds the paths one template is looked up under (HISS-02).
const maxTemplateCandidates = 32

// presentNames returns, in lookup order, each of t's names the repository carries as a file,
// whatever its content and wherever a symbolic link there resolves: the files a tool looking
// for those names finds. The first is the configuration in use when flavor apply decides
// whether to scaffold, and the file a searched template's tool reads when the audit judges it.
func presentNames(repoPath string, t TemplateItem) []string {
	names := t.names()
	present := make([]string, 0, len(names))
	for i := 0; i < len(names) && i < maxTemplateCandidates; i++ {
		if util.FileExists(filepath.Join(repoPath, filepath.FromSlash(names[i]))) {
			present = append(present, names[i])
		}
	}
	return present
}

// TemplateSatisfied reports whether the repository carries the template under its
// canonical path or any accepted alternative, as a regular file whose content satisfies
// the template's validator.
//
// Presence used to be the whole check. The scaffolder wrote a one-line comment for every
// template it had no body for, and the audit then scored that comment as the workflow,
// the gitleaks policy or the agent harness it stood in for (BUG-028, BUG-029). A directory
// at the path configures nothing either, and a symbolic link counts only where it resolves
// to a regular file inside the repository: readRequiredFile is the single policy for both.
//
// A template with a search order (TemplateItem.Search) is judged on the one file its tool
// reads, the first of its names present. A valid file under a later name does not satisfy
// it, because the tool never reads that file.
func TemplateSatisfied(repoPath string, t TemplateItem) bool {
	candidates := t.names()
	if t.Search != nil {
		candidates = presentNames(repoPath, t)
		candidates = candidates[:min(len(candidates), 1)]
	}
	for i := 0; i < len(candidates) && i < maxTemplateCandidates; i++ {
		content, ok := readRequiredFile(repoPath, candidates[i])
		if ok && (t.Validator == nil || t.Validator(content)) {
			return true
		}
	}
	return false
}

func auditSettings(ctx context.Context, repoPath string, settings []SettingItem, report *FlavorAuditReport) error {
	report.SettingsTotal += len(settings)
	for _, s := range settings {
		if err := auditCancelled(ctx, "setting", s.Path); err != nil {
			return err
		}
		if SettingSatisfied(repoPath, s) {
			report.SettingsValid++
		} else {
			report.MissingSettings = append(report.MissingSettings, s)
		}
	}
	return nil
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
	content, ok := readRequiredFile(repoPath, s.Path)
	if !ok {
		return false
	}
	if s.Validator == nil {
		return true
	}
	return s.Validator(content)
}

// readRequiredFile reads one file a flavor requires, or reports that the repository does
// not carry it. Templates and settings share it, so both follow one policy:
//
//   - the path is confined to the repository, and a symbolic link is followed only when it
//     resolves inside it (util.ConfinePath);
//   - only a regular file counts. A directory configures nothing, and a FIFO or device at a
//     configuration path would block or stream the read rather than end it;
//   - the read is bounded by maxSettingBytes (util.ReadConfinedLimited).
func readRequiredFile(repoPath, rel string) ([]byte, bool) {
	path, err := util.ConfinePath(repoPath, rel)
	if err != nil {
		return nil, false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	content, err := util.ReadConfinedLimited(repoPath, rel, maxSettingBytes)
	if err != nil {
		return nil, false
	}
	return content, true
}

func auditToolchains(ctx context.Context, repoPath string, toolchains []ToolchainItem, report *FlavorAuditReport) error {
	report.ToolchainsTotal = 0
	for _, tc := range toolchains {
		if err := auditCancelled(ctx, "toolchain", tc.Binary); err != nil {
			return err
		}
		// An item for another build engine than this repository's is neither counted nor
		// reported missing (ToolchainItem.Markers).
		if !tc.appliesTo(repoPath) {
			continue
		}
		report.ToolchainsTotal++
		if toolchainAvailable(repoPath, tc) {
			report.ToolchainsAvailable++
		} else {
			report.MissingToolchains = append(report.MissingToolchains, tc)
		}
	}
	return nil
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
