// Package repairrun executes bounded repair proposals under explicit local policy.
package repairrun

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const providerPromptLimit = 64 << 10
const providerResponseLimit = 512 << 10

// ProviderConfig binds one reviewed endpoint, model, and credential-helper snapshot.
type ProviderConfig struct {
	BaseURL            string `json:"base_url"`
	TokenCommand       string `json:"token_command"`
	TokenCommandSHA256 string `json:"token_command_sha256"`
	Model              string `json:"model"`
	MaxOutputTokens    int    `json:"max_output_tokens"`
	MaxInputBytes      int    `json:"max_input_bytes"`
}

// Edit is untrusted replacement content; the executor verifies its path and digest.
type Edit struct {
	Path           string `json:"path"`
	OriginalSHA256 string `json:"original_sha256"`
	Content        string `json:"content"`
}

// Usage reports provider token counts and optional gateway-reported dollar cost.
type Usage struct {
	InputTokens  int64    `json:"input_tokens"`
	OutputTokens int64    `json:"output_tokens"`
	CostUSD      *float64 `json:"cost_usd"`
}

// Proposal is generated evidence, not an applied or verified repair.
type Proposal struct {
	Summary     string `json:"summary"`
	Edits       []Edit `json:"edits"`
	Usage       Usage  `json:"usage"`
	ResponseID  string `json:"response_id"`
	ActualModel string `json:"actual_model"`
}

// ValidateProviderConfig checks configuration syntax without filesystem or network I/O.
func ValidateProviderConfig(cfg ProviderConfig) error {
	if cfg.BaseURL != "https://litellm.ai.cauda.dev/v1" && cfg.BaseURL != "https://gateway.ai.cauda.dev/v1" {
		return errors.New("repair provider endpoint is not reviewed")
	}
	if !filepath.IsAbs(cfg.TokenCommand) || filepath.Clean(cfg.TokenCommand) != cfg.TokenCommand || !providerSafeText(cfg.TokenCommand, 4096) {
		return errors.New("repair credential helper requires a clean absolute path")
	}
	if !providerDigest(cfg.TokenCommandSHA256) || !providerSafeText(cfg.Model, 256) {
		return errors.New("repair provider requires an exact helper digest and model")
	}
	if !providerValidLimits(cfg) {
		return errors.New("repair provider input or output limit is invalid")
	}
	return nil
}

func providerValidLimits(cfg ProviderConfig) bool {
	return cfg.MaxInputBytes >= 1 && cfg.MaxInputBytes <= providerPromptLimit && cfg.MaxOutputTokens >= 256 && cfg.MaxOutputTokens <= 8192
}

// Generate performs exactly one HTTP request within a 120-second operation deadline.
// Credential output and provider error bodies never appear in returned diagnostics.
func Generate(ctx context.Context, cfg ProviderConfig, prompt string) (*Proposal, error) {
	return providerGenerate(ctx, cfg, prompt, providerClient())
}

func providerGenerate(ctx context.Context, cfg ProviderConfig, prompt string, client *http.Client) (*Proposal, error) {
	if ctx == nil {
		return nil, errors.New("repair provider requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	if err := ValidateProviderConfig(cfg); err != nil {
		return nil, err
	}
	if len(prompt) == 0 || len(prompt) > cfg.MaxInputBytes || !utf8.ValidString(prompt) {
		return nil, errors.New("repair prompt must be bounded UTF-8 text")
	}
	token, err := providerToken(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if strings.Contains(prompt, token) || strings.Contains(cfg.Model, token) {
		return nil, errors.New("repair request context contains credential material")
	}
	return providerRequest(ctx, cfg, prompt, token, client)
}

func providerSafeText(value string, bound int) bool {
	if len(value) == 0 || len(value) > bound || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for i := 0; i < len(value) && i < bound; i++ {
		if value[i] < 32 || value[i] == 127 {
			return false
		}
	}
	return true
}

func providerDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
}
