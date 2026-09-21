package cavemansource

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/notebook"
	"gopkg.in/yaml.v3"
)

const maxStructuredMatches = config.MaxRegisterSourceOutputs

type nodeMatch struct {
	node *yaml.Node
	path string
}

func extractStructured(item discoveredInput, data []byte) ([]Source, error) {
	if item.input.Format == config.SourceFormatJSON {
		var value any
		if err := notebook.Decode(data, &value); err != nil {
			return nil, fmt.Errorf("caveman source %s: %w", item.path, err)
		}
	}
	root, err := decodeYAMLNode(data)
	if err != nil {
		return nil, fmt.Errorf("caveman source %s: %w", item.path, err)
	}
	matches, err := selectNodes(root, strings.Split(item.input.Selector, "."))
	if err != nil {
		return nil, fmt.Errorf("caveman source %s selector %s: %w", item.path, item.input.Selector, err)
	}
	sources := make([]Source, 0, len(matches))
	for index := range matches {
		node := matches[index].node
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
			return nil, fmt.Errorf("caveman source %s selector %s matched non-string %s", item.path, item.input.Selector, matches[index].path)
		}
		sources = append(sources, sourceFrom(item, node.Value, matches[index].path, node.Line))
	}
	return sources, nil
}

func decodeYAMLNode(data []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode structured source: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("structured source requires exactly one document")
	}
	if len(document.Content) != 1 {
		return nil, errors.New("structured source requires one root value")
	}
	return document.Content[0], nil
}

func selectNodes(root *yaml.Node, segments []string) ([]nodeMatch, error) {
	current := []nodeMatch{{node: root, path: "$"}}
	for _, segment := range segments {
		next := make([]nodeMatch, 0, len(current))
		for index := range current {
			matches, err := selectNodeSegment(current[index], segment)
			if err != nil {
				return nil, err
			}
			next = append(next, matches...)
			if len(next) > maxStructuredMatches {
				return nil, fmt.Errorf("selector exceeds %d matches", maxStructuredMatches)
			}
		}
		if len(next) == 0 {
			return nil, fmt.Errorf("segment %q matched zero values", segment)
		}
		current = next
	}
	return current, nil
}

func selectNodeSegment(match nodeMatch, segment string) ([]nodeMatch, error) {
	if match.node.Kind == yaml.AliasNode {
		return nil, errors.New("YAML aliases are unsupported in source selectors")
	}
	switch match.node.Kind {
	case yaml.MappingNode:
		return selectMapping(match, segment), nil
	case yaml.SequenceNode:
		return selectSequence(match, segment)
	default:
		return nil, fmt.Errorf("cannot descend through scalar at %s", match.path)
	}
}

func selectMapping(match nodeMatch, segment string) []nodeMatch {
	selected := make([]nodeMatch, 0, len(match.node.Content)/2)
	for index := 0; index+1 < len(match.node.Content); index += 2 {
		key, value := match.node.Content[index], match.node.Content[index+1]
		if key.Kind == yaml.ScalarNode && (segment == "*" || key.Value == segment) {
			selected = append(selected, nodeMatch{node: value, path: match.path + "." + key.Value})
		}
	}
	return selected
}

func selectSequence(match nodeMatch, segment string) ([]nodeMatch, error) {
	if segment == "*" {
		selected := make([]nodeMatch, 0, len(match.node.Content))
		for index := range match.node.Content {
			selected = append(selected, nodeMatch{node: match.node.Content[index], path: fmt.Sprintf("%s.%d", match.path, index)})
		}
		return selected, nil
	}
	index, numeric := sequenceIndex(segment)
	if !numeric || index < 0 || index >= len(match.node.Content) {
		return nil, nil
	}
	return []nodeMatch{{node: match.node.Content[index], path: fmt.Sprintf("%s.%d", match.path, index)}}, nil
}

func sequenceIndex(segment string) (int, bool) {
	index, err := strconv.Atoi(segment)
	return index, err == nil
}
