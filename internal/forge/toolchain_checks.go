package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/semver"
)

const (
	goManifestName        = "go.mod"
	templatesDirectory    = "templates"
	actionsRelativePath   = ".github/actions"
	workflowsRelativePath = ".github/workflows"
	dockerfilePrefix      = "Dockerfile"
	actionManifestPrefix  = "action."
	goVersionKey          = "go-version"
	inputDefaultKey       = "default"
	golangImagePrefix     = "FROM golang:"
	maxScannedLines       = 4096
	maxTemplateEntries    = 64
	maxVersionComponents  = 4
	maxDocumentNodes      = 8192
	// maxRangeClauses bounds both the `||` alternatives of one setup-go range and the
	// comparators of one alternative. A longer range is reported unparseable, not truncated.
	maxRangeClauses = 8
	// rangeUnion separates the alternatives of a node-semver range.
	rangeUnion = "||"
	// hyphenRangeSeparator is the standalone dash of a hyphen range `A - B`, which
	// node-semver reads as `>=A <=B`.
	hyphenRangeSeparator = "-"
	// stableAlias and oldstableAlias are the release aliases setup-go resolves at run time.
	stableAlias    = "stable"
	oldstableAlias = "oldstable"
	// anyVersionComponent is a wildcard component: setup-go resolves `1.25.x` and `1.x` to
	// the newest release matching the prefix, so the component cannot be below a directive.
	anyVersionComponent = -1
	// maxTemplateFiles is what one tree of archetype directories can legitimately yield:
	// every archetype's files, for every archetype.
	maxTemplateFiles = maxTemplateEntries * maxTemplateEntries
	// maxAuditedFiles covers every document auditedDocuments can hand over -- the workflows,
	// the composite actions and the container templates. A larger inventory is refused, not
	// truncated, so no document can fall off the end of the audit unreported.
	maxAuditedFiles = maxWorkflowFiles + 2*maxTemplateFiles
)

// ToolchainReason says what the audit established about a pin it reports.
type ToolchainReason string

const (
	// ToolchainBelow is a version, bare or `=`, older than the directive.
	ToolchainBelow ToolchainReason = "below"
	// ToolchainRangeBelow is a setup-go range whose lower bound is older than the directive,
	// so it admits a toolchain the module never declared.
	ToolchainRangeBelow ToolchainReason = "range-below"
	// ToolchainUpperBoundOnly is a range with an upper bound and no lower bound: nothing in
	// the file keeps the toolchain at or above the directive.
	ToolchainUpperBoundOnly ToolchainReason = "upper-bound-only"
	// ToolchainAlias is setup-go's oldstable alias. It names the release line before the
	// current stable one and is resolved only at run time, so the file alone cannot show it
	// meets the directive.
	ToolchainAlias ToolchainReason = "alias"
	// ToolchainUnparseable is a value that is no version, range or alias setup-go
	// documents, so the audit cannot compare it at all.
	ToolchainUnparseable ToolchainReason = "unparseable"
	// pinSatisfies is the verdict for a pin that is not a finding.
	pinSatisfies ToolchainReason = ""
)

// ToolchainFinding names one Go toolchain pin the audit reports against the module's go
// directive, and Reason says why: older than it, a range admitting older releases, an alias
// resolved only at run time, or a value the audit cannot read.
type ToolchainFinding struct {
	File      string
	Line      int
	Pin       string
	Directive string
	Reason    ToolchainReason
}

func (f ToolchainFinding) String() string {
	return fmt.Sprintf("%s:%d: pins Go %s, %s", f.File, f.Line, f.Pin, f.Reason.explain(f.Directive))
}

// explain renders the reason as the clause a finding line ends with.
func (r ToolchainReason) explain(directive string) string {
	against := "go.mod's " + directive + " directive"
	switch r {
	case ToolchainRangeBelow:
		return "a range whose lower bound is below " + against
	case ToolchainUpperBoundOnly:
		return "a range with no lower bound, so it admits releases below " + against
	case ToolchainAlias:
		return "an alias setup-go resolves only at run time, so it cannot be checked against " + against
	case ToolchainUnparseable:
		return "not a version, range or alias the audit can compare with " + against
	default:
		return "below " + against
	}
}

