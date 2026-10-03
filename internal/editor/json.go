package editor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/strictjson"
)

const (
	// maxEditorJSONDepth bounds object and array nesting in one editor JSON document.
	maxEditorJSONDepth = 128
	// maxJSONNodes bounds the values (root, object members and array items) in one
	// editor JSON document and therefore every merge and containment traversal.
	maxJSONNodes = 4096
)

var errEditorJSONNodeBound = fmt.Errorf("editor JSON exceeds %d nodes", maxJSONNodes)

// ErrExistingJSONInvalid marks a merge refused because the existing file is not one valid
// document in the dialect its editor reads it in (strictjson.DialectOf): a syntax error, a
// duplicate key, invalid UTF-8, an unpaired surrogate escape or a document past the node bound.
// Comments and trailing commas are a syntax error everywhere but in the .vscode files VS Code
// reads as JSON with Comments. Re-encoding such a file would drop what the decoder cannot
// represent, so a caller that keeps it (adoption) can say why.
var ErrExistingJSONInvalid = errors.New("existing JSON is invalid")

// CommentedJSONError refuses a merge into a file that is valid JSON with Comments, lacks
// managed values and carries comments or trailing commas. A merge decodes the document and
// writes it again, and the decoder keeps neither, so merging would silently delete the
// adopter's comments (#316). The file is left as it is; the operator adds the values by hand,
// or removes the comments and trailing commas so that the merge can write the file. A
// commented file that already holds every managed value is not refused: nothing is written.
type CommentedJSONError struct {
	// Lacks names each managed member the file lacks, as Resolution.Added names a merged one.
	Lacks []string
}

func (e *CommentedJSONError) Error() string {
	quoted := make([]string, 0, len(e.Lacks))
	for _, pointer := range e.Lacks {
		quoted = append(quoted, strconv.Quote(pointer))
	}
	return "it carries comments or trailing commas, which a merge would lose because it rewrites the file, and lacks the managed values " +
		strings.Join(quoted, ", ") + "; add them by hand, or remove the comments and trailing commas and run again"
}

// jsonPointerEscaper escapes one object key as an RFC 6901 reference token.
var jsonPointerEscaper = strings.NewReplacer("~", "~0", "/", "~1")

// editorJSON words the shared strictjson reader for editor configuration. Member names are
// compared exactly, not case-folded: an editor document decodes into Go maps, which keep every
// spelling, and settings such as files.watcherExclude and files.associations take
// case-sensitive glob patterns as names. buildWatcherExclude (editor.go) writes one such name
// per configured private directory, so "Notes" and "notes" are two settings, not an alias.
var editorJSON = strictjson.Options{
	MaxBytes:  contextopt.MaxSourceBytes,
	MaxDepth:  maxEditorJSONDepth,
	Names:     strictjson.ExactNames,
	UseNumber: true,
	Messages: strictjson.Messages{
		Size:      "editor JSON requires 1..%d UTF-8 bytes",
		Syntax:    "decode editor JSON token: %w",
		Surrogate: "editor JSON holds an unpaired UTF-16 surrogate escape",
		Depth:     "editor JSON nesting exceeds %d",
		Count:     "editor JSON token count exceeds byte bound",
		Duplicate: "editor JSON contains an invalid or duplicate key %q",
		Trailing:  "editor JSON requires exactly one document",
		Decode:    "decode editor JSON: %w",
	},
}

// decodeEditorJSON decodes exactly one bounded UTF-8 JSON document written in dialect. Numbers
// stay json.Number literals so re-encoding never rounds them, and duplicate object keys
// are rejected because a map decode would silently keep only the last value.
func decodeEditorJSON(raw []byte, dialect strictjson.Dialect) (any, error) {
	opts := editorJSON
	opts.Dialect = dialect
	var value any
	if err := strictjson.Decode(raw, &value, opts); err != nil {
		return nil, err
	}
	if err := validateJSONNodeBound(value); err != nil {
		return nil, err
	}
	return value, nil
}

