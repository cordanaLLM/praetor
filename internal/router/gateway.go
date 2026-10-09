package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"
)

// AliasStatus is the result of the last probe of an alias entry.
type AliasStatus string

const (
	// AliasAnswers means the gateway answered a probe call for the alias.
	AliasAnswers AliasStatus = "answers"
	// AliasUnanswered means the probe call failed; the entry records why.
	AliasUnanswered AliasStatus = "unanswered"
)

const (
	// aliasProbeTimeout bounds one probe call (HISS-02).
	aliasProbeTimeout = 10 * time.Second
	// maxProbeBodyBytes bounds the response body read from a probe.
	maxProbeBodyBytes = 64 << 10
	// maxProbeReasonBytes bounds a recorded unanswered reason.
	maxProbeReasonBytes = 200
	// maxEnvNameBytes bounds the name of the key variable.
	maxEnvNameBytes = 128
)

// ErrProbeNotRun means a probe could not be attempted (for example the key variable is
// unset). It is not evidence about the alias, so the recorded status stays as it was.
var ErrProbeNotRun = errors.New("alias probe not run")

// GatewayKeyEnvPrefix is the required prefix for gateway key_env variable names.
const GatewayKeyEnvPrefix = "PRAETOR_GATEWAY_"

// GatewayConfig declares the gateway serving the router aliases; the adopter supplies it.
type GatewayConfig struct {
	Address string `yaml:"address" json:"address"`
	KeyEnv  string `yaml:"key_env,omitempty" json:"key_env,omitempty"`
}

func validateGateway(cfg *RoutingConfig) error {
	gw := cfg.Gateway
	if gw == nil {
		return requireNoAliases(cfg)
	}
	if err := validateGatewayAddress(gw.Address); err != nil {
		return err
	}
	return validateGatewayKeyEnv(gw.KeyEnv)
}

func validateGatewayKeyEnv(name string) error {
	if name == "" {
		return nil
	}
	if len(name) > maxEnvNameBytes || !strings.HasPrefix(name, GatewayKeyEnvPrefix) || len(name) == len(GatewayKeyEnvPrefix) || !validKeyEnvSuffix(name[len(GatewayKeyEnvPrefix):]) {
		return fmt.Errorf("%w: gateway key_env must start with %s followed by at least one character of [A-Z0-9_]", ErrInvalidRoutingConfig, GatewayKeyEnvPrefix)
	}
	return nil
}

func isKeyEnvChar(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

func validKeyEnvSuffix(suffix string) bool {
	for i := 0; i < len(suffix); i++ {
		if !isKeyEnvChar(suffix[i]) {
			return false
		}
	}
	return true
}

// validateGatewayAddress accepts an https URL, or an http URL only on a loopback host, because
// the probe sends the key from key_env to this address.
func validateGatewayAddress(address string) error {
	parsed, err := url.Parse(address)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || len(address) > maxRoutingNameBytes {
		return fmt.Errorf("%w: gateway address must be an http(s) URL", ErrInvalidRoutingConfig)
	}
	if parsed.Scheme == "http" && !isLoopbackHost(parsed.Hostname()) {
		return fmt.Errorf("%w: gateway address must use https unless it is a loopback host, because the probe sends the key", ErrInvalidRoutingConfig)
	}
	return nil
}

// isLoopbackHost reports whether host is localhost or a loopback IP address.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback()
}

// requireNoAliases refuses an alias entry in a catalog that declares no gateway: nothing could
// probe it, and nothing says which service its alias names.
func requireNoAliases(cfg *RoutingConfig) error {
	for _, tier := range cfg.Tiers {
		for i := 0; i < len(tier.Models) && i < MaxModelsPerTier; i++ {
			if tier.Models[i].Alias != "" {
				return fmt.Errorf("%w: model %s names an alias but the catalog declares no gateway", ErrInvalidRoutingConfig, tier.Models[i].ID)
			}
		}
	}
	return nil
}

// TierServesAliases reports whether a tier holds at least one alias entry.
func TierServesAliases(tier Tier) bool {
	for i := 0; i < len(tier.Models) && i < MaxModelsPerTier; i++ {
		if tier.Models[i].Alias != "" {
			return true
		}
	}
	return false
}

// gatewayExclusion returns why the gateway rules exclude a candidate, or "" when it stays.
// An alias entry stays only once its probe answered. When a tier declares alias entries,
// its pinned models are excluded too, answering or not, because the gateway refuses concrete IDs its key cannot use;
// a model from a local runtime never passes through the gateway and stays.
func gatewayExclusion(model ModelDescriptor, tierServes bool) string {
	if model.Alias != "" {
		switch model.AliasStatus {
		case AliasAnswers:
			return ""
		case AliasUnanswered:
			return "alias did not answer the gateway probe: " + model.AliasReason
		default:
			return "alias has not been probed; run models sync --probe-aliases"
		}
	}
	if tierServes && model.Source != SourceLocal {
		return "pinned model; the tier serves aliases"
	}
	return ""
}

// validateAliasEntry checks the alias, probe and as_of fields of one model.
func validateAliasEntry(model ModelDescriptor) error {
	if model.Alias == "" && model.Provider != "" {
		return fmt.Errorf("%w: model %s declares provider without an alias", ErrInvalidRoutingConfig, model.ID)
	}
	if model.Alias != "" && !routingName(model.Alias) {
		return fmt.Errorf("%w: model %s has an invalid alias", ErrInvalidRoutingConfig, model.ID)
	}
	if model.Provider != "" && !routingName(model.Provider) {
		return fmt.Errorf("%w: model %s has an invalid provider", ErrInvalidRoutingConfig, model.ID)
	}
	return validateProbeFields(model)
}

