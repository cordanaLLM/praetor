package adopt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/cordanaLLM/praetor/internal/cavemansource"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

const maxAdoptManifestMappingNodes = 256

// manifestPlan is the manifest adoption leaves on disk and the report note saying why.
type manifestPlan struct {
	data []byte
	note string
}

// isPriorManifestRendering reports whether data is exactly the text an earlier adoption wrote
// for the manifest it decodes to: yaml.Marshal's rendering, before config.RenderManifest. An
// operator's edit, comment or reordering makes it differ, and the manifest is then left as
// written.
func isPriorManifestRendering(data []byte) bool {
	manifest, err := config.DecodeManifest(data)
	if err != nil {
		return false
	}
	prior, err := yaml.Marshal(manifest)
	return err == nil && bytes.Equal(prior, data)
}

// planExistingManifest adds or re-binds register.sources in an existing manifest and leaves
// every other declaration as written. A manifest that is exactly an earlier Praetor rendering
// is re-rendered in the current layout with the same declarations (migratePriorManifest).
func planExistingManifest(ctx context.Context, s *adoptSession, full string, data []byte) (manifestPlan, bool, error) {
	plan, changed, err := planManifestSources(ctx, s, full, data)
	if err != nil || !isPriorManifestRendering(data) {
		return plan, changed, err
	}
	return migratePriorManifest(ctx, plan, changed)
}

// migratePriorManifest renders the planned manifest in the current layout. It keeps the plan
// when the rendering is already current or would decode to anything else.
func migratePriorManifest(ctx context.Context, plan manifestPlan, changed bool) (manifestPlan, bool, error) {
	manifest, err := config.DecodeManifest(plan.data)
	if err != nil {
		return manifestPlan{}, false, fmt.Errorf("existing %s: %w", manifestFile, err)
	}
	rendered, err := config.RenderManifest(manifest)
	if err != nil {
		return manifestPlan{}, false, err
	}
	if bytes.Equal(rendered, plan.data) || !sameManifest(ctx, plan.data, rendered) {
		return plan, changed, nil
	}
	note := "Migrated the unmodified earlier Praetor manifest to the current layout (document start, " +
		"long values on their own line); declarations unchanged. " + plan.note
	return manifestPlan{data: rendered, note: note}, true, nil
}

// planManifestSources adds or re-binds register.sources in an existing manifest and leaves
// every other declaration as written.
func planManifestSources(ctx context.Context, s *adoptSession, full string, data []byte) (manifestPlan, bool, error) {
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
		return manifestPlan{}, false, fmt.Errorf("existing %s: %w", manifestFile, err)
	}
	return manifestPlan{data: replacement, note: manifestSourcesNote(declared, sources, changed, s.opts.Force)}, changed, nil
}

// unboundSourcesNote reports an existing manifest left without register.sources.
func unboundSourcesNote(harness harnessPlan) string {
	return "Existing standards manifest preserved without register.sources: " + unboundSourcesReason(harness)
}

// unboundSourcesReason says why a manifest has no register.sources: no harness exists and
// this run writes none, because the paperclip step is declined or the repository identity is
// unresolved, so adoption has no managed text to bind and the audit stays red until the
// operator resolves the identity and re-runs, or declares the repository's own sources.
func unboundSourcesReason(harness harnessPlan) string {
	reason, remedy := "paperclip is declined", ""
	if harness.unresolved {
		reason = "repository identity is unresolved"
		remedy = "set repository.owner and repository.name or add an origin remote and re-run, or "
	}
	return reason + " and .paperclip/harness.json does not exist, so adoption has no managed text to bind; " + remedy +
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
// only when replace is set; a null one (`sources:` or `sources: ~`) declares nothing, so it
// is always replaced. Every other line stays as the operator wrote it; a layout the text
// patch cannot address is re-encoded with every node, comment and key order kept.
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
	if err != nil || (found && !replace && !nullYAMLNode(current)) {
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

// manifestRegisterMapping returns the register mapping of root, adding one when register is
// absent. A null register (`register:` with no children, or only commented-out ones) loads
// as no register policy, so it is replaced by an empty mapping that keeps its comments.
func manifestRegisterMapping(root *yaml.Node) (*yaml.Node, error) {
	register, found, err := adoptYAMLMappingValue(root, "register")
	if err != nil {
		return nil, err
	}
	switch {
	case !found:
		register = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		appendAdoptYAMLMapping(root, "register", register)
	case nullYAMLNode(register):
		*register = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", HeadComment: register.HeadComment,
			LineComment: register.LineComment, FootComment: register.FootComment}
	}
	if register.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("manifest register must be a mapping or null, found %s at line %d",
			register.ShortTag(), register.Line)
	}
	return register, nil
}

// nullYAMLNode reports a YAML null scalar: an empty value, ~ or null.
func nullYAMLNode(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && node.ShortTag() == "!!null"
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

// encodeAdoptManifest re-encodes a manifest tree, comments and key order kept, as one
// lint-clean document (util.EncodeYAMLDocument).
func encodeAdoptManifest(document *yaml.Node) ([]byte, error) {
	output, err := util.EncodeYAMLDocument(document)
	if err != nil {
		return nil, fmt.Errorf("encode manifest with register sources: %w", err)
	}
	return output, nil
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
