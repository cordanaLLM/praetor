// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// GolangciConfigFile represents a discovered golangci-lint configuration file.
type GolangciConfigFile struct {
	Path   string
	Name   string
	Format string
}

// LocateGolangciConfig searches for a golangci-lint configuration file in rootDir.
func LocateGolangciConfig(rootDir string) (*GolangciConfigFile, error) {
	for _, name := range []string{".golangci.yml", ".golangci.yaml"} {
		p := filepath.Join(rootDir, name)
		if _, err := os.Stat(p); err == nil {
			return &GolangciConfigFile{Path: p, Name: name, Format: "yaml"}, nil
		}
	}
	for _, name := range []string{".golangci.toml", ".golangci.json"} {
		p := filepath.Join(rootDir, name)
		if _, err := os.Stat(p); err == nil {
			ext := strings.TrimPrefix(filepath.Ext(name), ".")
			return &GolangciConfigFile{Path: p, Name: name, Format: ext}, nil
		}
	}
	return nil, nil
}

// maxParentWalkDepth limits directory traversal up the filesystem tree.
const maxParentWalkDepth = 64

// FindGolangciConfig searches for a golangci-lint configuration file in startDir
// and parent directories up to a git root, go.work or filesystem root.
func FindGolangciConfig(startDir string) (*GolangciConfigFile, error) {
	cur := filepath.Clean(startDir)
	for i := 0; i < maxParentWalkDepth; i++ {
		cfg, err := LocateGolangciConfig(cur)
		if err != nil || cfg != nil {
			return cfg, err
		}
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			break
		}
		if _, err := os.Stat(filepath.Join(cur, "go.work")); err == nil {
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return nil, nil
}

// DepguardDenyEntry matches one package restriction in a rule's deny list.
type DepguardDenyEntry struct {
	Pkg  string `yaml:"pkg"`
	Desc string `yaml:"desc,omitempty"`
}

// DepguardRuleConfig defines the settings for a single depguard rule.
type DepguardRuleConfig struct {
	ListMode string              `yaml:"list-mode"`
	Files    []string            `yaml:"files"`
	Allow    []string            `yaml:"allow"`
	Deny     []DepguardDenyEntry `yaml:"deny"`
}

// UnmarshalYAML decodes DepguardRuleConfig supporting both list and mapping forms of deny.
func (r *DepguardRuleConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		ListMode string    `yaml:"list-mode"`
		Files    []string  `yaml:"files"`
		Allow    []string  `yaml:"allow"`
		Deny     yaml.Node `yaml:"deny"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	r.ListMode = raw.ListMode
	r.Files = raw.Files
	r.Allow = raw.Allow
	r.Deny = decodeDenyNode(&raw.Deny)
	return nil
}

func decodeDenyNode(node *yaml.Node) []DepguardDenyEntry {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.SequenceNode:
		return decodeDenySequence(node.Content)
	case yaml.MappingNode:
		return decodeDenyMapping(node.Content)
	default:
		return nil
	}
}

func decodeDenySequence(items []*yaml.Node) []DepguardDenyEntry {
	var entries []DepguardDenyEntry
	for _, item := range items {
		switch item.Kind {
		case yaml.ScalarNode:
			entries = append(entries, DepguardDenyEntry{Pkg: item.Value})
		case yaml.MappingNode:
			var entry DepguardDenyEntry
			if err := item.Decode(&entry); err == nil {
				entries = append(entries, entry)
			}
		}
	}
	return entries
}

func decodeDenyMapping(items []*yaml.Node) []DepguardDenyEntry {
	var entries []DepguardDenyEntry
	for i := 0; i < len(items); i += 2 {
		k := items[i].Value
		v := ""
		if i+1 < len(items) {
			v = items[i+1].Value
		}
		entries = append(entries, DepguardDenyEntry{Pkg: k, Desc: v})
	}
	return entries
}

// DepguardSettings holds rules declared in .golangci.yml.
type DepguardSettings struct {
	Rules map[string]DepguardRuleConfig `yaml:"rules"`
}

// LoadDepguardSettings reads .golangci.yml and returns the parsed depguard settings.
func LoadDepguardSettings(ctx context.Context, path string) (*DepguardSettings, error) {
	if ctx == nil {
		return nil, errors.New("context required")
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, contextopt.MaxDuration)
		defer cancel()
	}
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read depguard config %s: %w", path, err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse golangci config %s: %w", path, err)
	}

	settings := &DepguardSettings{Rules: make(map[string]DepguardRuleConfig)}
	var raw struct {
		Linters struct {
			Settings struct {
				Depguard DepguardSettings `yaml:"depguard"`
			} `yaml:"settings"`
		} `yaml:"linters"`
		LintersSettings struct {
			Depguard DepguardSettings `yaml:"depguard"`
		} `yaml:"linters-settings"`
	}
	if err := root.Decode(&raw); err != nil {
		return nil, err
	}
	for k, v := range raw.Linters.Settings.Depguard.Rules {
		settings.Rules[k] = v
	}
	for k, v := range raw.LintersSettings.Depguard.Rules {
		settings.Rules[k] = v
	}
	return settings, nil
}
