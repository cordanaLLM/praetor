// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package efficiency

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParseVectorField_Positive_ObjectAndInterval(t *testing.T) {
	f, err := parseVectorField[int64](json.RawMessage(`{"value": 42, "provenance": "measured"}`))
	if err != nil || f == nil || f.Value != 42 || f.Provenance != ProvenanceMeasured || f.Low != nil || f.High != nil {
		t.Fatalf("object: %+v %v", f, err)
	}
	m, err := parseVectorField[map[string]int64](json.RawMessage(`{"value": {"a": 5}, "provenance": "interval", "low": {"a": 4}, "high": {"a": 9}}`))
	if err != nil || m.Low == nil || m.High == nil || (*m.Low)["a"] != 4 || (*m.High)["a"] != 9 {
		t.Fatalf("interval: %+v %v", m, err)
	}
}

func TestParseVectorField_Boundary_NullIsNotMeasured(t *testing.T) {
	f, err := parseVectorField[int](json.RawMessage(` null `))
	if err != nil || f != nil {
		t.Errorf("null must read as not measured: %+v %v", f, err)
	}
}

func TestParseVectorField_Negative_ShapesRefusedWithCause(t *testing.T) {
	for name, c := range map[string]struct{ raw, want string }{
		"absent":           {``, "missing"},
		"bare scalar":      {`120`, `want {"value"`},
		"unknown member":   {`{"value": 1, "provenance": "measured", "unit": "s"}`, "unknown field"},
		"repeated member":  {`{"value": 1, "value": 2, "provenance": "measured"}`, "duplicate"},
		"missing value":    {`{"provenance": "measured"}`, "value missing"},
		"null value":       {`{"value": null, "provenance": "measured"}`, "value missing"},
		"null bound":       {`{"value": 1, "provenance": "interval", "low": null, "high": 2}`, "low missing"},
		"wrong value type": {`{"value": "ten", "provenance": "measured"}`, "value:"},
	} {
		_, err := parseVectorField[int](json.RawMessage(c.raw))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %s must be refused naming %q, got %v", name, c.raw, c.want, err)
		}
	}
	_, err := parseVectorField[int](json.RawMessage(`{"value": "ten", "provenance": "measured"}`))
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Errorf("the json type error must stay reachable through the wrapping: %v", err)
	}
}

func TestValidateRow_Negative_IntervalBoundsSignAndLabels(t *testing.T) {
	low, high, outside := int64(10), int64(20), int64(5)
	cases := map[string]*VectorField[int64]{
		"interval without bounds":   {Value: 15, Provenance: ProvenanceInterval},
		"interval without high":     {Value: 15, Provenance: ProvenanceInterval, Low: &low},
		"value below the low bound": {Value: 5, Provenance: ProvenanceInterval, Low: &low, High: &high},
		"low above high":            {Value: 15, Provenance: ProvenanceInterval, Low: &high, High: &low},
		"bounds on a measured one":  {Value: 15, Provenance: ProvenanceMeasured, Low: &outside, High: &high},
		"negative value":            {Value: -1, Provenance: ProvenanceMeasured},
		"no label":                  {Value: 15},
		"unknown label":             {Value: 15, Provenance: "guessed"},
	}
	for name, field := range cases {
		row := validRow()
		row.WallSeconds = field
		if err := ValidateRow(row); err == nil || !strings.Contains(err.Error(), `field "wall_seconds"`) {
			t.Errorf("%s must be refused naming the field: %v", name, err)
		}
	}
	tokens := validRow()
	tokens.TokensByProvider = &VectorField[map[string]int64]{Value: map[string]int64{"a": 1, "b": 1}, Provenance: ProvenanceInterval,
		Low: &map[string]int64{"a": 0}, High: &map[string]int64{"a": 2, "b": 2}}
	if err := ValidateRow(tokens); err == nil {
		t.Error("interval bounds that miss a provider must be refused")
	}
	empty := validRow()
	empty.TokensByProvider = &VectorField[map[string]int64]{Value: map[string]int64{}, Provenance: ProvenanceInterval}
	if err := ValidateRow(empty); err == nil || !strings.Contains(err.Error(), "both low and high") {
		t.Errorf("an interval without bounds is refused even when its value has no components: %v", err)
	}
}

func TestValidateRow_Positive_IntervalAndUnmeasuredFields(t *testing.T) {
	low, high := int64(10), int64(20)
	row := validRow()
	row.WallSeconds = &VectorField[int64]{Value: 10, Provenance: ProvenanceInterval, Low: &low, High: &high}
	row.Retries = nil
	if err := ValidateRow(row); err != nil {
		t.Errorf("an interval at its low bound and a not-measured field are valid: %v", err)
	}
}

func TestComponents_Negative_UnsupportedType(t *testing.T) {
	if _, err := components("ten"); err == nil {
		t.Error("a value type outside the four vector field types must be refused")
	}
}

func validRow() UnitReport {
	return UnitReport{
		PullRequestNumber: 1,
		MetricEpoch:       CurrentMetricEpoch,
		Disposition:       DispositionQualified,
		TokensByProvider:  &VectorField[map[string]int64]{Value: map[string]int64{}, Provenance: ProvenanceMeasured},
		WallSeconds:       &VectorField[int64]{Value: 1, Provenance: ProvenanceMeasured},
		ReviewRounds:      &VectorField[int]{Value: 1, Provenance: ProvenanceMeasured},
		Retries:           &VectorField[int]{Value: 0, Provenance: ProvenanceMeasured},
		OperatorMinutes:   &VectorField[float64]{Value: 1, Provenance: ProvenanceMeasured},
		EscapedDefects:    &VectorField[int]{Value: 0, Provenance: ProvenanceMeasured},
	}
}
