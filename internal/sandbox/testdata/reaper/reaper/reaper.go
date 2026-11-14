// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

// Package reaper exercises upstream session derivation against a stub engine.
// The list/create sequence models testcontainers v0.44.0 reaper.go:311-335;
// bootstrap is the pinned upstream implementation, not a local identity model.
// No Docker client is constructed, so the fixture never loads Docker credentials.
package reaper

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/internal/core/bootstrap"
)

// Start uses the supported session override or upstream's default and creates Ryuk.
func Start(t *testing.T) {
	t.Helper()
	session := os.Getenv("TESTCONTAINERS_SESSION_ID")
	t.Logf("pid=%d ppid=%d bootstrap=%s override=%s", os.Getpid(), os.Getppid(), bootstrap.SessionID(), session)
	if session == "" {
		session = bootstrap.SessionID()
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", os.Getenv("PRAETOR_REAPER_SOCKET"))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	request(t, client, http.MethodGet, "", http.StatusOK)
	request(t, client, http.MethodPost, session, http.StatusCreated)
}

// request checks the fixture engine's result, including a planted name Conflict.
func request(t *testing.T, client *http.Client, method, session string, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://engine/containers/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if method == http.MethodPost {
		req.URL.Path = "/containers/create"
		req.URL.RawQuery = url.Values{"name": {"reaper_" + session}}.Encode()
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf("reaper start: %s %s: %s", method, req.URL.RequestURI(), response.Status)
	}
}
