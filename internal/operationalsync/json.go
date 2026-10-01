package operationalsync

import "github.com/cordanaLLM/praetor/internal/strictjson"

// derivedJSON words the shared strictjson reader for a derived JSON file the overlay rewrites.
// Its byte bound is the 1 MiB every owner file is read under (readFiles, sync.go). Identical
// member names are refused at every depth. Names are compared exactly, because the document
// decodes into maps that keep every spelling and nested names can be case-sensitive data (a
// build argument may carry both HTTP_PROXY and http_proxy); ownerJSON refuses a second spelling
// of the one top-level name it replaces, which the exact-name replacement would leave behind.
var derivedJSON = strictjson.Options{
	Names:     strictjson.ExactNames,
	MaxBytes:  1 << 20,
	MaxDepth:  128,
	MaxTokens: 50000,
	Messages: strictjson.Messages{
		Duplicate: "duplicate JSON key %q",
		Count:     "JSON exceeds %d tokens",
		Decode:    "%w",
	},
}
