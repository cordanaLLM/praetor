// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import "gopkg.in/yaml.v3"

// YAMLMappingValue returns the value of key in a YAML mapping node, or nil when node is nil, is
// not a mapping, or does not hold key. The Ansible scanner (internal/hiss) and the workflow
// reader (internal/ghworkflow) look keys up through it, so a mapping is read one way.
func YAMLMappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
