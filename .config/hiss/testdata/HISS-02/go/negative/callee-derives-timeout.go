package oidc

import (
	"context"
	"net/http"
	"time"
)

// Config carries the discovery timeout the provider applies itself.
type Config struct {
	Issuer           string
	DiscoveryTimeout time.Duration
}

// The caller passes context.Background(), and NewProvider, a function of the same module,
// derives its own deadline from configuration before anything else uses the context.
func Setup(cfg Config) (*http.Response, error) {
	return NewProvider(context.Background(), cfg)
}

// NewProvider bounds discovery by cfg.DiscoveryTimeout.
func NewProvider(ctx context.Context, cfg Config) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.DiscoveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}
