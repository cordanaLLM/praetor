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

// isPriorManifestRendering reports whether data is the text an earlier adoption wrote for the
// manifest it decodes to, yaml.Marshal's rendering before config.RenderManifest, in one
// consistent line-ending style (util.CanonicalTextEquivalent, the rule priorRendering digests
// with). An operator's edit, comment or reordering makes it differ, and the manifest is then
// left as written.
func isPriorManifestRendering(data []byte) bool {
	manifest, err := config.DecodeManifest(data)
	if err != nil {
		return false
	}
	prior, err := yaml.Marshal(manifest)
	if err != nil {
		return false
	}
	equivalent, err := util.CanonicalTextEquivalent(data, prior)
	return err == nil && equivalent
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

// migratePriorManifest renders the planned manifest in the current layout, in the planned
// text's own line-ending style. It keeps the plan when the rendering is already current or
// would decode to anything else.
func migratePriorManifest(ctx context.Context, plan manifestPlan, changed bool) (manifestPlan, bool, error) {
	manifest, err := config.DecodeManifest(plan.data)
	if err != nil {
		return manifestPlan{}, false, fmt.Errorf("existing %s: %w", manifestFile, err)
	}
	_, crlf, err := util.NormalizeLineEndingsStrict(string(plan.data))
	if err != nil {
		return manifestPlan{}, false, fmt.Errorf("existing %s: %w", manifestFile, err)
	}
	lf, err := config.RenderManifest(manifest)
	if err != nil {
		return manifestPlan{}, false, err
	}
	rendered := []byte(util.RestoreLineEndings(string(lf), crlf))
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
	return manifestPlan{data: replacement, note: manifestSourcesNote(declared, sources, changed, harness, s.opts.Force)}, changed, nil
}

// unboundSourcesNote reports an existing manifest left without register.sources.
func unboundSourcesNote(harness harnessPlan) string {
	return "Existing standards manifest preserved without register.sources: " + unboundSourcesReason(harness)
}

// unboundSourcesReason says why a manifest has no register.sources: no harness exists and
// this run writes none, because the paperclip step is declined or the repository identity is
// unresolved, so adoption has no managed text to bind and the audit stays red until the
// operator resolves the identity and re-runs, or declares the repository's own sources, or
// declares that it has none (config.RegisterSources.DeclaresNone, #601).
func unboundSourcesReason(harness harnessPlan) string {
	reason, remedy := "paperclip is declined", ""
	if harness.unresolved {
		reason = "repository identity is unresolved"
		remedy = "set repository.owner and repository.name or add an origin remote and re-run, or "
	}
	return reason + " and .paperclip/harness.json does not exist, so adoption has no managed text to bind; " + remedy +
		"declare register.sources for this repository's agent-facing text before audit passes, " +
		"or, when it has none, declare register.sources with expected: 0 and a reason"
}

// declaredSourcesRemedy is what an operator does when a declared contract fails its own gate
// before the run. Adoption does not re-bind it, under --force either (#502 U9); the one
// exception is a harness-only contract over a harness this run writes where none existed
// (rebindsAbsentHarness), which never reaches here. The mismatch error names every extracted
// value that differs (cavemansource.verifyDeclaredResult), so the pins can be set from it
// directly; the configured-sources check reports the same values, but only for staged inputs
// (cavemansource.ExtractDeclared). A contract that also selects another file keeps its gate over
// an absent harness too: the values it reports then cover the harness this run writes.
func declaredSourcesRemedy(harness harnessPlan) string {
	pins := "set expected, not_applicable and sha256 under register.sources in " + manifestFile +
		" to the extracted values this error reports"
	if harness.writesOverAbsent() {
		return "the contract also selects files other than " + paperclipFile + ", whose drift adoption cannot tell " +
			"from the harness it writes, so it re-binds none: " + pins + ", which cover the harness this run writes, " +
			"and re-run praetorctl adopt"
	}
	remedy := "adoption never re-binds a contract to drift it did not cause, --force included: " + pins +
		", or recompute the pins with `praetorctl caveman check --configured-sources --root=.` once every input is " +
		"staged (git add)"
	if harness.neverWrites {
		return remedy + "; or restore the " + paperclipFile + " bytes the pins were bound to (git checkout): this " +
			"run writes no harness, because " + neverWritesReason(harness) + ", so deleting it regenerates nothing"
	}
	return remedy + "; or, when the drift is an edited " + paperclipFile + ", delete it and re-run praetorctl adopt " +
		"to regenerate it, which binds a contract selecting only that harness to the bytes it writes"
}

// neverWritesReason says why a kept harness plan writes nothing (keptHarnessPlan).
func neverWritesReason(harness harnessPlan) string {
	if harness.unresolved {
		return "the repository identity is unresolved"
	}
	return "adoption.decline lists the paperclip step"
}

// reconcileRegisterSources returns the register.sources adoption leaves in the manifest and
// whether they replace the declared contract. A missing contract gets the managed harness
// rows, or nil when no harness will exist. A declared contract must pass its own gate first,
// so adoption never re-blesses drift it did not cause (declaredSourcesRemedy); it then keeps
// every declared input and only recomputes the counts and digest when this run writes the
// harness those inputs select: a refresh of unmodified earlier output, with or without
// --force, or the --force platform patch of an operator-owned one. A harness this run keeps
// binds as it stands on disk, under --force too. A contract whose every input selects a
// harness this run writes where none existed skips that gate (rebindsAbsentHarness).
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
	if declared.DeclaresNone() {
		return declared, false, declaredNoneHolds(declared, harness)
	}
	if !rebindsAbsentHarness(declared, harness) {
		if err := verifyDeclaredSources(ctx, root, declared, harness); err != nil {
			return nil, false, fmt.Errorf("existing register.sources fails its configured gate: %w; %s", err,
				declaredSourcesRemedy(harness))
		}
	}
	if !harness.writes() {
		return declared, false, nil
	}
	result, err := cavemansource.ExtractInputsWithDocuments(ctx, root, declared.Inputs,
		map[string][]byte{paperclipFile: harness.data})
	if err != nil {
		return nil, false, fmt.Errorf("re-bind register.sources to the rewritten harness: %w", err)
	}
	rebound := &config.RegisterSources{Expected: result.Applicable, NotApplicable: result.NotApplicable,
		SHA256: result.SHA256, Inputs: declared.Inputs}
	return rebound, !equalRegisterSources(declared, rebound), nil
}

