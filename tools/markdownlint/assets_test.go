package markdownlint

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/forge"
)

func TestLockedAssetInventory(t *testing.T) {
	want := []string{"package.json", "package-lock.json", "markdownlint-cli2.yaml", "verify.mjs", "no-private-scratch-links.mjs"}
	got := Names()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("asset names = %v, want %v", got, want)
	}
	if _, err := Read("outside"); err == nil {
		t.Fatal("unknown asset accepted")
	}
}

func TestEmbeddedAssetCheckoutBytesPinnedToLF(t *testing.T) {
	attributes, err := os.ReadFile("../../.gitattributes")
	if err != nil {
		t.Fatal(err)
	}
	for _, policy := range []string{
		".gitattributes text eol=lf",
		"tools/markdownlint/* text eol=lf",
	} {
		if !containsExactLFLine(string(attributes), policy) {
			t.Fatalf("embedded Markdown gate checkout policy lacks exact LF line %q", policy)
		}
	}
	for _, name := range Names() {
		data, err := Read(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "\r") {
			t.Fatalf("embedded Markdown gate asset %s contains a carriage return", name)
		}
	}
}

func TestContainsExactLFLine(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "positive", text: "* text=auto\n.gitattributes text eol=lf\n", want: true},
		{name: "negative comment", text: "# .gitattributes text eol=lf\n", want: false},
		{name: "boundary suffix", text: ".gitattributes text eol=lf-extra\n", want: false},
		{name: "boundary CRLF", text: ".gitattributes text eol=lf\r\n", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := containsExactLFLine(test.text, ".gitattributes text eol=lf"); got != test.want {
				t.Fatalf("containsExactLFLine() = %t, want %t", got, test.want)
			}
		})
	}
}

func containsExactLFLine(text, want string) bool {
	if strings.Contains(text, "\r") {
		return false
	}
	for _, line := range strings.Split(text, "\n") {
		if line == want {
			return true
		}
	}
	return false
}