// validateJSONNodeBound counts the root and every nested value breadth first and
// rejects a document with more than maxJSONNodes values.
func validateJSONNodeBound(root any) error {
	queue := []any{root}
	for index := 0; index < len(queue) && index < maxJSONNodes; index++ {
		switch value := queue[index].(type) {
		case map[string]any:
			if len(value) > maxJSONNodes-len(queue) {
				return errEditorJSONNodeBound
			}
			for _, child := range value {
				queue = append(queue, child)
			}
		case []any:
			if len(value) > maxJSONNodes-len(queue) {
				return errEditorJSONNodeBound
			}
			queue = append(queue, value...)
		}
	}
	return nil
}

// encodeEditorJSON renders a merged document with two-space indentation and a final
// newline. HTML escaping is disabled so user strings keep their literal characters.
func encodeEditorJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("encode merged editor JSON: %w", err)
	}
	return buffer.Bytes(), nil
}

func isJSONEditorFile(path string) bool {
	return strings.HasSuffix(path, ".json") || strings.HasSuffix(path, ".sublime-project")
}

// mergeJSONDocument adds the managed values of desired to existing, which its editor reads in
// dialect, and returns the merged document with the JSON Pointer of every member it added (see
// Resolution.Added). Unrelated keys and list entries are retained; nothing added returns
// existing unchanged, comments included. A managed value that conflicts with an existing one is
// an error, and so is a document that needs values added while it carries comments or trailing
// commas (CommentedJSONError); nothing is returned for writing in either case. desired is
// generated and always strict JSON.
func mergeJSONDocument(existing, desired []byte, dialect strictjson.Dialect) ([]byte, []string, error) {
	have, err := decodeEditorJSON(existing, dialect)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrExistingJSONInvalid, err)
	}
	want, err := decodeEditorJSON(desired, strictjson.StrictJSON)
	if err != nil {
		return nil, nil, fmt.Errorf("generated JSON is invalid: %w", err)
	}
	merged, added, err := mergeJSONValue(have, want)
	if err != nil {
		return nil, nil, err
	}
	if len(added) == 0 {
		return existing, nil, nil
	}
	if carriesJSONCSyntax(existing, dialect) {
		return nil, nil, &CommentedJSONError{Lacks: added}
	}
	data, err := encodeEditorJSON(merged)
	if err != nil {
		return nil, nil, err
	}
	return data, added, nil
}

// carriesJSONCSyntax reports whether raw, which decodeEditorJSON accepted in dialect, uses what
// JSONC adds to JSON. The two dialects differ in comments and trailing commas only, so a
// document JSONC accepts and strict JSON refuses carries one of them.
func carriesJSONCSyntax(raw []byte, dialect strictjson.Dialect) bool {
	return dialect == strictjson.JSONC && strictjson.Validate(raw, editorJSON) != nil
}

type jsonMergeFrame struct {
	have    any
	want    any
	parent  map[string]any
	key     string
	pointer string // RFC 6901 JSON Pointer of have; "" is the whole document
}

func mergeJSONValue(have, want any) (any, []string, error) {
	root := have
	added := []string{}
	queue := []jsonMergeFrame{{have: have, want: want}}
	index := 0
	for ; index < len(queue) && index < maxJSONNodes; index++ {
		if err := mergeJSONFrameValue(&root, queue[index], &queue, &added); err != nil {
			return nil, nil, err
		}
	}
	if index < len(queue) {
		return nil, nil, errEditorJSONNodeBound
	}
	return root, added, nil
}

func mergeJSONFrameValue(root *any, frame jsonMergeFrame, queue *[]jsonMergeFrame, added *[]string) error {
	switch desired := frame.want.(type) {
	case map[string]any:
		return mergeJSONObject(frame, desired, queue, added)
	case []any:
		return mergeJSONArray(root, frame, desired, added)
	default:
		if !reflect.DeepEqual(frame.have, frame.want) {
			return fmt.Errorf("managed value for key %q conflicts with the existing value", frame.key)
		}
		return nil
	}
}

func mergeJSONObject(frame jsonMergeFrame, desired map[string]any, queue *[]jsonMergeFrame, added *[]string) error {
	existing, ok := frame.have.(map[string]any)
	if !ok {
		return fmt.Errorf("managed object for key %q conflicts with the existing value", frame.key)
	}
	for _, key := range slices.Sorted(maps.Keys(desired)) {
		desiredValue := desired[key]
		pointer := frame.pointer + "/" + jsonPointerEscaper.Replace(key)
		existingValue, found := existing[key]
		if !found {
			existing[key] = desiredValue
			*added = append(*added, pointer)
			continue
		}
		*queue = append(*queue, jsonMergeFrame{have: existingValue, want: desiredValue, parent: existing, key: key, pointer: pointer})
	}
	return nil
}

