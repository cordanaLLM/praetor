// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/dedupe"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/util"
)

// declaredLintersGate is the title used on every audit verdict line.
const declaredLintersGate = "Declared linters"

const maxArtifactWalkEntries = 50000

// DeclaredLintersOptions is what AuditDeclaredLinters reads: the repository root,
// the effective policy, the manifest's exceptions list (rule "declared-linters"),
// the day entries' expiry is judged against, and an optional golangci-lint log output.
type DeclaredLintersOptions struct {
	RootDir     string
	Policy      *config.EffectivePolicy
	Exceptions  []config.Exception
	Today       time.Time
	LintLogPath string
}

func prepareDeclaredLintersAudit(ctx context.Context, opts DeclaredLintersOptions) (time.Time, []config.Exception, []config.DeclaredLinter, error) {
	if ctx == nil {
		return time.Time{}, nil, nil, errors.New("[FAIL] " + declaredLintersGate + ": the audit requires a context")
	}
	if opts.Policy == nil {
		return time.Time{}, nil, nil, errors.New("[FAIL] " + declaredLintersGate + ": policy is required")
	}
	today := opts.Today
	if today.IsZero() {
		today = time.Now()
	}
	entries := config.ExceptionsFor(opts.Exceptions, config.ExceptionRuleDeclaredLinters)
	if err := config.ValidateExceptions(entries, today); err != nil {
		return time.Time{}, nil, nil, fmt.Errorf("[FAIL] %s: %w", declaredLintersGate, err)
	}
	declared, err := opts.Policy.DeclaredLinters(ctx)
	if err != nil {
		return time.Time{}, nil, nil, fmt.Errorf("[FAIL] %s: %w", declaredLintersGate, err)
	}
	return today, entries, declared, nil
}

func finalizeDeclaredLintersAudit(judged declaredLintersJudgement, applicableCount, baselineCount int, skipLines []string, exclusionFailures []string) (string, error) {
	var errLines []string
	if len(judged.unexcused) > 0 || len(judged.stale) > 0 {
		errLines = append(errLines, judged.failure())
	}
	if len(exclusionFailures) > 0 {
		errLines = append(errLines, strings.Join(exclusionFailures, "\n"))
	}
	if len(errLines) > 0 {
		return strings.Join(skipLines, "\n"), errors.New(strings.Join(errLines, "\n"))
	}
	passMsg := judged.pass(applicableCount, baselineCount)
	if len(skipLines) > 0 {
		passMsg = strings.Join(skipLines, "\n") + "\n" + passMsg
	}
	return passMsg, nil
}

// AuditDeclaredLinters verifies that every linter declared by the profiles or facets in policy
// is run by at least one CI workflow under .github/workflows in rootDir (#1289). Each missing
// linter is reported as one finding naming the linter and where it was declared, unless excused
// by a live exception entry.
func AuditDeclaredLinters(ctx context.Context, opts DeclaredLintersOptions) (string, error) {
	today, entries, declared, err := prepareDeclaredLintersAudit(ctx, opts)
	if err != nil {
		return "", err
	}
	if len(declared) == 0 {
		return handleEmptyDeclared(entries)
	}

	inputs, err := loadRepoLinterInputs(ctx, opts.RootDir)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: cannot inspect repository inputs: %w", declaredLintersGate, err)
	}

	applicable, skipLines := filterApplicableLinters(declared, inputs)
	if len(applicable) == 0 {
		return handleNoneApplicable(entries, skipLines)
	}

	missing, err := checkMissingWorkflowLinters(ctx, opts.RootDir, applicable)
	if err != nil {
		return strings.Join(skipLines, "\n"), fmt.Errorf("[FAIL] %s: cannot inspect workflows: %w", declaredLintersGate, err)
	}

	if hasDeclaredLinter(declared, "gitleaks") {
		if err := checkGitleaksWorkflowLogOpts(ctx, opts.RootDir); err != nil {
			return strings.Join(skipLines, "\n"), err
		}
	}

	judged := judgeDeclaredLinters(missing, entries, today)

	exclSkip, baselineCount, exclFailures, err := auditDeclaredLinterExclusions(opts, declared, today)
	if err != nil {
		return strings.Join(skipLines, "\n"), err
	}
	skipLines = append(skipLines, exclSkip...)

	return finalizeDeclaredLintersAudit(judged, len(applicable), baselineCount, skipLines, exclFailures)
}

