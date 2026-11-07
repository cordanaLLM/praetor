package adopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
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
// number (len(lines)+1 for the end of the file). Blank lines and comments at any indentation
// right before boundary belong to what follows it, so a replaced block never takes them.
func (m manifestText) blockEnd(boundary int) int {
	end := min(boundary-1, len(m.lines))
	for scanned := 0; end > 0 && scanned < maxManifestTextLines; scanned++ {
		line := strings.TrimSpace(m.lines[end-1])
		if line != "" && !strings.HasPrefix(line, "#") {
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
	if nullYAMLNode(root.Content[registerAt+1]) {
		return patchNullRegister(text, root.Content[registerAt:registerAt+2], sources, unit)
	}
	boundary := nextKeyLine(root, registerAt, len(text.lines)+1)
	return patchRegisterSources(text, root.Content[registerAt:registerAt+2], boundary, sources, unit)
}

// patchNullRegister turns a null register value (`register:`, `register: ~`) into a block
// mapping holding sources, inserted right below the key line. The key keeps its line comment,
// and commented-out children below it stay as written. entry is the register key and value.
func patchNullRegister(text manifestText, entry []*yaml.Node, sources *config.RegisterSources, unit int,
) ([]byte, bool, error) {
	key, value := entry[0], entry[1]
	at := key.Line - 1
	if value.Line != key.Line || at < 0 || at >= len(text.lines) {
		return nil, false, nil
	}
	keyLine, ok := nullRegisterKeyLine(strings.TrimRight(text.lines[at], "\r\n"), value)
	if !ok {
		return nil, false, nil
	}
	block, err := sourcesBlock(sources, unit, strings.Repeat(" ", key.Column-1+unit), text.eol)
	if err != nil {
		return nil, false, err
	}
	return text.splice(at, at+1, keyLine+text.eol+block), true, nil
}

// nullRegisterKeyLine drops an explicit null token (~, null) from the register key line and
// keeps its comment. ok is false when the token is not where the decoder placed it, and for
// an explicit tag (`register: !!null`): kept on the key line, the tag would type the inserted
// mapping as null, so the re-encode handles that layout.
func nullRegisterKeyLine(line string, value *yaml.Node) (string, bool) {
	if value.Style&yaml.TaggedStyle != 0 {
		return "", false
	}
	if value.Value == "" {
		return line, true
	}
	start := value.Column - 1
	if start < 0 || start > len(line) || !strings.HasPrefix(line[start:], value.Value) {
		return "", false
	}
	line = strings.TrimRight(line[:start], " \t")
	if value.LineComment != "" {
		line += " " + value.LineComment
	}
	return line, true
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
			out.WriteString(indent + line)
		}
	}
	// The block is Praetor's text, so its long digest is fitted the way a new manifest's is.
	fitted, err := util.FitYAMLLines([]byte(out.String()), unit)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(fitted), "\n", eol), nil
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

// patchYAMLPath extends the shared manifest text editor to a bounded mapping path. The caller
// keeps the patch only after manifestOutput proves it equals the edited node tree.
func patchYAMLPath(data []byte, root *yaml.Node, keys []string, value *yaml.Node) ([]byte, bool, error) {
	text := newManifestText(data)
	if len(keys) > 4 || len(text.lines) > maxManifestTextLines {
		return nil, false, nil
	}
	unit, boundary := manifestIndentUnit(root), len(text.lines)+1
	mapping := root
	for depth := 0; depth < len(keys) && depth < 4; depth++ {
		if mapping.Kind != yaml.MappingNode || mapping.Style&yaml.FlowStyle != 0 {
			return nil, false, nil
		}
		at := mappingKeyIndex(mapping, keys[depth])
		if at < 0 {
			return insertYAMLPath(text, mapping, keys[depth:], value, boundary, depth*unit)
		}
		key, old := mapping.Content[at], mapping.Content[at+1]
		boundary = nextKeyLine(mapping, at, boundary)
		if depth == len(keys)-1 {
			patched, ok := patchYAMLLeaf(text, key, old, value, boundary)
			return []byte(patched), ok, nil
		}
		if nullYAMLNode(old) {
			block, err := yamlPathBlock(keys[depth+1:], value, key.Column-1+unit, text.eol)
			patched, ok := replaceNullValueLine(text, key, old, block)
			return []byte(patched), ok && err == nil, err
		}
		mapping = old
	}
	return nil, false, nil
}

// insertYAMLPath appends a missing mapping path before the enclosing mapping's boundary.
func insertYAMLPath(text manifestText, mapping *yaml.Node, keys []string, value *yaml.Node, boundary, indent int) ([]byte, bool, error) {
	if len(mapping.Content) > 0 {
		indent = mapping.Content[0].Column - 1
	}
	block, err := yamlPathBlock(keys, value, indent, text.eol)
	if err != nil {
		return nil, false, err
	}
	end := text.blockEnd(boundary)
	return text.splice(end, end, block), true, nil
}

// yamlPathBlock renders a bounded chain of mapping keys at indent using the shared encoder.
func yamlPathBlock(keys []string, value *yaml.Node, indent int, eol string) (string, error) {
	for i := len(keys) - 1; i >= 0 && i < 4; i-- {
		parent := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		appendAdoptYAMLMapping(parent, keys[i], value)
		value = parent
	}
	encoded, err := util.EncodeYAMLDocument(value)
	if err != nil {
		return "", fmt.Errorf("encode managed YAML path: %w", err)
	}
	var block strings.Builder
	lines := strings.Split(strings.TrimSuffix(strings.TrimPrefix(string(encoded), util.YAMLDocumentStart), "\n"), "\n")
	for i := 0; i < len(lines) && i < maxManifestTextLines; i++ {
		block.WriteString(strings.Repeat(" ", indent) + lines[i] + eol)
	}
	return block.String(), nil
}

// patchYAMLLeaf edits a plain scalar token or appends one string to an existing sequence.
// Other layouts use the shared node encoder, preserving comments and unrelated values.
func patchYAMLLeaf(text manifestText, key, old, value *yaml.Node, boundary int) (string, bool) {
	if old.Kind == yaml.SequenceNode && value.Kind == yaml.SequenceNode && len(value.Content) == len(old.Content)+1 {
		item := util.YAMLScalar(value.Content[len(old.Content)].Value)
		if old.Style&yaml.FlowStyle != 0 {
			return extendFlowYAMLSequence(text, old, []string{item})
		}
		end := text.blockEnd(boundary)
		block := strings.Repeat(" ", old.Column-1) + "- " + item + text.eol
		return string(text.splice(end, end, block)), true
	}
	return patchYAMLScalar(text, key, old, value)
}

// patchYAMLScalar replaces a plain scalar token while retaining its line's spacing and comment.
func patchYAMLScalar(text manifestText, key, old, value *yaml.Node) (string, bool) {
	at, column := old.Line-1, old.Column-1
	if old.Kind != yaml.ScalarNode || value.Kind != yaml.ScalarNode || old.Style != 0 ||
		old.Line != key.Line || at < 0 || at >= len(text.lines) || column < 0 {
		return "", false
	}
	line := text.lines[at]
	if column > len(line) || !strings.HasPrefix(line[column:], old.Value) {
		return "", false
	}
	patched := line[:column] + value.Value + line[column+len(old.Value):]
	return string(text.splice(at, at+1, patched)), true
}
