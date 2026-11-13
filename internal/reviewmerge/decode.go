package reviewmerge

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/strictjson"
)

// UnmarshalJSON validates the document strictly and bounds finding allocation before retention.
func (d *Draft) UnmarshalJSON(raw []byte) error {
	var header struct {
		Model    string          `json:"model"`
		Kind     string          `json:"kind"`
		Findings json.RawMessage `json:"findings"`
	}
	if err := strictjson.Decode(raw, &header, strictjson.Options{MaxBytes: maxJSONBytes, MaxDepth: 16}); err != nil {
		return fmt.Errorf("decode draft: %w", err)
	}
	findings, err := decodeFindings(header.Findings)
	if err != nil {
		return fmt.Errorf("decode draft findings: %w", err)
	}
	*d = Draft{Model: header.Model, Kind: header.Kind, Findings: findings}
	return nil
}

// decodeFindings decodes at most MaxFindings records from an already strictly validated array.
func decodeFindings(raw []byte) ([]Finding, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	start, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("read array: %w", err)
	}
	if start != json.Delim('[') {
		return nil, fmt.Errorf("findings must be an array")
	}
	findings := make([]Finding, 0)
	for i := 0; i < MaxFindings && decoder.More(); i++ {
		var f Finding
		if err := decoder.Decode(&f); err != nil {
			return nil, fmt.Errorf("finding %d: %w", i, err)
		}
		findings = append(findings, f)
	}
	if decoder.More() {
		return nil, fmt.Errorf("finding count exceeds limit %d", MaxFindings)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("close array: %w", err)
	}
	return findings, nil
}
