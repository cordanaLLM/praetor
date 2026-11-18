package agentcontext

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/clientid"
	"github.com/cordanaLLM/praetor/internal/util"
)

func checkClientSize(file string, size int) error {
	cap := clientSizeCaps[file]
	if cap.value > 0 && size > cap.value {
		return fmt.Errorf("target file %s exceeds client size cap (%d %s > %d)", file, size, cap.unit, cap.value)
	}
	return nil
}

// Safety bounds apply even when a client has no documented context cap. The
// compiler's confined snapshot reader enforces the same per-file byte bound.
const (
	maxImportBytes  = 1 << 20
	maxImportVisits = 256
)

var contextImportRE = regexp.MustCompile(`^@(\S+)$`)

type contextImport struct {
	path  string
	line  int
	bytes int
}

func contextImports(text string) ([]string, []contextImport, error) {
	if len(text) > maxImportBytes {
		return nil, nil, fmt.Errorf("context exceeds import scan bound (%d bytes)", maxImportBytes)
	}
	lines := strings.Split(text, "\n")
	var imports []contextImport
	var fence util.MarkdownFence
	for i := 0; i < len(lines) && i <= maxImportBytes; i++ {
		if fence.Inside(strings.TrimSpace(lines[i])) {
			continue
		}
		match := contextImportRE.FindStringSubmatch(strings.TrimSuffix(lines[i], "\r"))
		if match == nil {
			continue
		}
		if len(imports) == maxImportVisits {
			return nil, nil, fmt.Errorf("too many context imports (cap %d)", maxImportVisits)
		}
		imports = append(imports, contextImport{path: match[1], line: i, bytes: len(lines[i])})
	}
	return lines, imports, nil
}

// prepareImports validates the complete loaded context before relocating imports or emitting pointers.
func (t *Transpiler) prepareImports(target vendorTarget, text string) (string, error) {
	text = strings.TrimRight(text, "\n") + "\n"
	lines, imports, err := contextImports(text)
	if err != nil {
		return "", fmt.Errorf("target file %s: %w", target.path, err)
	}
	if target.importDepth == 0 {
		for _, ref := range imports {
			lines[ref.line] = "See " + ref.path + " (read on demand)."
		}
		text = strings.Join(lines, "\n")
	}
	size, via, err := t.expandedSize(target, text, imports)
	if err != nil {
		return "", fmt.Errorf("target file %s: %w", target.path, err)
	}
	if err := t.checkLoadedSize(target, size); err != nil {
		return "", fmt.Errorf("%w; loaded canonical context via %s", err, via)
	}
	if target.importDepth > 0 {
		for _, ref := range imports {
			lines[ref.line] = target.importLine(ref.path)
		}
	}
	return strings.Join(lines, "\n"), nil
}

// checkLoadedSize applies the registry caps and line budget to all text a client loads.
func (t *Transpiler) checkLoadedSize(target vendorTarget, size contextSize) error {
	value := size.bytes
	if clientSizeCaps[target.path].unit == "characters" {
		value = size.characters
	}
	if err := checkClientSize(target.path, value); err != nil {
		return err
	}
	if err := t.checkCanonicalSize(target, size.bytes); err != nil {
		return err
	}
	if target.client == string(clientid.Codex) {
		size.lines += t.ProjectDocLines
	}
	if size.lines > t.MaxLines {
		return fmt.Errorf("target file %s exceeds max line budget (%d > %d)%s", target.path, size.lines, t.MaxLines, t.lineChainNote(target))
	}
	return nil
}

func (t *Transpiler) lineChainNote(target vendorTarget) string {
	if target.client == string(clientid.Codex) && t.ProjectDocLineChain != "" {
		return "; largest nested line chain ends at " + t.ProjectDocLineChain
	}
	return ""
}

// contextSize records byte, Unicode character and line counts from the same expansion walk.
type contextSize struct {
	bytes, characters, lines int
}

// add counts one loaded document, including its final empty line when present.
func (size *contextSize) add(text string) {
	size.bytes += len(text)
	size.characters += utf8.RuneCountInString(text)
	size.lines += countLines(text)
}

// importLine relocates one root-relative import to the directory of its emitted projection.
func (target vendorTarget) importLine(name string) string {
	return "@" + strings.Repeat("../", strings.Count(target.path, "/")) + name
}

type importFrame struct {
	path  string
	refs  []contextImport
	next  int
	depth int
}

// expandedSize counts loaded canonical text and substitutions without materializing documents.
// Native and sentence readers load both files without expanding canonical imports.
// The active visited set detects cycles; independent repeated imports still count.
func (t *Transpiler) expandedSize(target vendorTarget, text string, refs []contextImport) (contextSize, string, error) {
	if target.importDepth == 0 {
		return t.directSize(target, text)
	}
	var size contextSize
	size.add(text)
	stack := []importFrame{{path: target.path, refs: refs}}
	visited := map[string]bool{target.path: true}
	via := CanonicalFile
	visits := 0
	for step := 0; len(stack) > 0 && step <= 2*maxImportVisits; step++ {
		frame := &stack[len(stack)-1]
		if frame.next == len(frame.refs) {
			delete(visited, frame.path)
			stack = stack[:len(stack)-1]
			continue
		}
		ref := frame.refs[frame.next]
		frame.next++
		name, err := importPath(ref.path, frame.depth, target.importDepth, visited)
		if err != nil {
			return contextSize{}, "", err
		}
		if visits == maxImportVisits {
			return contextSize{}, "", fmt.Errorf("import %s exceeds traversal bound (%d imports)", name, maxImportVisits)
		}
		visits++
		data, err := t.loadImport(name)
		if err != nil {
			return contextSize{}, "", err
		}
		_, children, err := contextImports(data)
		if err != nil {
			return contextSize{}, "", fmt.Errorf("import %s: %w", name, err)
		}
		size.add(data)
		size.bytes -= ref.bytes
		size.characters -= utf8.RuneCountInString("@" + ref.path)
		size.lines-- // The import line is replaced, while its newline remains.
		via = name
		visited[name] = true
		stack = append(stack, importFrame{path: name, refs: children, depth: frame.depth + 1})
	}
	if len(stack) != 0 {
		return contextSize{}, "", fmt.Errorf("import %s exceeds traversal bound (%d imports)", via, maxImportVisits)
	}
	return size, via, nil
}

// directSize counts full projections once and native or sentence projections with canonical text.
func (t *Transpiler) directSize(target vendorTarget, text string) (contextSize, string, error) {
	var size contextSize
	size.add(text)
	if target.fullContent {
		return size, target.path, nil
	}
	data, err := t.loadImport(CanonicalFile)
	size.add(data)
	return size, CanonicalFile, err
}

func importPath(raw string, depth, limit int, visited map[string]bool) (string, error) {
	name := path.Clean(raw)
	if !fs.ValidPath(name) || strings.Contains(name, "\\") {
		return "", fmt.Errorf("import %s must be repository-root-relative", raw)
	}
	if visited[name] {
		return "", fmt.Errorf("import cycle at %s", raw)
	}
	if depth >= limit {
		return "", fmt.Errorf("import %s exceeds depth cap (%d)", raw, limit)
	}
	return name, nil
}

func (t *Transpiler) loadImport(name string) (string, error) {
	if t.ReadImport == nil {
		return "", fmt.Errorf("import %s missing or unreadable: no repository reader", name)
	}
	data, err := t.ReadImport(name)
	if err != nil {
		return "", fmt.Errorf("import %s missing or unreadable: %w", name, err)
	}
	return data, nil
}