func hasDeclaredLinter(declared []config.DeclaredLinter, name string) bool {
	lower := strings.ToLower(name)
	for i := 0; i < len(declared); i++ {
		if strings.ToLower(declared[i].Name) == lower {
			return true
		}
	}
	return false
}

func checkGitleaksWorkflowLogOpts(ctx context.Context, rootDir string) error {
	fault, err := forge.FindGitleaksHistoryScanWithoutMinusM(ctx, rootDir)
	if err != nil {
		return fmt.Errorf("[FAIL] %s: cannot inspect workflows: %w", declaredLintersGate, err)
	}
	if fault != nil {
		stepLabel := fault.StepName
		if stepLabel != "" {
			stepLabel = fmt.Sprintf("step %q", stepLabel)
		} else {
			stepLabel = fmt.Sprintf("step %d", fault.StepIndex)
		}
		return fmt.Errorf("[FAIL] %s: %s (%s): gitleaks history scan runs without -m in its log options", declaredLintersGate, fault.WorkflowPath, stepLabel)
	}
	return nil
}

func handleEmptyDeclared(entries []config.Exception) (string, error) {
	if len(entries) > 0 {
		return "", errors.New(staleExceptionEntries(declaredLintersGate, config.ExceptionRuleDeclaredLinters, "linter", entries, "policy declares no linters"))
	}
	return "[PASS] " + declaredLintersGate + ": policy declares no linters; no workflow is required.", nil
}

func handleNoneApplicable(entries []config.Exception, skipLines []string) (string, error) {
	if len(entries) > 0 {
		return strings.Join(skipLines, "\n"), errors.New(staleExceptionEntries(declaredLintersGate, config.ExceptionRuleDeclaredLinters, "linter", entries, "policy declares no applicable linters"))
	}
	passMsg := "[PASS] " + declaredLintersGate + ": policy declares no applicable linters; no workflow is required."
	if len(skipLines) > 0 {
		passMsg = strings.Join(skipLines, "\n") + "\n" + passMsg
	}
	return passMsg, nil
}

func filterApplicableLinters(declared []config.DeclaredLinter, inputs repoLinterInputs) ([]config.DeclaredLinter, []string) {
	var applicable []config.DeclaredLinter
	var skipLines []string
	for i := 0; i < len(declared); i++ {
		l := declared[i]
		if req, ok := linterRequirementFor(l.Name); ok && !req.isApplicable(inputs) {
			skipLines = append(skipLines, fmt.Sprintf("[SKIP] %s: not applicable, no %s", l.Name, req.inputDesc))
			continue
		}
		applicable = append(applicable, l)
	}
	return applicable, skipLines
}

func checkMissingWorkflowLinters(ctx context.Context, rootDir string, applicable []config.DeclaredLinter) ([]config.DeclaredLinter, error) {
	names := make([]string, 0, len(applicable))
	for _, l := range applicable {
		names = append(names, l.Name)
	}
	present, err := forge.MeasureWorkflowLinters(ctx, rootDir, names)
	if err != nil {
		return nil, err
	}
	viaHooks, err := hookRunLinters(ctx, rootDir, names)
	if err != nil {
		return nil, err
	}
	for name := range viaHooks {
		present[name] = true
	}
	var missing []config.DeclaredLinter
	for _, l := range applicable {
		if !present[l.Name] {
			missing = append(missing, l)
		}
	}
	return missing, nil
}

type repoLinterInputs struct {
	hasGoSources      bool
	hasCargoToml      bool
	hasCSources       bool
	hasProtoOrBufYaml bool
	hasOpenAPI        bool
	hasPackerHcl      bool
	hasShellScripts   bool
	hasMarkdown       bool
	hasYAML           bool
	hasKubernetes     bool
	hasTerraform      bool
	// hasCheckovTemplate is set by an infrastructure template checkov scans other than
	// Terraform and Kubernetes manifests, which have flags of their own (checkovApplies).
	hasCheckovTemplate bool
}

