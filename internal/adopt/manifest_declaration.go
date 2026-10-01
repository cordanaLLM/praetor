package adopt

import (
	"context"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"gopkg.in/yaml.v3"
)

// Keys of the two manifest lists `praetorctl profile set` replaces.
const (
	manifestProfilesKey = "profiles"
	manifestFacetsKey   = "facets"
)

// maxDeclarationLists bounds the lists one declaration change rewrites (HISS-02): the profiles
// and the facets.
const maxDeclarationLists = 2

// declarationList is one top-level manifest list and the ids it comes to hold.
type declarationList struct {
	key string
	ids []string
}

// setManifestDeclaration writes each of lists as the value of its top-level key and leaves every
// other line of data as written: comments, blank lines, key order and every other key. The text
// patch (patchManifestLists) is kept only when it decodes to exactly the document the full
// re-encode writes (manifestOutput); any other layout, such as a flow-style root, is re-encoded
// with every node, comment and key order kept.
func setManifestDeclaration(ctx context.Context, data []byte, lists []declarationList) ([]byte, error) {
	patched, patchedOK, err := patchManifestLists(ctx, data, lists)
	if err != nil {
		return nil, err
	}
	document, err := decodeAdoptManifestNode(ctx, data)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(lists) && i < maxDeclarationLists; i++ {
		setYAMLList(document.Content[0], lists[i])
	}
	return manifestOutput(ctx, document, patched, patchedOK)
}

// patchManifestLists applies patchManifestList for each list in turn, each to the text the one
// before it left, so every patch reads line positions from the text it changes.
func patchManifestLists(ctx context.Context, data []byte, lists []declarationList) ([]byte, bool, error) {
	patched := data
	for i := 0; i < len(lists) && i < maxDeclarationLists; i++ {
		next, ok, err := patchManifestList(ctx, patched, lists[i])
		if err != nil || !ok {
			return nil, false, err
		}
		patched = next
	}
	return patched, true, nil
}

// patchManifestList replaces the lines of list.key's root entry, from its key line to the last
// content line before the next key, with list.ids as a block sequence at the manifest's own
// sequence indentation (manifestListIndent), or `key: []` for none. A comment on the key line is
// kept, and so are the comment lines right above the next key. An absent key is appended at the
// end. ok is false for a layout the patch does not address: a flow-style root, or a key that does
// not start its line at the first column.
func patchManifestList(ctx context.Context, data []byte, list declarationList) ([]byte, bool, error) {
	document, err := decodeAdoptManifestNode(ctx, data)
	if err != nil {
		return nil, false, err
	}
	root := document.Content[0]
	text := newManifestText(data)
	if root.Style&yaml.FlowStyle != 0 || len(text.lines) > maxManifestTextLines {
		return nil, false, nil
	}
	indent := manifestListIndent(root)
	at := mappingKeyIndex(root, list.key)
	if at < 0 {
		return text.splice(len(text.lines), len(text.lines), listBlock(list, "", indent, text.eol)), true, nil
	}
	key, value := root.Content[at], root.Content[at+1]
	if key.Column != 1 || key.Line < 1 || key.Line > len(text.lines) {
		return nil, false, nil
	}
	comment := key.LineComment
	if comment == "" && value.Line == key.Line {
		comment = value.LineComment
	}
	end := max(text.blockEnd(nextKeyLine(root, at, len(text.lines)+1)), key.Line)
	return text.splice(key.Line-1, end, listBlock(list, comment, indent, text.eol)), true, nil
}

// listBlock renders list as complete lines: `key:` and one `- id` line per id at indent, or
// `key: []` for none, with comment kept on the key line. Each id is a YAML scalar as the encoder
// writes it, quoted only when it has to be.
func listBlock(list declarationList, comment string, indent int, eol string) string {
	head := list.key + ":"
	if len(list.ids) == 0 {
		head += " []"
	}
	if comment != "" {
		head += " " + comment
	}
	var out strings.Builder
	out.WriteString(head + eol)
	for i := 0; i < len(list.ids) && i < config.MaxManifestEntriesPerKind; i++ {
		out.WriteString(strings.Repeat(" ", indent) + "- " + yamlScalar(list.ids[i]) + eol)
	}
	return out.String()
}

// yamlScalar is id as the YAML encoder writes a one-line string scalar. An id that would not fit
// one line is refused before it reaches here (validateDeclarationIDs), so the fallback quote is
// never the only guard.
func yamlScalar(id string) string {
	encoded, err := yaml.Marshal(id)
	if err != nil || strings.Count(string(encoded), "\n") != 1 {
		return `"` + id + `"`
	}
	return strings.TrimSuffix(string(encoded), "\n")
}

// manifestListIndent is the dash indentation of the first root-level block sequence that holds
// an item, so a rewritten list matches the operator's style; the mapping indentation
// (manifestIndentUnit) when the manifest has none.
func manifestListIndent(root *yaml.Node) int {
	for index := 1; index < len(root.Content) && index < maxAdoptManifestMappingNodes; index += 2 {
		value := root.Content[index]
		if value.Kind == yaml.SequenceNode && value.Style&yaml.FlowStyle == 0 && len(value.Content) > 0 {
			return value.Column - 1
		}
	}
	return manifestIndentUnit(root)
}

// setYAMLList sets list.key in root to a sequence of list.ids, appending the key when absent.
// The key node, and with it its comments, stays in place. An empty list is the flow `[]`; the
// encoder writes a key's line comment before a flow value, which would push `[]` onto a line of
// its own, so that comment moves onto the value, the place it keeps on the key line.
func setYAMLList(root *yaml.Node, list declarationList) {
	sequence := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for i := 0; i < len(list.ids) && i < config.MaxManifestEntriesPerKind; i++ {
		sequence.Content = append(sequence.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: list.ids[i]})
	}
	at := mappingKeyIndex(root, list.key)
	if at < 0 {
		appendAdoptYAMLMapping(root, list.key, sequence)
		return
	}
	if len(sequence.Content) == 0 {
		key := root.Content[at]
		sequence.Style, sequence.LineComment, key.LineComment = yaml.FlowStyle, key.LineComment, ""
	}
	root.Content[at+1] = sequence
}
