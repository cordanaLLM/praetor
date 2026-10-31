// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Bounds and defaults of the manifest's ui section (HISS-02, order engine-fr-rule-checks-20261027).
const (
	// MinUIInputLatencyMs is the minimum allowed input-to-next-frame latency threshold in ms.
	MinUIInputLatencyMs = 1
	// MaxUIInputLatencyMs is the maximum allowed input-to-next-frame latency threshold in ms.
	MaxUIInputLatencyMs = 10000
	// MaxUIRoutes bounds the number of routes declared in ui.routes (HISS-02).
	MaxUIRoutes = 64
	// MaxUIRouteBytes bounds the byte length of a single route path in ui.routes (HISS-02).
	MaxUIRouteBytes = 256
	// DefaultUIRoute is the route path scanned when ui.routes is omitted or empty.
	DefaultUIRoute = "/"
	// MaxUIAPIPatternBytes bounds the byte length of ui.api_pattern (HISS-02).
	MaxUIAPIPatternBytes = 256
	// DefaultUIAPIPattern is the route mock glob used when ui.api_pattern is omitted or empty.
	DefaultUIAPIPattern = "**/api/**"
	// UIInputLatencyEnv is the environment variable carrying ui.input_latency_ms to Playwright.
	UIInputLatencyEnv = "PRAETOR_UI_INPUT_LATENCY_MS"
)

// UIPolicy is the manifest's ui section (order engine-fr-rule-checks-20261027): the
// input-to-next-frame latency threshold in milliseconds (input_latency_ms, 1..10000, required
// for UI repositories), the route paths scanned by the generated accessibility and
// responsiveness gates (routes, defaulting to ["/"]), and the route mock glob for the
// responsiveness spec's slow-API and HTTP 503 cases (api_pattern, defaulting to "**/api/**").
// ScreenMatrix names the A11Y-CHECK1 data file; ui-gates requires it for UI repositories.
// InputLatencySource is the pinned reference `<owner>/<repo>@<40-hex commit>:<path>` to measurement evidence.
type UIPolicy struct {
	InputLatencyMs     int      `yaml:"input_latency_ms,omitempty"`
	InputLatencySource string   `yaml:"input_latency_source,omitempty"`
	Routes             []string `yaml:"routes,omitempty"`
	APIPattern         string   `yaml:"api_pattern,omitempty"`
	ScreenMatrix       string   `yaml:"screen_matrix,omitempty"`
	hasLatency         bool
	hasLatencySource   bool
	hasAPIPattern      bool
	hasScreenMatrix    bool
}

// UnmarshalYAML decodes the manifest's ui section, refusing unknown or duplicate keys,
// non-integer latency values, out-of-range input_latency_ms values, and invalid routes or
// api_pattern globs or screen_matrix paths.
func (p *UIPolicy) UnmarshalYAML(node *yaml.Node) error {
	var policy UIPolicy
	present, err := registerIntFields(node, "ui", map[string]*int{
		"input_latency_ms":     &policy.InputLatencyMs,
		"input_latency_source": nil,
		"routes":               nil,
		"api_pattern":          nil,
		"screen_matrix":        nil,
	})
	if err != nil {
		return err
	}
	policy.hasLatency = present["input_latency_ms"]
	policy.hasLatencySource = present["input_latency_source"]
	policy.hasAPIPattern = present["api_pattern"]
	policy.hasScreenMatrix = present["screen_matrix"]
	if present["input_latency_source"] {
		src, srcErr := decodeUIString(node, "input_latency_source")
		if srcErr != nil {
			return srcErr
		}
		policy.InputLatencySource = src
	}
	if present["routes"] {
		routes, routeErr := decodeUIRoutes(node)
		if routeErr != nil {
			return routeErr
		}
		policy.Routes = routes
	}
	if present["api_pattern"] {
		pattern, patternErr := decodeUIString(node, "api_pattern")
		if patternErr != nil {
			return patternErr
		}
		policy.APIPattern = pattern
	}
	if present["screen_matrix"] {
		matrix, matrixErr := decodeUIString(node, "screen_matrix")
		if matrixErr != nil {
			return matrixErr
		}
		policy.ScreenMatrix = matrix
	}
	if err := validateUIPolicy(&policy); err != nil {
		return err
	}
	*p = policy
	return nil
}