// checkovInputDesc names the infrastructure templates that make checkov applicable (decision
// office-general-engine-1313-checkov: its IaC role in the gitops-infra profile; a repository
// that runs it for other frameworks is credited by running it).
const checkovInputDesc = "infrastructure templates checkov scans (*.tf, *.tf.json, Kubernetes manifests, Chart.yaml, " +
	"kustomization.yaml, Dockerfile, Containerfile, *.bicep, CloudFormation, serverless.yml)"

// checkovApplies reports whether the tree holds an input of checkovInputDesc.
func checkovApplies(in repoLinterInputs) bool {
	return in.hasTerraform || in.hasKubernetes || in.hasCheckovTemplate
}

// classifyCheckovName marks the templates checkov scans that a file name alone shows.
func classifyCheckovName(lower string, in *repoLinterInputs) {
	switch {
	case lower == "chart.yaml", lower == "kustomization.yaml", lower == "kustomization.yml",
		lower == "serverless.yml", lower == "serverless.yaml", strings.HasSuffix(lower, ".bicep"),
		lower == "dockerfile", lower == "containerfile", strings.HasPrefix(lower, "dockerfile."),
		strings.HasSuffix(lower, ".dockerfile"), strings.HasSuffix(lower, ".containerfile"):
		in.hasCheckovTemplate = true
	}
}

type linterRequirement struct {
	inputDesc    string
	isApplicable func(in repoLinterInputs) bool
}

var simpleLinterRequirements = map[string]linterRequirement{
	"clippy": {
		inputDesc:    "Cargo.toml",
		isApplicable: func(in repoLinterInputs) bool { return in.hasCargoToml },
	},
	"cppcheck": {
		inputDesc:    "C or C++ sources",
		isApplicable: func(in repoLinterInputs) bool { return in.hasCSources },
	},
	"clang-tidy": {
		inputDesc:    "C or C++ sources",
		isApplicable: func(in repoLinterInputs) bool { return in.hasCSources },
	},
	"buf": {
		inputDesc:    "*.proto or buf.yaml",
		isApplicable: func(in repoLinterInputs) bool { return in.hasProtoOrBufYaml },
	},
	"spectral": {
		inputDesc:    "OpenAPI or AsyncAPI document",
		isApplicable: func(in repoLinterInputs) bool { return in.hasOpenAPI },
	},
	"oapi-codegen": {
		inputDesc:    "OpenAPI or AsyncAPI document",
		isApplicable: func(in repoLinterInputs) bool { return in.hasOpenAPI },
	},
	"packer": {
		inputDesc:    "*.pkr.hcl",
		isApplicable: func(in repoLinterInputs) bool { return in.hasPackerHcl },
	},
	"shellcheck": {
		inputDesc:    "shell scripts",
		isApplicable: func(in repoLinterInputs) bool { return in.hasShellScripts },
	},
	"shfmt": {
		inputDesc:    "shell scripts",
		isApplicable: func(in repoLinterInputs) bool { return in.hasShellScripts },
	},
	"markdownlint": {
		inputDesc:    "*.md",
		isApplicable: func(in repoLinterInputs) bool { return in.hasMarkdown },
	},
	// #1313: the infrastructure linters a gitops profile declares apply only where their inputs
	// are.
	"kubeconform": {
		inputDesc:    "Kubernetes manifests (YAML documents with apiVersion and kind)",
		isApplicable: func(in repoLinterInputs) bool { return in.hasKubernetes },
	},
	"tflint": {
		inputDesc:    "*.tf",
		isApplicable: func(in repoLinterInputs) bool { return in.hasTerraform },
	},
	"yamllint": {
		inputDesc:    "*.yml or *.yaml",
		isApplicable: func(in repoLinterInputs) bool { return in.hasYAML },
	},
	"checkov": {
		inputDesc:    checkovInputDesc,
		isApplicable: checkovApplies,
	},
}

// linterRequirementFor maps declared linter names to their input requirements.
// Linters not present in this table are always required (fail closed).
func linterRequirementFor(name string) (linterRequirement, bool) {
	lower := strings.ToLower(name)
	if isGoLinter(lower) {
		return linterRequirement{
			inputDesc:    "Go sources",
			isApplicable: func(in repoLinterInputs) bool { return in.hasGoSources },
		}, true
	}
	req, ok := simpleLinterRequirements[lower]
	return req, ok
}

