// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
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
	// UIInputLatencyEnv is the environment variable carrying ui.input_latency_ms to Playwright.
	UIInputLatencyEnv = "PRAETOR_UI_INPUT_LATENCY_MS"
)

// UIPolicy is the manifest's ui section (order engine-fr-rule-checks-20261027): the
// input-to-next-frame latency threshold in milliseconds (input_latency_ms, 1..10000, required
// for UI repositories) and the route paths scanned by the generated accessibility and
// responsiveness gates (routes, defaulting to ["/"]).
type UIPolicy struct {
	InputLatencyMs int      `yaml:"input_latency_ms,omitempty"`
	Routes         []string `yaml:"routes,omitempty"`
	hasLatency     bool
}

// UnmarshalYAML decodes the manifest's ui section, refusing unknown or duplicate keys,
// non-integer latency values, out-of-range input_latency_ms values, and invalid routes.
func (p *UIPolicy) UnmarshalYAML(node *yaml.Node) error {
	var policy UIPolicy
	present, err := registerIntFields(node, "ui", map[string]*int{
		"input_latency_ms": &policy.InputLatencyMs,
		"routes":           nil,
	})
	if err != nil {
		return err
	}
	policy.hasLatency = present["input_latency_ms"]
	if present["routes"] {
		routes, routeErr := decodeUIRoutes(node)
		if routeErr != nil {
			return routeErr
		}
		policy.Routes = routes
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

// Validate checks a UIPolicy constructed in Go or decoded from YAML, requiring
// input_latency_ms in 1..10000 and valid bounded routes.
func (p *UIPolicy) Validate() error {
	if p == nil {
		return fmt.Errorf("ui.input_latency_ms is required (integer from %d to %d)", MinUIInputLatencyMs, MaxUIInputLatencyMs)
	}
	if err := validateDeclaredBound("ui", "input_latency_ms", &p.InputLatencyMs, MinUIInputLatencyMs, MaxUIInputLatencyMs); err != nil {
		return err
	}
	return validateUIRoutes(p.Routes)
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
	return validateUIRoutes(p.Routes)
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

// EffectiveRoutes returns the configured UI routes, or ["/"] when none are declared.
func (p *UIPolicy) EffectiveRoutes() []string {
	if p == nil || len(p.Routes) == 0 {
		return []string{DefaultUIRoute}
	}
	return slices.Clone(p.Routes)
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