// declaredNoneHolds keeps an explicit empty contract (config.RegisterSources.DeclaresNone) while
// it is true: no harness exists and this run writes none, which --force does not change (#601).
// Over a harness on disk, or one this run writes, the declaration would pass the audit with the
// harness text unlinted, so adoption refuses before its first write.
func declaredNoneHolds(declared *config.RegisterSources, harness harnessPlan) error {
	if harness.absent() {
		return nil
	}
	where := paperclipFile + " exists"
	if !harness.onDisk {
		where = "this run writes " + paperclipFile
	}
	return fmt.Errorf("register.sources declares no agent-facing text (reason: %s), but %s; remove the empty "+
		"declaration so adoption binds the harness text, or decline paperclip and remove the harness", declared.Reason, where)
}

// rebindsAbsentHarness reports a declared contract adoption re-binds without holding it to its
// pins first: this run writes the harness where no file exists, and every declared input selects
// that harness. The bytes are Praetor output, not an operator's edit, so binding the pins to them
// re-blesses no drift. The pins may name bytes that are gone: an earlier release's synthesis, or
// an edited harness the operator deleted to regenerate it; holding the fresh synthesis to them
// would fail every run, and deleting the harness again would not change that. An edited harness
// on disk is never re-blessed (verifyDeclaredSources), and neither is a contract that also
// selects another file, whose text is the operator's.
func rebindsAbsentHarness(declared *config.RegisterSources, harness harnessPlan) bool {
	if !harness.writesOverAbsent() {
		return false
	}
	for _, input := range declared.Inputs {
		if input.Path != paperclipFile {
			return false
		}
	}
	return true
}

// verifyDeclaredSources checks a declared contract against the harness as it stands: the
// file on disk, or the planned bytes when none exists yet and this run writes one. A
// contract that instead matches the harness this run is about to write also passes: an
// earlier step of the same run bound it there, and the paperclip step writes those bytes
// later in the chain. An absent harness overlays nothing, so a contract selecting it fails.
func verifyDeclaredSources(ctx context.Context, root string, declared *config.RegisterSources, harness harnessPlan) error {
	planned := map[string][]byte{paperclipFile: harness.data}
	var current map[string][]byte
	if harness.writesOverAbsent() {
		current = planned
	}
	_, err := cavemansource.ExtractDeclaredContent(ctx, root, declared, current)
	if err == nil || !harness.writes() || !harness.onDisk {
		return err
	}
	if _, plannedErr := cavemansource.ExtractDeclaredContent(ctx, root, declared, planned); plannedErr == nil {
		return nil
	}
	return err
}

// manifestSourcesNote reports what the manifest step did to register.sources. A re-bind to a
// harness this run writes where none existed says so, since it replaces pins without holding
// the contract to them first (rebindsAbsentHarness).
func manifestSourcesNote(declared, sources *config.RegisterSources, changed bool, harness harnessPlan, forced bool) string {
	switch {
	case !changed:
		return forcedManifestNote(forced)
	case declared == nil:
		return "Added omission-resistant register.sources coverage; preserved existing declarations"
	case harness.writesOverAbsent():
		return fmt.Sprintf("Re-bound register.sources to the Paperclip harness this run writes where none existed "+
			"(expected %d, not_applicable %d, %s): Praetor output, so the earlier pins no longer bind; "+
			"kept every declared input and declaration", sources.Expected, sources.NotApplicable, sources.SHA256)
	}
	return fmt.Sprintf("Re-bound register.sources to the rewritten Paperclip harness (expected %d, not_applicable %d, %s); "+
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
