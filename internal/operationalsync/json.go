package operationalsync

import "github.com/cordanaLLM/praetor/internal/strictjson"

// derivedJSON words the shared strictjson reader for a derived JSON file the overlay rewrites.
// Its byte bound is the 1 MiB every owner file is read under (readFiles, sync.go). Duplicate
// member names are refused at every depth, case-folded too: the overlay replaces one top-level
// scalar by its exact name, and a second spelling of that name would be a second identity the
// replacement leaves behind.
var derivedJSON = strictjson.Options{
	MaxBytes:  1 << 20,
	MaxDepth:  128,
	MaxTokens: 50000,
	Messages: strictjson.Messages{
		Duplicate: "duplicate JSON key %q",
		Count:     "JSON exceeds %d tokens",
		Decode:    "%w",
	},
}
