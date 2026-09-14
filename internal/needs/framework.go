package needs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cordanallm/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

const (
	defaultFrameworkModule = "github.com/golusoris/golusoris"
	// CapabilitiesFile is the framework's machine-readable capability contract
	// (schema: golusoris core/capabilities, version 1).
	CapabilitiesFile = "capabilities.yaml"
	// FrameworkPathEnv overrides framework discovery for every needs subcommand.
	FrameworkPathEnv = "GOLUSORIS_PATH"
	// UnknownVersion is reported when neither git nor go can name the version.
	UnknownVersion   = "unknown"
	frameworkTimeout = 20 * time.Second
)

// KnownDomainCapabilities is the offline fallback: golusoris root directories
// mapped to capability keys. Used only when no capabilities.yaml is reachable.
var KnownDomainCapabilities = map[string][]CapabilityKey{
	"core":          {"config.loader", "config.yaml", "telemetry.logging", "time.clock", "errors.typed", "crypto.password", "crypto.receipt", "id.uuid", "validate.struct", "build.version", "clikit.cli", "clikit.cobra", "clikit.ioc", "mcp.server", "git.worktree", "ast.analyzer", "needs.capabilities"},
	"db":            {"db.postgres", "db.orm", "db.clickhouse", "db.timescale", "db.cdc", "db.sqlite", "db.migrate"},
	"cache":         {"cache.redis", "cache.inmemory", "cache.twotier"},
	"httpx":         {"http.router", "http.middleware", "http.server", "http.client", "http.cors"},
	"jobs":          {"jobs.queue", "jobs.cron", "jobs.workflow"},
	"auth":          {"auth.jwt", "auth.oidc", "auth.apikey", "auth.session", "auth.passkeys"},
	"pubsub":        {"pubsub.nats", "pubsub.kafka"},
	"otel":          {"telemetry.otel"},
	"observability": {"telemetry.sentry", "telemetry.profiling"},
	"k8s":           {"telemetry.prometheus", "k8s.client", "k8s.health", "k8s.operator"},
	"clikit":        {"clikit.tui"},
	"ebpf":          {"kernel.ebpf"},
	"apidocs":       {"http.openapi"},
	"storage":       {"storage.bucket", "storage.s3", "storage.tus"},
	"ai":            {"ai.llm_client", "ai.embeddings"},
	"realtime":      {"realtime.sse", "realtime.webrtc"},
	"idempotency":   {"http.idempotency"},
	"notify":        {"notify.core"},
	"secrets":       {"security.secrets"},
	"grpc":          {"grpc.server"},
	"testutil":      {"test.assert"},
}

// capabilitiesDoc mirrors golusoris core/capabilities.Index (schema v1).
type capabilitiesDoc struct {
	Version   int      `yaml:"version"`
	Framework string   `yaml:"framework"`
	Modules   []string `yaml:"modules"`
	Packages  []struct {
		Import       string   `yaml:"import"`
		Module       string   `yaml:"module"`
		Domain       string   `yaml:"domain"`
		Capabilities []string `yaml:"capabilities"`
		Description  string   `yaml:"description"`
		Status       string   `yaml:"status"`
		Replaces     []string `yaml:"replaces"`
	} `yaml:"packages"`
}

// ResolveFrameworkPath picks the local framework checkout: the explicit flag
// value, then $GOLUSORIS_PATH, then the module cache (`go list -m`), else "".
func ResolveFrameworkPath(ctx context.Context, explicit string) string {
	if explicit != "" && util.DirExists(explicit) {
		return explicit
	}
	if env := os.Getenv(FrameworkPathEnv); env != "" && util.DirExists(env) {
		return env
	}
	cctx, cancel := context.WithTimeout(ctx, frameworkTimeout)
	defer cancel()
	out, err := exec.CommandContext(cctx, "go", "list", "-m", "-f", "{{.Dir}}", defaultFrameworkModule).Output()
	if err == nil {
		if dir := strings.TrimSpace(string(out)); dir != "" && util.DirExists(dir) {
			return dir
		}
	}
	return ""
}

