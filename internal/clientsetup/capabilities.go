package clientsetup

import (
	"context"
	"errors"
	"slices"
	"sort"
)

type LifecycleCapability struct {
	State           string   `json:"state"`
	DefinitionPaths []string `json:"definition_paths"`
	Activation      string   `json:"activation"`
}

type Capability struct {
	Client        Client              `json:"client"`
	Mode          string              `json:"mode"`
	RelativePath  string              `json:"relative_path,omitempty"`
	Documentation string              `json:"documentation"`
	Lifecycle     LifecycleCapability `json:"lifecycle"`
}

type CapabilityReport struct {
	SchemaVersion   int          `json:"schema_version"`
	RuntimeVerified bool         `json:"runtime_verified"`
	Clients         []Capability `json:"clients"`
}

func Capabilities(ctx context.Context) (*CapabilityReport, error) {
	if ctx == nil {
		return nil, errors.New("capabilities requires context")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	paths := map[Client][]string{Codex: {".codex/hooks.json"}, Claude: {".claude/settings.json"}, Gemini: {".gemini/settings.json"}, AGY: {".agents/mcp_config.json"}}
	items := make([]Capability, 0, len(adapters))
	for client, spec := range adapters {
		item := Capability{Client: client, Mode: spec.mode, RelativePath: spec.path, Documentation: spec.documentation, Lifecycle: LifecycleCapability{State: "unsupported", DefinitionPaths: []string{}, Activation: "unverified"}}
		if defined, ok := paths[client]; ok {
			item.Lifecycle.State = "adapter-defined"
			item.Lifecycle.DefinitionPaths = slices.Clone(defined)
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Client < items[j].Client })
	return &CapabilityReport{SchemaVersion: 1, RuntimeVerified: false, Clients: items}, nil
}
