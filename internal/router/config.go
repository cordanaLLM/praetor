package router

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// MaxRoutingTiers bounds task eligibility evaluation.
	MaxRoutingTiers = 16
	// MaxModelsPerTier bounds candidate evaluation within one tier.
	MaxModelsPerTier = 64
	// MaxRoutingModels bounds distinct model descriptors and supplied usage rows.
	MaxRoutingModels = MaxRoutingTiers * MaxModelsPerTier
	// MaxRoutingTags bounds task and capability declarations.
	MaxRoutingTags = 64
	// MaxRoutingFileBytes bounds configuration and usage snapshot reads.
	MaxRoutingFileBytes = 1 << 20
	maxRoutingNameBytes = 256
)

// ErrInvalidRoutingConfig identifies missing, ambiguous or invalid routing data.
var ErrInvalidRoutingConfig = errors.New("invalid routing configuration")

// LoadRoutingConfigContext loads one strict YAML document without network access.
// It refuses nonregular files, missing prices, duplicate IDs and oversized input.
func LoadRoutingConfigContext(ctx context.Context, path string) (*RoutingConfig, error) {
	data, err := readRoutingFile(ctx, path)
	if err != nil {
		return nil, err
	}
	var cfg RoutingConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalidRoutingConfig, path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: expected one YAML document", ErrInvalidRoutingConfig)
	}
	if err := ValidateRoutingConfig(&cfg); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg.SourceSHA256 = fmt.Sprintf("%x", sha256.Sum256(data))
	return &cfg, nil
}

// UnmarshalYAML records actual numeric price presence, including explicit zero.
func (model *ModelDescriptor) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode || len(node.Content) > 14 {
		return fmt.Errorf("%w: invalid model descriptor", ErrInvalidRoutingConfig)
	}
	prices := 0
	for i := 0; i+1 < len(node.Content) && i < 14; i += 2 {
		key, value := node.Content[i].Value, node.Content[i+1]
		switch key {
		case "id", "family", "rpm_limit", "tpm_limit", "capabilities":
		case "cost_per_m_in", "cost_per_m_out":
			if value.Tag != "!!int" && value.Tag != "!!float" {
				return fmt.Errorf("%w: %s must be explicitly numeric", ErrInvalidRoutingConfig, key)
			}
			prices++
		default:
			return fmt.Errorf("%w: unknown model field %s", ErrInvalidRoutingConfig, key)
		}
	}
	if prices != 2 {
		return fmt.Errorf("%w: both model cost rates must be declared", ErrInvalidRoutingConfig)
	}
	type plainModel ModelDescriptor
	var decoded plainModel
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*model = ModelDescriptor(decoded)
	model.CostRatesDeclared = true
	return nil
}

// ValidateRoutingConfig validates task routing bounds and declared model costs.
// Task/capability labels and prices are operator declarations, not measured facts.
func ValidateRoutingConfig(cfg *RoutingConfig) error {
	if cfg == nil || cfg.Version != 1 || len(cfg.Tiers) == 0 || len(cfg.Tiers) > MaxRoutingTiers {
		return fmt.Errorf("%w: version 1 and 1..%d tiers required", ErrInvalidRoutingConfig, MaxRoutingTiers)
	}
	if err := validateRoutingGovernance(cfg.Governance); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for name, tier := range cfg.Tiers {
		if err := validateRoutingTier(name, tier, cfg, seen); err != nil {
			return err
		}
	}
	return nil
}

func validateRoutingGovernance(policy GovernancePolicy) error {
	threshold := policy.ExhaustionThresholdPercent
	if !finiteNonnegative(threshold) || threshold > 100 || policy.MaxConcurrentSameModel < 0 {
		return fmt.Errorf("%w: invalid governance limits", ErrInvalidRoutingConfig)
	}
	return nil
}

func validateRoutingTier(name string, tier Tier, cfg *RoutingConfig, seen map[string]bool) error {
	if !routingName(name) || len(tier.Models) > MaxModelsPerTier {
		return fmt.Errorf("%w: invalid or oversized tier %q", ErrInvalidRoutingConfig, name)
	}
	if err := validateRoutingTags(tier.TargetTasks); err != nil {
		return err
	}
	if tier.FallbackTier != "" {
		if _, ok := cfg.Tiers[tier.FallbackTier]; !ok {
			return fmt.Errorf("%w: unknown fallback tier %s", ErrInvalidRoutingConfig, tier.FallbackTier)
		}
	}
	for i := 0; i < len(tier.Models) && i < MaxModelsPerTier; i++ {
		if err := validateRoutingModel(tier.Models[i], seen); err != nil {
			return err
		}
	}
	return nil
}

func validateRoutingModel(model ModelDescriptor, seen map[string]bool) error {
	if !routingName(model.ID) || !routingName(string(model.Family)) || seen[model.ID] {
		return fmt.Errorf("%w: empty or duplicate model ID/family %q", ErrInvalidRoutingConfig, model.ID)
	}
	seen[model.ID] = true
	if !model.CostRatesDeclared || !finiteNonnegative(model.CostPerMIn) || !finiteNonnegative(model.CostPerMOut) {
		return fmt.Errorf("%w: model %s requires explicit finite nonnegative costs", ErrInvalidRoutingConfig, model.ID)
	}
	if model.RPMLimit < 0 || model.TPMLimit < 0 {
		return fmt.Errorf("%w: negative capacity for %s", ErrInvalidRoutingConfig, model.ID)
	}
	return validateRoutingTags(model.Capabilities)
}

func validateRoutingTags(tags []string) error {
	if len(tags) > MaxRoutingTags {
		return fmt.Errorf("%w: too many task/capability tags", ErrInvalidRoutingConfig)
	}
	seen := make(map[string]bool, len(tags))
	for i := 0; i < len(tags) && i < MaxRoutingTags; i++ {
		if !routingName(tags[i]) || seen[tags[i]] {
			return fmt.Errorf("%w: empty or duplicate task/capability tag", ErrInvalidRoutingConfig)
		}
		seen[tags[i]] = true
	}
	return nil
}

func routingName(name string) bool {
	return name != "" && len(name) <= maxRoutingNameBytes && name == strings.TrimSpace(name) && !strings.ContainsAny(name, "\x00\r\n\t")
}

func finiteNonnegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func readRoutingFile(ctx context.Context, path string) (data []byte, err error) {
	if ctx == nil {
		return nil, errors.New("routing reads require a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := inspectRoutingFile(path)
	if err != nil {
		return nil, err
	}
	return readInspectedRoutingFile(ctx, path, info)
}

func readInspectedRoutingFile(ctx context.Context, path string, info os.FileInfo) (data []byte, err error) {
	file, err := openRoutingInput(path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	actual, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
		return nil, errors.New("routing input changed while opening")
	}
	data, err = io.ReadAll(io.LimitReader(file, MaxRoutingFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxRoutingFileBytes {
		return nil, errors.New("routing input exceeds 1 MiB")
	}
	if err := verifyRoutingRead(file, info, len(data)); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

func verifyRoutingRead(file *os.File, info os.FileInfo, length int) error {
	after, err := file.Stat()
	if err != nil {
		return err
	}
	if after.Size() != info.Size() || int64(length) != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return errors.New("routing input changed while reading")
	}
	return nil
}

func inspectRoutingFile(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect routing input: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > MaxRoutingFileBytes {
		return nil, errors.New("routing input must be regular and at most 1 MiB")
	}
	return info, nil
}