// InspectFramework builds the capability index for the framework at
// frameworkPath. It prefers the framework's own capabilities.yaml contract and
// falls back to directory heuristics (KnownDomainCapabilities) when the file is
// absent, so offline runs still work.
func InspectFramework(ctx context.Context, frameworkPath string) (*FrameworkIndex, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	index := &FrameworkIndex{
		Name:         defaultFrameworkModule,
		RootPath:     frameworkPath,
		Version:      UnknownVersion,
		Packages:     make(map[string]FrameworkPackage),
		Capabilities: make(map[CapabilityKey][]string),
		Replacements: make(map[string]string),
	}
	if frameworkPath == "" || !util.DirExists(frameworkPath) {
		populateDefaultFrameworkIndex(index)
		return index, nil
	}
	index.Version = frameworkVersion(ctx, frameworkPath)

	contract := filepath.Join(frameworkPath, CapabilitiesFile)
	if util.FileExists(contract) {
		if err := loadCapabilitiesContract(contract, index); err != nil {
			return nil, err
		}
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

// loadCapabilitiesContract fills index from a capabilities.yaml document.
func loadCapabilitiesContract(path string, index *FrameworkIndex) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var doc capabilitiesDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.Version != 1 {
		return fmt.Errorf("%s: unsupported schema version %d", path, doc.Version)
	}
	if doc.Framework != "" {
		index.Name = doc.Framework
	}
	index.Modules = append([]string{}, doc.Modules...)
	for _, p := range doc.Packages {
		caps := make([]CapabilityKey, 0, len(p.Capabilities))
		for _, c := range p.Capabilities {
			caps = append(caps, CapabilityKey(c))
		}
		index.Packages[p.Import] = FrameworkPackage{
			ImportPath:   p.Import,
			Module:       p.Module,
			Domain:       p.Domain,
			Capabilities: caps,
			Description:  p.Description,
			Replaces:     append([]string{}, p.Replaces...),
		}
		for _, c := range caps {
			index.Capabilities[c] = appendUnique(index.Capabilities[c], p.Import)
		}
		for _, r := range p.Replaces {
			index.Replacements[r] = p.Import
		}
	}
	for c := range index.Capabilities {
		sort.Strings(index.Capabilities[c])
	}
	return nil
}

// frameworkVersion asks git for the nearest tag, then falls back to the
// module version recorded by `go list -m`.
func frameworkVersion(ctx context.Context, frameworkPath string) string {
	cctx, cancel := context.WithTimeout(ctx, frameworkTimeout)
	defer cancel()
	git := exec.CommandContext(cctx, "git", "-C", frameworkPath, "describe", "--tags", "--abbrev=0")
	if out, err := git.Output(); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			return v
		}
	}
	golist := exec.CommandContext(cctx, "go", "list", "-m", "-f", "{{.Version}}", defaultFrameworkModule)
	golist.Dir = frameworkPath
	if out, err := golist.Output(); err == nil {
		if v := strings.TrimSpace(string(out)); v != "" {
			return v
		}
	}
	return UnknownVersion
}

// processDomainDir registers a domain directory into the framework index
// (heuristic fallback when capabilities.yaml is absent).
func processDomainDir(domain string, index *FrameworkIndex) {
	pkgPath := defaultFrameworkModule + "/" + domain
	caps, found := KnownDomainCapabilities[domain]
	if !found {
		caps = []CapabilityKey{CapabilityKey(domain + ".core")}
	}
	index.Packages[pkgPath] = FrameworkPackage{ImportPath: pkgPath, Domain: domain, Capabilities: caps}
	for _, c := range caps {
		index.Capabilities[c] = append(index.Capabilities[c], pkgPath)
	}
}

// populateDefaultFrameworkIndex supplies the static baseline index when offline.
func populateDefaultFrameworkIndex(index *FrameworkIndex) {
	for domain := range KnownDomainCapabilities {
		processDomainDir(domain, index)
	}
	// Offline mode keeps Replacements empty on purpose: dependency
	// classification then falls through to the static catalog, which carries
	// the capability key for each entry.
	for _, entry := range CanonicalCatalog {
		if entry.Status == StatusCovered && entry.GolusorisReplacement != "" {
			index.Capabilities[entry.Capability] = appendUnique(index.Capabilities[entry.Capability], entry.GolusorisReplacement)
		}
	}
}

// IsCapabilityCovered checks if the framework provides an implementation for capKey.
func (idx *FrameworkIndex) IsCapabilityCovered(capKey CapabilityKey) bool {
	pkgs, ok := idx.Capabilities[capKey]
	return ok && len(pkgs) > 0
}

// majorElementRE matches a Go major-version path element (v2, v10).
var majorElementRE = regexp.MustCompile(`^v[0-9]+$`)

// stripMajor removes major-version path elements so github.com/a/b/v2/x and
// github.com/a/b/v3 compare as the same library.
func stripMajor(p string) string {
	parts := strings.Split(p, "/")
	out := parts[:0]
	for _, e := range parts {
		if !majorElementRE.MatchString(e) {
			out = append(out, e)
		}
	}
	return strings.Join(out, "/")
}

// ResolveReplacement finds the framework package that supersedes importPath
// using longest-prefix matching at "/" boundaries over the contract's
// `replaces` entries, ignoring major-version path elements on both sides. It
// returns the framework import, the capability keys that package provides,
// and whether a match was found.
func (idx *FrameworkIndex) ResolveReplacement(importPath string) (string, []CapabilityKey, bool) {
	needle := stripMajor(importPath)
	best, bestLen := "", -1
	for old := range idx.Replacements {
		key := stripMajor(old)
		if (needle == key || strings.HasPrefix(needle, key+"/")) && len(key) > bestLen {
			best, bestLen = old, len(key)
		}
	}
	if bestLen < 0 {
		return "", nil, false
	}
	target := idx.Replacements[best]
	return target, idx.Packages[target].Capabilities, true
}
