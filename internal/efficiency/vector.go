// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/strictjson"
)

// vectorFieldJSON reads one vector field of a records row. The records file around it already
// passed forge's strict reader; this read adds the field's own unknown-member refusal.
var vectorFieldJSON = strictjson.Options{MaxBytes: forge.MaxMergedPRBytes, MaxDepth: 4}

// vectorFieldInput is the records shape of one vector field.
type vectorFieldInput struct {
	Value      json.RawMessage `json:"value"`
	Provenance Provenance      `json:"provenance"`
	Low        json.RawMessage `json:"low"`
	High       json.RawMessage `json:"high"`
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// parseVectorField is the one reader of a vector field. A field absent from the row is refused,
// an explicit null is not measured (nil field), and anything else must be an object carrying a
// value and a provenance label, plus low and high bounds for an interval. ValidateRow checks the
// label and the bounds of every row, records and live alike.
func parseVectorField[T any](raw json.RawMessage) (*VectorField[T], error) {
	if len(raw) == 0 {
		return nil, errors.New(`missing: a records row carries every vector field, as {"value": ..., "provenance": ...} or null when not measured`)
	}
	if isJSONNull(raw) {
		return nil, nil
	}
	var in vectorFieldInput
	if err := strictjson.Decode(raw, &in, vectorFieldJSON); err != nil {
		return nil, fmt.Errorf(`want {"value": ..., "provenance": ...}: %w`, err)
	}
	field := &VectorField[T]{Provenance: in.Provenance}
	if err := decodeVectorPart("value", in.Value, &field.Value); err != nil {
		return nil, err
	}
	var err error
	if field.Low, err = optionalVectorPart[T]("low", in.Low); err != nil {
		return nil, err
	}
	if field.High, err = optionalVectorPart[T]("high", in.High); err != nil {
		return nil, err
	}
	return field, nil
}

func decodeVectorPart[T any](part string, raw json.RawMessage, target *T) error {
	if len(raw) == 0 || isJSONNull(raw) {
		return fmt.Errorf("%s missing", part)
	}
	if err := strictjson.Decode(raw, target, vectorFieldJSON); err != nil {
		return fmt.Errorf("%s: %w", part, err)
	}
	return nil
}

func optionalVectorPart[T any](part string, raw json.RawMessage) (*T, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	target := new(T)
	if err := decodeVectorPart(part, raw, target); err != nil {
		return nil, err
	}
	return target, nil
}

// components returns a vector value as named numbers: one unnamed component for a scalar, one
// per key for a map. The four field types of UnitReport are the only ones accepted.
func components(value any) (map[string]float64, error) {
	switch v := value.(type) {
	case int:
		return map[string]float64{"": float64(v)}, nil
	case int64:
		return map[string]float64{"": float64(v)}, nil
	case float64:
		return map[string]float64{"": v}, nil
	case map[string]int64:
		out := make(map[string]float64, len(v))
		for key, n := range v {
			out[key] = float64(n)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported vector value type %T", value)
	}
}

// vectorReading is one present vector field as numbers, the form both validation and the
// summary read.
type vectorReading struct {
	provenance Provenance
	value      map[string]float64
	low, high  map[string]float64 // nil unless the field carries the bound
}

func (f *VectorField[T]) reading() (vectorReading, error) {
	r := vectorReading{provenance: f.Provenance}
	var err error
	if r.value, err = components(f.Value); err != nil {
		return r, err
	}
	if f.Low != nil {
		if r.low, err = components(*f.Low); err != nil {
			return r, err
		}
	}
	if f.High != nil {
		if r.high, err = components(*f.High); err != nil {
			return r, err
		}
	}
	return r, nil
}

// namedVector is one vector field of a row: nil reading means not measured.
type namedVector struct {
	name    string
	reading *vectorReading
}

func readingOf[T any](f *VectorField[T]) (*vectorReading, error) {
	if f == nil {
		return nil, nil
	}
	r, err := f.reading()
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// vectorFieldNames are the six vector fields of a row in schema order.
var vectorFieldNames = [...]string{"tokens_by_provider", "wall_seconds", "review_rounds", "retries", "operator_minutes", "escaped_defects"}

// vectors lists the six vector fields of a row in vectorFieldNames order.
func (u UnitReport) vectors() ([]namedVector, error) {
	var readings [len(vectorFieldNames)]*vectorReading
	var errs [len(vectorFieldNames)]error
	readings[0], errs[0] = readingOf(u.TokensByProvider)
	readings[1], errs[1] = readingOf(u.WallSeconds)
	readings[2], errs[2] = readingOf(u.ReviewRounds)
	readings[3], errs[3] = readingOf(u.Retries)
	readings[4], errs[4] = readingOf(u.OperatorMinutes)
	readings[5], errs[5] = readingOf(u.EscapedDefects)
	out := make([]namedVector, 0, len(vectorFieldNames))
	for i, name := range vectorFieldNames {
		if errs[i] != nil {
			return nil, fmt.Errorf("field %q: %w", name, errs[i])
		}
		out = append(out, namedVector{name: name, reading: readings[i]})
	}
	return out, nil
}

// validate checks the label, sign and bounds of one present field. Only an interval carries
// bounds, and it carries both, over the same components as its value, with low <= value <= high.
func (r vectorReading) validate() error {
	if !ValidProvenance(r.provenance) {
		return fmt.Errorf("missing or invalid provenance label %q (must be measured, modeled, cited, or interval)", r.provenance)
	}
	for key, v := range r.value {
		if v < 0 {
			return fmt.Errorf("negative value %v%s", v, componentSuffix(key))
		}
	}
	if r.provenance != ProvenanceInterval {
		if r.low != nil || r.high != nil {
			return fmt.Errorf("low and high bounds belong to an interval field, not a %s one", r.provenance)
		}
		return nil
	}
	if r.low == nil || r.high == nil {
		return errors.New("an interval field carries both low and high bounds")
	}
	return r.validateBounds()
}

func (r vectorReading) validateBounds() error {
	if len(r.low) != len(r.value) || len(r.high) != len(r.value) {
		return errors.New("interval bounds must name the same components as the value")
	}
	for _, key := range slices.Sorted(maps.Keys(r.value)) {
		low, okLow := r.low[key]
		high, okHigh := r.high[key]
		if !okLow || !okHigh {
			return fmt.Errorf("interval bounds miss component%s", componentSuffix(key))
		}
		if v := r.value[key]; low > v || v > high {
			return fmt.Errorf("interval bounds [%v, %v] do not hold value %v%s", low, high, v, componentSuffix(key))
		}
	}
	return nil
}

func componentSuffix(key string) string {
	if key == "" {
		return ""
	}
	return fmt.Sprintf(" for %q", key)
}

// ValidateRow checks that a unit row has the current metric epoch, a known disposition, and on
// every measured vector field a valid provenance label, no negative value and, for an interval,
// both bounds around the value. A row whose field carries no label is refused.
func ValidateRow(u UnitReport) error {
	if u.MetricEpoch == "" {
		return fmt.Errorf("unit #%d: missing metric_epoch tag", u.PullRequestNumber)
	}
	if u.MetricEpoch != CurrentMetricEpoch {
		return fmt.Errorf("unit #%d: incompatible metric epoch %q (expected %q)", u.PullRequestNumber, u.MetricEpoch, CurrentMetricEpoch)
	}
	if !ValidDisposition(u.Disposition) {
		return fmt.Errorf("unit #%d: invalid disposition %q", u.PullRequestNumber, u.Disposition)
	}
	fields, err := u.vectors()
	if err != nil {
		return fmt.Errorf("unit #%d: %w", u.PullRequestNumber, err)
	}
	for _, f := range fields {
		if f.reading == nil {
			continue
		}
		if err := f.reading.validate(); err != nil {
			return fmt.Errorf("unit #%d: field %q: %w", u.PullRequestNumber, f.name, err)
		}
	}
	return nil
}

// ValidateRows validates every row and ensures metric epochs are not mixed.
func ValidateRows(units []UnitReport) error {
	var firstEpoch string
	for i, u := range units {
		if i == 0 {
			firstEpoch = u.MetricEpoch
		} else if u.MetricEpoch != firstEpoch {
			return fmt.Errorf("mixed metric epochs in ledger: found %q and %q (schema change must not silently mix old and new rows)", firstEpoch, u.MetricEpoch)
		}
	}
	for _, u := range units {
		if err := ValidateRow(u); err != nil {
			return err
		}
	}
	return nil
}
