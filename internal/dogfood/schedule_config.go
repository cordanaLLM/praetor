package dogfood

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
)

const (
	maxScheduleEntries    = 200000
	maxScheduleInputBytes = 16 << 20
)

// ScheduleConfig is an explicit local schedule, never an instruction from a transcript.
// Defaults apply only to omitted numeric limits; zero is rejected.
type ScheduleConfig struct {
	RunnerBinary           string        `json:"runner_binary"`
	Version                int           `json:"version"`
	SuiteConfig            string        `json:"suite_config"`
	SourceRoot             string        `json:"source_root"`
	StateDir               string        `json:"state_dir"`
	AllowRemote            bool          `json:"allow_remote"`
	IntervalSeconds        int           `json:"interval_seconds"`
	RetrySeconds           int           `json:"retry_seconds"`
	MaxConsecutiveFailures int           `json:"max_consecutive_failures"`
	MaxRuns                int           `json:"max_runs"`
	MaxBytes               int64         `json:"max_bytes"`
	RepairPolicy           *RepairPolicy `json:"repair_policy,omitempty"`
}

// LoadScheduleConfig validates a bounded, strict private JSON configuration.
func LoadScheduleConfig(ctx context.Context, path string) (*ScheduleConfig, error) {
	_, config, err := readScheduleConfig(ctx, path)
	return config, err
}

func readScheduleConfig(ctx context.Context, path string) ([]byte, *ScheduleConfig, error) {
	if ctx == nil {
		return nil, nil, errors.New("schedule requires a context")
	}
	data, err := readSchedulePrivate(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	names := []string{"version", "suite_config", "source_root", "state_dir", "runner_binary", "allow_remote", "interval_seconds", "retry_seconds", "max_consecutive_failures", "max_runs", "max_bytes", "repair_policy"}
	fields, err := scheduleObject(data, names)
	if err != nil {
		return nil, nil, err
	}
	required := []string{"version", "suite_config", "source_root", "state_dir", "runner_binary", "allow_remote"}
	for i := 0; i < len(required); i++ {
		if fields[required[i]] == nil {
			return nil, nil, errors.New("schedule is missing required fields")
		}
	}
	if fields["repair_policy"] != nil {
		if err := validateScheduleRepairJSON(fields["repair_policy"]); err != nil {
			return nil, nil, err
		}
	}
	config := &ScheduleConfig{IntervalSeconds: 86400, RetrySeconds: 3600, MaxConsecutiveFailures: 3, MaxRuns: 32, MaxBytes: 2 << 30}
	if err := json.Unmarshal(data, config); err != nil {
		return nil, nil, errors.New("invalid schedule field type")
	}
	if err := validateScheduleConfig(config); err != nil {
		return nil, nil, err
	}
	return data, config, nil
}

func scheduleObject(data []byte, names []string) (map[string]json.RawMessage, error) {
	if err := validateRepairJSON(data); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("schedule requires a JSON object")
	}
	fields := make(map[string]json.RawMessage)
	for i := 0; decoder.More() && i <= len(names); i++ {
		if err := decodeScheduleField(decoder, fields, names); err != nil {
			return nil, err
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return nil, errors.New("invalid schedule object")
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing schedule content")
	}
	return fields, nil
}

func scheduleHasKey(names []string, key string) bool {
	for i := 0; i < len(names) && i < 32; i++ {
		if names[i] == key {
			return true
		}
	}
	return false
}

func validateScheduleConfig(config *ScheduleConfig) error {
	if config.Version != 1 {
		return errors.New("schedule version must be 1")
	}
	paths := []string{config.SuiteConfig, config.SourceRoot, config.StateDir, config.RunnerBinary}
	for i := 0; i < len(paths); i++ {
		if err := validateSuitePath(paths[i]); err != nil {
			return err
		}
		if !filepath.IsAbs(paths[i]) || filepath.Clean(paths[i]) != paths[i] {
			return errors.New("schedule paths must be clean absolute paths")
		}
	}
	return validateScheduleBounds(config)
}

func validateScheduleBounds(config *ScheduleConfig) error {
	if config.IntervalSeconds < 60 || config.IntervalSeconds > 2592000 || config.RetrySeconds < 60 || config.RetrySeconds > 86400 {
		return errors.New("schedule interval must be 60..2592000 seconds and retry 60..86400 seconds")
	}
	if config.MaxConsecutiveFailures < 1 || config.MaxConsecutiveFailures > 3 || config.MaxRuns < 1 || config.MaxRuns > 1024 {
		return errors.New("schedule failure limit must be 1..3 and run limit 1..1024")
	}
	if config.MaxBytes < 1<<20 || config.MaxBytes > 16<<30 {
		return errors.New("schedule byte limit must be 1 MiB..16 GiB")
	}
	return nil
}

func validateScheduleRepairJSON(data []byte) error {
	names := []string{"routing_config", "usage_path", "task", "input_tokens", "output_tokens", "max_cost",
		"register", "register_source", "max_output_tokens", "prompt_register", "prompt_register_source", "register_manifest_sha256"}
	fields, err := scheduleObject(data, names)
	if err != nil {
		return err
	}
	required := []string{"routing_config", "task", "input_tokens", "output_tokens", "max_cost"}
	for i := 0; i < len(required); i++ {
		if fields[required[i]] == nil {
			return errors.New("repair policy is missing required fields")
		}
	}
	return nil
}

func decodeScheduleField(decoder *json.Decoder, fields map[string]json.RawMessage, names []string) error {
	token, err := decoder.Token()
	if err != nil {
		return errors.New("invalid schedule key")
	}
	key, ok := token.(string)
	if !ok || !scheduleHasKey(names, key) || fields[key] != nil {
		return errors.New("unknown or duplicate schedule field")
	}
	var value json.RawMessage
	if err := decoder.Decode(&value); err != nil {
		return errors.New("invalid schedule value")
	}
	if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return errors.New("null schedule values are forbidden")
	}
	fields[key] = value
	return nil
}
