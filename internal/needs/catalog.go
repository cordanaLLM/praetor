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
		Package:              "github.com/spf13/viper",
		Capability:           "config.loader",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/config",
		Notes:                "Hierarchical configuration with environment override",
	},

	// Telemetry & Observability
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
		Package:              "go.uber.org/fx",
		Capability:           "clikit.ioc",
		Status:               StatusCovered,
		GolusorisReplacement: "github.com/golusoris/golusoris/clikit",
		Notes:                "Dependency injection container mapped to Golusoris clikit",
	},
}

// MatchPackage searches the catalog for the best prefix match for a given import path.
func MatchPackage(importPath string) (CatalogEntry, bool) {
	var bestMatch CatalogEntry
	longestPrefix := 0

	for _, entry := range CanonicalCatalog {
		if strings.HasPrefix(importPath, entry.Package) {
			if len(entry.Package) > longestPrefix {
				longestPrefix = len(entry.Package)
				bestMatch = entry
			}
		}
	}

	return bestMatch, longestPrefix > 0
}

// MapCapabilityToReplacement returns the default replacement package for a capability.
func MapCapabilityToReplacement(capKey CapabilityKey) string {
	for _, entry := range CanonicalCatalog {
		if entry.Capability == capKey && entry.Status == StatusCovered {
			return entry.GolusorisReplacement
		}
	}
	return ""
}
