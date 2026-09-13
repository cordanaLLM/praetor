package clientsetup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

func mergeYAML(ctx context.Context, registry Registry, existing []byte) ([]byte, error) {
	document, err := readYAML(ctx, existing)
	if err != nil {
		return nil, err
	}
	root := document.Content[0]
	changed, err := yamlMetadata(root)
	if err != nil {
		return nil, err
	}
	servers := yamlLookup(root, "mcpServers")
	if servers == nil {
		servers = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		yamlSet(root, "mcpServers", servers)
		changed = true
	}
	if servers.Kind != yaml.SequenceNode {
		return nil, errors.New("continue mcpServers must be a sequence")
	}
	additions, err := mergeYAMLServers(registry, servers)
	if err != nil {
		return nil, err
	}
	if !changed && !additions {
		return bytes.Clone(existing), nil
	}
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	err = encoder.Encode(document)
	if err = errors.Join(err, encoder.Close()); err != nil {
		return nil, errors.New("could not encode Continue configuration")
	}
	return output.Bytes(), nil
}

func readYAML(ctx context.Context, raw []byte) (*yaml.Node, error) {
	if len(raw) == 0 {
		return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}, nil
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, errors.New("invalid Continue YAML")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("continue YAML requires one document")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("continue YAML requires an object")
	}
	if err := validateYAMLNodes(ctx, &document, len(raw)); err != nil {
		return nil, err
	}
	return &document, nil
}

func validateYAMLNodes(ctx context.Context, root *yaml.Node, limit int) error {
	type pending struct {
		node  *yaml.Node
		depth int
	}
	queue := []pending{{root, 0}}
	for i := 0; i < len(queue) && i <= limit; i++ {
		if err := checkContext(ctx); err != nil {
			return err
		}
		entry := queue[i]
		if entry.depth > 32 {
			return errors.New("YAML nesting exceeds 32")
		}
		if entry.node.Kind == yaml.AliasNode || entry.node.Anchor != "" {
			return errors.New("YAML aliases and anchors are unsupported")
		}
		if err := validateYAMLMapping(entry.node); err != nil {
			return err
		}
		for _, child := range entry.node.Content {
			queue = append(queue, pending{child, entry.depth + 1})
		}
	}
	if len(queue) > limit+1 {
		return errors.New("YAML node bound exceeded")
	}
	return nil
}

func validateYAMLMapping(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	seen := map[string]bool{}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value == "<<" {
			return errors.New("YAML mapping keys must be literal strings")
		}
		if seen[key.Value] {
			return errors.New("duplicate YAML mapping key")
		}
		seen[key.Value] = true
	}
	return nil
}

func yamlLookup(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
func yamlSet(node *yaml.Node, key string, value *yaml.Node) {
	node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}
func yamlString(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func yamlMetadata(root *yaml.Node) (bool, error) {
	changed := false
	for _, field := range []struct{ key, value string }{{"name", "Praetor MCP"}, {"version", "1.0.0"}, {"schema", "v1"}} {
		node := yamlLookup(root, field.key)
		if node == nil {
			yamlSet(root, field.key, yamlString(field.value))
			changed = true
			continue
		}
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || node.Value == "" {
			return false, errors.New("continue metadata must be nonempty strings")
		}
		if field.key == "schema" && node.Value != "v1" {
			return false, errors.New("unsupported Continue schema")
		}
	}
	return changed, nil
}

func mergeYAMLServers(registry Registry, servers *yaml.Node) (bool, error) {
	existing, err := indexYAMLServers(servers)
	if err != nil {
		return false, err
	}

	changed := false
	for _, server := range registry.Servers {
		if node, ok := existing[server.Name]; ok {
			if !matchingYAMLServer(node, server) {
				return false, fmt.Errorf("%w: %s", ErrConflict, server.Name)
			}
			continue
		}
		node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		yamlSet(node, "name", yamlString(server.Name))
		yamlSet(node, "command", yamlString(server.Command))
		args := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, arg := range server.Args {
			args.Content = append(args.Content, yamlString(arg))
		}
		yamlSet(node, "args", args)
		servers.Content = append(servers.Content, node)
		changed = true
	}
	return changed, nil
}

func indexYAMLServers(servers *yaml.Node) (map[string]*yaml.Node, error) {
	existing := make(map[string]*yaml.Node)
	for _, node := range servers.Content {
		if node.Kind != yaml.MappingNode {
			return nil, errors.New("continue server must be an object")
		}
		name := yamlLookup(node, "name")
		if name == nil || name.Kind != yaml.ScalarNode || name.Tag != "!!str" || name.Value == "" {
			return nil, errors.New("continue server requires a name")
		}
		if _, ok := existing[name.Value]; ok {
			return nil, errors.New("duplicate Continue server name")
		}
		existing[name.Value] = node
	}
	return existing, nil
}

func matchingYAMLServer(node *yaml.Node, server Server) bool {
	command := yamlLookup(node, "command")
	if command == nil || command.Kind != yaml.ScalarNode || command.Tag != "!!str" || command.Value != server.Command {
		return false
	}
	if yamlLookup(node, "url") != nil {
		return false
	}
	return matchingYAMLArgs(yamlLookup(node, "args"), server.Args)
}

func matchingYAMLArgs(args *yaml.Node, wanted []string) bool {
	if args == nil {
		return len(wanted) == 0
	}
	if args.Kind != yaml.SequenceNode || len(args.Content) != len(wanted) {
		return false
	}
	for i, arg := range args.Content {
		if arg.Kind != yaml.ScalarNode || arg.Tag != "!!str" || arg.Value != wanted[i] {
			return false
		}
	}
	return true
}
