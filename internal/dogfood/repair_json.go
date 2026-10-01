package dogfood

import (
	"encoding/json"
	"errors"

	"github.com/cordanaLLM/praetor/internal/strictjson"
)

const maxRepairReportBytes = 8 * 1024 * 1024
const maxRepairJSONTokens = 262144

// repairJSONOptions words the shared strictjson reader for repair reports, schedules and
// adopted baselines. Duplicate member names are refused at every depth, free-form diagnostic
// maps included; a syntax error and a second document keep the bounded-UTF-8 refusal
// json.Valid gave.
var repairJSONOptions = strictjson.Options{
	MaxBytes:  maxRepairReportBytes,
	MaxDepth:  32,
	MaxTokens: maxRepairJSONTokens,
	Messages: strictjson.Messages{
		Size:      "repair report must be bounded UTF-8 JSON",
		Syntax:    "repair report must be bounded UTF-8 JSON",
		Trailing:  "repair report must be bounded UTF-8 JSON",
		Surrogate: "unpaired repair Unicode surrogate",
		Depth:     "repair JSON exceeds %d nesting levels",
		Tokens:    "repair JSON exceeds token bound",
		Duplicate: "duplicate repair JSON field",
		Decode:    "repair report schema or field type is invalid",
	},
}

func validateRepairJSON(data []byte) error {
	return strictjson.Validate(data, repairJSONOptions)
}

// The typed round-trip below defines exact field spelling and required fields.
// Iterative comparison avoids recursive schema walkers and permits diagnostic map keys.
func validateRepairShape(original, canonical []byte) error {
	var left, right any
	if err := json.Unmarshal(original, &left); err != nil {
		return errors.New("invalid repair report")
	}
	if err := json.Unmarshal(canonical, &right); err != nil {
		return err
	}
	queue := [][2]any{{left, right}}
	for index := 0; index < len(queue) && index < maxRepairJSONTokens; index++ {
		children, err := repairShapeChildren(queue[index])
		if err != nil {
			return err
		}
		queue = append(queue, children...)
		if len(queue) > maxRepairJSONTokens {
			return errors.New("repair report schema exceeds node bound")
		}
	}
	return nil
}

func repairShapeChildren(pair [2]any) ([][2]any, error) {
	switch expected := pair[1].(type) {
	case map[string]any:
		return repairObjectChildren(pair[0], expected)
	case []any:
		actual, ok := pair[0].([]any)
		if !ok || len(actual) != len(expected) {
			return nil, errors.New("repair report arrays must be explicit")
		}
		children := make([][2]any, len(expected))
		for i := 0; i < len(expected) && i < maxRepairJSONTokens; i++ {
			children[i] = [2]any{actual[i], expected[i]}
		}
		return children, nil
	default:
		if pair[0] == nil && pair[1] != nil {
			return nil, errors.New("repair report required scalar cannot be null")
		}
	}
	return nil, nil
}

func repairObjectChildren(value any, expected map[string]any) ([][2]any, error) {
	actual, ok := value.(map[string]any)
	if !ok || len(actual) != len(expected) {
		return nil, errors.New("repair report fields are missing, null or unknown")
	}
	children := make([][2]any, 0, len(expected))
	for key, value := range expected {
		got, exists := actual[key]
		if !exists {
			return nil, errors.New("repair report field spelling must be exact")
		}
		children = append(children, [2]any{got, value})
	}
	return children, nil
}

func decodeRepairReport(data []byte) (*SuiteReport, error) {
	var report SuiteReport
	if err := strictjson.Decode(data, &report, repairJSONOptions); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	// Optional explicitly empty fields may disappear under omitempty; reject them
	// rather than silently transforming their original representation.
	if err := validateRepairShape(data, canonical); err != nil {
		return nil, err
	}
	return &report, nil
}
