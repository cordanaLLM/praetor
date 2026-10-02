package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/cavemansource"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	cavemanUsage = "usage: praetorctl caveman check [--kind=message|brief|return|context] [--surface=<name>] [--root=.] [--ext=.md,.py] [--selector=<path>] <file|dir|-> [...]\n" +
		"       praetorctl caveman check --root=. --configured-sources [--max-words=N] [--max-tokens=N]\n" +
		"       praetorctl caveman floor <before> <after>\n" +
		"       praetorctl caveman compress [--stats|--in-place] <file|dir|-> [...]\n" +
		"       praetorctl caveman estimate <file|dir|-> [...]\n" +
		"       praetorctl caveman estimate --base=<git-rev> <file|dir> [...]"
	// maxCavemanFiles bounds the files one invocation reads, directories expanded (HISS-02).
	maxCavemanFiles = 4096
	// maxPrintedFindings bounds the findings printed per file; the count line says how many
	// more exist.
	maxPrintedFindings = 200
)

// cavemanInput is one text to measure: its display name and content.
type cavemanInput struct {
	name         string
	text         string
	kind         caveman.MessageKind
	maskRegister bool
	lineOffset   int
	provenance   string
}

// runCaveman lints agent-facing text (check), proves a rewrite lost nothing (floor), applies
// the cleanups that cannot change meaning (compress) or measures its token cost (estimate).
func runCaveman(args []string) error {
	// cavemanCommand bounds itself by contextopt.MaxDuration.
	return cavemanCommand(rootContext(), args, os.Stdin, os.Stdout)
}

func cavemanCommand(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(cavemanUsage)
	}
	ctx, cancel := context.WithTimeout(ctx, contextopt.MaxDuration)
	defer cancel()
	switch args[0] {
	case "check":
		return cavemanCheck(ctx, args[1:], stdin, out)
	case "floor":
		return cavemanFloor(ctx, args[1:], stdin, out)
	case "compress":
		return cavemanCompress(ctx, args[1:], stdin, out)
	case "estimate":
		return cavemanEstimate(ctx, args[1:], stdin, out)
	}
	return fmt.Errorf("unknown caveman subcommand %q\n%s", args[0], cavemanUsage)
}

// cavemanCheck prints one summary line per input and its findings, and fails when any
// input breaks a rule. --kind defaults to runtime message grammar; brief and return add
// schemas, while context selects the policy-document profile. With --surface it first
// resolves the register from --root and errors when the surface is not internal. --max-words
// and --max-tokens are opt-in ceilings (0 means none; a negative value is refused).
func cavemanCheck(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	fset := flag.NewFlagSet("caveman check", flag.ContinueOnError)
	kindName := fset.String("kind", string(caveman.KindMessage), "Caveman contract: message, brief, return, or context")
	surface := fset.String("surface", "", "Register surface whose manifest setting decides whether the lint applies")
	root := fset.String("root", ".", "Repository root for register resolution and source confinement")
	extensions := fset.String("ext", ".md", "Comma-separated extensions included below directory inputs")
	configured := fset.Bool("configured-sources", false, "Check register.sources from .standards.yaml with expected coverage")
	var selectors repeatedStringFlag
	fset.Var(&selectors, "selector", "Dotted JSON/YAML string selector; repeat for multiple fields")
	maxWords := fset.Int("max-words", 0, "Prose-word ceiling per input (caveman.Options.MaxProseWords, C7); 0 means no ceiling, a negative value is refused")
	maxTokens := fset.Int("max-tokens", 0, "Estimated-token ceiling per input (caveman.EstimateTokens, C8); 0 means no ceiling, a negative value is refused")
	if _, err := parseInterspersed(fset, args); err != nil {
		return err
	}
	explicit := visitedFlags(fset)
	if *surface == string(config.SurfaceHooks) && !explicit["ext"] {
		// Naming the hooks surface is the explicit opt-in requested by the hook-directory
		// contract. Ordinary directory checks retain the historical Markdown-only default.
		*extensions = ".sh,.py"
	}
	kind := caveman.MessageKind(*kindName)
	if !kind.Valid() {
		return fmt.Errorf("caveman check: unsupported kind %q (want message, brief, return, or context)", *kindName)
	}
	if err := validateCavemanCeilings(*maxWords, *maxTokens); err != nil {
		return err
	}
	inputs, note, err := prepareCavemanCheckInputs(ctx, stdin, cavemanCheckRequest{
		root: *root, surface: *surface, kind: kind, extensions: *extensions,
		selectors: selectors, configured: *configured, args: fset.Args(), explicit: explicit,
	})
	if err != nil {
		return err
	}
	text, failed := renderCavemanChecks(inputs, *maxWords, *maxTokens, false)
	if _, err := io.WriteString(out, note+text); err != nil {
		return fmt.Errorf("caveman check: write report: %w", err)
	}
	if failed > 0 {
		return fmt.Errorf("caveman check: %d of %d input(s) failed", failed, len(inputs))
	}
	return nil
}

