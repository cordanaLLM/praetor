package forge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
)

const (
	goManifestName        = "go.mod"
	templatesDirectory    = "templates"
	dockerfilePrefix      = "Dockerfile"
	goVersionKey          = "go-version:"
	golangImagePrefix     = "FROM golang:"
	workflowsRelativePath = ".github/workflows"
	maxScannedLines       = 4096
	maxTemplateEntries    = 64
	maxVersionComponents  = 4
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

// AuditGoToolchain reports every Go toolchain pin in the repository's workflows, and in
// the container templates it ships, that names a version older than go.mod's go directive.
//
// go.mod is where the toolchain is decided. A workflow key or a Dockerfile carrying a
// different number builds the module with a compiler it never declared, and when the copy
// is a template that mismatch is handed to every adopter who scaffolds from it. The copies
// drift because nothing compares them, which is what this audit does. Only a pin below the
// directive is a finding: a newer toolchain still satisfies the module.
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
	workflows, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	templates, err := readTemplateFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	return auditToolchainFiles(append(underDirectory(workflowsRelativePath, workflows), templates...), directive)
}

// auditToolchainFiles audits already-read documents, each named by its repository path.
func auditToolchainFiles(files []workflowFile, directive string) ([]ToolchainFinding, error) {
	var findings []ToolchainFinding
	for i := 0; i < len(files) && i < maxWorkflowFiles+maxTemplateEntries; i++ {
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
	lines := strings.Split(string(data), "\n")
	var findings []ToolchainFinding
	for i := 0; i < len(lines) && i < maxScannedLines; i++ {
		pin, pinned := toolchainPinIn(lines[i])
		if !pinned {
			continue
		}
		below, err := belowDirective(pin, directive)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", name, i+1, err)
		}
		if below {
			findings = append(findings, ToolchainFinding{File: name, Line: i + 1, Pin: pin, Directive: directive})
		}
	}
	return findings, nil
}

// toolchainPinIn extracts the Go version one line pins: a workflow `go-version:` key, or a
// `FROM golang:<tag>` build stage. A value supplied by a workflow expression or a template
// action is not a pin -- the file holds no version to compare -- and neither is a moving
// tag such as `latest`. `go-version-file:` names go.mod itself and is already correct.
func toolchainPinIn(line string) (string, bool) {
	trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	if value, found := strings.CutPrefix(trimmed, goVersionKey); found {
		return literalPin(value)
	}
	if image, found := strings.CutPrefix(trimmed, golangImagePrefix); found {
		return imageTagPin(image)
	}
	return "", false
}

// literalPin reads a YAML scalar as a version, dropping quotes and a trailing comment.
func literalPin(value string) (string, bool) {
	if comment := strings.Index(value, " #"); comment >= 0 {
		value = value[:comment]
	}
	return numericPin(strings.Trim(strings.TrimSpace(value), `'"`))
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

// numericPin accepts only a value beginning with a digit, which is what separates a
// version from an expression, a template action or a moving tag.
func numericPin(value string) (string, bool) {
	if value == "" || value[0] < '0' || value[0] > '9' {
		return "", false
	}
	return value, true
}

// belowDirective reports whether pin names a Go version older than directive. A component
// the pin omits counts as zero, so 1.27.1 satisfies a 1.27 directive and 1.27 is below a
// 1.27.1 one.
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
		if component != required[i] {
			return component < required[i], nil
		}
	}
	return false, nil
}

// versionComponents splits a dotted version into its numbers. A component that is not a
// number fails the audit rather than being quietly read as equal.
func versionComponents(version string) ([]int, error) {
	parts := strings.Split(version, ".")
	if len(parts) > maxVersionComponents {
		return nil, fmt.Errorf("version %q carries more than %d components", version, maxVersionComponents)
	}
	components := make([]int, 0, len(parts))
	for i := 0; i < len(parts) && i < maxVersionComponents; i++ {
		component, err := strconv.Atoi(parts[i])
		if err != nil {
			return nil, fmt.Errorf("version %q is not a dotted number: %w", version, err)
		}
		components = append(components, component)
	}
	return components, nil
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
func readTemplateFiles(ctx context.Context, repoPath string) (_ []workflowFile, err error) {
	root, err := contextopt.OpenDirectoryIn(ctx, repoPath, templatesDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	archetypes, err := boundedNames(root, true, "")
	if err != nil {
		return nil, err
	}
	var files []workflowFile
	for i := 0; i < len(archetypes) && i < maxTemplateEntries; i++ {
		found, readErr := readArchetypeTemplates(ctx, repoPath, archetypes[i])
		if readErr != nil {
			return nil, readErr
		}
		files = append(files, found...)
	}
	return files, nil
}

// readArchetypeTemplates reads one archetype directory's container templates.
func readArchetypeTemplates(ctx context.Context, repoPath, archetype string) (_ []workflowFile, err error) {
	root, err := contextopt.OpenDirectoryIn(ctx, repoPath, filepath.Join(templatesDirectory, archetype))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	names, err := boundedNames(root, false, dockerfilePrefix)
	if err != nil {
		return nil, err
	}
	files := make([]workflowFile, 0, len(names))
	for i := 0; i < len(names) && i < maxTemplateEntries; i++ {
		data, readErr := contextopt.ReadRootSnapshot(ctx, root, names[i])
		if readErr != nil {
			return nil, fmt.Errorf("template %s/%s: %w", archetype, names[i], readErr)
		}
		files = append(files, workflowFile{
			Name: templatesDirectory + "/" + archetype + "/" + names[i],
			Data: data,
		})
	}
	return files, nil
}

// boundedNames lists a pinned directory's entries in name order: its subdirectories when
// directories is true, otherwise its regular files whose name starts with prefix. A
// listing larger than the audit's bound is refused rather than truncated, so the tree
// being read cannot make the scan unbounded or make it miss a template in silence.
func boundedNames(root *os.Root, directories bool, prefix string) (_ []string, err error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, directory.Close()) }()
	entries, err := directory.ReadDir(maxTemplateEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > maxTemplateEntries {
		return nil, fmt.Errorf("template inventory exceeds %d entries", maxTemplateEntries)
	}
	var names []string
	for i := 0; i < len(entries) && i < maxTemplateEntries; i++ {
		if entries[i].IsDir() == directories && strings.HasPrefix(entries[i].Name(), prefix) {
			names = append(names, entries[i].Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
