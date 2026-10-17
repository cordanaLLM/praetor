package clientsetup

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"

	"github.com/cordanaLLM/praetor/internal/clientjson"
)

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
	root, err := jsonRoot(ctx, existing)
	if err != nil {
		return nil, err
	}
	key := "mcpServers"
	if client == OpenCodeV1 || client == Kilo {
		key = "mcp"
	}
	servers, err := jsonMember(root, key)
	if err != nil {
		return nil, err
	}
	changed, err := mergeJSONServers(registry, client, servers)
	if err != nil {
		return nil, err
	}
	schemaAdded := ensureOpenCodeSchema(root, client)
	if !changed && !schemaAdded {
		return bytes.Clone(existing), nil
	}
	if changed {
		root[key], err = marshalJSON(servers)
		if err != nil {
			return nil, err
		}
	}
	return marshalJSON(root)
}

// jsonRoot validates existing and decodes it as the configuration's root object; an empty file is
// an empty root.
func jsonRoot(ctx context.Context, existing []byte) (map[string]jsontext.Value, error) {
	if len(existing) == 0 {
		return make(map[string]jsontext.Value), nil
	}
	if err := clientjson.Validate(ctx, existing); err != nil {
		return nil, err
	}
	return jsonObject(existing)
}

// jsonMember decodes root's member key as an object, or returns an empty one when root has none.
func jsonMember(root map[string]jsontext.Value, key string) (map[string]jsontext.Value, error) {
	raw, ok := root[key]
	if !ok {
		return make(map[string]jsontext.Value), nil
	}
	return jsonObject(raw)
}

// openCodeSchema is the schema opencode writes into a loaded opencode.json that names none
// (opencode v1.18.7, packages/opencode/src/config/config.ts, loadConfig): a file adoption wrote
// without it turned dirty on opencode's first run (ledger engine-opencode-template-schema-20261017).
const openCodeSchema = `"https://opencode.ai/config.json"`

// ensureOpenCodeSchema adds openCodeSchema to an OpenCode configuration that names no schema and
// reports whether it did; a schema the file already names is kept.
func ensureOpenCodeSchema(root map[string]jsontext.Value, client Client) bool {
	if client != OpenCodeV1 {
		return false
	}
	if _, ok := root["$schema"]; ok {
		return false
	}
	root["$schema"] = jsontext.Value(openCodeSchema)
	return true
}

func mergeClaudeLocal(ctx context.Context, registry Registry, project string, existing []byte) ([]byte, error) {
	root := bytes.Clone(existing)
	if len(root) == 0 {
		root = []byte(`{}`)
	}
	if err := clientjson.Validate(ctx, root); err != nil {
		return nil, err
	}
	projects, hasProjects, err := objectMember(root, "projects")
	if err != nil {
		return nil, err
	}
	if !hasProjects {
		projects = []byte(`{}`)
	}
	mergedProjects, changed, err := mergeClaudeLocalProjects(ctx, registry, project, projects, hasProjects)
	if err != nil {
		return nil, err
	}
	if !changed && hasProjects {
		return bytes.Clone(existing), nil
	}
	return updateJSONMember(root, "projects", mergedProjects, hasProjects)
}

func mergeClaudeLocalProjects(ctx context.Context, registry Registry, project string, projects []byte, hasProjects bool) ([]byte, bool, error) {
	projectBytes, hasProject, err := objectMember(projects, project)
	if err != nil {
		return nil, false, err
	}
	if !hasProject {
		projectBytes = []byte(`{}`)
	}
	mergedProject, changed, err := mergeClaudeLocalProject(ctx, registry, projectBytes, hasProject)
	if err != nil {
		return nil, false, err
	}
	if !changed && hasProject {
		return bytes.Clone(projects), false, nil
	}
	updated, err := updateJSONMember(projects, project, mergedProject, hasProject)
	return updated, true, err
}

func mergeClaudeLocalProject(ctx context.Context, registry Registry, project []byte, hasProject bool) ([]byte, bool, error) {
	servers, hasServers, err := objectMember(project, "mcpServers")
	if err != nil {
		return nil, false, err
	}
	if !hasServers {
		servers = []byte(`{}`)
	}
	merged, changed, err := mergeOrderedJSONServers(ctx, registry, servers)
	if err != nil {
		return nil, false, err
	}
	if !hasServers {
		changed = true
	}
	if !hasProject {
		changed = true
	}
	if !changed {
		return bytes.Clone(project), false, nil
	}
	updated, err := updateJSONMember(project, "mcpServers", merged, hasServers)
	return updated, true, err
}

func updateJSONMember(raw []byte, name string, value []byte, exists bool) ([]byte, error) {
	if exists {
		return clientjson.ReplaceMember(raw, name, value)
	}
	return clientjson.AppendMember(raw, name, value)
}

func objectMember(raw []byte, name string) ([]byte, bool, error) {
	object, err := clientjson.DecodeObject(raw)
	if err != nil {
		return nil, false, err
	}
	value, found := object.Get(name)
	return value, found, nil
}

func mergeOrderedJSONServers(ctx context.Context, registry Registry, raw []byte) ([]byte, bool, error) {
	if err := clientjson.Validate(ctx, raw); err != nil {
		return nil, false, err
	}
	changed := false
	for _, server := range registry.Servers {
		if err := checkContext(ctx); err != nil {
			return nil, false, err
		}
		current, exists, err := objectMember(raw, server.Name)
		if err != nil {
			return nil, false, err
		}
		if exists {
			if !matchingJSONServer(current, ClaudeLocal, server) {
				return nil, false, fmt.Errorf("%w: %s", ErrConflict, server.Name)
			}
			continue
		}
		encoded, encodeErr := marshalJSON(serverFields(server, ClaudeLocal))
		if encodeErr != nil {
			return nil, false, encodeErr
		}
		raw, err = clientjson.AppendMember(raw, server.Name, encoded)
		if err != nil {
			return nil, false, err
		}
		changed = true
	}
	if !changed {
		return bytes.Clone(raw), false, nil
	}
	return raw, changed, nil
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
	if client == Claude || client == ClaudeLocal {
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
	for _, remote := range []string{"url", "httpUrl", "http_url", "serverUrl"} {
		if _, ok := fields[remote]; ok {
			return false
		}
	}
	if rawType, ok := fields["type"]; ok && clientjson.StringValue(rawType) != "stdio" {
		return false
	}
	return matchingArgs(fields["args"], server.Args)
}

func stringField(fields map[string]jsontext.Value, key string) string {
	return clientjson.StringValue(fields[key])
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
