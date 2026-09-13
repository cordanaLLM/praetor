package needs

import (
	"strings"
)

// CatalogEntry specifies a known library's mapping to Golusoris capabilities.
type CatalogEntry struct {
	Package              string
	Capability           CapabilityKey
	Status               CapabilityStatus
	GolusorisReplacement string
	Notes                string
	Relationship         *LibraryRelationship
}

// CanonicalCatalog provides the authoritative fleet mapping of Go libraries to Golusoris.
var CanonicalCatalog = []CatalogEntry{
	// Database
	{
		Package:              "github.com/jackc/pgx",
		Capability:           "db.postgres",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/db/pgx",
		Notes:                "Direct PostgreSQL driver and pool integration",
	},
	{
		Package:              "github.com/uptrace/bun",
		Capability:           "db.orm",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/db/bun",
		Notes:                "Idiomatic Go SQL-first query builder",
	},
	{
		Package:              "github.com/ClickHouse/clickhouse-go",
		Capability:           "db.clickhouse",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/db/clickhouse",
		Notes:                "High-performance analytical column-store database",
	},
	{
		Package:              "github.com/jmoiron/sqlx",
		Capability:           "db.sqlx",
		Status:               StatusAdapterAvailable,
		GolusorisReplacement: "github.com/golusoris/golusoris/db",
		Notes:                "Migrate queries to Golusoris db/bun or db/pgx",
	},
	{
		Package:              "gorm.io/gorm",
		Capability:           "db.orm",
		Status:               StatusAdapterAvailable,
		GolusorisReplacement: "github.com/golusoris/golusoris/db/bun",
		Notes:                "Migrate legacy GORM models to bun.Ident schema definitions",
	},
	{
		Package:              "github.com/lib/pq",
		Capability:           "db.postgres",
		Status:               StatusAdapterAvailable,
		GolusorisReplacement: "github.com/golusoris/golusoris/db/pgx",
		Notes:                "Replace deprecated lib/pq with Golusoris pgx pool",
	},

	// Cache
	{
		Package:              "github.com/redis/rueidis",
		Capability:           "cache.redis",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/cache/redis",
		Notes:                "Fast auto-pipelining Redis client",
	},
	{
		Package:              "github.com/redis/go-redis",
		Capability:           "cache.redis",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/cache/redis",
		Notes:                "Migrate to Golusoris cache/twotier or cache/redis",
	},
	{
		Package:              "github.com/patrickmn/go-cache",
		Capability:           "cache.inmemory",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/cache/memory",
		Notes:                "In-process concurrent cache with TTL",
	},

	// HTTP & Routing
	{
		Package:    "github.com/ogen-go/ogen",
		Capability: "http.openapi",
		Status:     StatusCovered,
		Relationship: &LibraryRelationship{Kind: RelationshipTooling,
			FrameworkPackage: "github.com/golusoris/golusoris/ogenkit", Basis: FrameworkCatalogDeclared},
		Notes: "Retain ogen for OpenAPI generation and runtime imports; ogenkit supplies integration helpers, not replacement APIs (Golusoris ADR-0004). A module dependency alone does not prove generator execution.",
	},
	{
		Package:              "github.com/go-chi/chi",
		Capability:           "http.router",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/httpx",
		Notes:                "Composable net/http middleware and router",
	},
	{
		Package:              "github.com/gin-gonic/gin",
		Capability:           "http.router",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/httpx",
		Notes:                "Fast HTTP router; migrate gin.Context handlers to httpx",
	},
	{
		Package:              "github.com/swaggo/swag",
		Capability:           "http.openapi",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/apidocs",
		Notes:                "Swagger/OpenAPI spec generation and UI serving",
	},
	{
		Package:              "github.com/swaggo/gin-swagger",
		Capability:           "http.openapi",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/apidocs",
		Notes:                "OpenAPI endpoint serving via Golusoris apidocs",
	},
	{
		Package:              "github.com/getkin/kin-openapi",
		Capability:           "http.openapi",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/apidocs",
		Notes:                "OpenAPI 3.0 validation and specification parser",
	},

	// Jobs & Queuing
	{
		Package:              "github.com/hibiken/asynq",
		Capability:           "jobs.queue",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/jobs",
		Notes:                "Migrate Redis worker tasks to Golusoris jobs (River PostgreSQL queue)",
	},
	{
		Package:              "github.com/riverqueue/river",
		Capability:           "jobs.queue",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/jobs",
		Notes:                "Native River queue engine wrapped in Golusoris",
	},
	{
		Package:              "github.com/robfig/cron",
		Capability:           "jobs.cron",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/jobs/cron",
		Notes:                "Scheduled recurring task manager",
	},

	// PubSub & Messaging
	{
		Package:              "github.com/nats-io/nats.go",
		Capability:           "pubsub.nats",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/pubsub/nats",
		Notes:                "NATS Core and JetStream messaging client",
	},
	{
		Package:              "github.com/confluentinc/confluent-kafka-go",
		Capability:           "pubsub.kafka",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/pubsub/kafka",
		Notes:                "High-throughput Kafka streaming client",
	},

	// Auth & Security
	{
		Package:              "github.com/golang-jwt/jwt",
		Capability:           "auth.jwt",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/auth/jwt",
		Notes:                "Secure HMAC/RSA/Ed25519 token issuance and verification",
	},
	{
		Package:              "github.com/coreos/go-oidc",
		Capability:           "auth.oidc",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/auth/oidc",
		Notes:                "OpenID Connect verification and claims extraction",
	},

	// Configuration
	{
		Package:    "github.com/knadh/koanf/v2",
		Capability: "config.loader",
		Status:     StatusCovered,
		Relationship: &LibraryRelationship{Kind: RelationshipWrappedBy,
			FrameworkPackage: "github.com/golusoris/golusoris/config", Basis: FrameworkCatalogDeclared},
		Notes: "Retain koanf as the modular configuration engine; Golusoris config provides the application adapter (Golusoris ADR-0002).",
	},
	{
		Package:              "github.com/spf13/viper",
		Capability:           "config.loader",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/config",
		Notes:                "Hierarchical configuration with environment override",
	},

	// Telemetry & Observability
	{
		Package:      "log/slog",
		Capability:   "telemetry.logging",
		Status:       StatusNative,
		Relationship: &LibraryRelationship{Kind: RelationshipFoundation, Basis: FrameworkCatalogDeclared},
		Notes:        "Retain the standard-library structured logging API; Golusoris log configures its handlers (Golusoris ADR-0003).",
	},
	{
		Package:    "github.com/lmittmann/tint",
		Capability: "telemetry.logging",
		Status:     StatusCovered,
		Relationship: &LibraryRelationship{Kind: RelationshipWrappedBy,
			FrameworkPackage: "github.com/golusoris/golusoris/log", Basis: FrameworkCatalogDeclared},
		Notes: "Retain tint as an optional slog handler; Golusoris log configures tint or JSON without changing the application logging API (Golusoris ADR-0003).",
	},
	{
		Package:              "go.opentelemetry.io/otel",
		Capability:           "telemetry.otel",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/otel",
		Notes:                "OpenTelemetry trace, metric, and baggage context propagation",
	},
	{
		Package:              "github.com/prometheus/client_golang",
		Capability:           "telemetry.prometheus",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/observability",
		Notes:                "Prometheus metrics collector and scraping handler",
	},
	{
		Package:              "go.uber.org/zap",
		Capability:           "telemetry.logging",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/log",
		Notes:                "High-performance structured JSON logging",
	},
	{
		Package:              "github.com/sirupsen/logrus",
		Capability:           "telemetry.logging",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/log",
		Notes:                "Migrate legacy Logrus calls to Golusoris slog/log",
	},

	// CLI & Terminal
	{
		Package:              "github.com/spf13/cobra",
		Capability:           "clikit.cobra",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/clikit",
		Notes:                "Command line interface framework with flags",
	},
	{
		Package:              "github.com/charmbracelet/bubbletea",
		Capability:           "clikit.tui",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/clikit",
		Notes:                "The Elm Architecture terminal user interface",
	},

	// Identifiers
	{
		Package:              "github.com/google/uuid",
		Capability:           "id.uuid",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/id",
		Notes:                "V4 and V7 UUID generation",
	},

	// eBPF
	{
		Package:              "github.com/cilium/ebpf",
		Capability:           "kernel.ebpf",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/ebpf",
		Notes:                "Kernel tracing and XDP/TC networking hooks",
	},

	// Testing & Inversion of Control
	{
		Package:              "github.com/stretchr/testify",
		Capability:           "test.assert",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/testutil",
		Notes:                "Testify assertions and mocks mapped to Golusoris testutil",
	},
	{
		Package:      "go.uber.org/fx",
		Capability:   "runtime.di",
		Status:       StatusNative,
		Relationship: &LibraryRelationship{Kind: RelationshipFoundation, Basis: FrameworkCatalogDeclared},
		Notes:        "Retain fx as the dependency-injection and lifecycle foundation; compose Golusoris modules through fx rather than replacing it with clikit (Golusoris ADR-0001).",
	},

	// Serialization & Configuration Formats
	{
		Package:              "gopkg.in/yaml.v3",
		Capability:           "config.yaml",
		Status:               StatusGap,
		GolusorisReplacement: "github.com/golusoris/golusoris/config",
		Notes:                "YAML parser and serializer; use Golusoris config or await zero-dependency codec",
	},
	{
		Package:              "gopkg.in/yaml.v2",
		Capability:           "config.yaml",
		Status:               StatusGap,
		GolusorisReplacement: "github.com/golusoris/golusoris/config",
		Notes:                "Legacy YAML v2 parser; upgrade to v3 or Golusoris config",
	},

	// Model Context Protocol (MCP)
	{
		Package:              "github.com/mark3labs/mcp-go",
		Capability:           "mcp.server",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/mcp",
		Notes:                "Migrate community mcp-go server to Golusoris mcp module",
	},
	{
		Package:              "github.com/modelcontextprotocol/go-sdk",
		Capability:           "mcp.server",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/mcp",
		Notes:                "Official MCP Go SDK wrapped in Golusoris mcp module",
	},
}