func isGoLinter(lower string) bool {
	switch lower {
	case "golangci-lint", "govet", "go vet", "staticcheck", "errcheck", "gosec", "gocyclo", "gocognit":
		return true
	}
	for i := 0; i < len(forge.AllGolangciLinters); i++ {
		if lower == forge.AllGolangciLinters[i] {
			return true
		}
	}
	return false
}

func loadRepoLinterInputs(ctx context.Context, rootDir string) (repoLinterInputs, error) {
	if ctx == nil {
		return repoLinterInputs{}, errors.New("context is required")
	}
	var in repoLinterInputs
	initInputsFromPlanAndDedupe(ctx, rootDir, &in)
	if err := scanLinterArtifacts(ctx, rootDir, &in); err != nil {
		return repoLinterInputs{}, err
	}
	return in, nil
}

// initInputsFromPlan populates language flags observed in the verification plan.
func initInputsFromPlan(ctx context.Context, rootDir string, in *repoLinterInputs) {
	if plan, err := ObserveVerificationPlanWithLimits(ctx, rootDir, nil); err == nil && plan != nil {
		langs := planLanguages(plan)
		in.hasGoSources = in.hasGoSources || (langs&hisscatalog.LanguageGo != 0)
		in.hasCargoToml = in.hasCargoToml || (langs&hisscatalog.LanguageRust != 0)
		in.hasCSources = in.hasCSources || (langs&hisscatalog.LanguageC != 0)
	}
}

// initInputsFromDedupe populates language flags detected by dedupe source counts.
func initInputsFromDedupe(ctx context.Context, rootDir string, in *repoLinterInputs) {
	if counts, err := dedupe.SourceLanguageCounts(ctx, rootDir); err == nil && counts != nil {
		in.hasGoSources = in.hasGoSources || (counts["go"] > 0)
		in.hasCSources = in.hasCSources || (counts["c"]+counts["cpp"] > 0)
		in.hasShellScripts = in.hasShellScripts || (counts["shell"]+counts["zsh"]+counts["ksh"] > 0)
	}
}

func initInputsFromPlanAndDedupe(ctx context.Context, rootDir string, in *repoLinterInputs) {
	initInputsFromPlan(ctx, rootDir, in)
	initInputsFromDedupe(ctx, rootDir, in)
}

func scanLinterArtifacts(ctx context.Context, rootDir string, in *repoLinterInputs) error {
	visible := hiss.GitVisiblePaths(ctx, rootDir)
	entries := 0
	return filepath.WalkDir(rootDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		entries++
		if entries > maxArtifactWalkEntries {
			return filepath.SkipAll
		}
		return visitLinterArtifact(ctx, rootDir, p, d, visible, in)
	})
}