func TestPackageLockPinsEveryInstalledPackage(t *testing.T) {
	wantDirect := map[string]string{
		"js-yaml":                   "5.4.1",
		"markdownlint-cli2":         "0.23.3",
		"micromark":                 "4.0.2",
		"micromark-extension-mdxjs": "3.0.0",
		"micromatch":                "4.0.8",
		"parse5":                    "8.0.1",
	}
	manifestData, err := Read("package.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("decode package manifest: %v", err)
	}
	assertDirectDependencies(t, manifest.Dependencies, wantDirect)

	data, err := Read("package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		LockfileVersion int `json:"lockfileVersion"`
		Packages        map[string]struct {
			Version      string            `json:"version"`
			Resolved     string            `json:"resolved"`
			Integrity    string            `json:"integrity"`
			Dependencies map[string]string `json:"dependencies"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatalf("decode package lock: %v", err)
	}
	if lock.LockfileVersion != 3 {
		t.Fatalf("lockfileVersion = %d, want 3", lock.LockfileVersion)
	}
	assertDirectDependencies(t, lock.Packages[""].Dependencies, wantDirect)
	for name, pkg := range lock.Packages {
		if name == "" {
			continue
		}
		if pkg.Version == "" || pkg.Resolved == "" || pkg.Integrity == "" {
			t.Fatalf("%s lacks exact version, source, or integrity", name)
		}
	}
	if lock.Packages["node_modules/markdownlint-cli2"].Version != "0.23.3" {
		t.Fatal("markdownlint-cli2 is not pinned to 0.23.3")
	}
	if lock.Packages["node_modules/micromark"].Version != "4.0.2" {
		t.Fatal("micromark is not pinned to 4.0.2")
	}
	if lock.Packages["node_modules/micromark-extension-mdx-jsx"].Version != "3.0.2" {
		t.Fatal("micromark-extension-mdx-jsx is not pinned to 3.0.2")
	}
	if lock.Packages["node_modules/micromark-extension-mdxjs"].Version != "3.0.0" {
		t.Fatal("micromark-extension-mdxjs is not pinned to 3.0.0")
	}
	if lock.Packages["node_modules/parse5"].Version != "8.0.1" {
		t.Fatal("parse5 is not pinned to 8.0.1")
	}
	// verify.mjs requires js-yaml (the .standards.yaml documentation block) and micromatch
	// (declared style exclusions) from the locked install, so both are direct dependencies.
	for name, version := range map[string]string{"js-yaml": "5.4.1", "micromatch": "4.0.8"} {
		if lock.Packages["node_modules/"+name].Version != version {
			t.Fatalf("%s is not installed at the top level pinned to %s", name, version)
		}
	}
}

func assertDirectDependencies(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("direct dependencies = %v, want %v", got, want)
	}
	for name, version := range want {
		if got[name] != version {
			t.Fatalf("direct dependency %s = %q, want %q", name, got[name], version)
		}
	}
}

func TestRunnerUsesLockedInstallWithoutNpx(t *testing.T) {
	data, err := Read("verify.mjs")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		`"ci", "--ignore-scripts", "--no-audit", "--no-fund"`, "spawnSync", "timeout",
		"MAX_CAPTURE_BYTES", "MAX_DIAGNOSTIC_OUTPUT_BYTES", "MAX_DIAGNOSTIC_OUTPUT_LINES", "emitBounded",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("runner lacks %q", required)
		}
	}
	if strings.Contains(text, "npx") {
		t.Fatal("runner must not resolve tools through npx")
	}
	for _, required := range []string{
		"const scratchFiles = inventory(root, settings)",
		"const selection = styleSelection(scratchFiles, settings, ",
		"const styleFiles = selection.styled",
		"runScratchRule(root, temporary, scratchFiles, false)",
		"runMarkdownlint(root, temporary, styleFiles)",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("runner does not keep privacy scanning broader than style lint: missing %q", required)
		}
	}
}

// markdownlint-cli2 reads .markdownlint* and .markdownlint-cli2.* files from its working directory
// and every directory down to a linted file, and a .markdownlint.* file there replaces the locked
// rules (#533). The runner must lint a staged copy of the selected files from an empty directory;
// the self-test (make docs-lint-test) proves loosening and tightening files have no effect.
func TestRunnerLintsAStagedCopy(t *testing.T) {
	data, err := Read("verify.mjs")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		"const tree = stageStyleTree(root, temporary, files);",
		"fs.copyFileSync(path.join(root, files[index]), target);",
		"[cli, \"--config\", config, \"--no-globs\", ...batch], {\n      cwd: tree,",
		"hermeticConfigSelfTest(temporary);",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("runner does not lint a hermetic staged copy: missing %q", required)
		}
	}
	if strings.Contains(text, "...batch], {\n      cwd: root,") {
		t.Fatal("runner lints from the repository root, where markdownlint-cli2 discovers repository configuration")
	}
}

// The gate validates the .standards.yaml documentation block itself because the hosted workflow
// runs it without praetorctl, and audit validates it through internal/config. Both must apply
// the same ranges: every bound verify.mjs and the private-link rule declare equals its Go twin.
func TestDocumentationSettingsMirrorConfig(t *testing.T) {
	want := map[string]map[string]int{
		"verify.mjs": {
			"DEFAULT_MAX_FILES":         config.DefaultDocumentationMaxFiles,
			"MAX_FILES_CEILING":         config.DocumentationMaxFilesCeiling,
			"DEFAULT_MAX_FILE_BYTES":    config.DefaultDocumentationMaxFileBytes,
			"MAX_FILE_BYTES_CEILING":    config.DocumentationMaxFileBytesCeiling,
			"MAX_STYLE_EXCLUSIONS":      config.MaxDocumentationStyleExclusions,
			"MAX_STYLE_EXCLUSION_BYTES": config.MaxDocumentationStyleExclusionBytes,
			"MAX_MANIFEST_BYTES":        contextopt.MaxSourceBytes,
		},
		"no-private-scratch-links.mjs": {
			"MAX_FILES":      config.DocumentationMaxFilesCeiling,
			"MAX_FILE_BYTES": config.DocumentationMaxFileBytesCeiling,
		},
	}
	for name, constants := range want {
		declared := scriptConstants(t, name)
		for constant, value := range constants {
			got, found := declared[constant]
			if !found || got != value {
				t.Errorf("%s declares %s = %d (found=%v), want %d", name, constant, got, found, value)
			}
		}
	}
	if _, found := scriptConstants(t, "verify.mjs")["MAX_FILES"]; found {
		t.Error("verify.mjs still declares a fixed MAX_FILES beside the declared bounds")
	}
}

func scriptConstants(t *testing.T, name string) map[string]int {
	t.Helper()
	data, err := Read(name)
	if err != nil {
		t.Fatal(err)
	}
	constants := map[string]int{}
	for _, match := range regexp.MustCompile(`(?m)^const ([A-Z_]+) = ([0-9_]+);$`).FindAllStringSubmatch(string(data), -1) {
		value, err := strconv.Atoi(strings.ReplaceAll(match[2], "_", ""))
		if err != nil {
			t.Fatalf("%s constant %s: %v", name, match[1], err)
		}
		constants[match[1]] = value
	}
	return constants
}

// Windows refuses to spawn a .cmd or .bat file without a shell (CVE-2024-27980), and a shell
// with an argument list is deprecated (DEP0190). The runner must reach npm through the Node
// binary on Windows, and its self-test must replay that resolution on every platform.
func TestRunnerStartsNpmWithoutWindowsBatchShim(t *testing.T) {
	data, err := Read("verify.mjs")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		`path.join(path.dirname(execPath), "node_modules", "npm", "bin", "npm-cli.js")`,
		"return { file: execPath, args: [cli, ...args] };",
		"npmInvocation(process.platform, process.execPath, NPM_CI_ARGS)",
		"npmInvocationSelfTest(temporary);",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("runner lacks Windows-safe npm resolution %q", required)
		}
	}
	for _, forbidden := range []string{`"npm.cmd"`, `'npm.cmd'`, "shell: true", `"cmd.exe"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("runner spawns npm through %s, which fails with EINVAL or needs a shell on Windows", forbidden)
		}
	}
}

