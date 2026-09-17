package clientsetup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/clientid"
)

// Client is the shared client identifier; the list of known clients lives in clientid.
type Client = clientid.ID

const (
	Codex      = clientid.Codex
	Claude     = clientid.Claude
	Gemini     = clientid.Gemini
	OpenCodeV1 = clientid.OpenCodeV1
	Continue   = clientid.Continue
	Cline      = clientid.Cline
	Kilo       = clientid.Kilo
	AGY        = clientid.AGY
)

var ErrConflict = errors.New("existing server conflicts with registry")
var ErrUnsupportedMerge = errors.New("client requires native configuration update or explicit export")

// Plan contains sensitive candidate bytes only in Content, excluded from JSON.
// Commands are argv arrays, never shell text. They can update native client state
// and require the caller to inspect existing settings before executing them.
// A plan is not evidence of discovery, connection, trust, or tool use.
type Plan struct {
	Client         Client     `json:"client"`
	Mode           string     `json:"mode"`
	RelativePath   string     `json:"relative_path,omitempty"`
	ExportName     string     `json:"export_name,omitempty"`
	SourceSHA256   string     `json:"source_sha256"`
	RegistrySHA256 string     `json:"registry_sha256"`
	Content        []byte     `json:"-"`
	Commands       [][]string `json:"commands,omitempty"`
	Documentation  string     `json:"documentation"`
	Changed        bool       `json:"changed"`
}

type adapter struct{ mode, path, export, documentation string }

var adapters = map[Client]adapter{
	Codex:      {"native", "", "codex-mcp.toml", "https://developers.openai.com/codex/mcp/"},
	Claude:     {"merge", ".mcp.json", "claude-mcp.json", "https://code.claude.com/docs/en/mcp"},
	Gemini:     {"merge", ".gemini/settings.json", "gemini-settings.json", "https://geminicli.com/docs/tools/mcp-server/"},
	OpenCodeV1: {"merge", "opencode.json", "opencode-v1.json", "https://opencode.ai/docs/mcp-servers/"},
	Continue:   {"merge", ".continue/mcpServers/praetor.yaml", "continue-mcp.yaml", "https://docs.continue.dev/customize/deep-dives/mcp"},
	Cline:      {"export", "", "cline-mcp.json", "https://docs.cline.bot/mcp/mcp-overview"},
	Kilo:       {"export", "", "kilo-mcp.json", "https://kilo.ai/docs/automate/mcp/using-in-kilo-code"},
	AGY:        {"merge", ".agents/mcp_config.json", "agy-mcp_config.json", "https://antigravity.google/docs/plugins"},
}

// BuildPlan merges explicit existing bytes in memory only. JSON inputs must be
// strict JSON, not JSONC. YAML aliases and merge keys are unsupported. Existing
// unrelated settings and server options are retained; conflicting command/args
// are errors. Codex delegates arbitrary native config updates to its CLI.
func BuildPlan(ctx context.Context, registry Registry, client Client, existing []byte) (*Plan, error) {
	registry, err := validatedRegistry(ctx, registry)
	if err != nil {
		return nil, err
	}
	spec, ok := adapters[client]
	if !ok {
		return nil, errors.New("unsupported client adapter")
	}
	if len(existing) > MaxConfigBytes {
		return nil, errors.New("existing client config exceeds 1 MiB")
	}
	registryBytes, err := marshalJSON(registry)
	if err != nil {
		return nil, err
	}
	p := &Plan{Client: client, Mode: spec.mode, RelativePath: spec.path, ExportName: spec.export,
		Documentation: spec.documentation, SourceSHA256: digest(existing), RegistrySHA256: digest(registryBytes)}
	if err := renderPlan(ctx, registry, existing, p); err != nil {
		return nil, err
	}
	if len(p.Content) > MaxOutputBytes {
		return nil, errors.New("client candidate exceeds 2 MiB")
	}
	p.Changed = len(p.Commands) != 0 || !bytes.Equal(existing, p.Content)
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

func renderPlan(ctx context.Context, registry Registry, existing []byte, p *Plan) error {
	var err error
	switch p.Client {
	case Codex:
		if len(existing) != 0 {
			return ErrUnsupportedMerge
		}
		p.Commands, p.Content = nativePlan(registry)
	case Continue:
		p.Content, err = mergeYAML(ctx, registry, existing)
	default:
		p.Content, err = mergeJSON(ctx, registry, p.Client, existing)
	}
	return err
}

func digest(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }
