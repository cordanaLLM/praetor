package router

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testGatewayAddress = "https://gateway.example.invalid/v1"

func aliasModel(id, alias string, status AliasStatus, reason string) ModelDescriptor {
	model := costTaskModel(id, 0.1, 0.1)
	model.Provider, model.Alias, model.AliasStatus, model.AliasReason = "gw", alias, status, reason
	return model
}

func gatewayConfig(models ...ModelDescriptor) *RoutingConfig {
	cfg := taskConfig(models...)
	cfg.Gateway = &GatewayConfig{Address: testGatewayAddress}
	return cfg
}

func fakeProber(answers map[string]error) AliasProber {
	return func(_ context.Context, _ GatewayConfig, alias string) error { return answers[alias] }
}

func TestProbeAliasesRecordsAnswerAndReason(t *testing.T) {
	cfg := gatewayConfig(aliasModel("a-light", "light", "", ""), aliasModel("a-dead", "dead", "", ""),
		aliasModel("a-idle", "idle", AliasAnswers, ""), costTaskModel("pinned", 1, 1))
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	probes := ProbeAliases(context.Background(), cfg, fakeProber(map[string]error{
		"dead": errors.New("HTTP 400: Invalid model name\x00 passed"),
		"idle": ErrProbeNotRun,
	}), now)
	if len(probes) != 3 {
		t.Fatalf("pinned entry probed or alias missed: %+v", probes)
	}
	models := cfg.Tiers["work"].Models
	if models[0].AliasStatus != AliasAnswers || models[0].AsOf != "2026-10-08" {
		t.Fatalf("answer not recorded: %+v", models[0])
	}
	if models[1].AliasStatus != AliasUnanswered || !strings.Contains(models[1].AliasReason, "Invalid model name") || strings.ContainsRune(models[1].AliasReason, 0) {
		t.Fatalf("reason not recorded cleanly: %+v", models[1])
	}
	if models[2].AliasStatus != AliasAnswers || models[2].AsOf != "" {
		t.Fatalf("a probe that did not run changed its entry: %+v", models[2])
	}
	if models[3].AliasStatus != "" {
		t.Fatal("pinned entry got a probe status")
	}
}

func TestProbeAliasesWithoutGatewayProbesNothing(t *testing.T) {
	cfg := taskConfig(costTaskModel("pinned", 1, 1))
	called := false
	prober := func(context.Context, GatewayConfig, string) error { called = true; return nil }
	if got := ProbeAliases(context.Background(), cfg, prober, time.Now()); got != nil || called {
		t.Fatal("probed without a gateway")
	}
	if got := ProbeAliases(context.Background(), nil, prober, time.Now()); got != nil {
		t.Fatal("nil config probed")
	}
}

func TestBoundedReasonBoundary(t *testing.T) {
	if got := boundedReason(strings.Repeat("x", maxProbeReasonBytes)); len(got) != maxProbeReasonBytes {
		t.Fatalf("reason at the bound was cut to %d", len(got))
	}
	if got := boundedReason(strings.Repeat("x", maxProbeReasonBytes+1)); len(got) != maxProbeReasonBytes {
		t.Fatalf("reason past the bound kept %d bytes", len(got))
	}
	if got := boundedReason("a\nb\tc"); got != "a b c" {
		t.Fatalf("control characters kept: %q", got)
	}
}

func respond(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	if _, err := io.WriteString(w, body); err != nil {
		t.Error(err)
	}
}

func TestHTTPAliasProber(t *testing.T) {
	var auth, model string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			t.Error(err)
		}
		model = string(body)
		if strings.Contains(model, `"dead"`) {
			w.WriteHeader(http.StatusBadRequest)
			respond(t, w, `{"error":{"message":"Invalid model name passed"}}`)
			return
		}
		respond(t, w, `{}`)
	}))
	defer server.Close()
	t.Setenv("PRAETOR_TEST_PROBE_KEY", "secret-value")
	gw := GatewayConfig{Address: server.URL, KeyEnv: "PRAETOR_TEST_PROBE_KEY"}
	prober := HTTPAliasProber(server.Client())
	if err := prober(context.Background(), gw, "light"); err != nil {
		t.Fatalf("answering alias failed: %v", err)
	}
	if auth != "Bearer secret-value" || !strings.Contains(model, `"light"`) || !strings.Contains(model, `"max_tokens":1`) {
		t.Fatalf("probe request wrong: auth %q body %q", auth, model)
	}
	err := prober(context.Background(), gw, "dead")
	if err == nil || !strings.Contains(err.Error(), "HTTP 400: Invalid model name passed") || strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("refusal not reported cleanly: %v", err)
	}
}

