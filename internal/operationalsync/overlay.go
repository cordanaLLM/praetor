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
		upstream, err := op.treeEntries(ctx, op.opts.SourcePath, sha)
		if err != nil {
			return err
		}
		for _, path := range paths {
			if _, exists := upstream[path]; exists {
				return fmt.Errorf("owner-only path exists in the public source at %s: %q", sha, path)
			}
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
