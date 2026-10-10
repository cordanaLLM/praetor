package clientsetup

import (
	"context"
	"errors"
	"slices"
	"sort"

	"github.com/cordanaLLM/praetor/internal/agenthook"
	"github.com/cordanaLLM/praetor/internal/clientschema"
)

type LifecycleCapability struct {
	State           string   `json:"state"`
	DefinitionPaths []string `json:"definition_paths"`
	Activation      string   `json:"activation"`
}

// TrafficCapability describes one independently observable agent-text boundary. State is
// adapter-defined until a real native record proves activation; boundaries missing required
// text or correlation metadata are unenforceable, never inferred passing.
type TrafficCapability struct {
	State           string   `json:"state"`
	DefinitionPaths []string `json:"definition_paths"`
	Activation      string   `json:"activation"`
}

// SchemaPin reports the upstream schema a client's formats are checked against. State is
// "pinned" with the version and the sources of the vendored schemas (internal/clientschema), or
// "none" where the client publishes no schema; Reason says why. Both come from the vendored
// manifest, never from a literal here.
type SchemaPin struct {
	State   string   `json:"state"`
	Version string   `json:"version,omitempty"`
	Sources []string `json:"sources,omitempty"`
	Reason  string   `json:"reason,omitempty"`
}

// schemaClients maps an adapter to the client key its schemas carry in the manifest.
var schemaClients = map[Client]string{Codex: "codex", Claude: "claude", Gemini: "gemini", OpenCodeV1: "opencode"}

// noSchemaReason is the stated reason for an adapter without a vendored schema.
const noSchemaReason = "no upstream JSON Schema vendored for this format (docs/guides/client-schemas.md lists the documentation source)"

func schemaPin(manifest *clientschema.Manifest, client Client) SchemaPin {
	key, ok := schemaClients[client]
	if !ok {
		return SchemaPin{State: "none", Reason: noSchemaReason}
	}
	version := manifest.PinnedVersion(key)
	if version == "" {
		return SchemaPin{State: "none", Reason: noSchemaReason}
	}
	pin := SchemaPin{State: "pinned", Version: version}
	for _, source := range manifest.ForClient(key) {
		pin.Sources = append(pin.Sources, source.Repo+"@"+source.Pin)
	}
	return pin
}

type Capability struct {
	Client        Client              `json:"client"`
	Mode          string              `json:"mode"`
	RelativePath  string              `json:"relative_path,omitempty"`
	Documentation string              `json:"documentation"`
	Schema        SchemaPin           `json:"schema"`
	Lifecycle     LifecycleCapability `json:"lifecycle"`
	BriefCapture  TrafficCapability   `json:"brief_capture"`
	ReturnCapture TrafficCapability   `json:"return_capture"`
	RegisterGate  TrafficCapability   `json:"register_enforcement"`
}

type CapabilityReport struct {
	SchemaVersion   int          `json:"schema_version"`
	RuntimeVerified bool         `json:"runtime_verified"`
	Clients         []Capability `json:"clients"`
}

// lifecycleDefinitions are the tracked files that register a client's tool guard,
// checkpoint and stop rows: the native hook files of agenthook's registration table, the same
// files adoption registers the pre-tool row in. AGY has none: its plugin registers only the
// subagent brief row, so its lifecycle stays unsupported although the engine serves agy
// pre-tool and stop.
var lifecycleDefinitions = nativeHookDefinitions(Codex, Claude, Gemini)

func nativeHookDefinitions(clients ...Client) map[Client][]string {
	definitions := make(map[Client][]string, len(clients))
	for _, client := range clients {
		if file, ok := agenthook.NativeHookFile(string(client)); ok {
			definitions[client] = []string{file.Path}
		}
	}
	return definitions
}

// trafficDefinitions are the tracked files that register a client's subagent text rows.
var trafficDefinitions = map[Client][]string{
	Codex: {".codex/hooks.json"}, Claude: {".claude/settings.json"}, Gemini: {".gemini/settings.json"},
	AGY: {".agents/plugins/praetor/hooks.json"},
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
	manifest, err := clientschema.LoadManifest()
	if err != nil {
		return nil, err
	}
	items := make([]Capability, 0, len(adapters))
	for client, spec := range adapters {
		unavailable := TrafficCapability{State: "unenforceable", DefinitionPaths: []string{}, Activation: "unverified"}
		item := Capability{Client: client, Mode: spec.mode, RelativePath: spec.path, Documentation: spec.documentation,
			Schema:       schemaPin(manifest, client),
			Lifecycle:    LifecycleCapability{State: "unsupported", DefinitionPaths: []string{}, Activation: "unverified"},
			BriefCapture: unavailable, ReturnCapture: unavailable, RegisterGate: unavailable}
		if defined, ok := lifecycleDefinitions[client]; ok {
			item.Lifecycle.State = "adapter-defined"
			item.Lifecycle.DefinitionPaths = slices.Clone(defined)
		}
		if defined, ok := trafficDefinitions[client]; ok {
			item.BriefCapture = trafficCapability("adapter-defined", defined)
			if client == Codex || client == Claude {
				item.ReturnCapture = trafficCapability("adapter-defined", defined)
			}
			if client == Claude {
				item.RegisterGate = trafficCapability("adapter-defined", defined)
			}
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Client < items[j].Client })
	return &CapabilityReport{SchemaVersion: 1, RuntimeVerified: false, Clients: items}, nil
}

func trafficCapability(state string, paths []string) TrafficCapability {
	return TrafficCapability{State: state, DefinitionPaths: slices.Clone(paths), Activation: "unverified"}
}