func TestHTTPAliasProberProbeNotRunConditions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respond(t, w, `{}`)
	}))
	defer server.Close()
	t.Setenv("PRAETOR_TEST_PROBE_KEY", "")
	gw := GatewayConfig{Address: server.URL, KeyEnv: "PRAETOR_TEST_PROBE_KEY"}
	prober := HTTPAliasProber(server.Client())
	if err := prober(context.Background(), gw, "light"); !errors.Is(err, ErrProbeNotRun) {
		t.Fatalf("missing key must not count as an unanswered alias: %v", err)
	}
	server.Close()
	t.Setenv("PRAETOR_TEST_PROBE_KEY", "k")
	if err := prober(context.Background(), gw, "light"); !errors.Is(err, ErrProbeNotRun) {
		t.Fatalf("an unreachable gateway is no evidence about the alias: %v", err)
	}
}

func TestProbeTreatsTransientStatusesAsNotRun(t *testing.T) {
	for _, tc := range []struct {
		status   int
		notRun   bool
		boundary string
	}{{http.StatusTooManyRequests, true, "rate limit"}, {http.StatusServiceUnavailable, true, "5xx"}, {http.StatusRequestTimeout, true, "timeout"},
		{http.StatusBadRequest, false, "refusal"}, {http.StatusUnauthorized, false, "key refused"}, {499, false, "just below 5xx"}, {500, true, "first 5xx"}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status) }))
		err := HTTPAliasProber(server.Client())(context.Background(), GatewayConfig{Address: server.URL}, "light")
		server.Close()
		if err == nil || errors.Is(err, ErrProbeNotRun) != tc.notRun {
			t.Fatalf("%s (HTTP %d): not-run = %v, err %v", tc.boundary, tc.status, errors.Is(err, ErrProbeNotRun), err)
		}
	}
}

func TestGatewayAddressRequiresHTTPSExceptOnLoopback(t *testing.T) {
	for address, ok := range map[string]bool{"https://gw.example/v1": true, "http://localhost:4000": true, "http://127.0.0.1:4000/v1": true,
		"http://[::1]:4000": true, "http://gw.example/v1": false, "http://127.0.0.1.example/v1": false, "http://10.0.0.5/v1": false} {
		cfg := gatewayConfig(aliasModel("alias", "light", "", ""))
		cfg.Gateway.Address = address
		if err := validateGateway(cfg); (err == nil) != ok {
			t.Errorf("%s: accepted = %v, err %v", address, err == nil, err)
		}
	}
}

func TestRoutePrefersAnsweringAliasOverCheaperPinnedModel(t *testing.T) {
	cfg := gatewayConfig(costTaskModel("pinned-free", 0, 0), aliasModel("alias-light", "light", AliasAnswers, ""))
	route := routeTask(t, NewModelCapacityArbiter(cfg, nil), TaskRequest{Task: "implement", InputTokens: 100})
	if route.Model.ID != "alias-light" {
		t.Fatalf("routed to %s", route.Model.ID)
	}
	if len(route.Skipped) != 1 || route.Skipped[0].Model != "pinned-free" || !strings.Contains(route.Skipped[0].Reason, "the tier serves aliases") {
		t.Fatalf("pinned model not reported as skipped: %+v", route.Skipped)
	}
	if ModelTarget(route.Model) != "light" {
		t.Fatal("the harness target must be the alias")
	}
}

