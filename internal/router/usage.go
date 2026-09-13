package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// UsageSnapshot is explicitly supplied observation data, not live provider polling.
// Counters remain as supplied; neither freshness nor rolling-window expiry is inferred.
type UsageSnapshot struct {
	Version    int                   `json:"version"`
	CapturedAt time.Time             `json:"captured_at"`
	Models     map[string]ModelUsage `json:"models"`
}

// LoadUsageSnapshot reads a bounded, strict JSON snapshot. Unknown fields fail.
func LoadUsageSnapshot(ctx context.Context, path string) (*UsageSnapshot, error) {
	data, err := readRoutingFile(ctx, path)
	if err != nil {
		return nil, err
	}
	if !json.Valid(data) {
		return nil, errors.New("usage snapshot must be valid JSON")
	}
	// A node view preserves duplicate and exact-case keys that encoding/json's
	// struct/map decoder would silently overwrite or case-fold.
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if len(document.Content) != 1 {
		return nil, errors.New("usage snapshot requires one document")
	}
	snapshot, err := decodeUsageSnapshot(document.Content[0])
	if err != nil {
		return nil, err
	}
	if err := validateUsageSnapshot(snapshot); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func decodeUsageSnapshot(node *yaml.Node) (*UsageSnapshot, error) {
	root, err := usageObject(node, 3)
	if err != nil {
		return nil, err
	}
	captured, err := usageSnapshotHeader(root)
	if err != nil {
		return nil, err
	}
	models, err := usageObject(root["models"], MaxRoutingModels)
	if err != nil {
		return nil, err
	}
	snapshot := &UsageSnapshot{Version: 1, CapturedAt: captured, Models: make(map[string]ModelUsage, len(models))}
	for id, model := range models {
		usage, err := decodeModelUsage(model)
		if err != nil {
			return nil, fmt.Errorf("usage model %s: %w", id, err)
		}
		snapshot.Models[id] = usage
	}
	return snapshot, nil
}

func usageSnapshotHeader(root map[string]*yaml.Node) (time.Time, error) {
	if len(root) != 3 || root["version"] == nil || root["captured_at"] == nil || root["models"] == nil {
		return time.Time{}, errors.New("usage snapshot requires exactly version, captured_at and models")
	}
	if root["version"].Tag != "!!int" || root["version"].Value != "1" {
		return time.Time{}, errors.New("usage snapshot version must be 1")
	}
	if root["captured_at"].Tag != "!!str" {
		return time.Time{}, errors.New("captured_at must be an RFC3339 timestamp string")
	}
	captured, err := time.Parse(time.RFC3339Nano, root["captured_at"].Value)
	if err != nil {
		return time.Time{}, fmt.Errorf("captured_at: %w", err)
	}
	return captured, nil
}

func usageObject(node *yaml.Node, limit int) (map[string]*yaml.Node, error) {
	if node.Kind != yaml.MappingNode || len(node.Content) > limit*2 {
		return nil, errors.New("usage object is missing, not an object, or oversized")
	}
	fields := make(map[string]*yaml.Node, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content) && i < MaxRoutingModels*2; i += 2 {
		key := node.Content[i]
		if key.Tag != "!!str" {
			return nil, errors.New("usage object keys must be strings")
		}
		if _, exists := fields[key.Value]; exists {
			return nil, fmt.Errorf("duplicate usage object key %q", key.Value)
		}
		fields[key.Value] = node.Content[i+1]
	}
	return fields, nil
}

func decodeModelUsage(node *yaml.Node) (ModelUsage, error) {
	var usage ModelUsage
	fields, err := usageObject(node, 5)
	if err != nil {
		return usage, err
	}
	if fields["current_rpm"] == nil || fields["current_tpm"] == nil {
		return usage, errors.New("explicit current_rpm and current_tpm counters are required")
	}
	for key, value := range fields {
		if err := validateUsageField(key, value); err != nil {
			return usage, err
		}
	}
	if err := node.Decode(&usage); err != nil {
		return usage, err
	}
	return usage, nil
}

func validateUsageField(key string, value *yaml.Node) error {
	switch key {
	case "current_rpm", "current_tpm", "error_count":
		if value.Tag == "!!int" {
			return nil
		}
	case "total_spend":
		if value.Tag == "!!int" || value.Tag == "!!float" {
			return nil
		}
	case "last_429_time":
		if value.Tag == "!!str" {
			if _, err := time.Parse(time.RFC3339Nano, value.Value); err == nil {
				return nil
			}
		}
	}
	return fmt.Errorf("unknown, aliased or invalid usage field %q", key)
}

// TrackerFromSnapshot accepts observations only for configured models. Missing
// models remain unobserved; callers can require observations in TaskRequest.
func TrackerFromSnapshot(cfg *RoutingConfig, snapshot *UsageSnapshot) (*LimitTracker, error) {
	if err := ValidateRoutingConfig(cfg); err != nil {
		return nil, err
	}
	if err := validateUsageSnapshot(snapshot); err != nil {
		return nil, err
	}
	known := make(map[string]bool)
	for _, tier := range cfg.Tiers {
		for i := 0; i < len(tier.Models) && i < MaxModelsPerTier; i++ {
			known[tier.Models[i].ID] = true
		}
	}
	tracker := NewLimitTracker()
	for id, usage := range snapshot.Models {
		if !known[id] {
			return nil, fmt.Errorf("usage snapshot names unconfigured model %s", id)
		}
		copy := usage
		tracker.usage[id] = &copy
	}
	return tracker, nil
}

func validateUsageSnapshot(snapshot *UsageSnapshot) error {
	if snapshot == nil || snapshot.Version != 1 || snapshot.CapturedAt.IsZero() {
		return errors.New("usage snapshot requires version 1 and captured_at")
	}
	if len(snapshot.Models) == 0 || len(snapshot.Models) > MaxRoutingModels {
		return fmt.Errorf("usage snapshot requires 1..%d models", MaxRoutingModels)
	}
	for id, usage := range snapshot.Models {
		if err := validateUsageEntry(id, usage); err != nil {
			return err
		}
	}

	return nil
}

func validateUsageEntry(id string, usage ModelUsage) error {
	if !routingName(id) || usage.CurrentRPM < 0 || usage.CurrentTPM < 0 || usage.ErrorCount < 0 || !finiteNonnegative(usage.TotalSpend) {
		return fmt.Errorf("invalid usage snapshot entry %q", id)
	}
	return nil
}