// validateCavemanCeilings refuses a negative --max-words or --max-tokens before any input is
// read. caveman.Options reads a non-positive ceiling as unset, so a typo such as
// --max-tokens=-1 would otherwise pass every input (#382). 0 stays the documented opt-out.
func validateCavemanCeilings(maxWords, maxTokens int) error {
	for _, ceiling := range []struct {
		flag  string
		value int
	}{{"--max-words", maxWords}, {"--max-tokens", maxTokens}} {
		if ceiling.value < 0 {
			return fmt.Errorf("caveman check: %s=%d is negative; use a positive ceiling, or 0 for none", ceiling.flag, ceiling.value)
		}
	}
	return nil
}

type cavemanCheckRequest struct {
	root, surface, extensions string
	kind                      caveman.MessageKind
	selectors                 []string
	configured                bool
	args                      []string
	explicit                  map[string]bool
}

// prepareCavemanCheckInputs returns the inputs to lint and a note the report opens with: under
// --configured-sources, the line naming a contract that declares no text.
func prepareCavemanCheckInputs(ctx context.Context, stdin io.Reader, request cavemanCheckRequest) ([]cavemanInput, string, error) {
	inputs, note, err := cavemanCheckInputs(ctx, stdin, request)
	if err != nil {
		return nil, "", err
	}
	if !request.configured {
		for index := range inputs {
			inputs[index].kind = request.kind
		}
	}
	if request.surface != "" {
		if err := requireCavemanSurface(ctx, request.root, config.RegisterSurface(request.surface)); err != nil {
			return nil, "", err
		}
	}
	return inputs, note, nil
}

// renderCavemanChecks lints every input and returns the report and the failure count. With
// failuresOnly the report carries only failing inputs, which is what the audit prints.
func renderCavemanChecks(inputs []cavemanInput, maxWords, maxTokens int, failuresOnly bool) (string, int) {
	var text strings.Builder
	failed := 0
	for _, input := range inputs {
		var report strings.Builder
		opts := caveman.Options{Kind: input.kind, MaxProseWords: maxWords, MaxTokens: maxTokens}
		result, masked := checkCavemanInput(input, opts)
		passed := formatCavemanReport(&report, input, result, masked)
		if !passed {
			failed++
		}
		if !passed || !failuresOnly {
			text.WriteString(report.String())
		}
	}
	return text.String(), failed
}

// checkCavemanInput lints one input and returns the report and the masked line count. A file
// or stdin input gets the register-block mask the context gate applies; a context input among
// them goes through the gate's own check (compiler.CheckContextText), which also judges adopter
// text below the harness end marker on its own, so the command reproduces the gate's verdict on
// an adopted AGENTS.md. Extracted source values are linted as they are.
func checkCavemanInput(input cavemanInput, opts caveman.Options) (caveman.Report, int) {
	if !input.maskRegister {
		return caveman.Check(input.text, opts), 0
	}
	if opts.Kind == caveman.KindContext {
		return compiler.CheckContextText(input.text, opts)
	}
	lintable, masked := compiler.MaskRegisterBlock(input.text)
	return caveman.Check(lintable, opts), masked
}

func visitedFlags(flags *flag.FlagSet) map[string]bool {
	visited := make(map[string]bool)
	flags.Visit(func(item *flag.Flag) { visited[item.Name] = true })
	return visited
}

func cavemanCheckInputs(ctx context.Context, stdin io.Reader, request cavemanCheckRequest) ([]cavemanInput, string, error) {
	if !request.configured {
		inputs, err := readCavemanCheckInputs(ctx, stdin, request)
		return inputs, "", err
	}
	if len(request.args) != 0 || request.explicit["kind"] || request.explicit["surface"] ||
		request.explicit["ext"] || request.explicit["selector"] {
		return nil, "", errors.New("--configured-sources owns kind, surface, extensions, and selectors; positional input and overrides are unsupported")
	}
	policy, _, err := compiler.LoadRegisterBlock(ctx, request.root)
	if err != nil {
		return nil, "", err
	}
	inputs, _, err := configuredCavemanInputs(ctx, request.root, policy)
	if err != nil || !policy.Sources.DeclaresNone() {
		return inputs, "", err
	}
	return nil, declaredNoSources(policy.Sources) + "; nothing to check.\n", nil
}

