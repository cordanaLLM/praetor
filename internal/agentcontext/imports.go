package agentcontext

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

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

func (t *Transpiler) prepareImports(target vendorTarget, text string) (string, error) {
	lines, imports, err := contextImports(text)
	if err != nil {
		return "", fmt.Errorf("target file %s: %w", target.path, err)
	}
	var size int
	if target.importDepth == 0 {
		for _, ref := range imports {
			lines[ref.line] = "See " + ref.path + " (read on demand)."
		}
		text = strings.Join(lines, "\n")
		size = len(text)
	} else {
		var via string
		size, via, err = t.expandedSize(target, text, imports)
		if err != nil {
			return "", fmt.Errorf("target file %s: %w", target.path, err)
		}
		if err := checkClientSize(target.path, size); err != nil {
			if via == "" {
				return "", err
			}
			return "", fmt.Errorf("%w; expanded import %s", err, via)
		}
	}
	if clientSizeCaps[target.path].unit == "characters" {
		size = utf8.RuneCountInString(text)
	}
	return text, checkClientSize(target.path, size)
}

type importFrame struct {
	path  string
	refs  []contextImport
	next  int
	depth int
}

// expandedSize counts substitutions without materializing expanded documents.
// The active visited set detects cycles; independent repeated imports still count.
func (t *Transpiler) expandedSize(target vendorTarget, text string, refs []contextImport) (int, string, error) {
	stack := []importFrame{{path: target.path, refs: refs}}
	visited := map[string]bool{target.path: true}
	size, via := len(text), ""
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
			return 0, "", err
		}
		if visits == maxImportVisits {
			return 0, "", fmt.Errorf("import %s exceeds traversal bound (%d imports)", name, maxImportVisits)
		}
		visits++
		data, err := t.loadImport(name)
		if err != nil {
			return 0, "", err
		}
		_, children, err := contextImports(data)
		if err != nil {
			return 0, "", fmt.Errorf("import %s: %w", name, err)
		}
		size += len(data) - ref.bytes
		via = name
		visited[name] = true
		stack = append(stack, importFrame{path: name, refs: children, depth: frame.depth + 1})
	}
	if len(stack) != 0 {
		return 0, "", fmt.Errorf("import %s exceeds traversal bound (%d imports)", via, maxImportVisits)
	}
	return size, via, nil
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
