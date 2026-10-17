package ruleregistry

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

const (
	maxTestFiles        = 256
	maxAuditRules       = 256
	maxEvidencePointers = 32
)

// Row is one known rule's audit result.
type Row struct {
	ID        string            `json:"id"`
	Class     Class             `json:"class"`
	Evidence  string            `json:"evidence"`
	Enforced  bool              `json:"enforced"`
	ProseOnly bool              `json:"prose_only"`
	Candidate bool              `json:"convert_or_delete_candidate"`
	Forge     []ForgeComparison `json:"forge,omitempty"`
}

// Report is the complete, deterministic registry audit result.
type Report struct {
	Rows     []Row    `json:"rules"`
	Problems []string `json:"problems,omitempty"`
}

// Valid reports whether all declared entries and evidence pointers resolved.
func (r *Report) Valid() bool { return r != nil && len(r.Problems) == 0 }

// Counts returns the number of known rules in each declared class.
func (r *Report) Counts() map[Class]int {
	counts := map[Class]int{}
	if r == nil {
		return counts
	}
	for i := 0; i < len(r.Rows) && i < maxAuditRules; i++ {
		counts[r.Rows[i].Class]++
	}
	return counts
}

// ProseOnlyIDs returns every rule that currently has no executable enforcement claim.
func (r *Report) ProseOnlyIDs() []string {
	ids := []string{}
	if r == nil {
		return ids
	}
	for i := 0; i < len(r.Rows) && i < maxAuditRules; i++ {
		if r.Rows[i].ProseOnly {
			ids = append(ids, r.Rows[i].ID)
		}
	}
	return ids
}

// UnenforcedIDs returns every rule without a resolved executable enforcement path.
func (r *Report) UnenforcedIDs() []string {
	ids := []string{}
	if r == nil {
		return ids
	}
	for i := 0; i < len(r.Rows) && i < maxAuditRules; i++ {
		if !r.Rows[i].Enforced {
			ids = append(ids, r.Rows[i].ID)
		}
	}
	return ids
}

// Audit classifies every known rule and resolves each declared evidence pointer.
func Audit(ctx context.Context, root string) (*Report, error) {
	return AuditWithOptions(ctx, root, AuditOptions{})
}

// AuditWithOptions classifies known rules and resolves local and live forge evidence.
func AuditWithOptions(ctx context.Context, root string, opts AuditOptions) (*Report, error) {
	known, err := KnownRules(ctx, root)
	if err != nil {
		return nil, err
	}
	registry, err := Load(ctx, root)
	if err != nil {
		return nil, err
	}
	report := &Report{}
	entries := indexEntries(registry, known, report)
	forgeStates := loadForgeStates(ctx, registry, opts, report)
	for i := 0; i < len(known) && i < maxAuditRules; i++ {
		row := auditRule(ctx, root, known[i], entries[known[i].ID], forgeStates, report)
		report.Rows = append(report.Rows, row)
	}
	if report.Valid() {
		return report, nil
	}
	return report, fmt.Errorf("rules audit: %d invalid declaration(s)", len(report.Problems))
}

func indexEntries(registry *Registry, known []Rule, report *Report) map[string]*Entry {
	knownIDs := make(map[string]bool, len(known))
	for i := 0; i < len(known) && i < maxAuditRules; i++ {
		knownIDs[known[i].ID] = true
	}
	entries := make(map[string]*Entry, len(registry.Rules))
	if registry.Version != 1 {
		report.Problems = append(report.Problems, fmt.Sprintf("registry version %d is unsupported", registry.Version))
	}
	for i := 0; i < len(registry.Rules) && i < maxAuditRules; i++ {
		entry := &registry.Rules[i]
		if !knownIDs[entry.ID] {
			report.Problems = append(report.Problems, fmt.Sprintf("unknown rule id %q", entry.ID))
			continue
		}
		if _, exists := entries[entry.ID]; exists {
			report.Problems = append(report.Problems, fmt.Sprintf("duplicate rule id %q", entry.ID))
			continue
		}
		entries[entry.ID] = entry
		validateEntry(entry, report)
	}
	return entries
}