func mergeJSONArray(root *any, frame jsonMergeFrame, desired []any, added *[]string) error {
	existing, ok := frame.have.([]any)
	if !ok {
		return fmt.Errorf("managed array for key %q conflicts with the existing value", frame.key)
	}
	appended := false
	for _, desiredItem := range desired {
		if !jsonArrayContains(existing, desiredItem) {
			existing = append(existing, desiredItem)
			appended = true
		}
	}
	if appended {
		*added = append(*added, frame.pointer+"/-")
	}
	if frame.parent == nil {
		*root = existing
	} else {
		frame.parent[frame.key] = existing
	}
	return nil
}

// jsonDocumentContains reports whether existing, which its editor reads in dialect, contains
// every managed value in desired while allowing unrelated keys and list items. Both documents
// are decoded through the one bounded reader, so ambiguous duplicate keys are an error rather
// than a pass; the comments and trailing commas of a JSONC file change nothing about what it
// holds.
func jsonDocumentContains(existing, desired []byte, dialect strictjson.Dialect) (bool, error) {
	have, err := decodeEditorJSON(existing, dialect)
	if err != nil {
		return false, err
	}
	want, err := decodeEditorJSON(desired, strictjson.StrictJSON)
	if err != nil {
		return false, err
	}
	return jsonContains(have, want), nil
}

func jsonContains(have, want any) bool {
	queue := []jsonMergeFrame{{have: have, want: want}}
	index := 0
	for ; index < len(queue) && index < maxJSONNodes; index++ {
		if !jsonFrameContains(queue[index], &queue) {
			return false
		}
	}
	return index == len(queue)
}

func jsonFrameContains(frame jsonMergeFrame, queue *[]jsonMergeFrame) bool {
	switch desired := frame.want.(type) {
	case map[string]any:
		return jsonObjectContains(frame.have, desired, queue)
	case []any:
		return jsonArrayContainsAll(frame.have, desired)
	default:
		return reflect.DeepEqual(frame.have, frame.want)
	}
}

func jsonObjectContains(have any, desired map[string]any, queue *[]jsonMergeFrame) bool {
	existing, ok := have.(map[string]any)
	if !ok {
		return false
	}
	for _, key := range slices.Sorted(maps.Keys(desired)) {
		existingValue, found := existing[key]
		if !found {
			return false
		}
		*queue = append(*queue, jsonMergeFrame{have: existingValue, want: desired[key]})
	}
	return true
}

func jsonArrayContainsAll(have any, desired []any) bool {
	existing, ok := have.([]any)
	if !ok {
		return false
	}
	for _, desiredItem := range desired {
		if !jsonArrayContains(existing, desiredItem) {
			return false
		}
	}
	return true
}

// jsonArrayContains matches a list item exactly, or a task-like object by the
// program or command it runs together with its arguments, so a relabelled human
// task that runs the same command is not duplicated.
func jsonArrayContains(existing []any, desired any) bool {
	desiredID := jsonItemIdentity(desired)
	for i := 0; i < len(existing) && i < maxJSONNodes; i++ {
		item := existing[i]
		if reflect.DeepEqual(item, desired) {
			return true
		}
		if desiredID != "" && desiredID == jsonItemIdentity(item) {
			return true
		}
	}
	return false
}

func jsonItemIdentity(value any) string {
	item, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	program := stringJSONValue(item["program"])
	if program == "" {
		program = stringJSONValue(item["command"])
	}
	args := arrayJSONValue(item["args"])
	parts := []string{program}
	for _, arg := range args {
		if text, ok := arg.(string); ok {
			parts = append(parts, text)
		}
	}
	if len(args) == 0 {
		parts = strings.Fields(program)
	}
	return strings.Join(parts, "\x00")
}

func stringJSONValue(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func arrayJSONValue(value any) []any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	return items
}