// validateProbeFields checks the recorded probe status and the as_of date of one model.
func validateProbeFields(model ModelDescriptor) error {
	switch model.AliasStatus {
	case "", AliasAnswers, AliasUnanswered:
	default:
		return fmt.Errorf("%w: model %s has unknown alias_status %q", ErrInvalidRoutingConfig, model.ID, model.AliasStatus)
	}
	if model.Alias == "" && (model.AliasStatus != "" || model.AliasReason != "") {
		return fmt.Errorf("%w: model %s records an alias probe without an alias", ErrInvalidRoutingConfig, model.ID)
	}
	if _, err := time.Parse(time.DateOnly, model.AsOf); model.AsOf != "" && err != nil {
		return fmt.Errorf("%w: model %s as_of must be a YYYY-MM-DD date", ErrInvalidRoutingConfig, model.ID)
	}
	return nil
}

// AliasProber makes one bounded probe call for an alias.
type AliasProber func(ctx context.Context, gw GatewayConfig, alias string) error

// AliasProbe is the outcome of probing one alias entry.
type AliasProbe struct {
	Model  string      `json:"model"`
	Alias  string      `json:"alias"`
	Status AliasStatus `json:"status,omitempty"`
	Reason string      `json:"reason,omitempty"`
}

// HTTPAliasProber returns the prober that sends one one-token chat completion to the gateway.
func HTTPAliasProber(client *http.Client) AliasProber {
	return func(ctx context.Context, gw GatewayConfig, alias string) error {
		return probeAlias(ctx, client, gw, alias)
	}
}

func probeRequest(ctx context.Context, gw GatewayConfig, alias string) (*http.Request, error) {
	key := ""
	if gw.KeyEnv != "" {
		if key = os.Getenv(gw.KeyEnv); key == "" {
			return nil, fmt.Errorf("%w: environment variable %s is empty", ErrProbeNotRun, gw.KeyEnv)
		}
	}
	body, err := json.Marshal(map[string]any{"model": alias, "max_tokens": 1,
		"messages": []map[string]string{{"role": "user", "content": "ping"}}})
	if err != nil {
		return nil, fmt.Errorf("encode probe: %w", err)
	}
	target := strings.TrimRight(gw.Address, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProbeNotRun, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req, nil
}

func probeAlias(ctx context.Context, client *http.Client, gw GatewayConfig, alias string) (resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, aliasProbeTimeout)
	defer cancel()
	req, err := probeRequest(ctx, gw, alias)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: gateway unreachable: %w", ErrProbeNotRun, err)
	}
	defer func() {
		closeErr := resp.Body.Close()
		if resultErr != nil {
			resultErr = errors.Join(resultErr, closeErr)
		}
	}()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBodyBytes))
	if err != nil {
		return fmt.Errorf("%w: read probe response: %w", ErrProbeNotRun, err)
	}
	if transientStatus(resp.StatusCode) {
		return fmt.Errorf("%w: HTTP %d: %s", ErrProbeNotRun, resp.StatusCode, gatewayMessage(data))
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, gatewayMessage(data))
	}
	return nil
}

// transientStatus reports an HTTP status that says nothing about the alias: a timeout, a rate
// limit or a gateway-side failure. The recorded status then stays as it was.
func transientStatus(code int) bool {
	return code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500
}

// gatewayMessage extracts the error message of an OpenAI-style error body, else the bare text.
func gatewayMessage(data []byte) string {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	text := string(data)
	if json.Unmarshal(data, &payload) == nil && payload.Error.Message != "" {
		text = payload.Error.Message
	}
	return boundedReason(text)
}

// boundedReason drops control characters and cuts the text to maxProbeReasonBytes.
func boundedReason(text string) string {
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(text))
	if len(text) > maxProbeReasonBytes {
		text = strings.ToValidUTF8(text[:maxProbeReasonBytes], "")
	}
	return text
}

// ProbeAliases probes every alias entry once and records the outcome on the entry, with now as
// its AsOf date when the gateway answered. A probe that could not run (ErrProbeNotRun) leaves
// the entry unchanged and is reported without a status. Without a gateway it probes nothing.
func ProbeAliases(ctx context.Context, cfg *RoutingConfig, prober AliasProber, now time.Time) []AliasProbe {
	if cfg == nil || cfg.Gateway == nil || prober == nil {
		return nil
	}
	var probes []AliasProbe
	for name, tier := range cfg.Tiers {
		for i := 0; i < len(tier.Models) && i < MaxModelsPerTier; i++ {
			if tier.Models[i].Alias == "" {
				continue
			}
			probes = append(probes, probeEntry(ctx, *cfg.Gateway, &tier.Models[i], prober, now))
		}
		cfg.Tiers[name] = tier
	}
	slices.SortFunc(probes, func(a, b AliasProbe) int { return strings.Compare(a.Model, b.Model) })
	return probes
}

func probeEntry(ctx context.Context, gw GatewayConfig, model *ModelDescriptor, prober AliasProber, now time.Time) AliasProbe {
	probe := AliasProbe{Model: model.ID, Alias: model.Alias}
	err := prober(ctx, gw, model.Alias)
	switch {
	case err == nil:
		model.AliasStatus, model.AliasReason, model.AsOf = AliasAnswers, "", now.UTC().Format(time.DateOnly)
	case errors.Is(err, ErrProbeNotRun):
		probe.Reason = boundedReason(err.Error())
		return probe
	default:
		model.AliasStatus, model.AliasReason = AliasUnanswered, boundedReason(err.Error())
	}
	probe.Status, probe.Reason = model.AliasStatus, model.AliasReason
	return probe
}