func validateEntry(entry *Entry, report *Report) {
	if !validClass(entry.Class) {
		report.Problems = append(report.Problems, fmt.Sprintf("%s: unknown class %q", entry.ID, entry.Class))
	}
	if entry.Class == ClassGuidance && len(entry.Evidence) > 0 {
		report.Problems = append(report.Problems, fmt.Sprintf("%s: guidance must not carry evidence", entry.ID))
	}
	if entry.Class != ClassGuidance && len(entry.Evidence) == 0 {
		report.Problems = append(report.Problems, fmt.Sprintf("%s: %s requires evidence", entry.ID, entry.Class))
	}
	for i := 0; i < len(entry.Evidence) && i < maxEvidencePointers; i++ {
		if len(entry.Evidence[i]) > maxPointerLength {
			report.Problems = append(report.Problems, fmt.Sprintf("%s: evidence pointer exceeds %d bytes", entry.ID, maxPointerLength))
		}
	}
}

func auditRule(
	ctx context.Context,
	root string,
	rule Rule,
	entry *Entry,
	forgeStates map[ForgeSetting]ForgeState,
	report *Report,
) Row {
	if entry == nil {
		return proseRow(rule, "guidance")
	}
	row := Row{ID: rule.ID, Class: entry.Class}
	if entry.Class == ClassGuidance {
		row = proseRow(rule, entry.Class)
		return row
	}
	if len(entry.Evidence) == 0 {
		row.Evidence = "unresolved: no evidence"
		return row
	}
	resolver := evidenceResolver{
		ctx:         ctx,
		root:        root,
		ruleID:      rule.ID,
		forgeStates: forgeStates,
		row:         &row,
		report:      report,
	}
	resolution := resolver.resolve(entry.Evidence)
	row.Enforced = row.Enforced || resolution.localPointers > 0 && resolution.localResolved
	row.Evidence = evidenceSummary(row, resolution.localResolved, resolution.forgePending)
	return row
}

type evidenceResolution struct {
	localPointers int
	localResolved bool
	forgePending  bool
}

type evidenceResolver struct {
	ctx         context.Context
	root        string
	ruleID      string
	forgeStates map[ForgeSetting]ForgeState
	row         *Row
	report      *Report
}

func (r evidenceResolver) resolve(pointers []string) evidenceResolution {
	result := evidenceResolution{localResolved: true}
	for i := 0; i < len(pointers) && i < maxEvidencePointers; i++ {
		pointer := pointers[i]
		if strings.HasPrefix(pointer, "forge:") {
			matched, pending := r.resolveForgePointer(pointer)
			r.row.Enforced = r.row.Enforced || matched
			result.forgePending = result.forgePending || pending
			continue
		}
		result.localPointers++
		if err := resolve(r.ctx, r.root, pointer); err != nil {
			result.localResolved = false
			r.row.Evidence = "unresolved: " + pointer
			r.report.Problems = append(r.report.Problems, fmt.Sprintf("%s: %v", r.ruleID, err))
		}
	}
	return result
}

