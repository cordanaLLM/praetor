package notebook

import "github.com/cordanaLLM/praetor/internal/strictjson"

// decodeOptions bounds one notebook artifact to 1 MiB and 32 nesting levels and keeps the
// default strictjson wording, which is this package's.
var decodeOptions = strictjson.Options{
	MaxBytes: 1 << 20,
	MaxDepth: 32,
	Messages: strictjson.Messages{Decode: "decode artifact: %w"},
}

// Decode accepts one bounded strict UTF-8 JSON value, rejecting ambiguous keys: two member
// names of one object that encoding/json would match to the same struct field.
func Decode(raw []byte, value any) error {
	return strictjson.Decode(raw, value, decodeOptions)
}
