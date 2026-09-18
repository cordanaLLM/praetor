// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

// OperatorSetting is one decoded key of a clients, hooks or update section. Path is the
// dotted key, for example clients.selected.agy.required. A scalar carries its canonical
// text in Value, a sequence of strings carries List, and hooks.python carries Argv.
type OperatorSetting struct {
	Path  string
	Value string
	List  []string
	Argv  [][]string
}

var operatorSectionNames = [...]string{"clients", "hooks", "update"}

type sectionEntry struct {
	node *yaml.Node
	path string
}

// decodeOperatorSections returns the settings of a document's clients, hooks and update
// sections, and whether it carries any of them. Root keys outside the owned sections stay
// tolerated; keys inside an owned section are strict.
func decodeOperatorSections(root *yaml.Node) ([]OperatorSetting, bool, error) {
	queue := make([]sectionEntry, 0, len(operatorSectionNames))
	for _, name := range operatorSectionNames {
		if node := policyMember(root, name); node != nil {
			queue = append(queue, sectionEntry{node, name})
		}
	}
	owned := len(queue) > 0
	settings := make([]OperatorSetting, 0, 16)
	for i := 0; i < len(queue) && i < maxPolicyNodes; i++ {
		children, decoded, err := decodeSectionMapping(queue[i])
		if err != nil {
			return nil, owned, err
		}
		queue = append(queue, children...)
		settings = append(settings, decoded...)
		if len(settings) > maxOperatorSettings {
			return nil, owned, fmt.Errorf("operator sections exceed %d settings", maxOperatorSettings)
		}
	}
	return settings, owned, nil
}

// decodeSectionMapping decodes one mapping: nested mappings are returned for the caller's
// queue, so the walk is iterative and bounded rather than recursive.
func decodeSectionMapping(entry sectionEntry) ([]sectionEntry, []OperatorSetting, error) {
	if entry.node.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%s must be a mapping", entry.path)
	}
	var children []sectionEntry
	var settings []OperatorSetting
	for i := 0; i+1 < len(entry.node.Content) && i < maxPolicyNodes; i += 2 {
		key, value := entry.node.Content[i].Value, entry.node.Content[i+1]
		if !settingKey.MatchString(key) {
			return nil, nil, fmt.Errorf("%s: key %q must be 1..64 lowercase letters, digits, '_' or '-'", entry.path, key)
		}
		path := entry.path + "." + key
		spec, err := settingSpecFor(path)
		if err != nil {
			return nil, nil, err
		}
		switch spec.kind {
		case kindMapping:
			children = append(children, sectionEntry{value, path})
		case kindEntry:
			children = append(children, sectionEntry{value, path})
			settings = append(settings, OperatorSetting{Path: path})
		default:
			setting, err := decodeSettingValue(value, path, spec)
			if err != nil {
				return nil, nil, err
			}
			settings = append(settings, setting)
		}
	}
	return children, settings, nil
}

func decodeSettingValue(node *yaml.Node, path string, spec settingSpec) (OperatorSetting, error) {
	setting := OperatorSetting{Path: path}
	var err error
	switch spec.kind {
	case kindBool:
		setting.Value, err = boolScalar(node, path)
	case kindList:
		setting.List, err = stringSequence(node, path, spec.maxItems)
	case kindArgv:
		setting.Argv, err = argvSequence(node, path)
	default:
		setting.Value, err = stringScalar(node, path)
	}
	return setting, err
}

func stringScalar(node *yaml.Node, path string) (string, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", fmt.Errorf("%s must be a string", path)
	}
	return node.Value, nil
}

func boolScalar(node *yaml.Node, path string) (string, error) {
	var value bool
	if node.Kind != yaml.ScalarNode || node.Tag != "!!bool" || node.Decode(&value) != nil {
		return "", fmt.Errorf("%s must be true or false", path)
	}
	return strconv.FormatBool(value), nil
}

func stringSequence(node *yaml.Node, path string, limit int) ([]string, error) {
	if node.Kind != yaml.SequenceNode || len(node.Content) > limit {
		return nil, fmt.Errorf("%s must be a list of at most %d strings", path, limit)
	}
	items := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		text, err := stringScalar(item, path+" entry")
		if err != nil {
			return nil, err
		}
		items = append(items, text)
	}
	return items, nil
}

// argvSequence decodes interpreter candidates: a string is a command without arguments,
// a list is a command followed by its arguments.
func argvSequence(node *yaml.Node, path string) ([][]string, error) {
	if node.Kind != yaml.SequenceNode || len(node.Content) > maxPythonCandidates {
		return nil, fmt.Errorf("%s must be a list of at most %d candidates", path, maxPythonCandidates)
	}
	candidates := make([][]string, 0, len(node.Content))
	for _, item := range node.Content {
		if item.Kind == yaml.SequenceNode {
			argv, err := stringSequence(item, path+" candidate", 1+maxPythonArgs)
			if err != nil {
				return nil, err
			}
			candidates = append(candidates, argv)
			continue
		}
		command, err := stringScalar(item, path+" candidate")
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, []string{command})
	}
	return candidates, nil
}
