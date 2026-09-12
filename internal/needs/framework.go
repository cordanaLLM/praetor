package needs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	defaultFrameworkModule = "github.com/golusoris/golusoris"
	// defaultFrameworkVersion is the framework release generated artifacts pin.
	defaultFrameworkVersion = "v0.8.0"
)

// KnownDomainCapabilities maps Golusoris root directories to standard capabilities.
var KnownDomainCapabilities = map[string][]CapabilityKey{
	"db":            {"db.postgres", "db.orm", "db.clickhouse", "db.timescale", "db.cdc"},
	"cache":         {"cache.redis", "cache.inmemory", "cache.twotier"},
	"httpx":         {"http.router", "http.middleware", "http.openapi", "http.cors"},
	"http":          {"http.router", "http.middleware"},
	"jobs":          {"jobs.queue", "jobs.cron", "jobs.workflow"},
	"auth":          {"auth.jwt", "auth.oidc", "auth.apikey", "auth.session", "auth.passkeys"},
	"pubsub":        {"pubsub.nats", "pubsub.kafka"},
	"config":        {"config.loader", "config.yaml"},
	"log":           {"telemetry.logging"},
	"otel":          {"telemetry.otel"},
	"observability": {"telemetry.prometheus"},
	"clikit":        {"clikit.cobra", "clikit.tui"},
	"id":            {"id.uuid", "id.nanoid"},
	"ebpf":          {"kernel.ebpf"},
	"apidocs":       {"http.openapi"},
	"storage":       {"storage.s3", "storage.tus"},
	"ai":            {"ai.llm_client", "ai.embeddings"},
	"realtime":      {"realtime.sse", "realtime.webrtc"},
	"idempotency":   {"http.idempotency"},
	"notify":        {"notify.email", "notify.push"},
	"secrets":       {"security.secrets"},
}

// ResolveFrameworkModule maps the operator-supplied framework location onto the module
// path that identifies the framework in generated artifacts.
//
// A directory is resolved through its own go.mod, so a checkout of a fork reports the
// fork's module path; a value already shaped like a module path is taken as-is; anything
// else - in particular a filesystem path that does not exist - falls back to the default
// module, because a local filesystem path must never be published as the target
// framework of an issue body or a migration plan.
func ResolveFrameworkModule(frameworkPath string) string {
	value := strings.TrimSpace(frameworkPath)
	if value == "" {
		return defaultFrameworkModule
	}
	if util.DirExists(value) {
		modulePath, _, _, err := parseGoMod(filepath.Join(value, "go.mod"))
		if err != nil || modulePath == "" || modulePath == "unknown" {
			return defaultFrameworkModule
		}
		return modulePath
	}
	if isModulePathShaped(value) {
		return value
	}
	return defaultFrameworkModule
}

// isModulePathShaped reports whether value looks like a Go module path rather than a
// filesystem location: it must not be absolute or relative-prefixed, and its first
// segment must be a host, i.e. contain a dot.
func isModulePathShaped(value string) bool {
	if filepath.IsAbs(value) || strings.HasPrefix(value, ".") || strings.HasPrefix(value, "~") {
		return false
	}
	first, _, ok := strings.Cut(value, "/")
	return ok && strings.Contains(first, ".")
}

// InspectFramework discovers exported packages and capability offerings from a local framework repo.
func InspectFramework(ctx context.Context, frameworkPath string) (*FrameworkIndex, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	index := &FrameworkIndex{
		Name:         ResolveFrameworkModule(frameworkPath),
		RootPath:     frameworkPath,
		Version:      defaultFrameworkVersion,
		Packages:     make(map[string]FrameworkPackage),
		Capabilities: make(map[CapabilityKey][]string),
	}

	if frameworkPath == "" || !util.DirExists(frameworkPath) {
		populateDefaultFrameworkIndex(index)
		return index, nil
	}

	entries, err := os.ReadDir(frameworkPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read framework directory %s: %w", frameworkPath, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		processDomainDir(entry.Name(), index)
	}

	return index, nil
}

// processDomainDir registers a domain directory into the framework index.
func processDomainDir(domain string, index *FrameworkIndex) {
	pkgPath := index.Name + "/" + domain
	caps, found := KnownDomainCapabilities[domain]
	if !found {
		caps = []CapabilityKey{CapabilityKey(domain + ".core")}
	}

	pkg := FrameworkPackage{
		ImportPath:   pkgPath,
		Domain:       domain,
		Capabilities: caps,
	}
	index.Packages[pkgPath] = pkg

	for _, c := range caps {
		index.Capabilities[c] = append(index.Capabilities[c], pkgPath)
	}
}

// populateDefaultFrameworkIndex supplies static baseline index when offline.
func populateDefaultFrameworkIndex(index *FrameworkIndex) {
	for domain, caps := range KnownDomainCapabilities {
		pkgPath := index.Name + "/" + domain
		index.Packages[pkgPath] = FrameworkPackage{
			ImportPath:   pkgPath,
			Domain:       domain,
			Capabilities: caps,
		}
		for _, c := range caps {
			index.Capabilities[c] = append(index.Capabilities[c], pkgPath)
		}
	}
}

// IsCapabilityCovered reports whether the index lists at least one framework package
// for capKey verbatim.
func (idx *FrameworkIndex) IsCapabilityCovered(capKey CapabilityKey) bool {
	if idx == nil {
		return false
	}
	pkgs, ok := idx.Capabilities[capKey]
	return ok && len(pkgs) > 0
}

// ProvidesCapability reports whether the inspected framework can serve capKey.
//
// A capability counts as provided when the index lists it verbatim, or when the
// framework exposes the domain the capability belongs to (the segment before the first
// "."). The domain fallback exists because a checkout contributes the capabilities of
// KnownDomainCapabilities only for the directories it actually has, and a directory
// outside that table contributes just "<domain>.core" - yet a `mcp/` package does serve
// "mcp.server".
//
// This is the production reader of FrameworkIndex.Capabilities: fleet aggregation uses
// it to demote a catalog-covered dependency whose capability the framework the operator
// pointed at does not in fact ship.
func (idx *FrameworkIndex) ProvidesCapability(capKey CapabilityKey) bool {
	if idx == nil {
		return false
	}
	if idx.IsCapabilityCovered(capKey) {
		return true
	}
	domain, _, ok := strings.Cut(string(capKey), ".")
	if !ok || domain == "" {
		return false
	}
	return idx.hasDomain(domain)
}

// hasDomain reports whether the index holds a package for the given framework domain.
func (idx *FrameworkIndex) hasDomain(domain string) bool {
	for _, pkg := range idx.Packages {
		if pkg.Domain == domain {
			return true
		}
	}
	return false
}