// toolchainDirective is go.mod's go directive as written and as the components every pin
// is compared against.
type toolchainDirective struct {
	text       string
	components []int
}

// toolchainPin is one Go version a document names, at the line a reader can open.
type toolchainPin struct {
	Version string
	Line    int
}

// AuditGoToolchain reports every Go toolchain pin in the repository's workflows, in the
// composite actions it ships, and in its container templates, that does not provably meet
// go.mod's go directive.
//
// go.mod is where the toolchain is decided. A workflow key, a composite action's input
// default or a Dockerfile carrying a different number builds the module with a compiler it
// never declared, and when the copy is something adopters consume -- a template they
// scaffold, an action they call by ref -- that mismatch is handed to every one of them.
// The copies drift because nothing compares them, which is what this audit does.
//
// A newer toolchain still satisfies the module, and a GitHub expression fixes no version in
// the file, so neither is a finding. Every other pin is a finding carrying its Reason: a
// version below the directive, a range whose lower bound is below it or that has none, the
// oldstable alias, or a value the audit cannot read. A pin the audit cannot read is reported
// at its file and line like any other, never an error that stops the audit before the pins
// after it; only what leaves nothing to compare against (no go.mod, no directive, a
// directive that is no version) fails the audit.
func AuditGoToolchain(ctx context.Context, repoPath string) ([]ToolchainFinding, error) {
	if ctx == nil {
		return nil, errors.New("go toolchain audit requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	manifest, err := contextopt.ReadSnapshot(ctx, filepath.Join(repoPath, goManifestName))
	if err != nil {
		return nil, fmt.Errorf("go manifest: %w", err)
	}
	directive, declared := gomanifest.GoDirective(manifest)
	if !declared {
		return nil, fmt.Errorf("%s declares no go directive, so no pin can be compared", goManifestName)
	}
	files, err := auditedDocuments(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	return auditToolchainFiles(files, directive)
}

// auditedDocuments reads every document the audit compares against the directive, each
// named by its repository path.
func auditedDocuments(ctx context.Context, repoPath string) ([]workflowFile, error) {
	workflows, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	actions, err := readActionFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	templates, err := readTemplateFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	documents := underDirectory(workflowsRelativePath, workflows)
	documents = append(documents, actions...)
	return append(documents, templates...), nil
}

// auditToolchainFiles audits already-read documents, each named by its repository path. An
// inventory beyond the audit's bound is refused rather than truncated, so a tree cannot
// push a stale pin past the end of the scan and be reported clean.
func auditToolchainFiles(files []workflowFile, directive string) ([]ToolchainFinding, error) {
	if len(files) > maxAuditedFiles {
		return nil, fmt.Errorf("audit inventory exceeds %d documents", maxAuditedFiles)
	}
	required, err := versionComponents(directive)
	if err != nil {
		return nil, fmt.Errorf("%s go directive: %w", goManifestName, err)
	}
	anchor := toolchainDirective{text: directive, components: required}
	var findings []ToolchainFinding
	for i := 0; i < len(files) && i < maxAuditedFiles; i++ {
		found, err := auditToolchainPins(files[i].Name, files[i].Data, anchor)
		if err != nil {
			return nil, err
		}
		findings = append(findings, found...)
	}
	return findings, nil
}

// auditToolchainPins reports the pins of one document that do not provably meet directive.
func auditToolchainPins(name string, data []byte, directive toolchainDirective) ([]ToolchainFinding, error) {
	pins, err := toolchainPinsIn(name, data)
	if err != nil {
		return nil, err
	}
	var findings []ToolchainFinding
	for i := 0; i < len(pins) && i < maxScannedLines; i++ {
		reason := pinReason(pins[i].Version, directive.components)
		if reason == pinSatisfies {
			continue
		}
		findings = append(findings, ToolchainFinding{
			File: name, Line: pins[i].Line, Pin: pins[i].Version, Directive: directive.text, Reason: reason,
		})
	}
	return findings, nil
}

// toolchainPinsIn reads every Go version one document names. A YAML document goes through
// the parser and a container template is scanned as text, because a Dockerfile is not YAML.
func toolchainPinsIn(name string, data []byte) ([]toolchainPin, error) {
	if isYAMLDocument(name) {
		return documentToolchainPins(name, data)
	}
	return imageToolchainPins(data), nil
}

// documentToolchainPins reports every Go version a YAML document pins, at the line the
// value is written on.
//
// The document is walked as parsed nodes rather than as text. A text scan sees only a line
// whose first characters are the key, so `with: {go-version: '1.24'}`, a matrix list and a
// composite action's `default:` all read as no pin at all -- silently, which is the one
// outcome an audit must not produce. The walk is iterative and refuses a document larger
// than its bound rather than stopping part-way through it.
func documentToolchainPins(name string, data []byte) ([]toolchainPin, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("%s: parse: %w", name, err)
	}
	pending := []*yaml.Node{&document}
	var pins []toolchainPin
	for i := 0; i < maxDocumentNodes && len(pending) > 0; i++ {
		node := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if node.Kind == yaml.MappingNode {
			pins = append(pins, mappingToolchainPins(node)...)
		}
		pending = append(pending, node.Content...)
	}
	if len(pending) > 0 {
		return nil, fmt.Errorf("%s: document exceeds %d nodes", name, maxDocumentNodes)
	}
	// The walk visits mappings in no particular order, so the pins are put back into the
	// order a reader opens the file in. A finding list that reshuffles between runs is a
	// report nobody can diff.
	sort.SliceStable(pins, func(i, j int) bool { return pins[i].Line < pins[j].Line })
	return pins, nil
}

// mappingToolchainPins reads the Go versions one mapping names under a `go-version` key.
func mappingToolchainPins(node *yaml.Node) []toolchainPin {
	var pins []toolchainPin
	for i := 0; i+1 < len(node.Content) && i < 2*maxDocumentNodes; i += 2 {
		if node.Content[i].Value != goVersionKey {
			continue
		}
		pins = append(pins, valueToolchainPins(node.Content[i+1])...)
	}
	return pins
}

// valueToolchainPins reads the versions one `go-version` value carries: a scalar, every
// leg of a matrix list, or the default of a composite action's input declaration.
func valueToolchainPins(value *yaml.Node) []toolchainPin {
	switch value.Kind {
	case yaml.ScalarNode:
		return scalarToolchainPin(value)
	case yaml.SequenceNode:
		var pins []toolchainPin
		for i := 0; i < len(value.Content) && i < maxMatrixLegs; i++ {
			pins = append(pins, scalarToolchainPin(value.Content[i])...)
		}
		return pins
	case yaml.MappingNode:
		return inputDefaultPin(value)
	default:
		return nil
	}
}

// inputDefaultPin reads the version a composite action's `go-version` input defaults to.
// docs/adoption.md documents calling the shipped action without that input, so the default
// is the toolchain every adopter's runner actually installs.
func inputDefaultPin(declaration *yaml.Node) []toolchainPin {
	for i := 0; i+1 < len(declaration.Content) && i < 2*maxMatrixLegs; i += 2 {
		if declaration.Content[i].Value == inputDefaultKey {
			return scalarToolchainPin(declaration.Content[i+1])
		}
	}
	return nil
}

// scalarToolchainPin reads one YAML scalar as a version. The parser has already dropped
// the quotes and any trailing comment, so only the value itself is judged. A GitHub
// expression stays a silent skip, while versions, aliases, ranges and unparseable literals
// are audited.
func scalarToolchainPin(value *yaml.Node) []toolchainPin {
	if value.Kind != yaml.ScalarNode {
		return nil
	}
	raw := strings.TrimSpace(value.Value)
	if raw == "" || strings.Contains(raw, expressionOpen) {
		return nil
	}
	return []toolchainPin{{Version: raw, Line: value.Line}}
}

// imageToolchainPins reads the `FROM golang:<tag>` build stages of a container template.
func imageToolchainPins(data []byte) []toolchainPin {
	lines := strings.Split(string(data), "\n")
	var pins []toolchainPin
	for i := 0; i < len(lines) && i < maxScannedLines; i++ {
		trimmed := strings.TrimSpace(strings.TrimSuffix(lines[i], "\r"))
		image, found := strings.CutPrefix(trimmed, golangImagePrefix)
		if !found {
			continue
		}
		if version, pinned := imageTagPin(image); pinned {
			pins = append(pins, toolchainPin{Version: version, Line: i + 1})
		}
	}
	return pins
}

// imageTagPin reads the version out of a golang image reference, dropping the distribution
// variant, a digest, and whatever follows the reference on the line.
func imageTagPin(image string) (string, bool) {
	fields := strings.Fields(image)
	if len(fields) == 0 {
		return "", false
	}
	tag := fields[0]
	if cut := strings.IndexAny(tag, "-@"); cut >= 0 {
		tag = tag[:cut]
	}
	return numericPin(tag)
}

// numericPin accepts only a value beginning with a digit, which is what separates a version
// from a moving tag such as `latest`.
func numericPin(value string) (string, bool) {
	if value == "" || value[0] < '0' || value[0] > '9' {
		return "", false
	}
	return value, true
}

// pinReason says why pin is a finding against the directive's components, or returns
// pinSatisfies. Every value reaches a verdict: a value the audit cannot read is a finding
// that says so, never an error that would stop the audit before the pins after it.
//
// A single version, bare or `=`, is a pin: a component it omits counts as zero, so 1.27.1
// satisfies a 1.27 directive and 1.27 is below a 1.27.1 one, and a wildcard component
// resolves to the newest release matching the prefix, so it is never below what the
// directive requires at that position. Anything else is a node-semver range, judged by the
// lower bound of each `||` alternative (alternativeReason).
func pinReason(pin string, required []int) ToolchainReason {
	switch pin {
	case stableAlias:
		return pinSatisfies
	case oldstableAlias:
		return ToolchainAlias
	}
	alternatives, readable := rangeAlternatives(pin)
	if !readable {
		return ToolchainUnparseable
	}
	if version, exact := exactVersion(alternatives); exact {
		return exactPinReason(version, required)
	}
	for i := 0; i < len(alternatives) && i < maxRangeClauses; i++ {
		if reason := alternativeReason(alternatives[i], required); reason != pinSatisfies {
			return reason
		}
	}
	return pinSatisfies
}

// exactVersion returns the version a range names when the whole range is one bare or `=`
// comparator, which node-semver reads as the same x-range a bare version is.
func exactVersion(alternatives [][]string) (string, bool) {
	if len(alternatives) != 1 || len(alternatives[0]) != 1 {
		return "", false
	}
	operator, version := semver.CutOperator(alternatives[0][0])
	return version, operator == "" || operator == "="
}

// exactPinReason judges one version against the directive's components.
func exactPinReason(version string, required []int) ToolchainReason {
	pinned, err := versionComponents(version)
	if err != nil {
		return ToolchainUnparseable
	}
	if componentsBelow(pinned, required) {
		return ToolchainBelow
	}
	return pinSatisfies
}

// alternativeReason judges one `||` alternative, whose comparators all have to hold. It
// admits a release below the directive when every lower bound it carries is below it, and
// nothing holds it up at all when it carries none.
func alternativeReason(comparators []string, required []int) ToolchainReason {
	bounded, lowerBelow := false, true
	for i := 0; i < len(comparators) && i < maxRangeClauses; i++ {
		lower, isLower, readable := lowerBound(comparators[i])
		if !readable {
			return ToolchainUnparseable
		}
		if isLower {
			bounded = true
			lowerBelow = lowerBelow && componentsBelow(lower, required)
		}
	}
	switch {
	case !bounded:
		return ToolchainUpperBoundOnly
	case lowerBelow:
		return ToolchainRangeBelow
	}
	return pinSatisfies
}

// lowerBound reads the lowest release one comparator admits. isLower is false for an upper
// bound (`<`, `<=`); readable is false for a comparator whose version is not a dotted number.
// A wildcard component in a range is the zero it stands for there: `~1.27.x` admits 1.27.0.
func lowerBound(comparator string) (lower []int, isLower, readable bool) {
	operator, version := semver.CutOperator(comparator)
	components, err := versionComponents(version)
	if err != nil {
		return nil, false, false
	}
	components = beforeWildcard(components)
	switch operator {
	case "<", "<=":
		return nil, false, true
	case ">":
		return exclusiveLowerBound(components)
	default:
		return components, true, true
	}
}

// beforeWildcard drops a version's components from its first wildcard on.
func beforeWildcard(components []int) []int {
	for i := 0; i < len(components) && i < maxVersionComponents; i++ {
		if components[i] == anyVersionComponent {
			return components[:i]
		}
	}
	return components
}

// exclusiveLowerBound is the first release `>V` admits. node-semver reads a partial `>1.26`
// as `>=1.27.0` and `>1` as `>=2.0.0`, and a full `>1.26.3` admits 1.26.4 first, so the last
// component V names is raised by one. `>*` admits nothing and is not readable.
func exclusiveLowerBound(components []int) (lower []int, isLower, readable bool) {
	if len(components) == 0 {
		return nil, false, false
	}
	next := append([]int(nil), components...)
	next[len(next)-1]++
	return next, true, true
}

// rangeAlternatives splits a node-semver range into its `||` alternatives, each a list of
// comparators. readable is false for a range the audit cannot read: an empty alternative, a
// dangling operator, or more alternatives or comparators than maxRangeClauses -- refused
// rather than truncated, so a clause past the bound cannot hide an older release.
func rangeAlternatives(pin string) ([][]string, bool) {
	parts := strings.SplitN(pin, rangeUnion, maxRangeClauses+1)
	if len(parts) > maxRangeClauses {
		return nil, false
	}
	alternatives := make([][]string, 0, len(parts))
	for i := 0; i < len(parts) && i < maxRangeClauses; i++ {
		comparators, readable := rangeComparators(strings.Fields(parts[i]))
		if !readable {
			return nil, false
		}
		alternatives = append(alternatives, comparators)
	}
	return alternatives, true
}

// rangeComparators rejoins the comparators of one alternative. An operator written apart
// from its version (`>= 1.27`, any amount of white space) is joined to it, and the dash of a
// hyphen range `A - B` becomes B's `<=`.
func rangeComparators(fields []string) ([]string, bool) {
	if len(fields) == 0 || len(fields) > maxRangeClauses || fields[0] == hyphenRangeSeparator {
		return nil, false
	}
	comparators := make([]string, 0, len(fields))
	pending := ""
	for i := 0; i < len(fields) && i < maxRangeClauses; i++ {
		operator, alone := standaloneOperator(fields[i])
		switch {
		case alone && pending != "":
			return nil, false
		case alone:
			pending = operator
		default:
			comparators = append(comparators, pending+fields[i])
			pending = ""
		}
	}
	return comparators, pending == ""
}

// standaloneOperator reports a field that is only a comparator operator, the version after
// it in the next field. A hyphen range's dash is its upper bound's `<=`.
func standaloneOperator(field string) (string, bool) {
	if field == hyphenRangeSeparator {
		return "<=", true
	}
	operator, rest := semver.CutOperator(field)
	return operator, operator != "" && rest == ""
}

// componentsBelow reports whether pinned is older than required, component by component.
// A component pinned omits counts as zero, and a wildcard component is never below.
func componentsBelow(pinned, required []int) bool {
	for i := 0; i < len(required) && i < maxVersionComponents; i++ {
		component := 0
		if i < len(pinned) {
			component = pinned[i]
		}
		if component == anyVersionComponent {
			return false
		}
		if component != required[i] {
			return component < required[i]
		}
	}
	return false
}

// versionComponents splits a dotted version into its numbers.
//
// setup-go's documented syntax is wider than a dotted number: `1.25.x` and `1.x` are
// wildcards and `1.24.0-rc.1` is a prerelease. Refusing those made a legal, satisfying pin
// a hard audit error, so a prerelease suffix is dropped before the split and a wildcard
// component becomes anyVersionComponent. A component that is neither still fails the audit
// rather than being quietly read as equal.
func versionComponents(version string) ([]int, error) {
	if cut := strings.IndexByte(version, '-'); cut >= 0 {
		version = version[:cut]
	}
	parts := strings.Split(version, ".")
	if len(parts) > maxVersionComponents {
		return nil, fmt.Errorf("version %q carries more than %d components", version, maxVersionComponents)
	}
	components := make([]int, 0, len(parts))
	for i := 0; i < len(parts) && i < maxVersionComponents; i++ {
		if isVersionWildcard(parts[i]) {
			components = append(components, anyVersionComponent)
			continue
		}
		component, err := strconv.Atoi(parts[i])
		if err != nil {
			return nil, fmt.Errorf("version %q is not a dotted number: %w", version, err)
		}
		components = append(components, component)
	}
	return components, nil
}

// isVersionWildcard reports whether a version component stands for any release.
func isVersionWildcard(part string) bool {
	return part == "x" || part == "X" || part == "*"
}

// underDirectory restates file names as repository paths, so a finding points at a file a
// reader can open.
func underDirectory(directory string, files []workflowFile) []workflowFile {
	named := make([]workflowFile, 0, len(files))
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		named = append(named, workflowFile{Name: directory + "/" + files[i].Name, Data: files[i].Data})
	}
	return named
}

// readTemplateFiles reads every container template praetor ships, as
// templates/<archetype>/Dockerfile*. A repository without the directory yields no files.
func readTemplateFiles(ctx context.Context, repoPath string) ([]workflowFile, error) {
	return readTreeFiles(ctx, repoPath, templatesDirectory, "template", isContainerTemplate)
}

// isContainerTemplate selects the Dockerfile templates of one archetype directory.
func isContainerTemplate(entry os.DirEntry) bool {
	return !entry.IsDir() && strings.HasPrefix(entry.Name(), dockerfilePrefix)
}

// readActionFiles reads every composite action praetor ships, as
// .github/actions/<action>/action.yml. A repository without the directory yields no files.
//
// A shipped action is consumed by ref, so a toolchain version in its input defaults is the
// one an adopter's runner installs without ever naming it -- the same copy-of-go.mod the
// workflows carry, in a directory the workflow reader does not reach.
func readActionFiles(ctx context.Context, repoPath string) ([]workflowFile, error) {
	return readTreeFiles(ctx, repoPath, actionsRelativePath, "composite action", isActionManifest)
}

// isActionManifest selects the action manifest of one composite action directory.
func isActionManifest(entry os.DirEntry) bool {
	name := entry.Name()
	return !entry.IsDir() && isYAMLDocument(name) && strings.HasPrefix(name, actionManifestPrefix)
}

// readTreeFiles reads the files of every immediate subdirectory of directory that keep
// selects, named as repository paths. A repository without the directory yields no files.
func readTreeFiles(
	ctx context.Context, repoPath, directory, inventory string, keep func(os.DirEntry) bool,
) (_ []workflowFile, err error) {
	root, err := contextopt.OpenDirectoryIn(ctx, repoPath, filepath.FromSlash(directory))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	children, err := boundedNames(root, maxTemplateEntries, inventory, isSubdirectory)
	if err != nil {
		return nil, err
	}
	var files []workflowFile
	for i := 0; i < len(children) && i < maxTemplateEntries; i++ {
		found, readErr := readChildFiles(ctx, repoPath, directory, children[i], inventory, keep)
		if readErr != nil {
			return nil, readErr
		}
		files = append(files, found...)
	}
	return files, nil
}

// isSubdirectory selects the child directories of a tree root.
func isSubdirectory(entry os.DirEntry) bool {
	return entry.IsDir()
}

// readChildFiles reads one subdirectory's selected files.
func readChildFiles(
	ctx context.Context, repoPath, directory, child, inventory string, keep func(os.DirEntry) bool,
) (_ []workflowFile, err error) {
	relative := filepath.Join(filepath.FromSlash(directory), child)
	root, err := contextopt.OpenDirectoryIn(ctx, repoPath, relative)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	names, err := boundedNames(root, maxTemplateEntries, inventory, keep)
	if err != nil {
		return nil, err
	}
	files := make([]workflowFile, 0, len(names))
	for i := 0; i < len(names) && i < maxTemplateEntries; i++ {
		data, readErr := contextopt.ReadRootSnapshot(ctx, root, names[i])
		if readErr != nil {
			return nil, fmt.Errorf("%s %s/%s: %w", inventory, child, names[i], readErr)
		}
		files = append(files, workflowFile{
			Name: directory + "/" + child + "/" + names[i],
			Data: data,
		})
	}
	return files, nil
}
