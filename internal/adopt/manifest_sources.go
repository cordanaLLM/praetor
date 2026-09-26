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

// manifestPlan is the manifest adoption leaves on disk and the report note saying why.
type manifestPlan struct {
	data []byte
	note string
}

// planExistingManifest adds or re-binds register.sources in an existing manifest and leaves
// every other declaration as written.
func planExistingManifest(ctx context.Context, s *adoptSession, full string, data []byte) (manifestPlan, bool, error) {
	// An existing manifest is an input: one the config loader rejects fails adoption here
	// and is never reported as verified present on existence alone (BUG-853).
	manifest, err := config.LoadManifest(full)
	if err != nil {
		return manifestPlan{}, false, fmt.Errorf("existing %s: %w", manifestFile, err)
	}
	harness, err := planHarness(ctx, s)
	if err != nil {
		return manifestPlan{}, false, err
	}
	var declared *config.RegisterSources
	if manifest.Register != nil {
		declared = manifest.Register.Sources
	}
	sources, replace, err := reconcileRegisterSources(ctx, s.repoPath, declared, harness)
	if err != nil {
		return manifestPlan{}, false, err
	}
	if sources == nil {
		return manifestPlan{data: data, note: unboundSourcesNote(harness)}, false, nil
	}
	replacement, changed, err := setManifestSources(ctx, data, sources, replace)
	if err != nil {
		return manifestPlan{}, false, err
	}
	return manifestPlan{data: replacement, note: manifestSourcesNote(declared, sources, changed, s.opts.Force)}, changed, nil
}

// unboundSourcesNote reports a manifest left without register.sources: no harness exists and
// this run writes none, because the paperclip step is declined or the repository identity is
// unresolved, so adoption has no managed text to bind and the audit stays red until the
// operator resolves the identity and re-runs, or declares the repository's own sources.
func unboundSourcesNote(harness harnessPlan) string {
	reason, remedy := "paperclip is declined", ""
	if harness.unresolved {
		reason = "repository identity is unresolved"
		remedy = "set repository.owner and repository.name or add an origin remote and re-run, or "
	}
	return "Existing standards manifest preserved without register.sources: " + reason +
		" and .paperclip/harness.json does not exist, so adoption has no managed text to bind; " + remedy +
		"declare register.sources for this repository's agent-facing text before audit passes"
}

// reconcileRegisterSources returns the register.sources adoption leaves in the manifest and
// whether they replace the declared contract. A missing contract gets the managed harness
// rows, or nil when no harness will exist. A declared contract must pass its own gate first, so adoption never re-blesses drift
// it did not cause; it then keeps every declared input and only recomputes the counts and
// digest when this run rewrites the harness those inputs select (--force, or a refresh of
// unmodified earlier output).
func reconcileRegisterSources(ctx context.Context, root string, declared *config.RegisterSources,
	harness harnessPlan,
) (*config.RegisterSources, bool, error) {
	if declared == nil {
		if harness.absent() {
			return nil, false, nil
		}
		sources, err := managedRegisterSources(ctx, harness.data)
		return sources, false, err
	}
	if err := verifyDeclaredSources(ctx, root, declared, harness); err != nil {
		return nil, false, fmt.Errorf("existing register.sources fails its configured gate: %w", err)
	}
	if harness.write == nil {
		return declared, false, nil
	}
	result, err := cavemansource.ExtractInputsWithDocuments(ctx, root, declared.Inputs,
		map[string][]byte{paperclipFile: harness.data})
	if err != nil {
		return nil, false, fmt.Errorf("re-bind register.sources to the regenerated harness: %w", err)
	}
	rebound := &config.RegisterSources{Expected: result.Applicable, NotApplicable: result.NotApplicable,
		SHA256: result.SHA256, Inputs: declared.Inputs}
	return rebound, !equalRegisterSources(declared, rebound), nil
}

