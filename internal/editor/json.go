package editor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

const (
	// maxEditorJSONDepth bounds object and array nesting in one editor JSON document.
	maxEditorJSONDepth = 128
	// maxJSONNodes bounds the values (root, object members and array items) in one
	// editor JSON document and therefore every merge and containment traversal.
	maxJSONNodes = 4096
)

var errEditorJSONNodeBound = fmt.Errorf("editor JSON exceeds %d nodes", maxJSONNodes)

type editorJSONFrame struct {
	keys    map[string]bool
	keyNext bool
}

// decodeEditorJSON decodes exactly one bounded UTF-8 JSON document. Numbers stay
// json.Number literals so re-encoding never rounds them, and duplicate object keys
// are rejected because a map decode would silently keep only the last value.
func decodeEditorJSON(raw []byte) (any, error) {
	if len(raw) == 0 || len(raw) > contextopt.MaxSourceBytes || !utf8.Valid(raw) {
		return nil, fmt.Errorf("editor JSON requires 1..%d UTF-8 bytes", contextopt.MaxSourceBytes)
	}
	if err := validateEditorJSONTokens(raw); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode editor JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("editor JSON requires exactly one document")
	}
	if err := validateJSONNodeBound(value); err != nil {
		return nil, err
	}
	return value, nil
}

func validateEditorJSONTokens(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	stack := make([]editorJSONFrame, 0, 16)
	for tokenCount := 0; tokenCount <= len(raw); tokenCount++ {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("decode editor JSON token: %w", err)
		}
		if delimiter, ok := token.(json.Delim); ok {
			var consumeErr error
			stack, consumeErr = consumeEditorJSONDelimiter(stack, delimiter)
			if consumeErr != nil {
				return consumeErr
			}
			continue
		}
		if err := consumeEditorJSONToken(stack, token); err != nil {
			return err
		}
	}
	return errors.New("editor JSON token count exceeds byte bound")
}

func consumeEditorJSONDelimiter(stack []editorJSONFrame, delimiter json.Delim) ([]editorJSONFrame, error) {
	if delimiter == '}' || delimiter == ']' {
		if len(stack) == 0 {
			return nil, errors.New("invalid editor JSON delimiter")
		}
		return stack[:len(stack)-1], nil
	}
	consumeEditorJSONValue(stack)
	frame := editorJSONFrame{}
	if delimiter == '{' {
		frame.keys, frame.keyNext = make(map[string]bool), true
	}
	stack = append(stack, frame)
	if len(stack) > maxEditorJSONDepth {
		return nil, fmt.Errorf("editor JSON nesting exceeds %d", maxEditorJSONDepth)
	}
	return stack, nil
}

func consumeEditorJSONToken(stack []editorJSONFrame, token any) error {
	if len(stack) == 0 {
		return nil
	}
	frame := &stack[len(stack)-1]
	if frame.keys != nil && frame.keyNext {
		key, ok := token.(string)
		if !ok || frame.keys[key] {
			return fmt.Errorf("editor JSON contains an invalid or duplicate key %q", key)
		}
		frame.keys[key], frame.keyNext = true, false
		return nil
	}
	consumeEditorJSONValue(stack)
	return nil
}

func consumeEditorJSONValue(stack []editorJSONFrame) {
	if len(stack) > 0 {
		stack[len(stack)-1].keyNext = stack[len(stack)-1].keys != nil
	}
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

// mergeJSONDocument adds the managed values of desired to existing. Unrelated keys
// and list entries are retained; a managed value that conflicts with an existing one
// is an error and nothing is returned for writing.
func mergeJSONDocument(existing, desired []byte) ([]byte, bool, error) {
	have, err := decodeEditorJSON(existing)
	if err != nil {
		return nil, false, fmt.Errorf("existing JSON is invalid: %w", err)
	}
	want, err := decodeEditorJSON(desired)
	if err != nil {
		return nil, false, fmt.Errorf("generated JSON is invalid: %w", err)
	}
	merged, changed, err := mergeJSONValue(have, want)
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return existing, false, nil
	}
	data, err := encodeEditorJSON(merged)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

type jsonMergeFrame struct {
	have   any
	want   any
	parent map[string]any
	key    string
}

func sortedJSONKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func mergeJSONValue(have, want any) (any, bool, error) {
	root := have
	changed := false
	queue := []jsonMergeFrame{{have: have, want: want}}
	index := 0
	for ; index < len(queue) && index < maxJSONNodes; index++ {
		frameChanged, err := mergeJSONFrameValue(&root, queue[index], &queue)
		if err != nil {
			return nil, false, err
		}
		changed = changed || frameChanged
	}
	if index < len(queue) {
		return nil, false, errEditorJSONNodeBound
	}
	return root, changed, nil
}

func mergeJSONFrameValue(root *any, frame jsonMergeFrame, queue *[]jsonMergeFrame) (bool, error) {
	switch desired := frame.want.(type) {
	case map[string]any:
		return mergeJSONObject(frame, desired, queue)
	case []any:
		return mergeJSONArray(root, frame, desired)
	default:
		if !reflect.DeepEqual(frame.have, frame.want) {
			return false, fmt.Errorf("managed value for key %q conflicts with the existing value", frame.key)
		}
		return false, nil
	}
}

func mergeJSONObject(frame jsonMergeFrame, desired map[string]any, queue *[]jsonMergeFrame) (bool, error) {
	existing, ok := frame.have.(map[string]any)
	if !ok {
		return false, fmt.Errorf("managed object for key %q conflicts with the existing value", frame.key)
	}
	changed := false
	for _, key := range sortedJSONKeys(desired) {
		desiredValue := desired[key]
		existingValue, found := existing[key]
		if !found {
			existing[key] = desiredValue
			changed = true
			continue
		}
		*queue = append(*queue, jsonMergeFrame{have: existingValue, want: desiredValue, parent: existing, key: key})
	}
	return changed, nil
}

func mergeJSONArray(root *any, frame jsonMergeFrame, desired []any) (bool, error) {
	existing, ok := frame.have.([]any)
	if !ok {
		return false, fmt.Errorf("managed array for key %q conflicts with the existing value", frame.key)
	}
	changed := false
	for _, desiredItem := range desired {
		if !jsonArrayContains(existing, desiredItem) {
			existing = append(existing, desiredItem)
			changed = true
		}
	}
	if frame.parent == nil {
		*root = existing
	} else {
		frame.parent[frame.key] = existing
	}
	return changed, nil
}

// jsonDocumentContains reports whether existing contains every managed value in
// desired while allowing unrelated keys and list items. Both documents are decoded
// strictly, so ambiguous duplicate keys are an error rather than a pass.
func jsonDocumentContains(existing, desired []byte) (bool, error) {
	have, err := decodeEditorJSON(existing)
	if err != nil {
		return false, err
	}
	want, err := decodeEditorJSON(desired)
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
	for _, key := range sortedJSONKeys(desired) {
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