func visitLinterArtifact(ctx context.Context, rootDir, fullPath string, d fs.DirEntry, visible *hiss.GitVisibleTree, in *repoLinterInputs) error {
	name := d.Name()
	if d.IsDir() {
		if name == ".git" || shouldSkipLinterDir(name) {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(rootDir, fullPath)
		if err == nil && visible != nil && !visible.HasDir(filepath.ToSlash(rel)) {
			return filepath.SkipDir
		}
		return nil
	}
	rel, err := filepath.Rel(rootDir, fullPath)
	if err != nil {
		return err
	}
	relSlash := filepath.ToSlash(rel)
	if visible != nil && !visible.HasFile(relSlash) {
		return nil
	}
	classifyLinterArtifactFile(ctx, fullPath, name, in)
	return nil
}

func shouldSkipLinterDir(name string) bool {
	if util.IsScratchDir(name) || util.IsToolchainTreeDir(name) {
		return true
	}
	switch name {
	case "vendor", "node_modules", "bin", "obj", "build", "dist", "target", ".venv", "__pycache__":
		return true
	default:
		return false
	}
}

func classifyKnownFilename(lower string, in *repoLinterInputs) bool {
	if lower == "cargo.toml" {
		in.hasCargoToml = true
		return true
	}
	if lower == "buf.yaml" || lower == "buf.yml" || strings.HasSuffix(lower, ".proto") {
		in.hasProtoOrBufYaml = true
		return true
	}
	return false
}

func classifySourceExtension(lower string, in *repoLinterInputs) {
	switch {
	case strings.HasSuffix(lower, ".pkr.hcl"):
		in.hasPackerHcl = true
	case strings.HasSuffix(lower, ".md"):
		in.hasMarkdown = true
	case strings.HasSuffix(lower, ".go"):
		in.hasGoSources = true
	case isCSuffix(lower):
		in.hasCSources = true
	case strings.HasSuffix(lower, ".sh") || strings.HasSuffix(lower, ".bash"):
		in.hasShellScripts = true
	case strings.HasSuffix(lower, ".tf") || strings.HasSuffix(lower, ".tf.json"):
		in.hasTerraform = true
	case isYAMLName(lower):
		in.hasYAML = true
	}
}

func classifyLinterArtifactFile(ctx context.Context, fullPath, name string, in *repoLinterInputs) {
	lower := strings.ToLower(name)
	if !classifyKnownFilename(lower, in) {
		classifySourceExtension(lower, in)
	}
	classifyCheckovName(lower, in)
	classifyDocumentContent(ctx, fullPath, lower, in)
}

// classifyDocumentContent reads a YAML or JSON file once, and only while a content rule still
// needs it: whether it is an OpenAPI or AsyncAPI document, and, for YAML, whether one of its
// documents is a Kubernetes manifest.
func classifyDocumentContent(ctx context.Context, fullPath, lower string, in *repoLinterInputs) {
	if strings.HasPrefix(lower, ".spectral.") {
		in.hasOpenAPI = true
		return
	}
	need := contentRulesFor(lower, *in)
	if need == (contentRules{}) {
		return
	}
	data, err := contextopt.ReadSnapshot(ctx, fullPath)
	if err != nil || len(data) == 0 {
		return
	}
	need.apply(data, in)
}

// contentRules says which content rules a file still has to answer.
type contentRules struct {
	openAPI, kubernetes, cloudFormation bool
}

// apply answers the rules need holds from a file's content.
func (need contentRules) apply(data []byte, in *repoLinterInputs) {
	if need.openAPI && docHasOpenAPIKey(data) {
		in.hasOpenAPI = true
	}
	if need.kubernetes && docIsKubernetesManifest(data) {
		in.hasKubernetes = true
	}
	// A CloudFormation template declares its format version; checkov reads it from YAML or JSON.
	if need.cloudFormation && bytes.Contains(data, []byte("AWSTemplateFormatVersion")) {
		in.hasCheckovTemplate = true
	}
}

// contentRulesFor returns the content rules a YAML or JSON file named lower answers that the
// tree read so far, in, has not answered yet.
func contentRulesFor(lower string, in repoLinterInputs) contentRules {
	document := isCandidateOpenAPIExtension(filepath.Ext(lower))
	return contentRules{
		openAPI:        document && !in.hasOpenAPI,
		kubernetes:     isYAMLName(lower) && !in.hasKubernetes,
		cloudFormation: document && !in.hasCheckovTemplate,
	}
}

// isYAMLName reports whether a lower-case file name is a YAML file.
func isYAMLName(lower string) bool {
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")
}

// maxManifestDocuments bounds the documents read from one YAML stream (HISS-02).
const maxManifestDocuments = 64

// docIsKubernetesManifest reports whether one document of a YAML stream is a Kubernetes object:
// a mapping whose apiVersion and kind are non-empty strings, the two fields kubeconform resolves
// a schema from. A stream that stops parsing is judged on the documents read so far.
func docIsKubernetesManifest(data []byte) bool {
	if !bytes.Contains(data, []byte("apiVersion")) || !bytes.Contains(data, []byte("kind")) {
		return false
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	for i := 0; i < maxManifestDocuments; i++ {
		var doc map[string]any
		if err := decoder.Decode(&doc); err != nil {
			return false
		}
		if nonEmptyString(doc["apiVersion"]) && nonEmptyString(doc["kind"]) {
			return true
		}
	}
	return false
}

func isCSuffix(lower string) bool {
	ext := filepath.Ext(lower)
	switch ext {
	case ".c", ".cc", ".cpp", ".cxx", ".h", ".hpp", ".hh", ".hxx":
		return true
	default:
		return false
	}
}

func isCandidateOpenAPIExtension(ext string) bool {
	return ext == ".yaml" || ext == ".yml" || ext == ".json"
}

func docHasOpenAPIKey(data []byte) bool {
	if !bytes.Contains(data, []byte("openapi")) && !bytes.Contains(data, []byte("asyncapi")) {
		return false
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil || doc == nil {
		return false
	}
	_, hasOpen := doc["openapi"]
	_, hasAsync := doc["asyncapi"]
	return hasOpen || hasAsync
}

type declaredLintersJudgement struct {
	excused   []config.DeclaredLinter
	unexcused []config.DeclaredLinter
	live      map[string]config.Exception
	expired   map[string]config.Exception
	stale     []config.Exception
}

func judgeDeclaredLinters(missing []config.DeclaredLinter, entries []config.Exception, today time.Time) declaredLintersJudgement {
	judged := declaredLintersJudgement{
		live:    make(map[string]config.Exception),
		expired: make(map[string]config.Exception),
	}
	missingMap := make(map[string]bool, len(missing))
	for _, m := range missing {
		missingMap[m.Name] = true
	}
	for i := 0; i < len(entries) && i < config.MaxExceptions; i++ {
		entry := entries[i]
		switch {
		case !missingMap[entry.Tool]:
			judged.stale = append(judged.stale, entry)
		case entry.Expired(today):
			judged.expired[entry.Tool] = entry
		default:
			judged.live[entry.Tool] = entry
		}
	}
	for _, m := range missing {
		if _, ok := judged.live[m.Name]; ok {
			judged.excused = append(judged.excused, m)
		} else {
			judged.unexcused = append(judged.unexcused, m)
		}
	}
	return judged
}

func (j declaredLintersJudgement) failure() string {
	lines := make([]string, 0, len(j.unexcused)*2+2)
	if len(j.unexcused) > 0 {
		lines = append(lines, fmt.Sprintf("[FAIL] %s: %d declared linter(s) not run by any workflow:", declaredLintersGate, len(j.unexcused)))
		for _, u := range j.unexcused {
			lines = append(lines, fmt.Sprintf("  - %s: declared by %s, not run by any workflow", u.Name, u.Source))
		}
		for _, u := range j.unexcused {
			if exp, ok := j.expired[u.Name]; ok {
				lines = append(lines, expiredExceptionEntry(declaredLintersGate, "the linter "+u.Name+" is missing", "run it in a CI workflow", exp))
			}
		}
	}
	if len(j.stale) > 0 {
		lines = append(lines, staleExceptionEntries(declaredLintersGate, config.ExceptionRuleDeclaredLinters, "linter", j.stale, "the linters they name are run or not declared"))
	}
	return strings.Join(lines, "\n")
}

func (j declaredLintersJudgement) pass(totalDeclared, baselineCount int) string {
	if len(j.excused) == 0 {
		if baselineCount > 0 {
			return fmt.Sprintf("[PASS] %s: every declared linter is run by a CI workflow (%d verified, %d baseline).",
				declaredLintersGate, totalDeclared, baselineCount)
		}
		return fmt.Sprintf("[PASS] %s: every declared linter is run by a CI workflow (%d verified).", declaredLintersGate, totalDeclared)
	}
	var b strings.Builder
	if baselineCount > 0 {
		fmt.Fprintf(&b, "[PASS] %s: %d declared linters run; %d excepted (%d baseline):",
			declaredLintersGate, totalDeclared-len(j.excused), len(j.excused), baselineCount)
	} else {
		fmt.Fprintf(&b, "[PASS] %s: %d declared linters run; %d excepted:",
			declaredLintersGate, totalDeclared-len(j.excused), len(j.excused))
	}
	for _, e := range j.excused {
		live := j.live[e.Name]
		fmt.Fprintf(&b, "\n  - %s, excepted until %s by the exceptions entry (rule %s, %s): %s",
			e.Name, live.Expires, live.Rule, live.Target(), live.Reason)
	}
	return b.String()
}

// nonEmptyString reports whether value is a string holding more than white space.
func nonEmptyString(value any) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) != ""
}
