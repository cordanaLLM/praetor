package promptopt

import (
	"fmt"

	"github.com/cordanaLLM/praetor/internal/notebook"
)

// UnmarshalJSON requires an explicit quality floor, including when that floor is
// zero. Missing and null values cannot silently relax the experiment contract.
func (e *Experiment) UnmarshalJSON(raw []byte) error {
	type fields Experiment
	var decoded fields
	wire := struct {
		*fields
		MinQuality *float64 `json:"min_quality"`
	}{fields: &decoded}
	if err := notebook.Decode(raw, &wire); err != nil {
		return err
	}
	if wire.MinQuality == nil {
		return fmt.Errorf("explicit non-null min_quality required")
	}
	decoded.MinQuality = *wire.MinQuality
	*e = Experiment(decoded)
	return nil
}

// UnmarshalJSON distinguishes measured zero and failed contracts from absent
// evidence. All measurements remain caller-supplied, not independently verified.
func (o *Observation) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Case           string   `json:"case"`
		Quality        *float64 `json:"quality"`
		CostUSD        *float64 `json:"cost_usd"`
		LatencyMS      *float64 `json:"latency_ms"`
		ContractPassed *bool    `json:"contract_passed"`
	}
	if err := notebook.Decode(raw, &wire); err != nil {
		return err
	}
	if wire.Quality == nil || wire.CostUSD == nil || wire.LatencyMS == nil || wire.ContractPassed == nil {
		return fmt.Errorf("explicit non-null quality, cost_usd, latency_ms and contract_passed required")
	}
	*o = Observation{Case: wire.Case, Quality: *wire.Quality, CostUSD: *wire.CostUSD,
		LatencyMS: *wire.LatencyMS, ContractPassed: *wire.ContractPassed}
	return nil
}
