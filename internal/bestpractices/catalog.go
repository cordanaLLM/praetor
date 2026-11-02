// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package bestpractices

import (
	"fmt"
	"sync"

	"go.yaml.in/yaml/v3"
)

const (
	embeddedCriteriaPath         = "upstream/criteria.yml"
	embeddedBaselineCriteriaPath = "upstream/baseline_criteria.yml"

	maxLevels           = 16
	maxMajorGroups      = 32
	maxMinorGroups      = 64
	maxCriteriaPerGroup = 256
)

type keyedNode struct {
	key   string
	value *yaml.Node
}

var loadCatalogOnce = sync.OnceValues(parseEmbeddedCatalog)

// LoadCatalog returns the parsed OpenSSF Best Practices and Baseline criteria catalog.
func LoadCatalog() (*Catalog, error) {
	return loadCatalogOnce()
}

// parseEmbeddedCatalog loads and parses criteria from embedded criteria and baseline YAML files.
func parseEmbeddedCatalog() (*Catalog, error) {
	metalRaw, err := upstreamFS.ReadFile(embeddedCriteriaPath)
	if err != nil {
		return nil, fmt.Errorf("read embedded %s: %w", embeddedCriteriaPath, err)
	}
	baselineRaw, err := upstreamFS.ReadFile(embeddedBaselineCriteriaPath)
	if err != nil {
		return nil, fmt.Errorf("read embedded %s: %w", embeddedBaselineCriteriaPath, err)
	}
	cat := &Catalog{
		Criteria: make(map[string]Criterion, 256),
		ByLevel:  make(map[string]map[string]Criterion, 8),
	}
	if err := parseCriteriaYAML(cat, embeddedCriteriaPath, metalRaw); err != nil {
		return nil, err
	}
	if err := parseCriteriaYAML(cat, embeddedBaselineCriteriaPath, baselineRaw); err != nil {
		return nil, err
	}
	return cat, nil
}

// parseCriteriaYAML decodes a criteria YAML document and populates the catalog criteria and metadata.
func parseCriteriaYAML(cat *Catalog, source string, raw []byte) error {
	root, err := decodeRootNode(source, raw)
	if err != nil {
		return err
	}
	levels, err := nodeEntries(root, maxLevels, source)
	if err != nil {
		return err
	}
	for i := 0; i < len(levels) && i < maxLevels; i++ {
		entry := levels[i]
		if entry.key == "_metadata" {
			if err := entry.value.Decode(&cat.Metadata); err != nil {
				return fmt.Errorf("decode %s _metadata: %w", source, err)
			}
			continue
		}
		if err := parseLevelNode(cat, entry.key, entry.value); err != nil {
			return fmt.Errorf("%s level %q: %w", source, entry.key, err)
		}
	}
	return nil
}

// decodeRootNode unmarshals raw YAML bytes into a root node and validates single document structure.
func decodeRootNode(source string, raw []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", source, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, fmt.Errorf("parse %s: expected single YAML document", source)
	}
	return doc.Content[0], nil
}

// parseLevelNode traverses major groups within a specific criteria level node.
func parseLevelNode(cat *Catalog, level string, levelNode *yaml.Node) error {
	if cat.ByLevel[level] == nil {
		cat.ByLevel[level] = make(map[string]Criterion, 64)
	}
	majors, err := nodeEntries(levelNode, maxMajorGroups, "level "+level)
	if err != nil {
		return err
	}
	for i := 0; i < len(majors) && i < maxMajorGroups; i++ {
		major := majors[i]
		if err := parseMajorGroup(cat, level, major.key, major.value); err != nil {
			return err
		}
	}
	return nil
}

// parseMajorGroup traverses minor groups within a criteria major group node.
func parseMajorGroup(cat *Catalog, level, major string, majorNode *yaml.Node) error {
	minors, err := nodeEntries(majorNode, maxMinorGroups, "major group "+major)
	if err != nil {
		return err
	}
	for i := 0; i < len(minors) && i < maxMinorGroups; i++ {
		minor := minors[i]
		if err := parseMinorGroup(cat, level, major, minor.key, minor.value); err != nil {
			return err
		}
	}
	return nil
}

// parseMinorGroup decodes criterion entries from a minor group node into the catalog.
func parseMinorGroup(cat *Catalog, level, major, minor string, minorNode *yaml.Node) error {
	items, err := nodeEntries(minorNode, maxCriteriaPerGroup, "minor group "+minor)
	if err != nil {
		return err
	}
	for i := 0; i < len(items) && i < maxCriteriaPerGroup; i++ {
		item := items[i]
		var crit Criterion
		if err := item.value.Decode(&crit); err != nil {
			return fmt.Errorf("decode criterion %q: %w", item.key, err)
		}
		crit.Name = item.key
		crit.Level = level
		crit.MajorGroup = major
		crit.MinorGroup = minor
		cat.ByLevel[level][crit.Name] = crit
		if _, exists := cat.Criteria[crit.Name]; !exists {
			cat.Criteria[crit.Name] = crit
		}
	}
	return nil
}

// nodeEntries extracts keyed child nodes from mapping or sequence YAML nodes up to a bounded limit.
func nodeEntries(node *yaml.Node, bound int, label string) ([]keyedNode, error) {
	switch node.Kind {
	case yaml.MappingNode:
		return mappingEntries(node, bound, label)
	case yaml.SequenceNode:
		return omapEntries(node, bound, label)
	default:
		return nil, fmt.Errorf("%s: unexpected YAML node kind %v", label, node.Kind)
	}
}

// mappingEntries extracts key-value pairs from a standard YAML mapping node.
func mappingEntries(node *yaml.Node, bound int, label string) ([]keyedNode, error) {
	count := len(node.Content) / 2
	if count > bound {
		return nil, fmt.Errorf("%s: mapping has %d entries (max %d)", label, count, bound)
	}
	out := make([]keyedNode, 0, count)
	for i := 0; i+1 < len(node.Content) && len(out) < bound; i += 2 {
		out = append(out, keyedNode{key: node.Content[i].Value, value: node.Content[i+1]})
	}
	return out, nil
}

// omapEntries extracts ordered key-value pairs from a sequence of single-pair mapping nodes.
func omapEntries(node *yaml.Node, bound int, label string) ([]keyedNode, error) {
	if len(node.Content) > bound {
		return nil, fmt.Errorf("%s: omap sequence has %d entries (max %d)", label, len(node.Content), bound)
	}
	out := make([]keyedNode, 0, len(node.Content))
	for i := 0; i < len(node.Content) && i < bound; i++ {
		pair := node.Content[i]
		if pair.Kind != yaml.MappingNode || len(pair.Content) != 2 {
			return nil, fmt.Errorf("%s: omap entry %d is not a single-pair mapping", label, i)
		}
		out = append(out, keyedNode{key: pair.Content[0].Value, value: pair.Content[1]})
	}
	return out, nil
}