// declaredNoSources names a register.sources contract that declares no text, with its reason,
// for the audit line and `caveman check --configured-sources`.
func declaredNoSources(sources *config.RegisterSources) string {
	return "register.sources declares no agent-facing text (reason: " + sources.Reason + ")"
}

// configuredCavemanInputs extracts policy's register.sources contract and returns the values
// to lint. `caveman check --configured-sources` and the audit both call it, so the two gates
// cannot disagree about which values are checked. A contract that declares no text
// (config.RegisterSources.DeclaresNone) has none, and holds only while the Paperclip harness,
// the text adoption binds, is absent (#601).
func configuredCavemanInputs(ctx context.Context, root string, policy config.RegisterPolicy) ([]cavemanInput, cavemansource.Result, error) {
	if policy.Sources.DeclaresNone() {
		return nil, cavemansource.Result{}, requireNoBoundHarness(root, policy.Sources)
	}
	result, err := cavemansource.ExtractDeclared(ctx, root, policy.Sources)
	if err != nil {
		return nil, cavemansource.Result{}, err
	}
	inputs, err := cavemanSourceInputs(policy, result.Sources)
	return inputs, result, err
}

// requireNoBoundHarness fails a contract that declares no text while .paperclip/harness.json
// exists: its text would pass unlinted. Adoption binds a harness it finds when register.sources
// is absent, and refuses the empty declaration over one (adopt.declaredNoneHolds).
func requireNoBoundHarness(root string, sources *config.RegisterSources) error {
	if !util.FileExists(filepath.Join(root, filepath.FromSlash(paperclipHarnessRel))) {
		return nil
	}
	return fmt.Errorf("%s, but %s exists; declare inputs that bind its text, or remove register.sources and run "+
		"'praetorctl adopt', which binds the harness it finds", declaredNoSources(sources), paperclipHarnessRel)
}

func filterCavemanSources(ctx context.Context, root string, declared []cavemansource.Source) ([]cavemanInput, error) {
	policy, _, err := compiler.LoadRegisterBlock(ctx, root)
	if err != nil {
		return nil, err
	}
	return cavemanSourceInputs(policy, declared)
}

// cavemanSourceInputs is the one mapping from extracted sources to lint inputs. A classified
// exclusion is bound by count and digest but never linted, since its text is a runtime
// expression rather than agent-owned prose; that skip comes first, so a surface carrying only
// exclusions needs no verdict. Every other value's surface must resolve to a Caveman verdict.
func cavemanSourceInputs(policy config.RegisterPolicy, declared []cavemansource.Source) ([]cavemanInput, error) {
	verified := make(map[config.RegisterSurface]bool)
	inputs := make([]cavemanInput, 0, len(declared))
	for _, source := range declared {
		if source.NotApplicable != "" {
			continue
		}
		if !verified[source.Surface] {
			if err := requireSurfaceVerdict(policy, source.Surface); err != nil {
				return nil, err
			}
			verified[source.Surface] = true
		}
		inputs = append(inputs, cavemanInput{name: source.Path, text: source.Text, kind: source.Kind,
			lineOffset: source.Line - 1, provenance: source.Provenance()})
	}
	return inputs, nil
}

func readCavemanCheckInputs(ctx context.Context, stdin io.Reader, request cavemanCheckRequest) ([]cavemanInput, error) {
	extensions, err := parseCavemanExtensions(request.extensions)
	if err != nil {
		return nil, err
	}
	paths, err := expandCavemanPaths(ctx, request.args, extensions)
	if err != nil {
		return nil, err
	}
	markdown, sources, err := partitionCavemanPaths(ctx, stdin, request, paths)
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return markdown, nil
	}
	result, err := cavemansource.ExtractInputs(ctx, request.root, sources)
	if err != nil {
		return nil, err
	}
	extracted, err := filterCavemanSources(ctx, request.root, result.Sources)
	if err != nil {
		return nil, err
	}
	return append(markdown, extracted...), nil
}