func TestRouteNeverFallsBackToPinnedModelWhenGatewayServesAliases(t *testing.T) {
	cfg := gatewayConfig(costTaskModel("pinned", 1, 1), aliasModel("alias-up", "up", AliasAnswers, ""),
		aliasModel("alias-down", "down", AliasUnanswered, "HTTP 400: not available for this key"))
	cfg.Tiers["other"] = Tier{TargetTasks: []string{"review"}, Models: []ModelDescriptor{costTaskModel("pinned-review", 0, 0), aliasModel("alias-down2", "down2", AliasUnanswered, "HTTP 404: gone")}}
	_, err := NewModelCapacityArbiter(cfg, nil).SelectForTask(context.Background(), TaskRequest{Task: "review", InputTokens: 1})
	if !errors.Is(err, ErrNoEligibleModel) {
		t.Fatalf("a pinned model was chosen or the error was lost: %v", err)
	}
	for _, want := range []string{"alias-down2: alias did not answer the gateway probe: HTTP 404: gone", "pinned-review: pinned model"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error lacks %q: %v", want, err)
		}
	}
}

func TestRouteExcludesPinnedModelsWhenNoAliasAnswers(t *testing.T) {
	cfg := gatewayConfig(costTaskModel("pinned", 1, 1), aliasModel("alias-new", "new", "", ""))
	_, err := NewModelCapacityArbiter(cfg, nil).SelectForTask(context.Background(), TaskRequest{Task: "implement", InputTokens: 1})
	if !errors.Is(err, ErrNoEligibleModel) || !strings.Contains(err.Error(), "alias-new: alias has not been probed") || !strings.Contains(err.Error(), "pinned: pinned model") {
		t.Fatalf("a gateway outage must not bring pinned models back: %v", err)
	}
}

func TestRouteKeepsLocalModelBesideAnsweringAlias(t *testing.T) {
	local := costTaskModel("local-9b", 0, 0)
	local.Source = SourceLocal
	cfg := gatewayConfig(local, costTaskModel("pinned", 0, 0), aliasModel("alias-light", "light", AliasAnswers, ""))
	route := routeTask(t, NewModelCapacityArbiter(cfg, nil), TaskRequest{Task: "implement", InputTokens: 1})
	if route.Model.ID != "local-9b" {
		t.Fatalf("a local runtime model never passes through the gateway, yet routed to %s", route.Model.ID)
	}
}

func TestTierOnlyRouteRanksByRatesWithoutEstimates(t *testing.T) {
	cfg := taskConfig(costTaskModel("dear", 3, 3), costTaskModel("zz-cheap", 1, 1), costTaskModel("middle", 1, 2))
	arbiter := NewModelCapacityArbiter(cfg, nil)
	route := routeTask(t, arbiter, TaskRequest{Task: "implement"})
	if route.Model.ID != "zz-cheap" || route.EstimatedCost != 0 || !strings.Contains(route.Basis, "no token estimates") {
		t.Fatalf("tier-only route wrong: %+v", route)
	}
	if got := routeTask(t, arbiter, TaskRequest{Task: "implement", OutputTokens: 1}); got.Model.ID != "zz-cheap" || strings.Contains(got.Basis, "no token estimates") {
		t.Fatalf("estimates must keep the cost basis: %+v", got)
	}
	tied := NewModelCapacityArbiter(taskConfig(costTaskModel("b", 1, 1), costTaskModel("a", 1, 1)), nil)
	if got := routeTask(t, tied, TaskRequest{Task: "implement"}); got.Model.ID != "a" {
		t.Fatalf("tier-only tie must resolve by model ID, got %s", got.Model.ID)
	}
	if _, err := arbiter.SelectForTask(context.Background(), TaskRequest{Task: "implement", InputTokens: -1}); err == nil {
		t.Fatal("a negative estimate must still be refused")
	}
}