func TestPrivateLinkDiagnosticsAreGloballyBounded(t *testing.T) {
	data, err := Read("no-private-scratch-links.mjs")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{
		"const MAX_FINDINGS = 64", "MAX_DIAGNOSTIC_FIELD_CHARS", "PRAETOR-MD002", "capFindings",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("private-link rule lacks bounded diagnostic contract %q", required)
		}
	}
}

// Positive: FS serves exactly the inventory Read returns, SourceFile is the file carrying this
// package's embed directive, and Workflow reports StatusContext as its job name.
func TestFamilySurfacePositive(t *testing.T) {
	for _, name := range Names() {
		want, err := Read(name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := fs.ReadFile(FS(), name)
		if err != nil || string(got) != string(want) {
			t.Fatalf("FS %s differs from Read: %v", name, err)
		}
	}
	source, err := os.ReadFile(filepath.Base(SourceFile))
	if err != nil || !strings.Contains(string(source), "\n//go:embed "+strings.Join(Names(), " ")+"\n") {
		t.Fatalf("%s does not carry the inventory directive: %v", SourceFile, err)
	}
	if !strings.Contains(Workflow, "\n    name: "+StatusContext+"\n") || !strings.Contains(Workflow, "node "+Directory+"/verify.mjs") {
		t.Fatal("Workflow does not run the gate under its status context")
	}
}

// Negative: FS holds no file beyond the inventory, and Read refuses a path escaping it.
func TestFamilySurfaceNegative(t *testing.T) {
	entries, err := fs.ReadDir(FS(), ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !slices.Contains(Names(), entry.Name()) {
			t.Fatalf("embedded tree carries %s outside the inventory", entry.Name())
		}
	}
	if _, err := Read("../assets.go"); err == nil {
		t.Fatal("Read accepted a path outside the inventory")
	}
}

// Boundary: the inventory fills MaxAssets exactly and Names returns a private copy.
func TestFamilySurfaceBoundary(t *testing.T) {
	names := Names()
	if len(names) != MaxAssets {
		t.Fatalf("inventory holds %d assets, MaxAssets is %d", len(names), MaxAssets)
	}
	names[0] = "mutated"
	if Names()[0] != "package.json" {
		t.Fatal("Names exposed the inventory for mutation")
	}
}

// Step names and the figure script the documentation workflow binds (docs/adr/0016-figures-for-
// adopters.md, operator decision 4 and section 1): the figure step runs check, then sources,
// right after the Markdown step, in the one job whose name is StatusContext.
const (
	markdownStepName = "Verify public Markdown"
	figureStepName   = "Verify figures"
	figureScript     = "node tools/figures/build.mjs check\nnode tools/figures/build.mjs sources"
)

// figureStepFault reads a documentation workflow through forge.WorkflowRuns and names the first
// way its figure step breaks the binding above, or returns "" when it holds.
func figureStepFault(doc string) string {
	runs, err := forge.WorkflowRuns([]byte(doc))
	if err != nil {
		return err.Error()
	}
	markdown, figures := slices.IndexFunc(runs, stepNamed(markdownStepName)), slices.IndexFunc(runs, stepNamed(figureStepName))
	switch {
	case markdown < 0 || figures < 0:
		return "the workflow lacks the Markdown or the figure step"
	case runs[markdown].JobName != StatusContext:
		return "the Markdown step's job is not named " + StatusContext
	case runs[figures].Job != runs[markdown].Job:
		return "the figure step runs in job " + runs[figures].Job + ", not in " + runs[markdown].Job
	case runs[figures].Index != runs[markdown].Index+1:
		return "the figure step is not the step right after the Markdown step"
	case runs[figures].Script != figureScript:
		return "the figure step runs " + strconv.Quote(runs[figures].Script)
	case slices.ContainsFunc(runs, func(run forge.WorkflowRun) bool { return run.Job != runs[markdown].Job }):
		return "the workflow runs commands outside the documentation job"
	}
	return ""
}

func stepNamed(name string) func(forge.WorkflowRun) bool {
	return func(run forge.WorkflowRun) bool { return run.Name == name }
}

// mutateWorkflow replaces old with replacement in Workflow once, failing when old is absent so
// a fixture never silently tests the unchanged text.
func mutateWorkflow(t *testing.T, old, replacement string) string {
	t.Helper()
	if !strings.Contains(Workflow, old) {
		t.Fatalf("Workflow lacks %q", old)
	}
	return strings.Replace(Workflow, old, replacement, 1)
}

const (
	markdownStep = "      - name: Verify public Markdown\n        run: node tools/markdownlint/verify.mjs\n"
	figureStep   = "      - name: Verify figures\n        run: |\n          node tools/figures/build.mjs check\n" +
		"          node tools/figures/build.mjs sources\n"
)

// Positive: the shipped Workflow holds the binding, and forge derives exactly one required status
// context from it, StatusContext, so branch-protection reconciliation is unchanged.
func TestWorkflowFigureStepPositive(t *testing.T) {
	if fault := figureStepFault(Workflow); fault != "" {
		t.Fatal(fault)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	contexts, err := forge.RequiredStatusContextsPlanned(ctx, t.TempDir(), map[string][]byte{WorkflowFile: []byte(Workflow)})
	if err != nil || !slices.Equal(contexts, []string{StatusContext}) {
		t.Fatalf("required status contexts = %v, %v; want [%s]", contexts, err, StatusContext)
	}
}

// Negative: a reordered, trimmed, weakened, relocated, renamed or missing figure step is refused,
// and so is a second job running commands beside the documentation job.
func TestWorkflowFigureStepNegative(t *testing.T) {
	const otherJob = "  figures:\n    name: Figures\n    runs-on: ubuntu-26.04\n    steps:\n"
	cases := map[string]struct{ doc, fault string }{
		"reordered":     {mutateWorkflow(t, markdownStep+figureStep, figureStep+markdownStep), "not the step right after"},
		"sources gone":  {mutateWorkflow(t, "          node tools/figures/build.mjs sources\n", ""), "runs \"node tools/figures/build.mjs check\""},
		"check gone":    {mutateWorkflow(t, "          node tools/figures/build.mjs check\n", ""), "runs \"node tools/figures/build.mjs sources\""},
		"sources muted": {mutateWorkflow(t, "build.mjs sources\n", "build.mjs sources || true\n"), "sources || true"},
		"other job":     {mutateWorkflow(t, figureStep, "") + otherJob + figureStep, "runs in job figures"},
		"second job":    {Workflow + otherJob + "      - run: make docs\n", "outside the documentation job"},
		"job renamed":   {mutateWorkflow(t, "    name: "+StatusContext+"\n", "    name: Docs\n"), "not named " + StatusContext},
		"step missing":  {mutateWorkflow(t, figureStep, ""), "lacks the Markdown or the figure step"},
		"not YAML":      {"jobs: [\n", "parse workflow"},
	}
	for name, test := range cases {
		t.Run(name, func(t *testing.T) {
			if fault := figureStepFault(test.doc); !strings.Contains(fault, test.fault) {
				t.Fatalf("figure step fault = %q, want one naming %q", fault, test.fault)
			}
		})
	}
}

// Boundary: an action between the two steps breaks "right after" although no run step sits
// between them, and the two figure commands swapped are not the bound script; the figure step as
// the job's last step, and a trailing blank line after it, still hold.
func TestWorkflowFigureStepBoundary(t *testing.T) {
	between := mutateWorkflow(t, markdownStep, markdownStep+"      - uses: actions/cache@v5\n")
	swapped := mutateWorkflow(t, figureStep, "      - name: Verify figures\n        run: |\n"+
		"          node tools/figures/build.mjs sources\n          node tools/figures/build.mjs check\n")
	for doc, want := range map[string]string{between: "not the step right after", swapped: "build.mjs sources\\nnode"} {
		if fault := figureStepFault(doc); !strings.Contains(fault, want) {
			t.Fatalf("figure step fault = %q, want one naming %q", fault, want)
		}
	}
	if !strings.HasSuffix(Workflow, figureStep) {
		t.Fatal("the figure step is not the job's last step")
	}
	if fault := figureStepFault(Workflow + "\n"); fault != "" {
		t.Fatalf("a trailing blank line breaks the figure step: %s", fault)
	}
}

// renovateConfig is the part of renovate.json that keeps the workflow's SHA pins current.
type renovateConfig struct {
	GitHubActions struct {
		ManagerFilePatterns []string `json:"managerFilePatterns"`
	} `json:"github-actions"`
	PackageRules []struct {
		MatchManagers  []string `json:"matchManagers"`
		MatchFileNames []string `json:"matchFileNames"`
		PinDigests     bool     `json:"pinDigests"`
		GroupName      string   `json:"groupName"`
	} `json:"packageRules"`
}

func readRenovateConfig(t *testing.T) renovateConfig {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "renovate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config renovateConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

// githubActionsReads reports whether one of the github-actions manager's added file patterns,
// a /re2/ literal as Renovate spells it, matches rel.
func githubActionsReads(t *testing.T, config renovateConfig, rel string) bool {
	t.Helper()
	for _, pattern := range config.GitHubActions.ManagerFilePatterns {
		expr, ok := strings.CutPrefix(pattern, "/")
		expr, closed := strings.CutSuffix(expr, "/")
		if !ok || !closed {
			t.Fatalf("github-actions file pattern %q is not a /regex/ literal", pattern)
		}
		if regexp.MustCompile(expr).MatchString(rel) {
			return true
		}
	}
	return false
}

// Positive: Renovate's github-actions manager reads the template source as well as the
// repository's own copy, pins digests, and groups the two files into one branch, so an update
// never leaves them apart for the audit byte lock to reject. Every uses: line sits on a line of
// its own in the source, where the manager's line-based extractor finds it.
func TestRenovateUpdatesTemplatePinsWithWorkflowCopy(t *testing.T) {
	config := readRenovateConfig(t)
	if !githubActionsReads(t, config, SourceFile) {
		t.Fatalf("renovate.json github-actions.managerFilePatterns does not cover %s", SourceFile)
	}
	pinned, grouped := false, false
	for _, rule := range config.PackageRules {
		if !slices.Contains(rule.MatchManagers, "github-actions") {
			continue
		}
		pinned = pinned || (rule.PinDigests && len(rule.MatchFileNames) == 0)
		grouped = grouped || (rule.GroupName != "" && slices.Contains(rule.MatchFileNames, WorkflowFile) &&
			slices.Contains(rule.MatchFileNames, SourceFile))
	}
	if !pinned || !grouped {
		t.Fatalf("renovate.json github-actions rules: pinDigests=%v, one group over %s and %s=%v", pinned, WorkflowFile, SourceFile, grouped)
	}
	source, err := os.ReadFile(filepath.Base(SourceFile))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(source), "\n")
	for _, line := range strings.Split(Workflow, "\n") {
		if strings.Contains(line, "uses:") && !slices.Contains(lines, line) {
			t.Fatalf("uses line %q is not a line of its own in %s", line, SourceFile)
		}
	}
}

// Negative and boundary: the added pattern names the template source alone, not its test or
// the neighbouring assets, which carry no workflow.
func TestRenovateTemplatePatternIsExact(t *testing.T) {
	config := readRenovateConfig(t)
	for _, rel := range []string{"tools/markdownlint/assets_test.go", Directory + "/verify.mjs", "x" + SourceFile, SourceFile + ".bak"} {
		if githubActionsReads(t, config, rel) {
			t.Fatalf("renovate.json github-actions.managerFilePatterns also matches %s", rel)
		}
	}
}

// priorTextDir holds every earlier text a digest in priorDigests names. A file is named after
// the managed file it once was: praetor-docs.* for WorkflowFile, <asset>.* for an asset.
var priorTextDir = filepath.Join("testdata", "prior")

// priorTextPath maps a testdata/prior file name to the managed path it was shipped at.
func priorTextPath(name string) string {
	if strings.HasPrefix(name, "praetor-docs.") {
		return WorkflowFile
	}
	asset, _, _ := strings.Cut(name, ".")
	for _, candidate := range Names() {
		if strings.HasPrefix(candidate, asset+".") {
			return Directory + "/" + candidate
		}
	}
	return ""
}

func lfDigest(data []byte) string {
	sum := sha256.Sum256([]byte(strings.ReplaceAll(string(data), "\r\n", "\n")))
	return hex.EncodeToString(sum[:])
}

// Positive: every file under testdata/prior reproduces exactly one digest of PriorDigests,
// mapped to the managed path the file was shipped at, and every digest is reproduced.
func TestPriorDigestsReproduce(t *testing.T) {
	entries, err := os.ReadDir(priorTextDir)
	if err != nil {
		t.Fatal(err)
	}
	digests := PriorDigests()
	if len(entries) != len(digests) {
		t.Fatalf("testdata/prior holds %d texts for %d digests", len(entries), len(digests))
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(priorTextDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		rel, known := digests[lfDigest(data)]
		if want := priorTextPath(entry.Name()); !known || want == "" || rel != want {
			t.Fatalf("%s: digest maps to %q (known=%v), want %q", entry.Name(), rel, known, want)
		}
	}
}

// Negative: the current texts are not prior texts, and PriorDigests hands out a copy.
func TestPriorDigestsExcludeCurrentTexts(t *testing.T) {
	digests := PriorDigests()
	current := map[string][]byte{WorkflowFile: []byte(Workflow)}
	for _, name := range Names() {
		data, err := Read(name)
		if err != nil {
			t.Fatal(err)
		}
		current[Directory+"/"+name] = data
	}
	for rel, data := range current {
		if _, listed := digests[lfDigest(data)]; listed {
			t.Fatalf("the current text of %s is listed as an earlier text", rel)
		}
	}
	for digest := range digests {
		delete(digests, digest)
	}
	if len(PriorDigests()) == 0 {
		t.Fatal("PriorDigests exposed its map for mutation")
	}
}

// Boundary: a text differing from an earlier one by a single trailing byte, or by carriage
// returns alone, maps as the file-name convention says.
func TestPriorDigestsBoundary(t *testing.T) {
	entries, err := os.ReadDir(priorTextDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no earlier texts: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(priorTextDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout may convert testdata to CRLF; the fixtures below start from LF.
	data := []byte(strings.ReplaceAll(string(raw), "\r\n", "\n"))
	digests := PriorDigests()
	if _, known := digests[lfDigest(append(data, '\n'))]; known {
		t.Fatal("an earlier text with one more byte is listed")
	}
	if _, known := digests[lfDigest([]byte(strings.ReplaceAll(string(data), "\n", "\r\n")))]; !known {
		t.Fatal("the CRLF form of an earlier text does not reduce to its listed digest")
	}
	if got := priorTextPath("markdownlint-cli2.v1.yaml"); got != Directory+"/markdownlint-cli2.yaml" {
		t.Fatalf("asset naming convention maps to %q", got)
	}
	if got := priorTextPath("unknown.v1.txt"); got != "" {
		t.Fatalf("an unknown prior file maps to %q", got)
	}
}

// ActionlintLabels (#593). Positive: each label is the runs-on value of a Workflow job.
// Negative: the returned slice is a private copy. Boundary: the list holds no repeat, and each
// label is one token a YAML list item carries unquoted.
func TestActionlintLabelsPositiveNegativeBoundary(t *testing.T) {
	labels := ActionlintLabels()
	if len(labels) == 0 {
		t.Fatal("no label declared; actionlint would then know every runner of Workflow, so drop this test with the list")
	}
	for index, label := range labels {
		if !strings.Contains(Workflow, "\n    runs-on: "+label+"\n") {
			t.Errorf("Workflow does not run on %s", label)
		}
		if slices.Index(labels, label) != index || label == "" || strings.ContainsAny(label, " \t\"'#:,[]{}") {
			t.Errorf("label %q is repeated or not one plain token", label)
		}
	}
	labels[0] = "mutated"
	if ActionlintLabels()[0] == "mutated" {
		t.Fatal("ActionlintLabels exposed the list for mutation")
	}
}