// partitionCavemanPaths reads Markdown files and stdin directly and turns every other path
// into ad-hoc source declarations for the extractor.
func partitionCavemanPaths(ctx context.Context, stdin io.Reader, request cavemanCheckRequest,
	paths []string,
) ([]cavemanInput, []config.RegisterSourceInput, error) {
	markdown := make([]cavemanInput, 0, len(paths))
	sources := []config.RegisterSourceInput{}
	for _, path := range paths {
		if path == "-" || strings.EqualFold(filepath.Ext(path), ".md") {
			input, err := readCavemanInput(ctx, path, stdin)
			if err != nil {
				return nil, nil, err
			}
			markdown = append(markdown, input)
			continue
		}
		declared, err := adHocSourceInputs(request, path)
		if err != nil {
			return nil, nil, err
		}
		sources = append(sources, declared...)
	}
	return markdown, sources, nil
}

func adHocSourceInputs(request cavemanCheckRequest, sourcePath string) ([]config.RegisterSourceInput, error) {
	if err := validateAdHocSourceRequest(request, sourcePath); err != nil {
		return nil, err
	}
	format, err := sourceFormatForExtension(filepath.Ext(sourcePath))
	if err != nil {
		return nil, err
	}
	rel, err := rootRelativeSource(request.root, sourcePath)
	if err != nil {
		return nil, err
	}
	base := config.RegisterSourceInput{Path: rel, Surface: config.RegisterSurface(request.surface),
		Kind: string(request.kind), Format: format}
	return adHocSourceSelectors(base, request.selectors, sourcePath)
}

func validateAdHocSourceRequest(request cavemanCheckRequest, sourcePath string) error {
	if request.surface == "" {
		return fmt.Errorf("caveman check: non-Markdown source %s requires --surface", sourcePath)
	}
	if request.kind == caveman.KindContext {
		return errors.New("caveman check: non-Markdown source kind must be message, brief, or return")
	}
	return nil
}

func adHocSourceSelectors(base config.RegisterSourceInput, selectors []string, sourcePath string) ([]config.RegisterSourceInput, error) {
	format := base.Format
	selected := format == config.SourceFormatGo || format == config.SourceFormatJSON || format == config.SourceFormatYAML
	if selected && len(selectors) == 0 {
		return nil, fmt.Errorf("caveman check: %s requires at least one --selector", sourcePath)
	}
	if !selected && len(selectors) > 0 {
		return nil, fmt.Errorf("caveman check: --selector applies only to Go/JSON/YAML, not %s", sourcePath)
	}
	if !selected {
		return []config.RegisterSourceInput{base}, nil
	}
	inputs := make([]config.RegisterSourceInput, len(selectors))
	for index := range selectors {
		inputs[index] = base
		inputs[index].Selector = selectors[index]
	}
	return inputs, nil
}

func rootRelativeSource(root, sourcePath string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(sourcePath)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("caveman source %s is outside --root %s", sourcePath, root)
	}
	return filepath.ToSlash(rel), nil
}

func sourceFormatForExtension(extension string) (config.RegisterSourceFormat, error) {
	switch strings.ToLower(extension) {
	case ".sh":
		return config.SourceFormatShell, nil
	case ".py":
		return config.SourceFormatPython, nil
	case ".go":
		return config.SourceFormatGo, nil
	case ".json":
		return config.SourceFormatJSON, nil
	case ".yaml", ".yml":
		return config.SourceFormatYAML, nil
	}
	return "", fmt.Errorf("caveman check: unsupported source extension %q", extension)
}

func parseCavemanExtensions(value string) (map[string]bool, error) {
	result := make(map[string]bool)
	for _, extension := range strings.Split(value, ",") {
		extension = strings.ToLower(strings.TrimSpace(extension))
		if extension == "" || !strings.HasPrefix(extension, ".") {
			return nil, fmt.Errorf("caveman check: invalid --ext value %q", extension)
		}
		if extension != ".md" {
			if _, err := sourceFormatForExtension(extension); err != nil {
				return nil, err
			}
		}
		result[extension] = true
	}
	return result, nil
}

// requireCavemanSurface reads the register policy through the loader compile-context uses.
// A non-internal surface has no Caveman verdict and is an error, never a green skip.
func requireCavemanSurface(ctx context.Context, root string, surface config.RegisterSurface) error {
	policy, _, err := compiler.LoadRegisterBlock(ctx, root)
	if err != nil {
		return err
	}
	return requireSurfaceVerdict(policy, surface)
}