func decodeUIRoutes(node *yaml.Node) ([]string, error) {
	for i := 0; i+1 < len(node.Content) && i < maxPolicyNodes; i += 2 {
		if node.Content[i].Value != "routes" {
			continue
		}
		seq := node.Content[i+1]
		if seq.Kind != yaml.SequenceNode {
			return nil, fmt.Errorf("ui.routes must be a sequence of route paths")
		}
		if len(seq.Content) > MaxUIRoutes {
			return nil, fmt.Errorf("ui.routes has %d entries; maximum is %d", len(seq.Content), MaxUIRoutes)
		}
		routes := make([]string, 0, len(seq.Content))
		for j := 0; j < len(seq.Content) && j < MaxUIRoutes; j++ {
			item := seq.Content[j]
			if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
				return nil, fmt.Errorf("ui.routes[%d] must be a string path", j)
			}
			routes = append(routes, item.Value)
		}
		return routes, nil
	}
	return nil, nil
}

func decodeUIString(node *yaml.Node, key string) (string, error) {
	for i := 0; i+1 < len(node.Content) && i < maxPolicyNodes; i += 2 {
		if node.Content[i].Value != key {
			continue
		}
		val := node.Content[i+1]
		if val.Kind != yaml.ScalarNode || val.Tag != "!!str" {
			return "", fmt.Errorf("ui.%s must be a string", key)
		}
		return val.Value, nil
	}
	return "", nil
}

// Validate checks a UIPolicy constructed in Go or decoded from YAML, requiring
// input_latency_ms in 1..10000 and valid bounded routes and api_pattern.
func (p *UIPolicy) Validate() error {
	if p == nil {
		return fmt.Errorf("ui.input_latency_ms is required (integer from %d to %d)", MinUIInputLatencyMs, MaxUIInputLatencyMs)
	}
	if err := validateDeclaredBound("ui", "input_latency_ms", &p.InputLatencyMs, MinUIInputLatencyMs, MaxUIInputLatencyMs); err != nil {
		return err
	}
	if err := validateUIRoutes(p.Routes); err != nil {
		return err
	}
	return validateUIStrings(p)
}

func validateManifestUI(m *Manifest) error {
	if m == nil {
		return nil
	}
	return validateUIPolicy(m.UI)
}

func validateUIPolicy(p *UIPolicy) error {
	if p == nil {
		return nil
	}
	if p.hasLatency || p.InputLatencyMs != 0 {
		if err := validateDeclaredBound("ui", "input_latency_ms", &p.InputLatencyMs, MinUIInputLatencyMs, MaxUIInputLatencyMs); err != nil {
			return err
		}
	}
	if err := validateUIRoutes(p.Routes); err != nil {
		return err
	}
	return validateUIStrings(p)
}

func validateUIRoutes(routes []string) error {
	if len(routes) > MaxUIRoutes {
		return fmt.Errorf("ui.routes has %d entries; maximum is %d", len(routes), MaxUIRoutes)
	}
	seen := make(map[string]int, len(routes))
	for i := 0; i < len(routes) && i < MaxUIRoutes; i++ {
		if err := validateSingleUIRoute(i, routes[i], seen); err != nil {
			return err
		}
		seen[routes[i]] = i
	}
	return nil
}

func validateSingleUIRoute(index int, route string, seen map[string]int) error {
	switch {
	case route == "":
		return fmt.Errorf("ui.routes[%d] must not be empty", index)
	case len(route) > MaxUIRouteBytes:
		return fmt.Errorf("ui.routes[%d] exceeds %d bytes", index, MaxUIRouteBytes)
	case !strings.HasPrefix(route, "/"):
		return fmt.Errorf("ui.routes[%d] %q must start with '/'", index, route)
	case strings.ContainsAny(route, " \t\r\n\x00"):
		return fmt.Errorf("ui.routes[%d] %q must not contain whitespace or control characters", index, route)
	}
	if first, dup := seen[route]; dup {
		return fmt.Errorf("ui.routes[%d] repeats %q from ui.routes[%d]", index, route, first)
	}
	return nil
}

func validateUIAPIPattern(pattern string, present bool) error {
	if present && pattern == "" {
		return fmt.Errorf("ui.api_pattern must not be empty")
	}
	if pattern == "" {
		return nil
	}
	if len(pattern) > MaxUIAPIPatternBytes {
		return fmt.Errorf("ui.api_pattern exceeds %d bytes", MaxUIAPIPatternBytes)
	}
	if strings.ContainsAny(pattern, " \t\r\n\x00") {
		return fmt.Errorf("ui.api_pattern %q must not contain whitespace or control characters", pattern)
	}
	return nil
}

