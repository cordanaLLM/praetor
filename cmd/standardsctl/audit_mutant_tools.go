// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/strictjson"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

func mutationConfigTool(path string) (string, bool) {
	if path == cargoMutantsConfig {
		return "cargo-mutants", true
	}
	return mutationConfigToolBase(filepath.Base(path))
}

func mutationConfigToolBase(base string) (string, bool) {
	switch base {
	case "pyproject.toml", "setup.cfg":
		return "mutmut", true
	case "stryker.conf.json", "stryker.config.json", "stryker-config.json":
		return "stryker", true
	case "pom.xml":
		return "pit", true
	case ".gremlins.yaml":
		return "gremlins", true
	case "go-mutesting.yaml", ".mutesting.yaml", "mutesting.yaml":
		return "go-mutesting", true
	default:
		return "", false
	}
}

func mutationConfigExclusionCount(root, path, tool string) (int, error) {
	if tool == "cargo-mutants" {
		return cargoMutantsExclusionCount(root, path)
	}
	data, err := readMutantConfig(root, path)
	if err != nil {
		return 0, err
	}
	switch tool {
	case "mutmut":
		return countMutmutExclusions(path, string(data))
	case "stryker":
		return countStrykerExclusions(data)
	case "pit":
		return countPITExclusions(data)
	case "gremlins":
		return countYAMLExclusions(data, "exclude-files")
	case "go-mutesting":
		return countGoMutestingExclusions(data)
	default:
		return 0, fmt.Errorf("unsupported mutation tool %q", tool)
	}
}

func readMutantConfig(root, path string) ([]byte, error) {
	data, err := util.ReadConfinedLimited(root, path, maxMutantsConfigBytes)
	if err != nil {
		return nil, err
	}
	if len(data) > maxMutantsConfigBytes {
		return nil, fmt.Errorf("exceeds %d bytes", maxMutantsConfigBytes)
	}
	return data, nil
}

func countMutmutExclusions(path, content string) (int, error) {
	if filepath.Base(path) == "setup.cfg" {
		return countMutmutINI(content)
	}
	return countMutmutTOML(path, content)
}

func countMutmutTOML(path, content string) (int, error) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > maxMutantsConfigLines {
		return 0, fmt.Errorf("has %d lines, maximum is %d", len(lines), maxMutantsConfigLines)
	}
	section, total := "", 0
	seen := make(map[string]bool, 3)
	for index := 0; index < len(lines) && index < maxMutantsConfigLines; index++ {
		var count int
		var err error
		section, count, err = mutmutTOMLLine(path, lines, &index, section, seen)
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func mutmutTOMLLine(path string, lines []string, index *int, section string, seen map[string]bool) (string, int, error) {
	line := strings.TrimSpace(lines[*index])
	if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
		return strings.Trim(line, "[]"), 0, nil
	}
	key, value, ok := tomlAssignment(line)
	if !ok || !mutmutSection(path, section) || !mutmutExclusionKey(key) {
		return section, 0, nil
	}
	if seen[key] {
		return section, 0, fmt.Errorf("%s repeats %s", path, key)
	}
	seen[key] = true
	items, closed, valid := readStringList(value, lines, index)
	if !valid || !closed {
		return section, 0, fmt.Errorf("%s: %s must be a string or string array", path, key)
	}
	return section, len(items), nil
}

func countMutmutINI(content string) (int, error) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > maxMutantsConfigLines {
		return 0, fmt.Errorf("setup.cfg has %d lines, maximum is %d", len(lines), maxMutantsConfigLines)
	}
	section, key, total := "", "", 0
	for index := 0; index < len(lines) && index < maxMutantsConfigLines; index++ {
		nextSection, nextKey, count, err := mutmutINILine(lines[index], section, key)
		if err != nil {
			return 0, err
		}
		section, key = nextSection, nextKey
		total += count
	}
	return total, nil
}