// MatchPackage searches the catalog for the longest module-path match for a given import
// path.
//
// The match is anchored on a path boundary: an entry matches the import path itself or a
// package inside it, never a different module that merely starts with the same
// characters. Without the boundary, "github.com/uptrace/bunrouter" would inherit the
// "github.com/uptrace/bun" mapping and be reported as covered by an unrelated
// replacement.
func MatchPackage(importPath string) (CatalogEntry, bool) {
	var bestMatch CatalogEntry
	longestPrefix := 0

	for _, entry := range CanonicalCatalog {
		if !matchesModuleBoundary(importPath, entry.Package) {
			continue
		}
		if len(entry.Package) > longestPrefix {
			longestPrefix = len(entry.Package)
			bestMatch = entry
		}
	}

	return bestMatch, longestPrefix > 0
}

// matchesModuleBoundary reports whether importPath is modulePath itself or a package
// nested inside it.
func matchesModuleBoundary(importPath, modulePath string) bool {
	if modulePath == "" {
		return false
	}
	return importPath == modulePath || strings.HasPrefix(importPath, modulePath+"/")
}

// CatalogMapping defines the framework mapping for a non-Go dependency.
type CatalogMapping struct {
	Capability  CapabilityKey
	Status      CapabilityStatus
	Replacement string
	Notes       string
}

