package repairrun

import "github.com/cordanaLLM/praetor/internal/strictjson"

// providerJSON words the shared strictjson reader for a repair provider response and the
// proposal it carries. A syntax error and a second document keep the oversized-or-invalid
// refusal json.Valid gave.
var providerJSON = strictjson.Options{
	MaxBytes:  providerResponseLimit,
	MaxDepth:  16,
	MaxTokens: 65536,
	Messages: strictjson.Messages{
		Size:      "repair provider returned invalid or oversized UTF-8 JSON",
		Syntax:    "repair provider returned invalid or oversized UTF-8 JSON",
		Trailing:  "repair provider returned invalid or oversized UTF-8 JSON",
		Surrogate: "repair provider Unicode surrogate is unpaired",
		Depth:     "repair provider JSON exceeds nesting bound",
		Tokens:    "repair provider JSON exceeds token bound",
		Duplicate: "repair provider JSON contains duplicate or case-aliased fields",
	},
}

func providerValidateJSON(data []byte) error {
	return strictjson.Validate(data, providerJSON)
}
