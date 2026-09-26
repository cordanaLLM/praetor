package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"gopkg.in/yaml.v3"
)

const (
	maxPolicyNodes = 65536
	maxPolicyDepth = 64
)

func decodePolicyDocument(ctx context.Context, data []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode policy: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("policy requires exactly one document")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("policy must be a mapping")
	}
	if err := validatePolicyNodes(ctx, &document); err != nil {
		return nil, err
	}
	return document.Content[0], nil
}

type policyNode struct {
	node  *yaml.Node
	depth int
}

func validatePolicyNodes(ctx context.Context, root *yaml.Node) error {
	queue := []policyNode{{root, 0}}
	for i := 0; i < len(queue) && i < maxPolicyNodes; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := queue[i]
		if err := validatePolicyNode(entry); err != nil {
			return err
		}
		if len(queue)+len(entry.node.Content) > maxPolicyNodes {
			return errors.New("policy exceeds node bound")
		}
		for _, child := range entry.node.Content {
			queue = append(queue, policyNode{child, entry.depth + 1})
		}
	}
	return nil
}

func validatePolicyNode(entry policyNode) error {
	node := entry.node
	if entry.depth > maxPolicyDepth || node.Kind == yaml.AliasNode || node.Anchor != "" {
		return errors.New("policy exceeds depth bound or uses unsupported YAML anchors/aliases")
	}
	if node.Kind != yaml.MappingNode {
		return nil
	}
	seen := make(map[string]bool, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content) && i < maxPolicyNodes; i += 2 {
		key := node.Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || seen[key.Value] {
			return errors.New("policy mapping keys must be unique strings; merge keys are unsupported")
		}
		seen[key.Value] = true
	}
	return nil
}

func policyMember(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content) && i < maxPolicyNodes; i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// requireKnownKeys rejects a mapping key outside allowed. It serves sections whose custom
// unmarshaler decodes through yaml.Node.Decode, which drops the caller's KnownFields.
func requireKnownKeys(node *yaml.Node, section string, allowed []string) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content) && i < maxPolicyNodes; i += 2 {
		if key := node.Content[i].Value; !slices.Contains(allowed, key) {
			return fmt.Errorf("%s: unknown key %q", section, key)
		}
	}
	return nil
}

// Only the complexity subsection is owned here. Unrelated existing manifest and
// fleet settings remain valid; misspelled complexity keys do not.
func decodeComplexity(node *yaml.Node) (ComplexityOverride, error) {
	return decodeComplexityLimits(node, false)
}

// decodeComplexityLimits decodes a complexity mapping. zeroUnbounded lets a catalog
// archetype state "no bound" with 0, as upstream-fork does: that limit then contributes
// nothing, exactly like an omitted one. Everywhere else an explicit zero is an error.
func decodeComplexityLimits(node *yaml.Node, zeroUnbounded bool) (ComplexityOverride, error) {
	var result ComplexityOverride
	if node == nil {
		return result, nil
	}
	if node.Kind != yaml.MappingNode {
		return result, errors.New("complexity must be a mapping")
	}
	fields := map[string]**int{
		"max_cyclomatic": &result.MaxCyclomatic, "max_cognitive": &result.MaxCognitive,
		"max_func_loc": &result.MaxFuncLOC, "max_statements": &result.MaxStatements,
	}
	for i := 0; i+1 < len(node.Content) && i < maxPolicyNodes; i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		field, ok := fields[key]
		if !ok {
			return ComplexityOverride{}, fmt.Errorf("complexity %q must be a known integer limit", key)
		}
		number, err := complexityLimit(key, value, zeroUnbounded)
		if err != nil {
			return ComplexityOverride{}, err
		}
		if number > 0 {
			*field = &number
		}
	}
	return result, nil
}

// complexityLimit decodes one integer limit; zero passes only when zeroUnbounded.
func complexityLimit(key string, value *yaml.Node, zeroUnbounded bool) (int, error) {
	if value.Kind != yaml.ScalarNode || value.Tag != "!!int" {
		return 0, fmt.Errorf("complexity %q must be a known integer limit", key)
	}
	var number int
	if err := value.Decode(&number); err != nil || number < 0 || (number == 0 && !zeroUnbounded) {
		return 0, fmt.Errorf("complexity %q must be a positive integer", key)
	}
	return number, nil
}
