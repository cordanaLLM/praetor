package operationalsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var ownerPaths = []string{".standards.yaml", ".devcontainer/devcontainer.json", ".paperclip/harness.json", ".paperclip/rules.md"}

// ownerOnlyPrefixes names where an operational repository may carry files the public source never
// has. The list is engine schema, not operator data: every entry is ignored by the engine's own
// .gitignore (a test replays that), so upstream cannot grow a colliding path. An entry ending in
// "/" is a directory prefix matched on whole path segments; any other entry is one exact file.
var ownerOnlyPrefixes = []string{".config/fleet.yaml", ".config/fleet-topology.yaml", ".config/orgs/", ".config/operator/", "deploy/arc/", "deploy/k8s/"}

type identity struct{ Owner, Name, Visibility string }

func mappingValue(node *yaml.Node, key string) (*yaml.Node, error) {
	if node.Kind != yaml.MappingNode {
		return nil, errors.New("expected YAML mapping")
	}
	for i := 0; i < len(node.Content) && i < 10000; i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1], nil
		}
	}
	return nil, fmt.Errorf("required manifest field missing: %s", key)
}

// setMappingScalar sets an existing top-level string scalar of node to value, or appends a new
// key/value pair when node does not yet declare key. It is used only for repository.source: the
// overlay is that field's only writer, in both a first init (the canonical manifest never
// declares it) and a later sync (the owner's own manifest already carries the value a prior
// overlay wrote). Every other overlaid field already exists in the canonical manifest and goes
// through mappingValue, so it is never reordered.
func setMappingScalar(node *yaml.Node, key, value string) error {
	if node.Kind != yaml.MappingNode {
		return errors.New("expected YAML mapping")
	}
	for i := 0; i < len(node.Content) && i < 10000; i += 2 {
		if node.Content[i].Value == key {
			if node.Content[i+1].Anchor != "" {
				return fmt.Errorf("repository.%s anchor could change unrelated aliases", key)
			}
			node.Content[i+1].Kind = yaml.ScalarNode
			node.Content[i+1].Tag = "!!str"
			node.Content[i+1].Value = value
			return nil
		}
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	return nil
}

func decodeManifest(raw []byte) (*yaml.Node, error) {
	var doc yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("manifest requires one YAML document")
	}
	var decoded map[string]any
	if err := doc.Decode(&decoded); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 {
		return nil, errors.New("empty manifest")
	}
	version, err := mappingValue(doc.Content[0], "version")
	if err != nil || version.Kind != yaml.ScalarNode || version.Tag != "!!int" || version.Value != "1" {
		return nil, errors.New("operational sync requires manifest version 1")
	}
	return &doc, nil
}

func manifest(raw []byte) (*yaml.Node, identity, error) {
	doc, err := decodeManifest(raw)
	if err != nil {
		return nil, identity{}, err
	}
	repo, err := mappingValue(doc.Content[0], "repository")
	if err != nil {
		return nil, identity{}, err
	}
	values := make([]string, 3)
	for i, key := range []string{"owner", "name", "visibility"} {
		node, err := mappingValue(repo, key)
		if err != nil {
			return nil, identity{}, err
		}
		if node.Kind != yaml.ScalarNode || node.Tag != "!!str" || node.Value == "" {
			return nil, identity{}, fmt.Errorf("invalid repository.%s", key)
		}
		values[i] = node.Value
	}
	return doc, identity{values[0], values[1], values[2]}, nil
}

