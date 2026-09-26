package needs

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	FrameworkCatalogDeclared = "catalog-declared"
	FrameworkSourceObserved  = "source-observed"
	// FrameworkNotConfigured is the basis of an index for which no framework is selected:
	// no checkout, no contract and no module.
	FrameworkNotConfigured = "not-configured"
	// defaultFrameworkVersion is the legacy go target's catalog label, not a verified
	// release pin (TRANSITION, see defaultFrameworkModule).
	defaultFrameworkVersion = "v0.8.0"
	// declaredFrameworkVersion is the version of an index a contract declares.
	declaredFrameworkVersion = "declared"
	// FrameworkContractFile is the capability contract a framework checkout may publish
	// at its root; when present it is the package inventory, not directory heuristics.
	FrameworkContractFile = "capabilities.yaml"
	// FrameworkNativeCapability classifies a consumer's imports of the selected
	// framework's own modules: they are retained, never third-party demand.
	FrameworkNativeCapability CapabilityKey = "fleet.framework"
)

// KnownDomainCapabilities maps the legacy go target's package directories to standard
// capabilities; the lean core packages live in its nested core/ module. It is built-in
// framework data (TRANSITION, see defaultFrameworkModule) and declares packages only for
// that module.
var KnownDomainCapabilities = map[string][]CapabilityKey{
	"db":              {"db.postgres", "db.orm", "db.clickhouse", "db.timescale", "db.cdc"},
	"cache":           {"cache.redis", "cache.inmemory", "cache.twotier"},
	"httpx":           {"http.router", "http.middleware", "http.openapi", "http.cors"},
	"http":            {"http.router", "http.middleware"},
	"jobs":            {"jobs.queue", "jobs.cron", "jobs.workflow"},
	"auth":            {"auth.jwt", "auth.oidc", "auth.apikey", "auth.session", "auth.passkeys"},
	"pubsub":          {"pubsub.nats", "pubsub.kafka"},
	"core/config":     {"config.loader"},
	"core/codec/yaml": {"config.yaml"},
	"core/log":        {"telemetry.logging"},
	"core/mcp":        {"mcp.server"},
	"otel":            {"telemetry.otel"},
	"observability":   {"telemetry.prometheus"},
	"core/clikit":     {"clikit.cobra"},
	"clikit/tui":      {"clikit.tui"},
	"core/id":         {"id.uuid", "id.nanoid"},
	"ebpf":            {"kernel.ebpf"},
	"apidocs":         {"http.openapi"},
	"storage":         {"storage.s3", "storage.tus"},
	"ai":              {"ai.llm_client", "ai.embeddings"},
	"realtime":        {"realtime.sse", "realtime.webrtc"},
	"idempotency":     {"http.idempotency"},
	"notify":          {"notify.email", "notify.push"},
	"secrets":         {"security.secrets"},
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
	if config.IsModulePathShaped(value) {
		return value
	}
	return defaultFrameworkModule
}

// InspectFramework builds the index of the framework source selects. A checkout is
// observed from source; a contract declares packages without observing them; the legacy go
// target's module alone declares the built-in catalog (TRANSITION); any other module alone
// names the framework without declaring a package; nothing selected is not configured. It
// does not run builds or establish tested correctness.
func InspectFramework(ctx context.Context, source FrameworkSource) (*FrameworkIndex, error) {
	if ctx == nil {
		return nil, errors.New("framework inspection requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	index := &FrameworkIndex{
		Name: source.Module, RootPath: source.Checkout, CatalogModule: source.Module,
		Version: declaredFrameworkVersion, Basis: FrameworkCatalogDeclared,
		Packages:     make(map[string]FrameworkPackage),
		Capabilities: make(map[CapabilityKey][]string),
	}
	switch {
	case source.Checkout != "":
		index.Basis, index.Version = FrameworkSourceObserved, "unverified"
		if err := observeFramework(ctx, index); err != nil {
			return nil, err
		}
	case source.Contract != "":
		if err := declareContractFramework(ctx, index, source.Contract); err != nil {
			return nil, err
		}
	default:
		declareModuleFramework(index)
	}
	return index, nil
}

// declareModuleFramework fills an index selected by its module alone.
func declareModuleFramework(index *FrameworkIndex) {
	switch index.Name {
	case "":
		index.Basis, index.Version = FrameworkNotConfigured, ""
	case defaultFrameworkModule:
		index.Version = defaultFrameworkVersion
		populateDefaultFrameworkIndex(index)
	default:
		index.Basis, index.Version = FrameworkIdentityDeclared, "unverified"
	}
}

// populateDefaultFrameworkIndex declares the legacy go target's built-in catalog.
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
	// Related adapters come from the same catalog as dependency relationships.
	for _, entry := range CanonicalCatalog {
		if entry.Relationship == nil || entry.Relationship.FrameworkPackage == "" {
			continue
		}
		if relative, ok := index.catalogRelative(entry.Relationship.FrameworkPackage); ok {
			addObservedPackage(index, relative, []CapabilityKey{entry.Capability})
		}
	}
}

// catalogRelative returns the path of a built-in catalog package relative to the module the
// catalog resolves against (CatalogModule). A package outside that module, or any package
// when no module is selected, belongs to no package of this framework.
func (idx *FrameworkIndex) catalogRelative(catalogPath string) (string, bool) {
	if idx.CatalogModule == "" {
		return "", false
	}
	return strings.CutPrefix(catalogPath, idx.CatalogModule+"/")
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

// ProvidesCapability requires an exact mapping. Basis distinguishes declarations
// from observed source availability; neither establishes tested correctness.
func (idx *FrameworkIndex) ProvidesCapability(capKey CapabilityKey) bool {
	return idx.IsCapabilityCovered(capKey)
}
