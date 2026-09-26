package adopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/cordanaLLM/praetor/internal/cavemansource"
	"github.com/cordanaLLM/praetor/internal/config"
	"gopkg.in/yaml.v3"
)

const maxAdoptManifestMappingNodes = 256

func verifyExistingRegisterSources(ctx context.Context, repoPath string, manifest *config.Manifest,
	desired *config.RegisterSources, force bool,
) error {
	if manifest.Register == nil || manifest.Register.Sources == nil {
		return nil
	}
	existing := manifest.Register.Sources
	if _, err := cavemansource.ExtractDeclaredContent(ctx, repoPath, existing); err != nil {
		return fmt.Errorf("existing register.sources fails its configured gate: %w", err)
	}
	if force && !equalRegisterSources(existing, desired) {
		return errors.New("existing register.sources is valid but --force would change a selected source; preserve the harness or update the reviewed contract first")
	}
	return nil
}

func equalRegisterSources(left, right *config.RegisterSources) bool {
	if left == nil || right == nil || left.Expected != right.Expected ||
		left.NotApplicable != right.NotApplicable || left.SHA256 != right.SHA256 || len(left.Inputs) != len(right.Inputs) {
		return false
	}
	for index := range left.Inputs {
		if left.Inputs[index] != right.Inputs[index] {
			return false
		}
	}
	return true
}

func addManifestSources(ctx context.Context, data []byte, sources *config.RegisterSources) ([]byte, bool, error) {
	document, err := decodeAdoptManifestNode(ctx, data)
	if err != nil {
		return nil, false, err
	}
	root := document.Content[0]
	register, found, err := adoptYAMLMappingValue(root, "register")
	if err != nil {
		return nil, false, err
	}
	if !found {
		register = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		appendAdoptYAMLMapping(root, "register", register)
	}
	if register.Kind != yaml.MappingNode {
		return nil, false, errors.New("manifest register must be a mapping")
	}
	if _, found, err = adoptYAMLMappingValue(register, "sources"); err != nil || found {
		return data, false, err
	}
	var sourceNode yaml.Node
	if err := sourceNode.Encode(sources); err != nil {
		return nil, false, fmt.Errorf("encode register sources: %w", err)
	}
	encodedSource := &sourceNode
	if sourceNode.Kind == yaml.DocumentNode && len(sourceNode.Content) == 1 {
		encodedSource = sourceNode.Content[0]
	}
	appendAdoptYAMLMapping(register, "sources", encodedSource)
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := errors.Join(encoder.Encode(document), encoder.Close()); err != nil {
		return nil, false, fmt.Errorf("encode manifest with register sources: %w", err)
	}
	return output.Bytes(), true, nil
}

func decodeAdoptManifestNode(ctx context.Context, data []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode manifest for source coverage: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("manifest source coverage requires exactly one YAML document")
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("manifest source coverage requires a root mapping")
	}
	if err := config.ValidateYAMLNodes(ctx, &document); err != nil {
		return nil, fmt.Errorf("validate manifest for source coverage: %w", err)
	}
	return &document, nil
}

func adoptYAMLMappingValue(mapping *yaml.Node, key string) (*yaml.Node, bool, error) {
	if mapping.Kind != yaml.MappingNode || len(mapping.Content) > maxAdoptManifestMappingNodes {
		return nil, false, errors.New("manifest mapping exceeds source reconciliation bound")
	}
	for index := 0; index+1 < len(mapping.Content) && index < maxAdoptManifestMappingNodes; index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1], true, nil
		}
	}
	return nil, false, nil
}

func appendAdoptYAMLMapping(mapping *yaml.Node, key string, value *yaml.Node) {
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}