// EffectiveRoutes returns the configured UI routes, or ["/"] when none are declared.
func (p *UIPolicy) EffectiveRoutes() []string {
	if p == nil || len(p.Routes) == 0 {
		return []string{DefaultUIRoute}
	}
	return slices.Clone(p.Routes)
}

// EffectiveAPIPattern returns the configured API route glob, or "**/api/**" when omitted.
func (p *UIPolicy) EffectiveAPIPattern() string {
	if p == nil || p.APIPattern == "" {
		return DefaultUIAPIPattern
	}
	return p.APIPattern
}

// DeclaredUI returns a copy of the manifest's ui section, or nil when omitted.
func (m *Manifest) DeclaredUI() *UIPolicy {
	if m == nil || m.UI == nil {
		return nil
	}
	out := *m.UI
	out.Routes = slices.Clone(m.UI.Routes)
	return &out
}

// UIRoutes returns the routes the manifest configures for UI gates, defaulting to ["/"].
func (m *Manifest) UIRoutes() []string {
	if m == nil {
		return []string{DefaultUIRoute}
	}
	return m.UI.EffectiveRoutes()
}

// UIAPIPattern returns the API route glob the manifest configures for UI gates, defaulting to
// "**/api/**".
func (m *Manifest) UIAPIPattern() string {
	if m == nil {
		return DefaultUIAPIPattern
	}
	return m.UI.EffectiveAPIPattern()
}

var (
	uiSourceOwnerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	uiSourceRepoPattern  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// validateUIStrings holds optional string fields to their declared contracts.
func validateUIStrings(p *UIPolicy) error {
	if err := validateUIAPIPattern(p.APIPattern, p.hasAPIPattern); err != nil {
		return err
	}
	if p.hasScreenMatrix || p.ScreenMatrix != "" {
		if !ValidRepositoryPath(p.ScreenMatrix) || !filepath.IsLocal(p.ScreenMatrix) || strings.Contains(p.ScreenMatrix, ":") {
			return fmt.Errorf("ui.screen_matrix must be a non-empty repository-relative path of at most %d bytes, confined to the repository", maxRepositoryPath)
		}
	}
	if p.hasLatencySource || p.InputLatencySource != "" {
		if err := validateUIInputLatencySource(p.InputLatencySource); err != nil {
			return err
		}
	}
	return nil
}

func validateUIInputLatencySource(source string) error {
	if !validUIInputLatencySource(source) {
		return fmt.Errorf("ui.input_latency_source must be a pinned reference '<owner>/<repo>@<40-hex commit>:<path>'")
	}
	return nil
}

func validUIInputLatencySource(source string) bool {
	if source == "" || len(source) > maxRepositoryPath*2 {
		return false
	}
	repoCoord, rest, hasAt := strings.Cut(source, "@")
	if !hasAt || !validUISourceRepoCoord(repoCoord) {
		return false
	}
	commit, relPath, hasColon := strings.Cut(rest, ":")
	if !hasColon || len(commit) != 40 || !isLowerHex40(commit) {
		return false
	}
	return validUISourcePath(relPath)
}

func validUISourceRepoCoord(repoCoord string) bool {
	owner, repo, hasSlash := strings.Cut(repoCoord, "/")
	if !hasSlash || strings.Contains(repo, "/") {
		return false
	}
	return validUISourceName(owner, uiSourceOwnerPattern) && validUISourceName(repo, uiSourceRepoPattern)
}

func validUISourceName(name string, pattern *regexp.Regexp) bool {
	if name == "" || name == "." || name == ".." || len(name) > 100 {
		return false
	}
	return pattern.MatchString(name)
}

func isLowerHex40(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isLowerHexChar(s[i]) {
			return false
		}
	}
	return true
}

func isLowerHexChar(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
}

func validUISourcePath(p string) bool {
	if p == "" || len(p) > maxRepositoryPath || strings.HasPrefix(p, "/") || strings.Contains(p, "..") {
		return false
	}
	if strings.ContainsAny(p, "\\\x00\r\n \t") {
		return false
	}
	return path.Clean(p) == p && p != "."
}
