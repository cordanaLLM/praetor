// Package clientsetup projects one explicit MCP registry into reviewed client
// configuration candidates. It performs no filesystem writes, process execution,
// trust changes, environment discovery, or credential handling.
package clientsetup

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	MaxConfigBytes        = 1 << 20
	MaxServers            = 32
	MaxArgs               = 64
	MaxValueBytes         = 4096
	MaxRegistryValueBytes = 256 << 10
	MaxOutputBytes        = 2 << 20
)

type Server struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type Registry struct {
	Version int      `json:"version"`
	Servers []Server `json:"servers"`
}

// DecodeRegistry rejects unknown keys, duplicate keys, null required identity
// values, interpolation, excessive nesting, and unsupported environment fields.
func DecodeRegistry(ctx context.Context, raw []byte) (Registry, error) {
	if err := validateJSON(ctx, raw); err != nil {
		return Registry{}, err
	}
	var registry Registry
	if err := json.Unmarshal(raw, &registry, json.RejectUnknownMembers(true)); err != nil {
		return Registry{}, errors.New("registry must contain only version and explicit stdio servers")
	}
	return validatedRegistry(ctx, registry)
}

func validatedRegistry(ctx context.Context, registry Registry) (Registry, error) {
	if err := checkContext(ctx); err != nil {
		return Registry{}, err
	}
	if registry.Version != 1 || len(registry.Servers) < 1 || len(registry.Servers) > MaxServers {
		return Registry{}, errors.New("registry requires version 1 and 1..32 servers")
	}
	result := Registry{Version: 1, Servers: make([]Server, len(registry.Servers))}
	seen := make(map[string]bool)
	total := 0
	for i, server := range registry.Servers {
		if err := validateServer(server); err != nil {
			return Registry{}, fmt.Errorf("server %d: %w", i, err)
		}
		if seen[server.Name] {
			return Registry{}, errors.New("duplicate registry server name")
		}
		seen[server.Name] = true
		total += len(server.Name) + len(server.Command)
		for _, arg := range server.Args {
			total += len(arg)
		}
		server.Args = append([]string{}, server.Args...)
		result.Servers[i] = server
	}
	if total > MaxRegistryValueBytes {
		return Registry{}, errors.New("registry values exceed 256 KiB")
	}
	slices.SortFunc(result.Servers, func(a, b Server) int { return strings.Compare(a.Name, b.Name) })
	return result, checkContext(ctx)
}

func validateServer(server Server) error {
	if !serverName(server.Name) {
		return errors.New("server name requires 1..64 ASCII letters, digits, underscores or hyphens and a letter first")
	}
	if !literal(server.Command) || !filepath.IsAbs(server.Command) || filepath.Clean(server.Command) != server.Command {
		return errors.New("server command requires a clean absolute literal executable path")
	}
	if len(server.Args) > MaxArgs {
		return errors.New("server exceeds 64 arguments")
	}
	for _, arg := range server.Args {
		if !literal(arg) {
			return errors.New("arguments require bounded literal UTF-8 strings without interpolation or control bytes")
		}
	}
	return nil
}

func serverName(name string) bool {
	if len(name) < 1 || len(name) > 64 {
		return false
	}
	if !asciiLetter(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if !asciiLetter(c) && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func asciiLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func literal(value string) bool {
	if len(value) > MaxValueBytes || !utf8.ValidString(value) {
		return false
	}
	for _, marker := range []string{"$", "`", "{env:", "{file:"} {
		if strings.Contains(value, marker) {
			return false
		}
	}
	for _, c := range value {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("client setup requires a context")
	}
	return ctx.Err()
}