func (r evidenceResolver) resolveForgePointer(pointer string) (bool, bool) {
	setting, ok := forgeSetting(pointer)
	if !ok {
		r.report.Problems = append(r.report.Problems, fmt.Sprintf("%s: unresolved evidence pointer %q", r.ruleID, pointer))
		return false, false
	}
	state, ok := r.forgeStates[setting]
	if !ok || state.Asserted == "" {
		r.report.Problems = append(r.report.Problems,
			fmt.Sprintf("%s: forge setting %s has no committed assertion", r.ruleID, setting))
		return false, false
	}
	comparison := ForgeComparison{Setting: setting, Asserted: state.Asserted, Observed: state.Observed,
		Match: state.Match, ReadBack: state.ReadBack}
	r.row.Forge = append(r.row.Forge, comparison)
	if !state.AssertedEnforced {
		r.report.Problems = append(r.report.Problems,
			fmt.Sprintf("%s: forge setting %s is asserted as %s", r.ruleID, setting, state.Asserted))
		return false, false
	}
	if !state.ReadBack {
		return false, true
	}
	if state.Match {
		return true, false
	}
	r.report.Problems = append(r.report.Problems, fmt.Sprintf(
		"%s: forge setting %s mismatch: asserted %s, observed %s", r.ruleID, setting, state.Asserted, state.Observed))
	return false, false
}

func evidenceSummary(row Row, localResolved, forgePending bool) string {
	switch {
	case !localResolved:
		return row.Evidence
	case row.Enforced && forgePending:
		return "resolved; forge asserted, not read back"
	case row.Enforced:
		return "resolved"
	case forgePending:
		return "asserted, not read back"
	default:
		return "unresolved"
	}
}

func proseRow(rule Rule, class Class) Row {
	return Row{ID: rule.ID, Class: class, Evidence: "prose-only", ProseOnly: true, Candidate: prohibited(rule.Text)}
}

func resolve(ctx context.Context, root, pointer string) error {
	kind, value, ok := strings.Cut(pointer, ":")
	if !ok || value == "" || len(pointer) > maxPointerLength {
		return fmt.Errorf("unresolved evidence pointer %q", pointer)
	}
	switch kind {
	case "test":
		return resolveTest(ctx, root, value)
	case "hook":
		return resolveHook(ctx, root, value)
	case "make":
		return resolveMake(ctx, root, value)
	case "workflow":
		return resolveWorkflow(ctx, root, value)
	case "gate":
		if slices.Contains(gating.StageNames(), value) {
			return nil
		}
	}
	return fmt.Errorf("unresolved evidence pointer %q", pointer)
}

func resolveTest(ctx context.Context, root, value string) error {
	index := strings.LastIndex(value, ".")
	if index < 1 || index == len(value)-1 {
		return fmt.Errorf("unresolved evidence pointer %q", "test:"+value)
	}
	directory, name := value[:index], value[index+1:]
	if !safeRelative(directory) || !regexp.MustCompile(`^Test[A-Za-z0-9_]+$`).MatchString(name) {
		return fmt.Errorf("unresolved evidence pointer %q", "test:"+value)
	}
	files, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(directory), "*_test.go"))
	if err != nil || len(files) > maxTestFiles {
		return fmt.Errorf("unresolved evidence pointer %q", "test:"+value)
	}
	matcher := regexp.MustCompile(`(?m)^func\s+` + regexp.QuoteMeta(name) + `\s*\(`)
	for i := 0; i < len(files); i++ {
		data, readErr := readFile(ctx, root, filepath.ToSlash(strings.TrimPrefix(files[i], root+string(filepath.Separator))))
		if readErr == nil && matcher.Match(data) {
			return nil
		}
	}
	return fmt.Errorf("unresolved evidence pointer %q", "test:"+value)
}

func resolveMake(ctx context.Context, root, target string) error {
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(target) {
		return fmt.Errorf("unresolved evidence pointer %q", "make:"+target)
	}
	data, err := readFile(ctx, root, "Makefile")
	if err == nil && regexp.MustCompile(`(?m)^`+regexp.QuoteMeta(target)+`\s*:`).Match(data) {
		return nil
	}
	return fmt.Errorf("unresolved evidence pointer %q", "make:"+target)
}