// requireSurfaceVerdict fails when surface resolves to a register the Caveman lint does not
// judge (docs or social).
func requireSurfaceVerdict(policy config.RegisterPolicy, surface config.RegisterSurface) error {
	enforced, err := policy.LintEnforced(surface)
	if err != nil {
		return err
	}
	if enforced {
		return nil
	}
	resolution := policy.Resolve(surface, "")
	return fmt.Errorf("caveman check: %s = %s has no Caveman verdict", resolution.Source, resolution.Register)
}

// formatCavemanReport appends the summary line and the bounded findings; it returns whether
// the input passed. masked is the number of register block lines left out of the lint.
func formatCavemanReport(out *strings.Builder, input cavemanInput, report caveman.Report, masked int) bool {
	verdict := "PASS"
	if !report.Passed() {
		verdict = "FAIL"
	}
	provenance := ""
	if input.provenance != "" {
		provenance = " " + input.provenance
	}
	fmt.Fprintf(out, "%s: %s prose_words=%d articles=%d density=%.1f/100 limit=%.1f off_regions=%d register_block_lines=%d front_matter_lines=%d tokens_est=%d findings=%d contract=%s mechanical_rules=%s advisory_rules=%s%s\n",
		input.name, verdict, report.ProseWords, report.Articles, report.Density(), caveman.DefaultMaxArticleDensity,
		report.OffRegions, masked, report.FrontMatterLines, report.EstimatedTokens, len(report.Findings), report.Kind,
		coverageRules(report, caveman.EnforcementMechanical), coverageRules(report, caveman.EnforcementAdvisory), provenance)
	appendCavemanFindingsAt(out, input.name, report.Findings, input.lineOffset)
	return report.Passed()
}

func coverageRules(report caveman.Report, enforcement caveman.Enforcement) string {
	var ids []string
	for _, row := range report.Coverage {
		if row.Enforcement == enforcement {
			ids = append(ids, fmt.Sprint(row.SkillRule))
		}
	}
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ",")
}

// appendCavemanFindings prints at most maxPrintedFindings findings as <name>:<line> <rule>:
// <excerpt>, then a count of the rest.
func appendCavemanFindings(out *strings.Builder, name string, findings []caveman.Finding) {
	appendCavemanFindingsAt(out, name, findings, 0)
}

func appendCavemanFindingsAt(out *strings.Builder, name string, findings []caveman.Finding, lineOffset int) {
	for i := 0; i < len(findings) && i < maxPrintedFindings; i++ {
		f := findings[i]
		line := f.Line
		if line > 0 {
			line += lineOffset
		}
		fmt.Fprintf(out, "%s:%d %s: %s\n", name, line, f.Rule, f.Excerpt)
	}
	if extra := len(findings) - maxPrintedFindings; extra > 0 {
		fmt.Fprintf(out, "%s: (+%d more findings)\n", name, extra)
	}
}

// cavemanFloor compares a rewrite with its original and fails when the rewrite lost a code
// span, a shell command, an id, a link target, a marker or a number, or dropped a MUST, a
// prohibition or a numbered rule. Exactly two inputs, a file or "-" each (at most one "-");
// findings name their line in <before>, line 0 for a count.
func cavemanFloor(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	if len(args) != 2 {
		return errors.New(cavemanUsage)
	}
	if args[0] == "-" && args[1] == "-" {
		return errors.New("caveman floor: at most one input can be standard input")
	}
	before, err := readCavemanInput(ctx, args[0], stdin)
	if err != nil {
		return err
	}
	after, err := readCavemanInput(ctx, args[1], stdin)
	if err != nil {
		return err
	}
	report := caveman.Floor(before.text, after.text)
	verdict := "PASS"
	if !report.Passed() {
		verdict = "FAIL"
	}
	var text strings.Builder
	fmt.Fprintf(&text, "%s -> %s: %s findings=%d\n", before.name, after.name, verdict, len(report.Findings))
	appendCavemanFindings(&text, before.name, report.Findings)
	if _, err := io.WriteString(out, text.String()); err != nil {
		return fmt.Errorf("caveman floor: write report: %w", err)
	}
	if !report.Passed() {
		return fmt.Errorf("caveman floor: %s lost %d fact(s) of %s", after.name, len(report.Findings), before.name)
	}
	return nil
}

