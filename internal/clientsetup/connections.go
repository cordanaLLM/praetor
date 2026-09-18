package clientsetup

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/httpendpoint"
	"github.com/cordanaLLM/praetor/internal/repairrun"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ConnectionProfile contains deployment settings and credential references, never
// credential values. Its projections use the existing adapters and repair client.
type ConnectionProfile struct {
	Version  int                       `json:"version"`
	Gateway  GatewayConnection         `json:"gateway"`
	Provider *repairrun.ProviderConfig `json:"provider,omitempty"`
	Memory   *MemoryBinding            `json:"memory,omitempty"`
}

type GatewayConnection struct {
	BridgeCommand string            `json:"bridge_command"`
	TokenFile     string            `json:"token_file"`
	Endpoints     []GatewayEndpoint `json:"endpoints"`
	LocalServers  []Server          `json:"local_servers,omitempty"`
}

type GatewayEndpoint struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// MemoryBinding adds an exact project mapping without changing fleet defaults.
// The installed Hindsight client resolves linked worktrees to the main root.
type MemoryBinding struct {
	ProjectRoot string `json:"project_root"`
	BankID      string `json:"bank_id"`
}

// DecodeConnectionProfile validates bounded, explicit configuration without I/O.
func DecodeConnectionProfile(ctx context.Context, raw []byte) (ConnectionProfile, error) {
	if err := validateJSON(ctx, raw); err != nil {
		return ConnectionProfile{}, err
	}
	var profile ConnectionProfile
	if err := json.Unmarshal(raw, &profile, json.RejectUnknownMembers(true)); err != nil {
		return profile, errors.New("invalid connection profile fields")
	}
	if _, err := ConnectionRegistry(ctx, profile); err != nil {
		return profile, err
	}
	return profile, nil
}

// ConnectionRegistry binds all gateway endpoints to one bridge and token file.
// Local servers use the same strict stdio contract as a hand-authored registry.
func ConnectionRegistry(ctx context.Context, profile ConnectionProfile) (Registry, error) {
	if err := validateConnections(profile); err != nil {
		return Registry{}, err
	}
	gateway := profile.Gateway
	servers := make([]Server, 0, len(gateway.Endpoints)+len(gateway.LocalServers))
	for _, endpoint := range gateway.Endpoints {
		if err := validateGatewayURL(endpoint.URL); err != nil {
			return Registry{}, err
		}
		servers = append(servers, Server{Name: endpoint.Name, Command: gateway.BridgeCommand,
			Args: []string{"--endpoint", endpoint.URL, "--token-file", gateway.TokenFile}})
	}
	servers = append(servers, gateway.LocalServers...)
	return validatedRegistry(ctx, Registry{Version: 1, Servers: servers})
}

func validateConnections(profile ConnectionProfile) error {
	gateway := profile.Gateway
	if profile.Version != 1 || len(gateway.Endpoints) < 1 || len(gateway.Endpoints)+len(gateway.LocalServers) > MaxServers {
		return errors.New("connections require version 1 and 1..32 servers including a gateway endpoint")
	}
	if !absoluteLiteral(gateway.BridgeCommand) || !absoluteLiteral(gateway.TokenFile) {
		return errors.New("gateway bridge and token file require clean absolute literal paths")
	}
	if profile.Provider != nil {
		if err := repairrun.ValidateProviderConfig(*profile.Provider); err != nil {
			return err
		}
	}
	if profile.Memory != nil {
		return validateMemoryBinding(*profile.Memory)
	}
	return nil
}

func absoluteLiteral(value string) bool { return util.CleanAbsoluteLiteral(value, MaxValueBytes) }

func validateGatewayURL(value string) error {
	if !literal(value) || strings.TrimSpace(value) != value {
		return errors.New("gateway endpoint requires a literal HTTPS URL")
	}
	return httpendpoint.ValidateHTTPS(value)
}

// ConnectionArtifacts exports private inputs for existing client and repair
// commands. Producing these bytes does not claim connectivity or model readiness.
func ConnectionArtifacts(ctx context.Context, profile ConnectionProfile) (map[string][]byte, error) {
	registry, err := ConnectionRegistry(ctx, profile)
	if err != nil {
		return nil, err
	}
	raw, err := marshalJSON(registry)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{"registry.json": raw}
	if profile.Provider != nil {
		files["provider.json"], err = marshalJSON(profile.Provider)
		if err != nil {
			return nil, fmt.Errorf("encode provider connection: %w", err)
		}
	}
	return files, checkContext(ctx)
}
