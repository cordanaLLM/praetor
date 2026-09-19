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

// ToolchainFinding names one Go toolchain pin older than the module's go directive.
type ToolchainFinding struct {
	File      string
	Line      int
	Pin       string
	Directive string
}

func (f ToolchainFinding) String() string {
	return fmt.Sprintf("%s:%d: pins Go %s, below go.mod's %s directive", f.File, f.Line, f.Pin, f.Directive)
}

// toolchainPin is one Go version a document names, at the line a reader can open.
type toolchainPin struct {
	Version string
	Line    int
}

// AuditGoToolchain reports every Go toolchain pin in the repository's workflows, in the
// composite actions it ships, and in its container templates, that names a version older
// than go.mod's go directive.
//
// go.mod is where the toolchain is decided. A workflow key, a composite action's input
// default or a Dockerfile carrying a different number builds the module with a compiler it
// never declared, and when the copy is something adopters consume -- a template they
// scaffold, an action they call by ref -- that mismatch is handed to every one of them.
// The copies drift because nothing compares them, which is what this audit does. Only a
// pin below the directive is a finding: a newer toolchain still satisfies the module.
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
	var findings []ToolchainFinding
	for i := 0; i < len(files) && i < maxAuditedFiles; i++ {
		found, err := auditToolchainPins(files[i].Name, files[i].Data, directive)
		if err != nil {
			return nil, err
		}
		findings = append(findings, found...)
	}
	return findings, nil
}

// auditToolchainPins reports the pins of one document that fall below directive.
func auditToolchainPins(name string, data []byte, directive string) ([]ToolchainFinding, error) {
	pins, err := toolchainPinsIn(name, data)
	if err != nil {
		return nil, err
	}
	var findings []ToolchainFinding
	for i := 0; i < len(pins) && i < maxScannedLines; i++ {
		below, err := belowDirective(pins[i].Version, directive)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", name, pins[i].Line, err)
		}
		if below {
			findings = append(findings, ToolchainFinding{
				File: name, Line: pins[i].Line, Pin: pins[i].Version, Directive: directive,
			})
		}
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
// the quotes and any trailing comment, so only the value itself is judged.
func scalarToolchainPin(value *yaml.Node) []toolchainPin {
	if value.Kind != yaml.ScalarNode {
		return nil
	}
	version, pinned := numericPin(strings.TrimSpace(value.Value))
	if !pinned {
		return nil
	}
	return []toolchainPin{{Version: version, Line: value.Line}}
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
// from a workflow expression, a template action, a moving tag such as `latest`, a setup-go
// alias (`stable`, `oldstable`) and a semver range (`^1.25.1`, `>=1.22.0 <1.24.0`). None of
// those names a version the file itself fixes, so none of them is a pin to compare.
func numericPin(value string) (string, bool) {
	if value == "" || value[0] < '0' || value[0] > '9' {
		return "", false
	}
	return value, true
}

// belowDirective reports whether pin names a Go version older than directive. A component
// the pin omits counts as zero, so 1.27.1 satisfies a 1.27 directive and 1.27 is below a
// 1.27.1 one. A wildcard component resolves to the newest release matching the prefix, so
// it is never below what the directive requires at that position.
func belowDirective(pin, directive string) (bool, error) {
	pinned, err := versionComponents(pin)
	if err != nil {
		return false, err
	}
	required, err := versionComponents(directive)
	if err != nil {
		return false, err
	}
	for i := 0; i < len(required) && i < maxVersionComponents; i++ {
		component := 0
		if i < len(pinned) {
			component = pinned[i]
		}
		if component == anyVersionComponent {
			return false, nil
		}
		if component != required[i] {
			return component < required[i], nil
		}
	}
	return false, nil
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
