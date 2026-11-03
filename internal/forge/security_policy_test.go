// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// securityPolicyTransport keeps setting reads entirely in memory, including error cases.
type securityPolicyTransport func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper without a network connection.
func (f securityPolicyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// TestPrivateVulnerabilityReporting checks actual driver decoding, status handling and
// timeout propagation; absent or malformed enabled fields never become disabled values.
func TestPrivateVulnerabilityReporting(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		want, fail bool
	}{
		{"enabled", `{"enabled":true}`, http.StatusOK, true, false},
		{"disabled", `{"enabled":false}`, http.StatusOK, false, false},
		{"absent", `{}`, http.StatusOK, false, true},
		{"null", `{"enabled":null}`, http.StatusOK, false, true},
		{"wrong type", `{"enabled":"false"}`, http.StatusOK, false, true},
		{"malformed", `{`, http.StatusOK, false, true},
		{"forbidden", `{}`, http.StatusForbidden, false, true},
		{"not found", `{}`, http.StatusNotFound, false, true},
		{"unavailable", `{}`, http.StatusServiceUnavailable, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			driver := NewGitHubDriver("fixture-token", "https://forge.invalid")
			driver.SetRepository("acme", "widgets")
			driver.HTTPClient.Transport = securityPolicyTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/widgets/private-vulnerability-reporting" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if _, bounded := r.Context().Deadline(); !bounded {
					t.Error("request has no context timeout")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{}}, nil
			})
			got, err := driver.PrivateVulnerabilityReporting(t.Context())
			if (err != nil) != tc.fail || got != tc.want {
				t.Fatalf("setting = %v, %v; want %v, failure %v", got, err, tc.want, tc.fail)
			}
		})
	}
}

// TestPrivateVulnerabilityReportingReadError proves transport failures and missing
// repository identities propagate as errors without contacting an external forge.
func TestPrivateVulnerabilityReportingReadError(t *testing.T) {
	driver := NewGitHubDriver("fixture-token", "https://forge.invalid")
	driver.SetRepository("acme", "widgets")
	unavailable := errors.New("network unavailable")
	driver.HTTPClient.Transport = securityPolicyTransport(func(*http.Request) (*http.Response, error) {
		return nil, unavailable
	})
	if _, err := driver.PrivateVulnerabilityReporting(t.Context()); !errors.Is(err, unavailable) {
		t.Fatalf("transport failure = %v", err)
	}
	driver.SetRepository("..", "widgets")
	if _, err := driver.PrivateVulnerabilityReporting(t.Context()); err == nil {
		t.Fatal("invalid repository accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	driver.SetRepository("acme", "widgets")
	driver.HTTPClient.Transport = securityPolicyTransport(func(r *http.Request) (*http.Response, error) {
		return nil, r.Context().Err()
	})
	if _, err := driver.PrivateVulnerabilityReporting(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read = %v", err)
	}
}
