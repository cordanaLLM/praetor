package needs

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cordanaLLM/standards/internal/util"
)

const defaultFrameworkModule = "github.com/golusoris/golusoris"

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

// InspectFramework discovers exported packages and capability offerings from a local framework repo.
func InspectFramework(ctx context.Context, frameworkPath string) (*FrameworkIndex, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	index := &FrameworkIndex{
		Name:         defaultFrameworkModule,
		RootPath:     frameworkPath,
		Version:      "v0.8.0",
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
		processDomainDir(frameworkPath, entry.Name(), index)
	}

	return index, nil
}

// processDomainDir registers a domain directory into the framework index.
func processDomainDir(root, domain string, index *FrameworkIndex) {
	pkgPath := defaultFrameworkModule + "/" + domain
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
		pkgPath := defaultFrameworkModule + "/" + domain
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

// IsCapabilityCovered checks if the framework provides an implementation for capKey.
func (idx *FrameworkIndex) IsCapabilityCovered(capKey CapabilityKey) bool {
	pkgs, ok := idx.Capabilities[capKey]
	return ok && len(pkgs) > 0
}
