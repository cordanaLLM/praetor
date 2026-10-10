package schemacheck

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"

	"gopkg.in/yaml.v3"
)

// maxYAMLNodes bounds the nodes one conversion walks (HISS-02).
const maxYAMLNodes = 1 << 20

// ReadYAML reads one YAML document as YAML 1.2 would and returns a JSON-compatible value
// (map[string]any, []any, string, float64, bool, nil) that Schema.ValidateValue accepts.
//
// The plain scalar on is a string, not the boolean a YAML 1.1 loader makes of it, so the
// workflow key "on:" stays a key. Only true and false (in the three 1.2 spellings) are booleans.
// ReadYAML refuses a document whose mapping keys are not scalars, a repeated key, several
// documents and a document over the node bound.
func ReadYAML(raw []byte) (any, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var node yaml.Node
	if err := decoder.Decode(&node); err != nil {
		return nil, fmt.Errorf("read YAML: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("read YAML: expected exactly one document")
	}
	return convert(&node)
}

type frame struct {
	node *yaml.Node
	set  func(any)
}

// convert turns a yaml node tree into plain values with an explicit stack (HISS-01).
func convert(root *yaml.Node) (any, error) {
	var result any
	stack := []frame{{node: root, set: func(v any) { result = v }}}
	for budget := maxYAMLNodes; len(stack) > 0; budget-- {
		if budget == 0 {
			return nil, errors.New("read YAML: document exceeds the node bound")
		}
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		pushed, err := step(top)
		if err != nil {
			return nil, err
		}
		stack = append(stack, pushed...)
	}
	return result, nil
}

// step settles one node and returns the child frames still to settle.
func step(top frame) ([]frame, error) {
	node := top.node
	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) != 1 {
			return nil, errors.New("read YAML: empty document")
		}
		return []frame{{node: node.Content[0], set: top.set}}, nil
	case yaml.AliasNode:
		return []frame{{node: node.Alias, set: top.set}}, nil
	case yaml.SequenceNode:
		items := make([]any, len(node.Content))
		top.set(items)
		children := make([]frame, 0, len(items))
		for i, child := range node.Content {
			children = append(children, frame{node: child, set: func(v any) { items[i] = v }})
		}
		return children, nil
	case yaml.MappingNode:
		return mapping(top)
	case yaml.ScalarNode:
		top.set(scalar(node))
		return nil, nil
	default:
		return nil, fmt.Errorf("read YAML: unsupported node kind %d", node.Kind)
	}
}

func mapping(top frame) ([]frame, error) {
	node := top.node
	object := make(map[string]any, len(node.Content)/2)
	top.set(object)
	children := make([]frame, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Kind != yaml.ScalarNode {
			return nil, errors.New("read YAML: a mapping key is not a scalar")
		}
		name := key.Value
		if _, dup := object[name]; dup {
			return nil, fmt.Errorf("read YAML: duplicate key %q", name)
		}
		object[name] = nil
		children = append(children, frame{node: node.Content[i+1], set: func(v any) { object[name] = v }})
	}
	return children, nil
}

// scalar applies the YAML 1.2 core schema: null, true/false, numbers; everything else is a
// string, including on, off, yes, no and y.
func scalar(node *yaml.Node) any {
	quoted := yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle | yaml.LiteralStyle | yaml.FoldedStyle
	if node.Style&quoted != 0 {
		return node.Value
	}
	switch node.Value {
	case "", "~", "null", "Null", "NULL":
		return nil
	case "true", "True", "TRUE":
		return true
	case "false", "False", "FALSE":
		return false
	}
	if number, err := strconv.ParseFloat(node.Value, 64); err == nil && numeric(node.Value) {
		return number
	}
	return node.Value
}

// numeric admits the 1.2 core decimal number forms only: no underscores, hex or octal prefixes,
// and none of the words strconv reads as numbers (inf, nan).
func numeric(text string) bool {
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c >= '0' && c <= '9', c == '.', c == '-', c == '+', c == 'e', c == 'E':
		default:
			return false
		}
	}
	return text != ""
}