// verifyDeclaredSources checks a declared contract against the harness as it stands: the
// file on disk, or the planned bytes when none exists yet and this run writes one. A
// contract that instead matches the harness this run is about to write also passes: an
// earlier step of the same run bound it there, and the paperclip step writes those bytes
// later in the chain. An absent harness overlays nothing, so a contract selecting it fails.
func verifyDeclaredSources(ctx context.Context, root string, declared *config.RegisterSources, harness harnessPlan) error {
	planned := map[string][]byte{paperclipFile: harness.data}
	var current map[string][]byte
	if !harness.onDisk && harness.write != nil {
		current = planned
	}
	_, err := cavemansource.ExtractDeclaredContent(ctx, root, declared, current)
	if err == nil || harness.write == nil || !harness.onDisk {
		return err
	}
	if _, plannedErr := cavemansource.ExtractDeclaredContent(ctx, root, declared, planned); plannedErr == nil {
		return nil
	}
	return err
}

func manifestSourcesNote(declared, sources *config.RegisterSources, changed, forced bool) string {
	switch {
	case !changed:
		return forcedManifestNote(forced)
	case declared == nil:
		return "Added omission-resistant register.sources coverage; preserved existing declarations"
	}
	return fmt.Sprintf("Re-bound register.sources to the regenerated Paperclip harness (expected %d, not_applicable %d, %s); "+
		"kept every declared input and declaration", sources.Expected, sources.NotApplicable, sources.SHA256)
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

// setManifestSources writes sources under register.sources. An existing value is replaced
// only when replace is set. Every other line stays as the operator wrote it; a layout the
// text patch cannot address is re-encoded with every node, comment and key order kept.
func setManifestSources(ctx context.Context, data []byte, sources *config.RegisterSources, replace bool) ([]byte, bool, error) {
	document, err := decodeAdoptManifestNode(ctx, data)
	if err != nil {
		return nil, false, err
	}
	// The text patch reads the line positions of the tree as decoded, so it runs first.
	patched, patchedOK, err := patchManifestSources(data, document.Content[0], sources)
	if err != nil {
		return nil, false, err
	}
	register, err := manifestRegisterMapping(document.Content[0])
	if err != nil {
		return nil, false, err
	}
	current, found, err := adoptYAMLMappingValue(register, "sources")
	if err != nil || (found && !replace) {
		return data, false, err
	}
	encoded, err := encodeRegisterSourcesNode(sources)
	if err != nil {
		return nil, false, err
	}
	if found {
		*current = *encoded
	} else {
		appendAdoptYAMLMapping(register, "sources", encoded)
	}
	output, err := manifestOutput(ctx, document, patched, patchedOK)
	return output, err == nil, err
}

// manifestOutput re-encodes document, and returns the text patch instead when it decodes to
// the same manifest.
func manifestOutput(ctx context.Context, document *yaml.Node, patched []byte, patchedOK bool) ([]byte, error) {
	output, err := encodeAdoptManifest(document)
	if err != nil {
		return nil, err
	}
	if patchedOK && sameManifest(ctx, patched, output) {
		return patched, nil
	}
	return output, nil
}

func manifestRegisterMapping(root *yaml.Node) (*yaml.Node, error) {
	register, found, err := adoptYAMLMappingValue(root, "register")
	if err != nil {
		return nil, err
	}
	if !found {
		register = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		appendAdoptYAMLMapping(root, "register", register)
	}
	if register.Kind != yaml.MappingNode {
		return nil, errors.New("manifest register must be a mapping")
	}
	return register, nil
}

func encodeRegisterSourcesNode(sources *config.RegisterSources) (*yaml.Node, error) {
	var sourceNode yaml.Node
	if err := sourceNode.Encode(sources); err != nil {
		return nil, fmt.Errorf("encode register sources: %w", err)
	}
	if sourceNode.Kind == yaml.DocumentNode && len(sourceNode.Content) == 1 {
		return sourceNode.Content[0], nil
	}
	return &sourceNode, nil
}

func encodeAdoptManifest(document *yaml.Node) ([]byte, error) {
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := errors.Join(encoder.Encode(document), encoder.Close()); err != nil {
		return nil, fmt.Errorf("encode manifest with register sources: %w", err)
	}
	return output.Bytes(), nil
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
	if index := mappingKeyIndex(mapping, key); index >= 0 {
		return mapping.Content[index+1], true, nil
	}
	return nil, false, nil
}

func appendAdoptYAMLMapping(mapping *yaml.Node, key string, value *yaml.Node) {
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}