func TestAliasValidation(t *testing.T) {
	bad := map[string]func(*RoutingConfig){
		"provider without alias": func(c *RoutingConfig) { c.Tiers["work"].Models[0].Alias = "" },
		"unknown status":         func(c *RoutingConfig) { c.Tiers["work"].Models[0].AliasStatus = "maybe" },
		"status without alias": func(c *RoutingConfig) {
			c.Tiers["work"].Models[1].AliasStatus = AliasAnswers
		},
		"alias without gateway":     func(c *RoutingConfig) { c.Gateway = nil },
		"gateway not a web address": func(c *RoutingConfig) { c.Gateway.Address = "file:///etc/passwd" },
		"gateway without host":      func(c *RoutingConfig) { c.Gateway.Address = "http://" },
		"key variable shape":        func(c *RoutingConfig) { c.Gateway.KeyEnv = "lower-case" },
		"bad as_of":                 func(c *RoutingConfig) { c.Tiers["work"].Models[1].AsOf = "yesterday" },
		"window negative":           func(c *RoutingConfig) { c.Governance.CatalogMaxAgeDays = -1 },
		"window too large":          func(c *RoutingConfig) { c.Governance.CatalogMaxAgeDays = MaxCatalogMaxAgeDays + 1 },
	}
	for name, mutate := range bad {
		cfg := gatewayConfig(aliasModel("a", "light", "", ""), costTaskModel("p", 1, 1))
		mutate(cfg)
		if err := ValidateRoutingConfig(cfg); !errors.Is(err, ErrInvalidRoutingConfig) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	ok := gatewayConfig(aliasModel("a", "light", AliasAnswers, ""), costTaskModel("p", 1, 1))
	ok.Gateway.KeyEnv = "PRAETOR_GATEWAY_KEY"
	ok.Tiers["work"].Models[1].AsOf = "2026-10-08"
	ok.Governance.CatalogMaxAgeDays = MaxCatalogMaxAgeDays
	if err := ValidateRoutingConfig(ok); err != nil {
		t.Fatalf("valid alias catalog refused: %v", err)
	}
}

func TestValidateGatewayKeyEnvPrefix(t *testing.T) {
	cases := map[string]bool{
		"GITHUB_TOKEN":        true,
		"PRAETOR_GATEWAY_":    true,
		"PRAETOR_GATEWAY_KEY": false,
	}
	for name, wantErr := range cases {
		cfg := gatewayConfig()
		cfg.Gateway.KeyEnv = name
		err := validateGateway(cfg)
		if (err != nil) != wantErr {
			t.Errorf("%s: err=%v, wantErr=%v", name, err, wantErr)
		}
	}
}

type closeErrReader struct {
	io.Reader
	closeErr error
}

func (c *closeErrReader) Close() error {
	return c.closeErr
}

type errRoundTripper struct {
	roundTrip func(*http.Request) (*http.Response, error)
}

func (rt errRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return rt.roundTrip(req)
}

func TestProbeAliasBodyCloseErrorDoesNotMarkUnanswered(t *testing.T) {
	client := &http.Client{
		Transport: errRoundTripper{
			roundTrip: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: &closeErrReader{
						Reader:   strings.NewReader(`{"choices":[{"message":{"content":"pong"}}]}`),
						closeErr: errors.New("simulated close error"),
					},
				}, nil
			},
		},
	}
	gw := GatewayConfig{Address: "https://gateway.example.invalid/v1"}
	err := probeAlias(context.Background(), client, gw, "light")
	if err != nil {
		t.Fatalf("expected nil error on answered probe with body close error, got: %v", err)
	}
}

type readErrReader struct {
	readErr error
}

func (r *readErrReader) Read(p []byte) (n int, err error) {
	return 0, r.readErr
}

func (r *readErrReader) Close() error {
	return nil
}

func TestProbeAliasReadErrorWrapsErrProbeNotRun(t *testing.T) {
	client := &http.Client{
		Transport: errRoundTripper{
			roundTrip: func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: &readErrReader{
						readErr: errors.New("simulated read error"),
					},
				}, nil
			},
		},
	}
	gw := GatewayConfig{Address: "https://gateway.example.invalid/v1"}
	err := probeAlias(context.Background(), client, gw, "light")
	if !errors.Is(err, ErrProbeNotRun) {
		t.Fatalf("expected ErrProbeNotRun on body read error, got: %v", err)
	}
}

func TestValidateAliasEntryOptionalProvider(t *testing.T) {
	model := ModelDescriptor{ID: "gw-light", Alias: "light"}
	if err := validateAliasEntry(model); err != nil {
		t.Fatalf("alias without provider rejected: %v", err)
	}
}
