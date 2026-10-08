package schemacheck

import (
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

// ReadTOML reads one TOML document and returns a JSON-compatible value that
// Schema.ValidateValue accepts.
func ReadTOML(raw []byte) (any, error) {
	var result any
	if err := toml.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("read TOML: %w", err)
	}
	if result == nil {
		result = map[string]any{}
	}
	return result, nil
}