func lookupNodeCatalog(pkg string) (CatalogMapping, bool) {
	nodeMappings := map[string]CatalogMapping{
		"svelte":         {Capability: "ui.framework", Status: StatusCovered, Replacement: "github.com/golusoris/sveltesentio", Notes: "Core Svelte reactive UI framework"},
		"@sveltejs/kit":  {Capability: "ui.framework", Status: StatusCovered, Replacement: "github.com/golusoris/sveltesentio", Notes: "SvelteKit application framework"},
		"tailwindcss":    {Capability: "ui.styling", Status: StatusCovered, Replacement: "github.com/golusoris/sveltesentio", Notes: "Utility-first CSS styling engine"},
		"clsx":           {Capability: "ui.styling", Status: StatusCovered, Replacement: "github.com/golusoris/sveltesentio", Notes: "Class name construction helper"},
		"tailwind-merge": {Capability: "ui.styling", Status: StatusCovered, Replacement: "github.com/golusoris/sveltesentio", Notes: "Conflict-free Tailwind class merger"},
		"lucide-svelte":  {Capability: "ui.icons", Status: StatusCovered, Replacement: "github.com/golusoris/sveltesentio/icons", Notes: "Clean SVG icons for Svelte"},
		"@lucide/svelte": {Capability: "ui.icons", Status: StatusCovered, Replacement: "github.com/golusoris/sveltesentio/icons", Notes: "Scoped Lucide SVG icons"},
		"bits-ui":        {Capability: "ui.components", Status: StatusCovered, Replacement: "github.com/golusoris/sveltesentio/components", Notes: "Headless primitives for Svelte"},
		"shadcn-svelte":  {Capability: "ui.components", Status: StatusCovered, Replacement: "github.com/golusoris/sveltesentio/components", Notes: "Accessible styled UI components"},
		"zod":            {Capability: "ui.forms", Status: StatusCovered, Replacement: "github.com/golusoris/sveltesentio/forms", Notes: "TypeScript schema validation with type inference"},
		"svelte-sonner":  {Capability: "ui.toast", Status: StatusCovered, Replacement: "github.com/golusoris/sveltesentio/toast", Notes: "Toast notification component"},
		"axios":          {Capability: "http.client", Status: StatusAdapterAvailable, Replacement: "github.com/golusoris/sveltesentio/fetch", Notes: "HTTP client; migrate to native fetch with SvelteSentio interceptors"},
	}
	m, ok := nodeMappings[pkg]
	return m, ok
}