// cavemanEstimate prints bytes, lines and estimated tokens per input and in total. With
// --base=<rev> it compares each path with that git revision instead (cavemanEstimateBase).
func cavemanEstimate(ctx context.Context, args []string, stdin io.Reader, out io.Writer) error {
	fset := flag.NewFlagSet("caveman estimate", flag.ContinueOnError)
	base := fset.String("base", "", "Git revision to compare each path with: prints before, after and difference per file and in total")
	paths, err := parseInterspersed(fset, args)
	if err != nil {
		return err
	}
	if visitedFlags(fset)["base"] {
		return cavemanEstimateBase(ctx, *base, paths, out)
	}
	inputs, err := readCavemanInputs(ctx, paths, stdin)
	if err != nil {
		return err
	}
	var text strings.Builder
	var total cavemanMeasure
	for _, input := range inputs {
		measure := measureCavemanText(input.text)
		fmt.Fprintf(&text, "%s: bytes=%d lines=%d tokens_est=%d\n", input.name, measure.bytes, measure.lines, measure.tokens)
		total = total.plus(measure)
	}
	fmt.Fprintf(&text, "total: inputs=%d bytes=%d tokens_est=%d\n", len(inputs), total.bytes, total.tokens)
	_, err = io.WriteString(out, text.String())
	return err
}

// readCavemanInputs reads every named file, the Markdown files below every named directory
// and, for "-", standard input. Files go through the bounded snapshot reader (1 MiB, UTF-8,
// symlink-resistant) that compile-context uses.
func readCavemanInputs(ctx context.Context, args []string, stdin io.Reader) ([]cavemanInput, error) {
	if len(args) == 0 {
		return nil, errors.New(cavemanUsage)
	}
	paths, err := expandCavemanPaths(ctx, args, map[string]bool{".md": true})
	if err != nil {
		return nil, err
	}
	inputs := make([]cavemanInput, 0, len(paths))
	for _, path := range paths {
		input, err := readCavemanInput(ctx, path, stdin)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, input)
	}
	return inputs, nil
}

func readCavemanInput(ctx context.Context, path string, stdin io.Reader) (cavemanInput, error) {
	if path == "-" {
		data, err := io.ReadAll(io.LimitReader(stdin, contextopt.MaxSourceBytes+1))
		if err != nil {
			return cavemanInput{}, fmt.Errorf("read standard input: %w", err)
		}
		if len(data) > contextopt.MaxSourceBytes {
			return cavemanInput{}, fmt.Errorf("standard input exceeds %d bytes", contextopt.MaxSourceBytes)
		}
		return cavemanInput{name: "-", text: string(data), maskRegister: true}, nil
	}
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return cavemanInput{}, fmt.Errorf("read %s: %w", path, err)
	}
	return cavemanInput{name: filepath.ToSlash(path), text: string(data), maskRegister: true}, nil
}

// expandCavemanPaths replaces each directory with matching extension files in lexical order.
func expandCavemanPaths(ctx context.Context, args []string, extensions map[string]bool) ([]string, error) {
	if len(args) == 0 {
		return nil, errors.New(cavemanUsage)
	}
	var paths []string
	for _, arg := range args {
		if arg == "-" {
			paths = append(paths, arg)
			continue
		}
		// #nosec G703 -- arg is a file the operator names on the command line to lint, as with
		// cat; no privilege boundary is crossed, and the read itself goes through the bounded,
		// symlink-resistant contextopt.ReadSnapshot.
		info, err := os.Stat(arg)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", arg, err)
		}
		if !info.IsDir() {
			paths = append(paths, arg)
		} else {
			before := len(paths)
			if paths, err = appendCavemanFiles(ctx, paths, arg, extensions); err != nil {
				return nil, err
			}
			if len(paths) == before {
				return nil, fmt.Errorf("caveman check: directory %s matched zero files for --ext", arg)
			}
		}
		if len(paths) > maxCavemanFiles {
			return nil, fmt.Errorf("caveman: more than %d input files", maxCavemanFiles)
		}
	}
	return paths, nil
}

func appendCavemanFiles(ctx context.Context, paths []string, dir string, extensions map[string]bool) ([]string, error) {
	// #nosec G703 -- dir is a directory the operator names on the command line; WalkDir does
	// not follow symlinked directories, only regular selected files are kept, and the count is
	// bounded by maxCavemanFiles.
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type().IsRegular() && extensions[strings.ToLower(filepath.Ext(path))] {
			paths = append(paths, path)
		}
		if len(paths) > maxCavemanFiles {
			return fmt.Errorf("caveman: more than %d input files", maxCavemanFiles)
		}
		return nil
	})
	return paths, err
}
