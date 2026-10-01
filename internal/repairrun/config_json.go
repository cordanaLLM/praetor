package repairrun

import (
	"encoding/json"
	"errors"
	"reflect"

	"github.com/cordanaLLM/praetor/internal/strictjson"
)

// maxRepairJSONBytes bounds every document decodeExactJSON reads: the largest is result.json,
// read under 1 MiB (run_state.go); configuration, start state and proposals are smaller.
const maxRepairJSONBytes = 1 << 20

// repairJSON words the shared strictjson reader for repair configuration and run state. A
// syntax error and a second document keep the "invalid UTF-8 JSON" refusal json.Valid gave.
func repairJSON(allowNull bool) strictjson.Options {
	return strictjson.Options{
		MaxBytes:   maxRepairJSONBytes,
		MaxDepth:   16,
		MaxTokens:  65536,
		RejectNull: !allowNull,
		Messages: strictjson.Messages{
			Size:      "invalid UTF-8 JSON",
			Syntax:    "invalid UTF-8 JSON",
			Trailing:  "invalid UTF-8 JSON",
			Surrogate: "unpaired Unicode surrogate",
			Null:      "invalid or null repair JSON value",
			Depth:     "repair JSON nesting bound exceeded",
			Count:     "repair JSON token bound exceeded",
			Duplicate: "duplicate or aliased repair JSON field",
			Decode:    "invalid repair JSON schema",
		},
	}
}

// Token validation rejects duplicate, aliased and null keys before typed decoding.
func decodeConfigJSON(data []byte, value any) error {
	return decodeExactJSON(data, value, false)
}

func decodeExactJSON(data []byte, value any, allowNull bool) error {
	if err := strictjson.Decode(data, value, repairJSON(allowNull)); err != nil {
		return err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var input, output any
	if err := json.Unmarshal(data, &input); err != nil {
		return err
	}
	if err := json.Unmarshal(canonical, &output); err != nil {
		return err
	}
	if !reflect.DeepEqual(input, output) {
		return errors.New("repair JSON requires exact field spelling and explicit fields")
	}
	return nil
}
