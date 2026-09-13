package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxDevContainerFeatures = 512

// ResolveDevContainerFeatures decodes features from the exact pinned catalog
// artifacts selected by effective policy resolution. Duplicate references must
// carry identical options; differing options are ambiguous and fail closed.
func ResolveDevContainerFeatures(ctx context.Context, policy *EffectivePolicy) ([]DevContainerFeature, error) {
	if ctx == nil || policy == nil {
		return nil, errors.New("devcontainer feature resolution requires context and policy")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(policy.CatalogArtifacts) > maxDevContainerFeatures {
		return nil, errors.New("selected catalog contains too many DevContainer feature sources")
	}
	features := make(map[string]DevContainerFeature)
	total := 0
	for i := 0; i < len(policy.CatalogArtifacts); i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		artifactFeatures, count, err := decodeArtifactFeatures(ctx, policy.CatalogArtifacts[i])
		if err != nil {
			return nil, err
		}
		total += count
		if total > maxDevContainerFeatures {
			return nil, errors.New("selected DevContainer feature union exceeds bounds")
		}
		if err := mergeDevContainerFeatures(features, artifactFeatures); err != nil {
			return nil, err
		}
	}
	result := make([]DevContainerFeature, 0, len(features))
	for _, feature := range features {
		result = append(result, feature)
	}
	slices.SortFunc(result, func(a, b DevContainerFeature) int { return strings.Compare(a.Ref, b.Ref) })
	return result, nil
}

func mergeDevContainerFeatures(features map[string]DevContainerFeature, additions []DevContainerFeature) error {
	for _, feature := range additions {
		if err := mergeDevContainerFeature(features, feature); err != nil {
			return err
		}
	}
	return nil
}

func decodeArtifactFeatures(ctx context.Context, artifact PolicyArtifact) ([]DevContainerFeature, int, error) {
	document, err := decodePolicyDocument(ctx, artifact.Content)
	if err != nil {
		return nil, 0, fmt.Errorf("decode selected catalog feature source %s: %w", artifact.RelativePath, err)
	}
	member := policyMember(document, "devcontainer_features")
	if member == nil {
		return nil, 0, nil
	}
	if member.Kind != yaml.SequenceNode {
		return nil, 0, fmt.Errorf("%s devcontainer_features must be a sequence", artifact.RelativePath)
	}
	if len(member.Content) > maxDevContainerFeatures {
		return nil, 0, fmt.Errorf("%s devcontainer_features exceeds bounds", artifact.RelativePath)
	}
	features := make([]DevContainerFeature, 0, len(member.Content))
	for i, node := range member.Content {
		feature, err := decodeDevContainerFeature(node)
		if err != nil {
			return nil, 0, fmt.Errorf("%s devcontainer_features[%d]: %w", artifact.RelativePath, i, err)
		}
		features = append(features, feature)
	}
	return features, len(features), nil
}

func mergeDevContainerFeature(features map[string]DevContainerFeature, feature DevContainerFeature) error {
	identity := feature.Identity()
	previous, exists := features[identity]
	if exists && (previous.Ref != feature.Ref || !sameFeatureOptions(previous.Options, feature.Options)) {
		return fmt.Errorf("devcontainer feature identity %q has conflicting references or options in selected catalog", identity)
	}
	features[identity] = feature
	return nil
}

func decodeDevContainerFeature(node *yaml.Node) (DevContainerFeature, error) {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		ref := strings.TrimSpace(node.Value)
		if ref == "" {
			return DevContainerFeature{}, errors.New("feature reference cannot be empty")
		}
		return DevContainerFeature{Ref: ref, Options: map[string]interface{}{}}, nil
	}
	if node.Kind != yaml.MappingNode || len(node.Content) != 2 || node.Content[0].Value == "" {
		return DevContainerFeature{}, errors.New("feature must be a string or one-entry mapping")
	}
	ref := strings.TrimSpace(node.Content[0].Value)
	if ref == "" || node.Content[1].Kind != yaml.MappingNode {
		return DevContainerFeature{}, errors.New("feature mapping requires a reference and options mapping")
	}
	options := make(map[string]interface{})
	if err := node.Content[1].Decode(&options); err != nil {
		return DevContainerFeature{}, fmt.Errorf("decode feature options: %w", err)
	}
	if err := validateFeatureOptions(options); err != nil {
		return DevContainerFeature{}, err
	}
	return DevContainerFeature{Ref: ref, Options: options}, nil
}

func sameFeatureOptions(a, b map[string]interface{}) bool {
	return reflect.DeepEqual(a, b)
}

// Identity returns the feature repository without its tag or digest selector.
func (feature DevContainerFeature) Identity() string {
	ref := feature.Ref
	if at := strings.LastIndexByte(ref, '@'); at > strings.LastIndexByte(ref, '/') {
		ref = ref[:at]
	}
	lastSlash := strings.LastIndexByte(ref, '/')
	lastColon := strings.LastIndexByte(ref, ':')
	if lastColon > lastSlash {
		return ref[:lastColon]
	}
	return ref
}

func validateFeatureOptions(options map[string]interface{}) error {
	data, err := json.Marshal(options)
	if err != nil {
		return fmt.Errorf("feature options must be JSON-compatible: %w", err)
	}
	if len(data) > 1<<20 {
		return errors.New("feature options exceed 1 MiB")
	}
	return nil
}
