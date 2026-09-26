package adopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"gopkg.in/yaml.v3"
)

// maxManifestTextLines bounds the line scans of the manifest text patch (HISS-02). A longer
// manifest takes the full re-encode.
const maxManifestTextLines = 1 << 14

// manifestText is an operator manifest split into lines that keep their endings, so a patch
// changes only the lines it inserts or replaces: comments, blank lines, key order and
// indentation elsewhere stay as the operator wrote them.
type manifestText struct {
	lines []string
	eol   string
}

func newManifestText(data []byte) manifestText {
	eol := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		eol = "\r\n"
	}
	lines := strings.SplitAfter(string(data), "\n")
	if last := len(lines) - 1; last >= 0 && lines[last] == "" {
		lines = lines[:last]
	}
	return manifestText{lines: lines, eol: eol}
}

// blockEnd returns the index one past the last content line before boundary, a 1-based line
// number (len(lines)+1 for the end of the file). Blank lines and top-level comments right
// before boundary belong to what follows it.
func (m manifestText) blockEnd(boundary int) int {
	end := min(boundary-1, len(m.lines))
	for scanned := 0; end > 0 && scanned < maxManifestTextLines; scanned++ {
		line := strings.TrimRight(m.lines[end-1], "\r\n")
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "#") {
			break
		}
		end--
	}
	return end
}

// splice replaces lines [from, to) with block, a text of complete lines.
func (m manifestText) splice(from, to int, block string) []byte {
	var out strings.Builder
	for _, line := range m.lines[:from] {
		out.WriteString(line)
	}
	if from > 0 && !strings.HasSuffix(m.lines[from-1], "\n") {
		out.WriteString(m.eol)
	}
	out.WriteString(block)
	for _, line := range m.lines[to:] {
		out.WriteString(line)
	}
	return []byte(out.String())
}

// patchManifestSources writes sources into the manifest text and leaves every other line as
// written. root is the decoded, unmodified root mapping of data. ok is false when the layout
// needs the full re-encode: a flow-style root or register mapping, or a key sharing its line.
func patchManifestSources(data []byte, root *yaml.Node, sources *config.RegisterSources) ([]byte, bool, error) {
	text := newManifestText(data)
	if root.Style&yaml.FlowStyle != 0 || len(text.lines) > maxManifestTextLines {
		return nil, false, nil
	}
	unit := manifestIndentUnit(root)
	registerAt := mappingKeyIndex(root, "register")
	if registerAt < 0 {
		block, err := sourcesBlock(sources, unit, strings.Repeat(" ", unit), text.eol)
		if err != nil {
			return nil, false, err
		}
		return text.splice(len(text.lines), len(text.lines), "register:"+text.eol+block), true, nil
	}
	boundary := nextKeyLine(root, registerAt, len(text.lines)+1)
	return patchRegisterSources(text, root.Content[registerAt:registerAt+2], boundary, sources, unit)
}

// patchRegisterSources inserts sources at the end of the register mapping, or replaces the
// lines of the existing sources value. entry is the register key and its mapping value.
func patchRegisterSources(text manifestText, entry []*yaml.Node, boundary int,
	sources *config.RegisterSources, unit int,
) ([]byte, bool, error) {
	key, register := entry[0], entry[1]
	if register.Style&yaml.FlowStyle != 0 || len(register.Content) == 0 || register.Content[0].Line <= key.Line {
		return nil, false, nil
	}
	block, err := sourcesBlock(sources, unit, strings.Repeat(" ", register.Content[0].Column-1), text.eol)
	if err != nil {
		return nil, false, err
	}
	sourcesAt := mappingKeyIndex(register, "sources")
	if sourcesAt < 0 {
		end := text.blockEnd(boundary)
		return text.splice(end, end, block), true, nil
	}
	from := register.Content[sourcesAt].Line - 1
	end := text.blockEnd(nextKeyLine(register, sourcesAt, boundary))
	if end <= from {
		return nil, false, nil
	}
	return text.splice(from, end, block), true, nil
}

// sourcesBlock renders "sources:" and its value as complete lines at indent.
func sourcesBlock(sources *config.RegisterSources, unit int, indent, eol string) (string, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(unit)
	value := map[string]*config.RegisterSources{"sources": sources}
	if err := errors.Join(encoder.Encode(value), encoder.Close()); err != nil {
		return "", fmt.Errorf("encode register sources: %w", err)
	}
	var out strings.Builder
	for _, line := range strings.SplitAfter(buffer.String(), "\n") {
		if line != "" {
			out.WriteString(indent + strings.TrimSuffix(line, "\n") + eol)
		}
	}
	return out.String(), nil
}

// manifestIndentUnit is the indentation of the first nested block mapping, so a new block
// matches the operator's style; 2 when the manifest has none.
func manifestIndentUnit(root *yaml.Node) int {
	for index := 1; index < len(root.Content) && index < maxAdoptManifestMappingNodes; index += 2 {
		value := root.Content[index]
		if value.Kind == yaml.MappingNode && value.Style&yaml.FlowStyle == 0 &&
			len(value.Content) > 0 && value.Content[0].Column > 1 {
			return value.Content[0].Column - 1
		}
	}
	return 2
}

// mappingKeyIndex is the Content index of key in mapping, or -1.
func mappingKeyIndex(mapping *yaml.Node, key string) int {
	for index := 0; index+1 < len(mapping.Content) && index < maxAdoptManifestMappingNodes; index += 2 {
		if mapping.Content[index].Value == key {
			return index
		}
	}
	return -1
}

// nextKeyLine is the line of the key after keyIndex in mapping, or fallback for the last one.
func nextKeyLine(mapping *yaml.Node, keyIndex, fallback int) int {
	if keyIndex+2 < len(mapping.Content) {
		return mapping.Content[keyIndex+2].Line
	}
	return fallback
}

// sameManifest reports whether patched decodes to exactly the document the full re-encode
// wrote. The text patch is kept only then, so it can change layout, never content.
func sameManifest(ctx context.Context, patched, encoded []byte) bool {
	var left, right any
	patchedDocument, err := decodeAdoptManifestNode(ctx, patched)
	if err != nil || patchedDocument.Decode(&left) != nil {
		return false
	}
	encodedDocument, err := decodeAdoptManifestNode(ctx, encoded)
	if err != nil || encodedDocument.Decode(&right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}
