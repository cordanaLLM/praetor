package dogfood

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"
)

const maxRepairReportBytes = 8 * 1024 * 1024
const maxRepairJSONTokens = 262144

type repairJSONFrame struct {
	keys    map[string]bool
	wantKey bool
}

// Duplicate keys are rejected at every depth, including free-form diagnostic maps.
func validateRepairJSON(data []byte) error {
	if len(data) > maxRepairReportBytes || !utf8.Valid(data) || !json.Valid(data) {
		return errors.New("repair report must be bounded UTF-8 JSON")
	}
	if err := validateRepairEscapes(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	stack := []repairJSONFrame{}
	for i := 0; i < maxRepairJSONTokens; i++ {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errors.New("invalid repair JSON")
		}
		stack, err = acceptRepairToken(stack, token)
		if err != nil {
			return err
		}
	}
	return errors.New("repair JSON exceeds token bound")
}

func acceptRepairToken(stack []repairJSONFrame, token json.Token) ([]repairJSONFrame, error) {
	var frame *repairJSONFrame
	if len(stack) > 0 {
		frame = &stack[len(stack)-1]
	}
	if delimiter, ok := token.(json.Delim); ok {
		return repairJSONDelimiter(stack, frame, delimiter)
	}
	if frame == nil || frame.keys == nil {
		return stack, nil
	}
	if frame.wantKey {
		key, ok := token.(string)
		if !ok || frame.keys[key] {
			return nil, errors.New("duplicate repair JSON field")
		}
		frame.keys[key] = true
	}
	frame.wantKey = !frame.wantKey
	return stack, nil
}

func repairJSONDelimiter(stack []repairJSONFrame, frame *repairJSONFrame, delimiter json.Delim) ([]repairJSONFrame, error) {
	if delimiter == '}' || delimiter == ']' {
		return stack[:len(stack)-1], nil
	}
	if frame != nil && frame.keys != nil {
		frame.wantKey = true
	}
	if len(stack) >= 32 {
		return nil, errors.New("repair JSON exceeds 32 nesting levels")
	}
	next := repairJSONFrame{}
	if delimiter == '{' {
		next.keys = make(map[string]bool)
		next.wantKey = true
	}
	return append(stack, next), nil
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

func validateRepairEscapes(data []byte) error {
	quoted := false
	for i := 0; i < len(data) && i < maxRepairReportBytes; i++ {
		if data[i] == '"' {
			quoted = !quoted
			continue
		}
		if data[i] != '\\' || !quoted {
			continue
		}
		end, err := repairEscapeEnd(data, i+1)
		if err != nil {
			return err
		}
		i = end
	}
	return nil
}

// JSON syntax validation precedes this helper, so each first escape is complete.
func repairEscapeEnd(data []byte, i int) (int, error) {
	if data[i] != 'u' {
		return i, nil
	}
	value, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
	if err != nil {
		return 0, errors.New("invalid repair Unicode escape")
	}
	i += 4
	if value < 0xd800 || value > 0xdfff {
		return i, nil
	}
	if value >= 0xdc00 || i+6 >= len(data) || string(data[i+1:i+3]) != `\u` {
		return 0, errors.New("unpaired repair Unicode surrogate")
	}
	second, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
	if err != nil || second < 0xdc00 || second > 0xdfff {
		return 0, errors.New("unpaired repair Unicode surrogate")
	}
	return i + 6, nil
}

func decodeRepairReport(data []byte) (*SuiteReport, error) {
	if err := validateRepairJSON(data); err != nil {
		return nil, err
	}
	var report SuiteReport
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return nil, errors.New("repair report schema or field type is invalid")
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