func ownerManifest(raw []byte, owner identity) ([]byte, error) {
	doc, source, err := manifest(raw)
	if err != nil {
		return nil, err
	}
	if source.Name != owner.Name {
		return nil, errors.New("repository name differs from source")
	}
	repo, err := mappingValue(doc.Content[0], "repository")
	if err != nil {
		return nil, err
	}
	if repo.Anchor != "" {
		return nil, errors.New("repository anchor could change unrelated manifest aliases")
	}
	for key, value := range map[string]string{"owner": owner.Owner, "visibility": owner.Visibility} {
		node, err := mappingValue(repo, key)
		if err != nil {
			return nil, err
		}
		if node.Anchor != "" {
			return nil, fmt.Errorf("repository.%s anchor could change unrelated aliases", key)
		}
		node.Value = value
	}
	// repository.source records the public identity the overlay is rewriting owner/name away
	// from, so internal/forge can resolve the fork's guarded workflows and required-context
	// ruleset against the identity their checked-in literals still name (#255).
	if err := setMappingScalar(repo, "source", source.Owner+"/"+source.Name); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(doc); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func ownerJSON(raw []byte, key, before, after string) ([]byte, error) {
	if err := validateJSONKeys(raw); err != nil {
		return nil, err
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	var old string
	if err := json.Unmarshal(value[key], &old); err != nil {
		return nil, err
	}
	if old != before {
		return nil, fmt.Errorf("derived %s is not source identity", key)
	}
	encoded, err := json.Marshal(after)
	if err != nil {
		return nil, err
	}
	value[key] = encoded
	// Replace only the validated scalar, retaining upstream formatting and unknown fields.
	oldScalar, err := json.Marshal(old)
	if err != nil {
		return nil, err
	}
	needle := []byte(fmt.Sprintf("%q: %s", key, oldScalar))
	if bytes.Count(raw, needle) != 1 {
		return nil, fmt.Errorf("derived %s requires canonical generator formatting", key)
	}
	result := bytes.Replace(raw, needle, []byte(fmt.Sprintf("%q: %s", key, encoded)), 1)
	var actual map[string]json.RawMessage
	if err := json.Unmarshal(result, &actual); err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(actual, value) {
		return nil, fmt.Errorf("derived %s replacement changed another field", key)
	}
	return result, nil
}

func overlay(files map[string][]byte, owner identity) (map[string][]byte, error) {
	_, source, err := manifest(files[ownerPaths[0]])
	if err != nil {
		return nil, err
	}
	before, after := source.Owner+"/"+source.Name, owner.Owner+"/"+owner.Name
	result := make(map[string][]byte, 4)
	result[ownerPaths[0]], err = ownerManifest(files[ownerPaths[0]], owner)
	if err != nil {
		return nil, err
	}
	for i, key := range []string{"name", "platform"} {
		result[ownerPaths[i+1]], err = ownerJSON(files[ownerPaths[i+1]], key, before, after)
		if err != nil {
			return nil, err
		}
	}
	heading := "# Paperclip Operating Rules (" + before + ")\n"
	if !strings.HasPrefix(string(files[ownerPaths[3]]), heading) {
		return nil, errors.New("derived Paperclip rules heading differs from source")
	}
	result[ownerPaths[3]] = []byte("# Paperclip Operating Rules (" + after + ")\n" + string(files[ownerPaths[3]][len(heading):]))
	return result, nil
}

func equivalent(path string, a, b []byte) bool {
	if path != ownerPaths[0] {
		return bytes.Equal(a, b)
	}
	var av, bv any
	return yaml.Unmarshal(a, &av) == nil && yaml.Unmarshal(b, &bv) == nil && reflect.DeepEqual(av, bv)
}

// addableManifestRepositoryFields lists repository.* keys a later overlay may start writing
// that an already-overlaid owner manifest predates. Today that is only repository.source
// (#255/#258): the first overlay #253 shipped never wrote it, so an owner manifest overlaid
// before #258 has no repository.source at all. equivalentManifestOverlay treats an owner
// manifest missing exactly these keys as equivalent to the current expected overlay, and
// prepare backfills them into the candidate, so a fork overlaid before the field existed
// converges instead of failing its own sync gate forever (#263).
var addableManifestRepositoryFields = []string{"source"}

// equivalentManifestOverlay reports whether current's .standards.yaml is the same overlay as
// expected's, treating current's absence of a key named in addableManifestRepositoryFields as
// a match -- never a present key with a different value, which stays a mismatch so a fork
// manifest recording the wrong source is still refused. Both arguments are trusted overlay
// output or a reviewed owner manifest; a decode failure is reported rather than swallowed,
// since checkOverlay must fail closed on unparsable input.
func equivalentManifestOverlay(expected, current []byte) (bool, error) {
	var exp, cur any
	if err := yaml.Unmarshal(expected, &exp); err != nil {
		return false, fmt.Errorf("decode expected overlay: %w", err)
	}
	if err := yaml.Unmarshal(current, &cur); err != nil {
		return false, fmt.Errorf("decode owner manifest: %w", err)
	}
	expMap, expOK := exp.(map[string]any)
	curMap, curOK := cur.(map[string]any)
	if !expOK || !curOK {
		return reflect.DeepEqual(exp, cur), nil
	}
	expRepo, _ := expMap["repository"].(map[string]any)
	curRepo, _ := curMap["repository"].(map[string]any)
	patched := make(map[string]any, len(curRepo)+len(addableManifestRepositoryFields))
	for key, value := range curRepo {
		patched[key] = value
	}
	for _, field := range addableManifestRepositoryFields {
		if _, present := curRepo[field]; !present {
			if value, ok := expRepo[field]; ok {
				patched[field] = value
			}
		}
	}
	curMap["repository"] = patched
	return reflect.DeepEqual(expMap, curMap), nil
}

const (
	maxOwnerOnlyPaths = 256
	maxOwnerOnlyBytes = 1 << 20
)

// treeEntry is one recursive ls-tree record; size is -1 for anything that is not a blob.
type treeEntry struct {
	mode, kind string
	size       int64
}

// ownerOnlyPath matches whole path segments: ".config/orgs/" accepts ".config/orgs/a.yaml" and
// refuses ".config/orgsx/a.yaml"; an entry without a trailing slash names exactly one file.
func ownerOnlyPath(path string) bool {
	for _, prefix := range ownerOnlyPrefixes {
		if strings.HasSuffix(prefix, "/") {
			if strings.HasPrefix(path, prefix) && path != prefix {
				return true
			}
			continue
		}
		if path == prefix {
			return true
		}
	}
	return false
}

// checkOwnerOnly accepts operator files only when the public source never had them, at the
// incorporated base and at the reviewed source, and when each is a bounded regular 0644 blob.
func (op *operation) checkOwnerOnly(ctx context.Context, dir, tree string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if len(paths) > maxOwnerOnlyPaths {
		return fmt.Errorf("owner-only paths exceed %d: %d", maxOwnerOnlyPaths, len(paths))
	}
	for _, sha := range []string{op.opts.BaseSHA, op.opts.SourceSHA} {
		if err := op.checkAbsentUpstream(ctx, sha, paths); err != nil {
			return err
		}
	}
	current, err := op.treeEntries(ctx, dir, tree)
	if err != nil {
		return err
	}
	for _, path := range paths {
		entry, exists := current[path]
		if err := validateOwnerOnlyEntry(path, entry, exists); err != nil {
			return err
		}
	}
	return nil
}

// checkAbsentUpstream compares case-folded paths: a name that differs from a public one only by
// letter case would land on the same file in a case-insensitive checkout (Windows, macOS default).
func (op *operation) checkAbsentUpstream(ctx context.Context, sha string, paths []string) error {
	upstream, err := op.treeEntries(ctx, op.opts.SourcePath, sha)
	if err != nil {
		return err
	}
	folded := make(map[string]bool, len(upstream))
	for path := range upstream {
		folded[strings.ToLower(path)] = true
	}
	for _, path := range paths {
		if folded[strings.ToLower(path)] {
			return fmt.Errorf("owner-only path exists in the public source at %s: %q", sha, path)
		}
	}
	return nil
}

func validateOwnerOnlyEntry(path string, entry treeEntry, exists bool) error {
	if !exists {
		return fmt.Errorf("owner-only path is absent from the owner tree: %q", path)
	}
	if entry.mode != "100644" || entry.kind != "blob" {
		return fmt.Errorf("owner-only path must be a regular non-executable file, got %s %s: %q", entry.mode, entry.kind, path)
	}
	if entry.size < 0 || entry.size > maxOwnerOnlyBytes {
		return fmt.Errorf("owner-only path exceeds 1 MiB: %q", path)
	}
	return nil
}

// treeEntries lists everything a tree holds under the owner-only prefixes in one bounded call.
func (op *operation) treeEntries(ctx context.Context, dir, tree string) (map[string]treeEntry, error) {
	args := []string{"ls-tree", "-r", "-z", "-l", tree, "--"}
	for _, prefix := range ownerOnlyPrefixes {
		args = append(args, strings.TrimSuffix(prefix, "/"))
	}
	out, err := op.git.run(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	return parseTreeEntries(out)
}

// parseTreeEntries reads `ls-tree -r -z -l` records: "<mode> <type> <object> <size>\t<path>".
func parseTreeEntries(out []byte) (map[string]treeEntry, error) {
	entries := make(map[string]treeEntry)
	for _, record := range strings.Split(string(out), "\x00") {
		if record == "" {
			continue
		}
		meta, path, found := strings.Cut(record, "\t")
		fields := strings.Fields(meta)
		if !found || path == "" || len(fields) != 4 {
			return nil, errors.New("unexpected ls-tree record")
		}
		entry := treeEntry{mode: fields[0], kind: fields[1], size: -1}
		if fields[3] != "-" {
			size, err := strconv.ParseInt(fields[3], 10, 64)
			if err != nil || size < 0 {
				return nil, errors.New("unexpected ls-tree object size")
			}
			entry.size = size
		}
		entries[path] = entry
	}
	return entries, nil
}
