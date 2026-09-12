// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package hindsight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Client executes rate-limited, fail-safe communication with a Hindsight server instance.
type Client struct {
	config     ClientConfig
	httpClient *http.Client
	limiter    *time.Ticker
}

// NewClient initializes a bounded Hindsight client.
func NewClient(cfg ClientConfig) *Client {
	interval := time.Second * 2 // Default 30 RPM = 1 request every 2s
	if cfg.RateLimitRPM > 0 {
		interval = time.Minute / time.Duration(cfg.RateLimitRPM)
	}

	timeout := 5 * time.Second
	if cfg.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}

	return &Client{
		config:     cfg,
		httpClient: &http.Client{Timeout: timeout},
		limiter:    time.NewTicker(interval),
	}
}

// IngestFact transmits an atomic fact as a Hindsight document.
func (c *Client) IngestFact(ctx context.Context, bankID string, fact MemoryFact) (resultErr error) {
	if c.config.OfflineOnly {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("hindsight client: context cannot be nil")
	}

	select {
	case <-c.limiter.C:
	case <-ctx.Done():
		return ctx.Err()
	}

	payload := HindsightDocument{
		DocumentID: fmt.Sprintf("fact-%s", fact.ID),
		Content:    fmt.Sprintf("[%s] %s: %s (Evidence: %s)", fact.Category, fact.Subject, fact.Statement, fact.Evidence),
		Context:    fmt.Sprintf("praetor-intelligence/%s", fact.Category),
		Tags:       fact.Tags,
		Metadata: map[string]string{
			"category": string(fact.Category),
			"subject":  fact.Subject,
			"evidence": fact.Evidence,
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal document: %w", err)
	}

	url := fmt.Sprintf("%s/v1/default/banks/%s/document-transfer", c.config.BaseURL, bankID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.config.Token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.config.Token))
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("hindsight ingest request: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, resp.Body.Close()) }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("hindsight ingest: HTTP status %d", resp.StatusCode)
	}

	return nil
}

// Close releases the internal rate limiter ticker.
func (c *Client) Close() {
	if c.limiter != nil {
		c.limiter.Stop()
	}
}
