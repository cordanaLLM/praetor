package clientsetup

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"strings"
)

func validateMemoryBinding(binding MemoryBinding) error {
	if !absoluteLiteral(binding.ProjectRoot) {
		return errors.New("memory project root requires a clean absolute literal directory")
	}
	if len(binding.BankID) < 1 || len(binding.BankID) > 256 || !literal(binding.BankID) || strings.TrimSpace(binding.BankID) != binding.BankID {
		return errors.New("memory bank requires 1..256 literal bytes without surrounding whitespace")
	}
	return nil
}

// BuildMemoryPlan adds a repository mapping to an existing Hindsight coding-agent
// config. Unknown settings (including auth and other banks) are retained privately.
// Conflicting exact mappings are rejected, and a no-op preserves bytes exactly.
func BuildMemoryPlan(ctx context.Context, binding MemoryBinding, existing []byte) (*Plan, error) {
	if err := validateMemoryBinding(binding); err != nil {
		return nil, err
	}
	if err := validateJSON(ctx, existing); err != nil {
		return nil, err
	}
	root, err := jsonObject(existing)
	if err != nil {
		return nil, err
	}
	content, err := mergeMemoryBinding(binding, existing, root)
	if err != nil {
		return nil, err
	}
	if len(content) > MaxConfigBytes {
		return nil, errors.New("memory candidate exceeds 1 MiB")
	}
	return &Plan{Client: "hindsight", Mode: "merge", ExportName: "hindsight-config.json", SourceSHA256: digest(existing),
		Content: content, Changed: !bytes.Equal(content, existing), Documentation: "installed Hindsight coding-agent mapPathToBank contract"}, checkContext(ctx)
}

func mergeMemoryBinding(binding MemoryBinding, existing []byte, root map[string]jsontext.Value) ([]byte, error) {
	raw, ok := root["mapPathToBank"]
	if !ok {
		raw = []byte(`{}`)
	}
	mapping, err := jsonObject(raw)
	if err != nil {
		return nil, errors.New("memory mapPathToBank must be an object")
	}
	content := bytes.Clone(existing)
	if current, found := mapping[binding.ProjectRoot]; found {
		if stringValue(current) != binding.BankID {
			return nil, errors.New("memory project already maps to a different bank")
		}
	} else {
		mapping[binding.ProjectRoot], err = marshalJSON(binding.BankID)
		if err != nil {
			return nil, err
		}
		root["mapPathToBank"], err = marshalJSON(mapping)
		if err != nil {
			return nil, err
		}
		content, err = marshalJSON(root)
		if err != nil {
			return nil, err
		}
	}
	return content, nil
}
