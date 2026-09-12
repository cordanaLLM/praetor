package clientsetup

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"slices"
)

func validateJSON(ctx context.Context, raw []byte) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if len(raw) == 0 || len(raw) > MaxConfigBytes {
		return errors.New("JSON input requires 1..1048576 bytes")
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(raw))
	complete := false
	for i := 0; i <= len(raw); i++ {
		if err := checkContext(ctx); err != nil {
			return err
		}
		token, err := decoder.ReadToken()
		if errors.Is(err, io.EOF) && complete {
			return nil
		}
		if err != nil {
			return errors.New("invalid or ambiguous client JSON")
		}
		if err := validateJSONPosition(token, i, complete, decoder.StackDepth()); err != nil {
			return err
		}
		complete = decoder.StackDepth() == 0
	}
	return errors.New("JSON token bound exceeded")
}

func validateJSONPosition(token jsontext.Token, index int, complete bool, depth int) error {
	if complete || (index == 0 && token.Kind() != '{') {
		return errors.New("client JSON requires exactly one object")
	}
	if depth > 32 {
		return errors.New("JSON nesting exceeds 32")
	}
	return nil
}

func marshalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value, json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		return nil, errors.New("could not encode client configuration")
	}
	return append(raw, '\n'), nil
}

func jsonObject(raw []byte) (map[string]jsontext.Value, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return nil, errors.New("client config section must be an object")
	}
	var value map[string]jsontext.Value
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, errors.New("invalid client config object")
	}
	return value, nil
}

func mergeJSON(ctx context.Context, registry Registry, client Client, existing []byte) ([]byte, error) {
	root := make(map[string]jsontext.Value)
	if len(existing) != 0 {
		if err := validateJSON(ctx, existing); err != nil {
			return nil, err
		}
		var err error
		root, err = jsonObject(existing)
		if err != nil {
			return nil, err
		}
	}
	key := "mcpServers"
	if client == OpenCodeV1 || client == Kilo {
		key = "mcp"
	}
	servers := make(map[string]jsontext.Value)
	if raw, ok := root[key]; ok {
		var err error
		servers, err = jsonObject(raw)
		if err != nil {
			return nil, err
		}
	}
	changed, err := mergeJSONServers(registry, client, servers)
	if err != nil {
		return nil, err
	}
	if !changed {
		return bytes.Clone(existing), nil
	}
	root[key], err = marshalJSON(servers)
	if err != nil {
		return nil, err
	}
	return marshalJSON(root)
}

func mergeJSONServers(registry Registry, client Client, servers map[string]jsontext.Value) (bool, error) {
	changed := false
	for _, server := range registry.Servers {
		if current, ok := servers[server.Name]; ok {
			if !matchingJSONServer(current, client, server) {
				return false, fmt.Errorf("%w: %s", ErrConflict, server.Name)
			}
			continue
		}
		raw, err := marshalJSON(serverFields(server, client))
		if err != nil {
			return false, err
		}
		servers[server.Name] = raw
		changed = true
	}
	return changed, nil
}

func serverFields(server Server, client Client) map[string]any {
	if client == OpenCodeV1 || client == Kilo {
		return map[string]any{"type": "local", "command": append([]string{server.Command}, server.Args...)}
	}
	fields := map[string]any{"command": server.Command, "args": server.Args}
	if client == Claude {
		fields["type"] = "stdio"
	}
	return fields
}

func matchingJSONServer(raw []byte, client Client, server Server) bool {
	fields, err := jsonObject(raw)
	if err != nil {
		return false
	}
	if client == OpenCodeV1 || client == Kilo {
		return stringField(fields, "type") == "local" && matchingArgs(fields["command"], append([]string{server.Command}, server.Args...))
	}
	if stringField(fields, "command") != server.Command {
		return false
	}
	for _, remote := range []string{"url", "httpUrl", "http_url"} {
		if _, ok := fields[remote]; ok {
			return false
		}
	}
	if rawType, ok := fields["type"]; ok && stringValue(rawType) != "stdio" {
		return false
	}
	return matchingArgs(fields["args"], server.Args)
}

func stringField(fields map[string]jsontext.Value, key string) string {
	return stringValue(fields[key])
}
func stringValue(raw []byte) string {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}

func matchingArgs(raw []byte, wanted []string) bool {
	if len(raw) == 0 {
		return len(wanted) == 0
	}
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '[' {
		return false
	}
	var args []string
	return json.Unmarshal(raw, &args) == nil && slices.Equal(args, wanted)
}