func mutmutINILine(text, section, key string) (string, string, int, error) {
	line := strings.TrimSpace(text)
	if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
		return strings.Trim(line, "[]"), "", 0, nil
	}
	if section != "mutmut" {
		return section, key, 0, nil
	}
	if name, value, ok := strings.Cut(line, "="); ok {
		key = strings.TrimSpace(name)
		if !mutmutExclusionKey(key) {
			return section, key, 0, nil
		}
		count, err := countCommaValues(value)
		return section, key, count, err
	}
	if mutmutExclusionKey(key) && line != "" && !strings.HasPrefix(line, "#") {
		return section, key, 1, nil
	}
	return section, key, 0, nil
}

func mutmutSection(path, section string) bool {
	if filepath.Base(path) == "pyproject.toml" {
		return section == "tool.mutmut"
	}
	return section == "mutmut"
}

func mutmutExclusionKey(key string) bool {
	return key == "do_not_mutate" || key == "do_not_mutate_patterns" || key == "only_mutate"
}

func tomlAssignment(line string) (string, string, bool) {
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	key, value, ok := strings.Cut(line, "=")
	return strings.TrimSpace(key), strings.TrimSpace(value), ok && strings.TrimSpace(key) != ""
}

func readStringList(value string, lines []string, index *int) ([]string, bool, bool) {
	items, closed, ok := readTOMLStrings(value)
	for !closed && ok && *index+1 < len(lines) && *index+1 < maxMutantsConfigLines {
		*index++
		more, end, valid := readTOMLStrings("[" + strings.TrimSpace(lines[*index]))
		items = append(items, more...)
		closed, ok = end, valid
	}
	return items, closed, ok
}

func readTOMLStrings(text string) ([]string, bool, bool) {
	return util.TOMLStringArray(text)
}

func countStrykerExclusions(data []byte) (int, error) {
	var root map[string]json.RawMessage
	if err := strictjson.Decode(data, &root, strictjson.Options{
		MaxBytes: maxMutantsConfigBytes, MaxDepth: 64, MaxTokens: 100000, Names: strictjson.ExactNames,
	}); err != nil {
		return 0, fmt.Errorf("decode Stryker JSON config: %w", err)
	}
	mutate, err := jsonStrings(root["mutate"])
	if err != nil {
		return 0, fmt.Errorf("stryker mutate: %w", err)
	}
	total := countNegatedPatterns(mutate)
	for _, key := range []string{"ignorePatterns"} {
		items, err := jsonStrings(root[key])
		if err != nil {
			return 0, fmt.Errorf("stryker %s: %w", key, err)
		}
		total += countNegatedPatterns(items)
	}
	var mutator struct {
		Excluded []string `json:"excludedMutations"`
	}
	if raw, exists := root["mutator"]; exists {
		if err := json.Unmarshal(raw, &mutator); err != nil {
			return 0, fmt.Errorf("decode Stryker mutator config: %w", err)
		}
		total += len(mutator.Excluded)
	}
	return total, nil
}

func jsonStrings(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func countNegatedPatterns(patterns []string) int {
	total := 0
	for index := 0; index < len(patterns) && index < maxMutantsConfigLines; index++ {
		if strings.HasPrefix(patterns[index], "!") {
			total++
		} else if patterns[index] != "" {
			total++
		}
	}
	return total
}

func countPITExclusions(data []byte) (int, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	total, tokens := 0, 0
	for tokens < maxMutantsConfigLines*10 {
		token, err := decoder.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return total, nil
			}
			return 0, fmt.Errorf("decode PIT Maven config: %w", err)
		}
		tokens++
		start, ok := token.(xml.StartElement)
		if ok && pitExclusionElement(start.Name.Local) {
			var values pitExclusionValues
			if err := decoder.DecodeElement(&values, &start); err != nil {
				return 0, fmt.Errorf("read PIT %s: %w", start.Name.Local, err)
			}
			count, err := countCommaValues(values.Text)
			if err != nil {
				return 0, err
			}
			total += len(values.Params) + len(values.Avoid) + count
		}
	}
	return 0, fmt.Errorf("PIT Maven config exceeds %d XML tokens", maxMutantsConfigLines*10)
}

