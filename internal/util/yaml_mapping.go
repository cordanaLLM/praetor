// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// YAMLScalar is value as the YAML encoder writes a one-line string scalar: plain where the
// encoder keeps it plain, quoted where a plain scalar would read as something else. A value the
// encoder would fold over several lines is written double-quoted instead, with Go's escapes,
// each of which YAML's double-quoted style reads the same way for valid UTF-8. Adoption's
// manifest list patch and the coverage title sync (internal/hisscoverage) write a value into one
// existing line through it.
func YAMLScalar(value string) string {
	encoded, err := yaml.Marshal(value)
	if err != nil || strings.Count(string(encoded), "\n") != 1 {
		return strconv.Quote(value)
	}
	return strings.TrimSuffix(string(encoded), "\n")
}

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
