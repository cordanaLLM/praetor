// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"gopkg.in/yaml.v3"
)

const (
	// MCPOffloadMinBytes is the smallest declared offload threshold; a smaller value would
	// turn nearly every result into a pointer line.
	MCPOffloadMinBytes = 1024
	// MCPOffloadMaxBytes is the largest declared offload threshold, the 4 MiB bound on one
	// tool result (internal/mcp.MaxResultTextBytes).
	MCPOffloadMaxBytes = 4 << 20
)

// MCPPolicy is the manifest's mcp section: settings of the standards-mcp server for this
// repository, read once at server start through LoadManifest (cmd/standards-mcp).
type MCPPolicy struct {
	// OffloadThresholdBytes is the size in bytes of all text items of one tool result above
	// which standards-mcp writes the largest items to .standards/cache/mcp-out and serves
	// pointer lines. Absent keeps the default of 16 KiB; 0 turns offloading off, so every
	// result is served inline up to the 4 MiB bound; any other value lies in
	// MCPOffloadMinBytes..MCPOffloadMaxBytes.
	OffloadThresholdBytes *int `yaml:"offload_threshold_bytes,omitempty"`
}

// UnmarshalYAML decodes the section strictly at the source boundary, so every manifest decoder
// (DecodeManifest included, which skips the policy validations) refuses it alike: known keys
// once each, an integer written as an integer, and a value that is 0 or within the bounds.
func (p *MCPPolicy) UnmarshalYAML(node *yaml.Node) error {
	var threshold int
	present, err := registerIntFields(node, "mcp", map[string]*int{"offload_threshold_bytes": &threshold})
	if err != nil {
		return err
	}
	*p = MCPPolicy{}
	if !present["offload_threshold_bytes"] {
		return nil
	}
	if threshold != 0 {
		if err := validateDeclaredBound("mcp", "offload_threshold_bytes", &threshold, MCPOffloadMinBytes, MCPOffloadMaxBytes); err != nil {
			return err
		}
	}
	p.OffloadThresholdBytes = &threshold
	return nil
}

// DeclaredMCP returns the manifest's mcp section, nil for a nil manifest or one that declares
// none.
func (m *Manifest) DeclaredMCP() *MCPPolicy {
	if m == nil {
		return nil
	}
	return m.MCP
}
