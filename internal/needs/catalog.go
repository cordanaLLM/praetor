package needs

import (
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// CatalogEntry classifies a known Go library into a standard capability. It names no
// framework: which framework package replaces, adapts, wraps or retains the library is
// declared by the operator's framework contract (framework.targets.<lang>.contract,
// ADR-0014 §6), never by this catalog.
type CatalogEntry struct {
	Package    string
	Capability CapabilityKey
	Notes      string
}

// CapabilityCatalog classifies well-known Go libraries, and the standard-library imports the
// scan records, into capabilities. A library it does not list is a custom.* capability.
var CapabilityCatalog = []CatalogEntry{
	// Database
	{Package: "github.com/jackc/pgx", Capability: "db.postgres", Notes: "Direct PostgreSQL driver and pool integration"},
	{Package: "github.com/uptrace/bun", Capability: "db.orm", Notes: "Idiomatic Go SQL-first query builder"},
	{Package: "github.com/ClickHouse/clickhouse-go", Capability: "db.clickhouse", Notes: "High-performance analytical column-store database"},
	{Package: "github.com/jmoiron/sqlx", Capability: "db.sqlx", Notes: "database/sql extensions for struct scanning and named queries"},
	{Package: "gorm.io/gorm", Capability: "db.orm", Notes: "ORM with model-based schema definitions"},
	{Package: "github.com/lib/pq", Capability: "db.postgres", Notes: "PostgreSQL driver for database/sql, in maintenance mode"},

	// Cache
	{Package: "github.com/redis/rueidis", Capability: "cache.redis", Notes: "Fast auto-pipelining Redis client"},
	{Package: "github.com/redis/go-redis", Capability: "cache.redis", Notes: "Redis client"},
	{Package: "github.com/patrickmn/go-cache", Capability: "cache.inmemory", Notes: "In-process concurrent cache with TTL"},

	// HTTP & Routing
	{Package: "github.com/ogen-go/ogen", Capability: "http.openapi",
		Notes: "OpenAPI code generator with runtime packages. A module dependency alone does not prove generator execution."},
	{Package: "github.com/go-chi/chi", Capability: "http.router", Notes: "Composable net/http middleware and router"},
	{Package: "github.com/gin-gonic/gin", Capability: "http.router", Notes: "Fast HTTP router and web framework"},
	{Package: "github.com/swaggo/swag", Capability: "http.openapi", Notes: "Swagger/OpenAPI spec generation and UI serving"},
	{Package: "github.com/swaggo/gin-swagger", Capability: "http.openapi", Notes: "Swagger UI serving for gin"},
	{Package: "github.com/getkin/kin-openapi", Capability: "http.openapi", Notes: "OpenAPI 3.0 validation and specification parser"},

	// Jobs & Queuing
	{Package: "github.com/hibiken/asynq", Capability: "jobs.queue", Notes: "Redis-backed distributed task queue"},
	{Package: "github.com/riverqueue/river", Capability: "jobs.queue", Notes: "PostgreSQL-backed job queue"},
	{Package: "github.com/robfig/cron", Capability: "jobs.cron", Notes: "Scheduled recurring task manager"},

	// PubSub & Messaging
	{Package: "github.com/nats-io/nats.go", Capability: "pubsub.nats", Notes: "NATS Core and JetStream messaging client"},
	{Package: "github.com/confluentinc/confluent-kafka-go", Capability: "pubsub.kafka", Notes: "High-throughput Kafka streaming client"},

	// Auth & Security
	{Package: "github.com/golang-jwt/jwt", Capability: "auth.jwt", Notes: "Secure HMAC/RSA/Ed25519 token issuance and verification"},
	{Package: "github.com/coreos/go-oidc", Capability: "auth.oidc", Notes: "OpenID Connect verification and claims extraction"},

	// Configuration
	{Package: "github.com/knadh/koanf/v2", Capability: "config.loader", Notes: "Modular configuration engine"},
	{Package: "github.com/spf13/viper", Capability: "config.loader", Notes: "Hierarchical configuration with environment override"},

	// Telemetry & Observability
	{Package: "log/slog", Capability: "telemetry.logging", Notes: "Standard-library structured logging API"},
	{Package: "github.com/lmittmann/tint", Capability: "telemetry.logging", Notes: "Colourised slog handler"},
	{Package: "go.opentelemetry.io/otel", Capability: "telemetry.otel", Notes: "OpenTelemetry trace, metric, and baggage context propagation"},
	{Package: "github.com/prometheus/client_golang", Capability: "telemetry.prometheus", Notes: "Prometheus metrics collector and scraping handler"},
	{Package: "go.uber.org/zap", Capability: "telemetry.logging", Notes: "High-performance structured JSON logging"},
	{Package: "github.com/sirupsen/logrus", Capability: "telemetry.logging", Notes: "Structured logging with hooks and formatters"},

	// CLI & Terminal
	{Package: "github.com/spf13/cobra", Capability: "clikit.cobra", Notes: "Command line interface framework with flags"},
	{Package: "github.com/charmbracelet/bubbletea", Capability: "clikit.tui", Notes: "The Elm Architecture terminal user interface"},

	// Identifiers
	{Package: "github.com/google/uuid", Capability: "id.uuid", Notes: "V4 and V7 UUID generation"},

	// eBPF
	{Package: "github.com/cilium/ebpf", Capability: "kernel.ebpf", Notes: "Kernel tracing and XDP/TC networking hooks"},

	// Testing & Inversion of Control
	{Package: "github.com/stretchr/testify", Capability: "test.assert", Notes: "Test assertions and mocks"},
	{Package: "go.uber.org/fx", Capability: "runtime.di", Notes: "Dependency injection and application lifecycle"},

	// Serialization & Configuration Formats
	{Package: "gopkg.in/yaml.v3", Capability: "config.yaml", Notes: "YAML parser and serializer"},
	{Package: "gopkg.in/yaml.v2", Capability: "config.yaml", Notes: "Legacy YAML v2 parser and serializer"},

	// Model Context Protocol (MCP)
	{Package: "github.com/mark3labs/mcp-go", Capability: "mcp.server", Notes: "Community Model Context Protocol server library"},
	{Package: "github.com/modelcontextprotocol/go-sdk", Capability: "mcp.server", Notes: "Official Model Context Protocol Go SDK"},
}

// MatchPackage searches the catalog for the longest module-path match for a given import
// path.
//
// The match is anchored on a path boundary: an entry matches the import path itself or a
// package inside it, never a different module that merely starts with the same
// characters. Without the boundary, "github.com/uptrace/bunrouter" would inherit the
// "github.com/uptrace/bun" classification. The boundary is util.ModuleImportDir.
func MatchPackage(importPath string) (CatalogEntry, bool) {
	var bestMatch CatalogEntry
	longestPrefix := 0

	for _, entry := range CapabilityCatalog {
		if _, inside := util.ModuleImportDir(importPath, entry.Package); !inside {
			continue
		}
		if len(entry.Package) > longestPrefix {
			longestPrefix = len(entry.Package)
			bestMatch = entry
		}
	}

	return bestMatch, longestPrefix > 0
}

// CatalogMapping classifies a non-Go dependency into a standard capability. Like
// CatalogEntry it names no framework.
type CatalogMapping struct {
	Capability CapabilityKey
	Notes      string
}

// nodeCatalog classifies well-known npm packages.
var nodeCatalog = map[string]CatalogMapping{
	"svelte":         {Capability: "ui.framework", Notes: "Core Svelte reactive UI framework"},
	"@sveltejs/kit":  {Capability: "ui.framework", Notes: "SvelteKit application framework"},
	"tailwindcss":    {Capability: "ui.styling", Notes: "Utility-first CSS styling engine"},
	"clsx":           {Capability: "ui.styling", Notes: "Class name construction helper"},
	"tailwind-merge": {Capability: "ui.styling", Notes: "Conflict-free Tailwind class merger"},
	"lucide-svelte":  {Capability: "ui.icons", Notes: "Clean SVG icons for Svelte"},
	"@lucide/svelte": {Capability: "ui.icons", Notes: "Scoped Lucide SVG icons"},
	"bits-ui":        {Capability: "ui.components", Notes: "Headless primitives for Svelte"},
	"shadcn-svelte":  {Capability: "ui.components", Notes: "Accessible styled UI components"},
	"zod":            {Capability: "ui.forms", Notes: "TypeScript schema validation with type inference"},
	"svelte-sonner":  {Capability: "ui.toast", Notes: "Toast notification component"},
	"axios":          {Capability: "http.client", Notes: "Promise-based HTTP client"},
}

// pythonCatalog classifies well-known PyPI packages, keyed in lower case.
var pythonCatalog = map[string]CatalogMapping{
	"fastapi":    {Capability: "http.router", Notes: "Async web framework for building APIs"},
	"pydantic":   {Capability: "data.validation", Notes: "Data validation and settings management"},
	"httpx":      {Capability: "http.client", Notes: "Async HTTP client for Python"},
	"requests":   {Capability: "http.client", Notes: "Synchronous HTTP library"},
	"redis":      {Capability: "cache.redis", Notes: "Redis in-memory data store client"},
	"sqlalchemy": {Capability: "db.orm", Notes: "Python SQL toolkit and Object Relational Mapper"},
	"asyncpg":    {Capability: "db.postgres", Notes: "Fast PostgreSQL driver for Python asyncio"},
	"click":      {Capability: "clikit.cli", Notes: "Composable command line interface kit"},
	"litellm":    {Capability: "ai.llm_client", Notes: "Unified multi-provider LLM gateway client"},
}

// rustCatalog classifies well-known Cargo crates, keyed in lower case.
var rustCatalog = map[string]CatalogMapping{
	"tokio":   {Capability: "runtime.async", Notes: "Asynchronous runtime for Rust"},
	"serde":   {Capability: "data.serialization", Notes: "Generic serialization/deserialization framework"},
	"axum":    {Capability: "http.router", Notes: "Ergonomic and modular web framework"},
	"reqwest": {Capability: "http.client", Notes: "Higher level HTTP client library"},
	"clap":    {Capability: "clikit.cli", Notes: "Command Line Argument Parser for Rust"},
	"tracing": {Capability: "telemetry.logging", Notes: "Application-level tracing and diagnostic instrumentation"},
}

// nativeCatalog classifies well-known native libraries, keyed in lower case: CMake's
// canonical module names are capitalised (CUDA, Vulkan, OpenCL).
var nativeCatalog = map[string]CatalogMapping{
	"libavcodec":  {Capability: "media.ffmpeg", Notes: "FFmpeg audio/video decoding and encoding library"},
	"libavformat": {Capability: "media.ffmpeg", Notes: "FFmpeg container demuxing and muxing library"},
	"libavfilter": {Capability: "media.ffmpeg", Notes: "FFmpeg audio/video graph filtering library"},
	"libvmaf":     {Capability: "media.vmaf", Notes: "VMAF perceptual video quality metric library"},
	"cuda":        {Capability: "gpu.cuda", Notes: "NVIDIA CUDA compute acceleration library"},
	"vulkan":      {Capability: "gpu.vulkan", Notes: "Cross-platform 3D graphics and compute API"},
	"opencl":      {Capability: "gpu.opencl", Notes: "Heterogeneous parallel computing framework"},
}

// manifestClassifier classifies the dependencies one non-Go analyzer reads from its
// manifests: a catalog hit takes the catalog's capability, anything else an external
// capability under the language's prefix.
type manifestClassifier struct {
	language, ecosystem string
	catalog             map[string]CatalogMapping
	// foldLookup looks packages up in lower case; foldExternal also lower-cases the
	// external capability key.
	foldLookup, foldExternal bool
	// externalPrefix and externalNote classify a package the catalog does not list.
	externalPrefix, externalNote string
}

var (
	nodeClassifier = manifestClassifier{language: "typescript", ecosystem: "npm", catalog: nodeCatalog,
		externalPrefix: "ui.external.", externalNote: "External npm dependency requiring a target framework adapter or evaluation"}
	pythonClassifier = manifestClassifier{language: "python", ecosystem: "pypi", catalog: pythonCatalog, foldLookup: true,
		externalPrefix: "python.external.", externalNote: "External PyPI dependency requiring a target framework adapter or evaluation"}
	rustClassifier = manifestClassifier{language: "rust", ecosystem: "cargo", catalog: rustCatalog, foldLookup: true,
		externalPrefix: "rust.external.", externalNote: "External Cargo crate requiring a target framework adapter or evaluation"}
	nativeClassifier = manifestClassifier{language: "native", ecosystem: "system", catalog: nativeCatalog,
		foldLookup: true, foldExternal: true,
		externalPrefix: "native.external.", externalNote: "Native C/C++/GPU system library requiring a target framework binding"}
)

// classify returns the demand for one manifest dependency, routed to kit (the language
// target's routing kit). The catalog assigns a capability only: every demand starts as a
// gap, and a framework contract decides what covers it (reconcileDependency).
func (c manifestClassifier) classify(pkg, ver, kit string) DependencyDemand {
	demand := DependencyDemand{Package: pkg, Version: ver, Language: c.language, Ecosystem: c.ecosystem,
		Status: StatusGap, TargetBuilderKit: kit}
	lookup, external := pkg, pkg
	if c.foldLookup {
		lookup = strings.ToLower(pkg)
	}
	if c.foldExternal {
		external = strings.ToLower(pkg)
	}
	if mapping, found := c.catalog[lookup]; found {
		demand.Capability, demand.Notes = mapping.Capability, mapping.Notes
		return demand
	}
	demand.Capability, demand.Notes = CapabilityKey(c.externalPrefix+cleanDepKey(external)), c.externalNote
	return demand
}

func cleanDepKey(pkg string) string {
	replacer := strings.NewReplacer("@", "", "/", "_", ".", "_", "-", "_")
	return replacer.Replace(pkg)
}