type pitExclusionValues struct {
	Params []string `xml:"param"`
	Avoid  []string `xml:"avoid"`
	Text   string   `xml:",chardata"`
}

func pitExclusionElement(name string) bool {
	return name == "excludedClasses" || name == "excludedMethods" || name == "avoidCallsTo"
}

func countCommaValues(value string) (int, error) {
	count := 0
	items := strings.Split(value, ",")
	if len(items) > maxMutantsConfigLines {
		return 0, fmt.Errorf("mutation exclusion list exceeds %d values", maxMutantsConfigLines)
	}
	for index := 0; index < len(items) && index < maxMutantsConfigLines; index++ {
		if strings.TrimSpace(items[index]) != "" {
			count++
		}
	}
	return count, nil
}

func countYAMLExclusions(data []byte, key string) (int, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return 0, fmt.Errorf("decode mutation-tool YAML config: %w", err)
	}
	return countYAMLKey(&root, key)
}

func countGoMutestingExclusions(data []byte) (int, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return 0, fmt.Errorf("decode go-mutesting YAML config: %w", err)
	}
	total := 0
	for _, key := range []string{"exclude_dirs", "disable_mutators", "enable_mutators", "ignore_source_lines"} {
		count, err := countYAMLKey(&root, key)
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func countYAMLKey(node *yaml.Node, key string) (int, error) {
	stack := []*yaml.Node{node}
	steps := 0
	total := 0
	for len(stack) > 0 && len(stack) <= maxMutantsConfigLines && steps < maxMutantsConfigLines {
		steps++
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		count, children, err := inspectYAMLNode(current, key)
		if err != nil {
			return 0, err
		}
		total += count
		if len(stack)+len(children) > maxMutantsConfigLines {
			return 0, fmt.Errorf("YAML mutation config exceeds %d pending nodes", maxMutantsConfigLines)
		}
		stack = append(stack, children...)
	}
	if len(stack) > 0 || steps >= maxMutantsConfigLines {
		return 0, fmt.Errorf("YAML mutation config exceeds %d nodes", maxMutantsConfigLines)
	}
	return total, nil
}

func inspectYAMLNode(node *yaml.Node, key string) (int, []*yaml.Node, error) {
	if len(node.Content) > maxMutantsConfigLines {
		return 0, nil, fmt.Errorf("YAML mutation config node exceeds %d children", maxMutantsConfigLines)
	}
	total := 0
	if node.Kind == yaml.MappingNode {
		count, err := inspectYAMLMapping(node, key)
		if err != nil {
			return 0, nil, err
		}
		total = count
	}
	return total, node.Content, nil
}

func inspectYAMLMapping(node *yaml.Node, key string) (int, error) {
	if len(node.Content) > maxMutantsConfigLines*2 {
		return 0, fmt.Errorf("YAML mutation config mapping exceeds %d entries", maxMutantsConfigLines)
	}
	seen := make(map[string]bool, len(node.Content)/2)
	total := 0
	for index := 0; index+1 < len(node.Content) && index < maxMutantsConfigLines*2; index += 2 {
		field := node.Content[index].Value
		if seen[field] {
			return 0, fmt.Errorf("YAML mutation config repeats key %q", field)
		}
		seen[field] = true
		if field == key {
			count, err := yamlValueCount(node.Content[index+1])
			if err != nil {
				return 0, err
			}
			total += count
		}
	}
	return total, nil
}

func yamlValueCount(node *yaml.Node) (int, error) {
	if node.Kind != yaml.SequenceNode {
		return 0, fmt.Errorf("mutation exclusion setting must be a YAML list")
	}
	if len(node.Content) > maxMutantsConfigLines {
		return 0, fmt.Errorf("YAML mutation exclusion list exceeds %d entries", maxMutantsConfigLines)
	}
	return len(node.Content), nil
}