func lookupPythonCatalog(pkg string) (CatalogMapping, bool) {
	pythonMappings := map[string]CatalogMapping{
		"fastapi":    {Capability: "http.router", Status: StatusCovered, Replacement: "github.com/golusoris/pykit/httpx", Notes: "Async web framework for building APIs"},
		"pydantic":   {Capability: "data.validation", Status: StatusCovered, Replacement: "github.com/golusoris/pykit/schema", Notes: "Data validation and settings management"},
		"httpx":      {Capability: "http.client", Status: StatusCovered, Replacement: "github.com/golusoris/pykit/client", Notes: "Async HTTP client for Python"},
		"requests":   {Capability: "http.client", Status: StatusCovered, Replacement: "github.com/golusoris/pykit/client", Notes: "HTTP library; migrate to PyKit async client"},
		"redis":      {Capability: "cache.redis", Status: StatusCovered, Replacement: "github.com/golusoris/pykit/cache", Notes: "Redis in-memory data store client"},
		"sqlalchemy": {Capability: "db.orm", Status: StatusCovered, Replacement: "github.com/golusoris/pykit/db", Notes: "Python SQL toolkit and Object Relational Mapper"},
		"asyncpg":    {Capability: "db.postgres", Status: StatusCovered, Replacement: "github.com/golusoris/pykit/db", Notes: "Fast PostgreSQL driver for Python asyncio"},
		"click":      {Capability: "clikit", Status: StatusCovered, Replacement: "github.com/golusoris/pykit/cli", Notes: "Composable command line interface kit"},
		"litellm":    {Capability: "ai.llm_client", Status: StatusCovered, Replacement: "github.com/golusoris/pykit/ai", Notes: "Unified multi-provider LLM gateway client"},
	}
	m, ok := pythonMappings[pkg]
	return m, ok
}

func lookupRustCatalog(pkg string) (CatalogMapping, bool) {
	rustMappings := map[string]CatalogMapping{
		"tokio":   {Capability: "runtime.async", Status: StatusCovered, Replacement: "github.com/golusoris/rustkit/runtime", Notes: "Asynchronous runtime for Rust"},
		"serde":   {Capability: "data.serialization", Status: StatusCovered, Replacement: "github.com/golusoris/rustkit/serde", Notes: "Generic serialization/deserialization framework"},
		"axum":    {Capability: "http.router", Status: StatusCovered, Replacement: "github.com/golusoris/rustkit/http", Notes: "Ergonomic and modular web framework"},
		"reqwest": {Capability: "http.client", Status: StatusCovered, Replacement: "github.com/golusoris/rustkit/client", Notes: "Higher level HTTP client library"},
		"clap":    {Capability: "clikit", Status: StatusCovered, Replacement: "github.com/golusoris/rustkit/cli", Notes: "Command Line Argument Parser for Rust"},
		"tracing": {Capability: "telemetry.logging", Status: StatusCovered, Replacement: "github.com/golusoris/rustkit/tracing", Notes: "Application-level tracing and diagnostic instrumentation"},
	}
	m, ok := rustMappings[pkg]
	return m, ok
}

func lookupNativeCatalog(pkg string) (CatalogMapping, bool) {
	nativeMappings := map[string]CatalogMapping{
		"libavcodec":  {Capability: "media.ffmpeg", Status: StatusCovered, Replacement: "github.com/golusoris/template-native-gpu/ffmpeg", Notes: "FFmpeg audio/video decoding and encoding library"},
		"libavformat": {Capability: "media.ffmpeg", Status: StatusCovered, Replacement: "github.com/golusoris/template-native-gpu/ffmpeg", Notes: "FFmpeg container demuxing and muxing library"},
		"libavfilter": {Capability: "media.ffmpeg", Status: StatusCovered, Replacement: "github.com/golusoris/template-native-gpu/ffmpeg", Notes: "FFmpeg audio/video graph filtering library"},
		"libvmaf":     {Capability: "media.vmafx", Status: StatusCovered, Replacement: "github.com/golusoris/template-native-gpu/vmafx", Notes: "VMAFx video quality metric engine"},
		"cuda":        {Capability: "gpu.cuda", Status: StatusCovered, Replacement: "github.com/golusoris/template-native-gpu/cuda", Notes: "NVIDIA CUDA compute acceleration library"},
		"vulkan":      {Capability: "gpu.vulkan", Status: StatusCovered, Replacement: "github.com/golusoris/template-native-gpu/vulkan", Notes: "Cross-platform 3D graphics and compute API"},
		"opencl":      {Capability: "gpu.opencl", Status: StatusCovered, Replacement: "github.com/golusoris/template-native-gpu/opencl", Notes: "Heterogeneous parallel computing framework"},
	}
	m, ok := nativeMappings[pkg]
	return m, ok
}

func cleanDepKey(pkg string) string {
	replacer := strings.NewReplacer("@", "", "/", "_", ".", "_", "-", "_")
	return replacer.Replace(pkg)
}
