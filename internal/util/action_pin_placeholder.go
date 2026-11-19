// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package util

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// validateActionPlaceholders checks the sole YAML reader's nodes after key/uses tag validation.
func validateActionPlaceholders(root *yaml.Node, placeholders []string, text string) error {
	if len(placeholders) == 0 {
		return nil
	}
	if len(placeholders) != 1 || placeholders[0] == "" {
		return fmt.Errorf("unsupported uses placeholder: expected one non-empty reserved scalar")
	}
	remaining := strings.Count(text, placeholders[0])
	allowed := make(map[*yaml.Node]bool)
	stack := []*yaml.Node{root}
	for visited := 0; len(stack) > 0 && visited < maxActionYAMLNodes; visited++ {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if strings.Contains(node.Value, placeholders[0]) && !allowed[node] {
			return fmt.Errorf("line %d: unsupported uses template placeholder: only non-uses scalar mapping values allowed", node.Line)
		}
		remaining -= strings.Count(node.Value, placeholders[0])
		allowTemplateValues(node, allowed)
		stack = append(stack, node.Content...)
	}
	if len(stack) != 0 {
		return fmt.Errorf("unsupported uses placeholder: exceeds %d YAML nodes", maxActionYAMLNodes)
	}
	if remaining != 0 {
		return fmt.Errorf("unsupported uses template placeholder: not preserved in a permitted scalar value")
	}
	return nil
}

// allowTemplateValues classifies scalar values under previously validated non-uses keys.
func allowTemplateValues(node *yaml.Node, allowed map[*yaml.Node]bool) {
	if node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content) && i < maxActionYAMLNodes; i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		allowed[value] = key.Kind == yaml.ScalarNode && key.Value != "uses" && value.Kind == yaml.ScalarNode
	}
}