func resolveWorkflow(ctx context.Context, root, value string) error {
	file, job, ok := strings.Cut(value, "#")
	if !ok || !safeWorkflow(file) || !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(job) {
		return fmt.Errorf("unresolved evidence pointer %q", "workflow:"+value)
	}
	data, err := readFile(ctx, root, filepath.ToSlash(filepath.Join(".github/workflows", file)))
	if err != nil || !hasYAMLMember(data, "jobs", job) {
		return fmt.Errorf("unresolved evidence pointer %q", "workflow:"+value)
	}
	return nil
}

func resolveHook(ctx context.Context, root, value string) error {
	hook, command, ok := strings.Cut(value, ".")
	if !ok || !validHookName(hook) || !validHookName(command) {
		return fmt.Errorf("unresolved evidence pointer %q", "hook:"+value)
	}
	files, err := hookFiles(ctx, root)
	if err != nil {
		return fmt.Errorf("unresolved evidence pointer %q", "hook:"+value)
	}
	for i := 0; i < len(files); i++ {
		data, readErr := readFile(ctx, root, files[i])
		if readErr == nil && hasHookCommand(data, hook, command) {
			return nil
		}
	}
	return fmt.Errorf("unresolved evidence pointer %q", "hook:"+value)
}

func safeRelative(value string) bool {
	return value != "" && !filepath.IsAbs(value) && filepath.IsLocal(filepath.FromSlash(value))
}

func safeWorkflow(file string) bool {
	return safeRelative(file) && (strings.HasSuffix(file, ".yml") || strings.HasSuffix(file, ".yaml"))
}

func validHookName(value string) bool {
	return regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(value)
}

func hookFiles(ctx context.Context, root string) ([]string, error) {
	queue, files := []string{"lefthook.yml"}, []string{}
	seen := map[string]bool{}
	for i := 0; i < len(queue) && i < 16; i++ {
		file := queue[i]
		if seen[file] {
			return nil, fmt.Errorf("lefthook extensions form a cycle")
		}
		data, err := readFile(ctx, root, file)
		if err != nil {
			return nil, err
		}
		seen[file], files = true, append(files, file)
		extends, err := checkedExtensions(data, len(queue))
		if err != nil {
			return nil, err
		}
		queue = append(queue, extends...)
	}
	return files, nil
}

// checkedExtensions are the extends entries of one lefthook file: at most 16 files in all, each a safe relative path
// below .config/lefthook/.
func checkedExtensions(data []byte, queued int) ([]string, error) {
	extends, err := lefthookExtends(data)
	if err != nil {
		return nil, err
	}
	if queued+len(extends) > 16 {
		return nil, fmt.Errorf("lefthook extensions exceed 16 files")
	}
	for j := 0; j < len(extends); j++ {
		if !safeRelative(extends[j]) || !strings.HasPrefix(extends[j], ".config/lefthook/") {
			return nil, fmt.Errorf("unsafe lefthook extension")
		}
	}
	return extends, nil
}

func lefthookExtends(data []byte) ([]string, error) {
	var document struct {
		Extends []string `yaml:"extends"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode lefthook YAML: %w", err)
	}
	return document.Extends, nil
}

func hasYAMLMember(data []byte, parent, child string) bool {
	var document yaml.Node
	if yaml.Unmarshal(data, &document) != nil || len(document.Content) != 1 {
		return false
	}
	parentNode := util.YAMLMappingValue(document.Content[0], parent)
	return util.YAMLMappingValue(parentNode, child) != nil
}

func hasHookCommand(data []byte, hook, command string) bool {
	var document yaml.Node
	if yaml.Unmarshal(data, &document) != nil || len(document.Content) != 1 {
		return false
	}
	hookNode := util.YAMLMappingValue(document.Content[0], hook)
	return util.YAMLMappingValue(util.YAMLMappingValue(hookNode, "commands"), command) != nil ||
		util.YAMLMappingValue(util.YAMLMappingValue(hookNode, "scripts"), command) != nil ||
		util.YAMLMappingValue(util.YAMLMappingValue(hookNode, "jobs"), command) != nil
}
