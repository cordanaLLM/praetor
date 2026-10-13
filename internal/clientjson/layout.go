package clientjson

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
)

// MaxPath bounds the member path SetAt and SetElement descend (HISS-02).
const MaxPath = 32

// emptyIndent indents what is written into a document that shows no indentation of its own,
// one written on one line or an empty object or array: there is no layout to keep, so the
// engine's own two-space rendering (Object.Encode) applies.
const emptyIndent = "  "

var (
	errPath    = fmt.Errorf("member path must hold 1 to %d names", MaxPath)
	errNoArray = errors.New("JSON member is not an array")
)

// Indent returns the indentation unit raw, the text of a JSON document, is written with: a tab
// when its first indented line starts with one, else that line's leading spaces. A document with
// no indented line, written on one line or flush left, has none, and Indent returns "". JSON
// strings hold no raw line break, so every line break in raw is part of the layout.
func Indent(raw []byte) string {
	rest := raw
	for i := 0; i < len(raw); i++ {
		at := bytes.IndexByte(rest, '\n')
		if at < 0 {
			return ""
		}
		rest = rest[at+1:]
		line := rest
		if end := bytes.IndexByte(line, '\n'); end >= 0 {
			line = line[:end]
		}
		trimmed := bytes.TrimLeft(line, " \t")
		if len(trimmed) == 0 || len(trimmed) == len(line) || trimmed[0] == '\r' {
			continue
		}
		if line[0] == '\t' {
			return "\t"
		}
		return string(line[:len(line)-len(bytes.TrimLeft(line, " "))])
	}
	return ""
}

// layout is how a value is written into one object or array of a document: the indentation
// unit, the line ending, and whether the container spans lines.
type layout struct {
	unit, newline string
	multiline     bool
}

// layoutOf returns the layout of the container at raw[start:end], holding count entries. An
// empty container is written across lines, in emptyIndent when raw has no indentation.
func layoutOf(raw []byte, start, end, count int) layout {
	l := layout{unit: Indent(raw), newline: "\n"}
	if bytes.Contains(raw, []byte("\r\n")) {
		l.newline = "\r\n"
	}
	l.multiline = l.unit != "" && bytes.Contains(raw[start:end], []byte("\n"))
	if count == 0 {
		l.multiline = true
		if l.unit == "" {
			l.unit = emptyIndent
		}
	}
	return l
}

// prefix is the indentation of a line at depth.
func (l layout) prefix(depth int) string {
	return strings.Repeat(l.unit, depth)
}

// render renders value for an entry at depth: indented, its continuation lines prefixed for
// that depth and ended in the layout's line ending, or on one line when the container is.
func (l layout) render(value jsontext.Value, depth int) ([]byte, error) {
	out := value.Clone()
	var err error
	if l.multiline {
		err = out.Indent(jsontext.WithIndentPrefix(l.prefix(depth)), jsontext.WithIndent(l.unit))
	} else {
		err = out.Compact()
	}
	if err != nil {
		return nil, errors.Join(errEncode, err)
	}
	if l.newline != "\n" {
		return bytes.ReplaceAll(out, []byte("\n"), []byte(l.newline)), nil
	}
	return out, nil
}

// separator is what goes between an entry and one appended after it at depth.
func (l layout) separator(depth int) string {
	if l.multiline {
		return "," + l.newline + l.prefix(depth)
	}
	return ", "
}

// enclose wraps entry, the only one of an empty container at depth, in its open and close.
func (l layout) enclose(open, closing string, entry []byte, depth int) []byte {
	return []byte(open + l.newline + l.prefix(depth) + string(entry) + l.newline + l.prefix(depth-1) + closing)
}

// SetAt returns raw, the text of one JSON object, with the member at path set to value. path
// names members from the root object down through objects only; the last name is replaced
// where it stands or, when its object lacks it, appended after that object's last member. The
// written value takes raw's own layout (layoutOf): its indentation unit (Indent) at the
// member's depth and its line ending, or one line inside an object written on one line. Every
// other byte of raw stays as written, so a tab-indented file stays tab-indented and member
// order, escapes and the final newline are kept. A document written on one line has no layout
// to keep and comes back in two-space indentation (expandOneLine). The result must load as one
// JSON object without a duplicate member name, or nothing is returned.
func SetAt(raw []byte, path []string, value jsontext.Value) ([]byte, error) {
	if err := checkEdit(raw, path, value, false); err != nil {
		return nil, err
	}
	start, end, err := objectSpan(raw, path[:len(path)-1])
	if err != nil {
		return nil, err
	}
	out, err := setMember(raw, start, end, path, value)
	if err != nil {
		return nil, err
	}
	return finishEdit(raw, out)
}

// SetElement returns raw, the text of one JSON object, with element index of the array held by
// the member at path replaced by value, appended when index is the array's length, or removed
// when value is nil. Like SetAt it writes in raw's own layout and keeps every other byte, the
// other elements included, as written.
func SetElement(raw []byte, path []string, index int, value jsontext.Value) ([]byte, error) {
	if err := checkEdit(raw, path, value, true); err != nil {
		return nil, err
	}
	start, end, err := arraySpan(raw, path)
	if err != nil {
		return nil, err
	}
	out, err := setArrayElement(raw, start, end, len(path)+1, index, value)
	if err != nil {
		return nil, err
	}
	return finishEdit(raw, out)
}

// checkEdit refuses an edit of raw at path with value before any byte is read for it; a nil
// value passes only where it means a removal (removable).
func checkEdit(raw []byte, path []string, value jsontext.Value, removable bool) error {
	if len(path) == 0 || len(path) > MaxPath {
		return errPath
	}
	if !jsontext.Value(raw).IsValid() {
		return errNotObject
	}
	if (value != nil || !removable) && !value.IsValid() {
		return fmt.Errorf("value for member %q is not one valid JSON value", path[len(path)-1])
	}
	return nil
}

// finishEdit returns out, the edited raw, expanded when raw was written on one line, and refuses
// it unless it loads as one JSON value without a duplicate member name.
func finishEdit(raw, out []byte) ([]byte, error) {
	if !bytes.Contains(raw, []byte("\n")) {
		var err error
		if out, err = expandOneLine(out); err != nil {
			return nil, err
		}
	}
	if !jsontext.Value(out).IsValid() {
		return nil, errors.New("the edit did not leave one valid JSON object")
	}
	return out, nil
}

// objectSpan returns the byte span of the object path names in raw, the root object for an
// empty path. Every name must hold an object.
func objectSpan(raw []byte, path []string) (int, int, error) {
	start := bytes.IndexByte(raw, '{')
	end := bytes.LastIndexByte(raw, '}') + 1
	if start < 0 || end <= start {
		return 0, 0, errNotObject
	}
	for _, name := range path {
		valueStart, valueEnd, err := memberSpan(raw, start, end, name)
		if err != nil {
			return 0, 0, err
		}
		if raw[valueStart] != '{' {
			return 0, 0, fmt.Errorf("member %q is not an object", name)
		}
		start, end = valueStart, valueEnd
	}
	return start, end, nil
}

// arraySpan returns the byte span of the array the member at path holds.
func arraySpan(raw []byte, path []string) (int, int, error) {
	start, end, err := objectSpan(raw, path[:len(path)-1])
	if err != nil {
		return 0, 0, err
	}
	valueStart, valueEnd, err := memberSpan(raw, start, end, path[len(path)-1])
	if err != nil {
		return 0, 0, err
	}
	if raw[valueStart] != '[' {
		return 0, 0, fmt.Errorf("%w: %q", errNoArray, path[len(path)-1])
	}
	return valueStart, valueEnd, nil
}

// memberSpan returns the byte span of the value of the member called name in the object at
// raw[start:end].
func memberSpan(raw []byte, start, end int, name string) (int, int, error) {
	object, ends, err := decodeMembers(raw[start:end])
	if err != nil {
		return 0, 0, err
	}
	index := memberIndex(object, name)
	if index < 0 {
		return 0, 0, fmt.Errorf("%w: %q", errNoMember, name)
	}
	valueEnd := start + ends[index]
	return valueEnd - len(object[index].Value), valueEnd, nil
}

// memberIndex returns the position of the member called name in object, -1 when it is absent.
func memberIndex(object Object, name string) int {
	for i, member := range object {
		if member.Name == name {
			return i
		}
	}
	return -1
}

// setMember sets the last name of path in the object at raw[start:end], depth len(path).
func setMember(raw []byte, start, end int, path []string, value jsontext.Value) ([]byte, error) {
	object, ends, err := decodeMembers(raw[start:end])
	if err != nil {
		return nil, err
	}
	depth, name := len(path), path[len(path)-1]
	l := layoutOf(raw, start, end, len(object))
	rendered, err := l.render(value, depth)
	if err != nil {
		return nil, err
	}
	if index := memberIndex(object, name); index >= 0 {
		valueEnd := start + ends[index]
		return splice(raw, valueEnd-len(object[index].Value), valueEnd, rendered), nil
	}
	key, err := json.Marshal(name)
	if err != nil {
		return nil, errEncode
	}
	member := append(append(key, ": "...), rendered...)
	if len(object) == 0 {
		return splice(raw, start, end, l.enclose("{", "}", member, depth)), nil
	}
	last := start + ends[len(ends)-1]
	return splice(raw, last, last, append([]byte(l.separator(depth)), member...)), nil
}

// setArrayElement sets element index of the array at raw[start:end], whose elements stand at
// depth: replaced, appended at the array's length, or removed when value is nil.
func setArrayElement(raw []byte, start, end, depth, index int, value jsontext.Value) ([]byte, error) {
	elements, ends, err := decodeElements(raw[start:end])
	if err != nil {
		return nil, err
	}
	if index < 0 || index > len(elements) || (value == nil && index == len(elements)) {
		return nil, fmt.Errorf("array element %d is out of range for %d elements", index, len(elements))
	}
	if value == nil {
		return removeElement(raw, start, end, elements, ends, index), nil
	}
	l := layoutOf(raw, start, end, len(elements))
	rendered, err := l.render(value, depth)
	if err != nil {
		return nil, err
	}
	switch {
	case index < len(elements):
		elementEnd := start + ends[index]
		return splice(raw, elementEnd-len(elements[index]), elementEnd, rendered), nil
	case len(elements) == 0:
		return splice(raw, start, end, l.enclose("[", "]", rendered, depth)), nil
	}
	last := start + ends[len(ends)-1]
	return splice(raw, last, last, append([]byte(l.separator(depth)), rendered...)), nil
}

// removeElement removes element index of the array at raw[start:end], with the separator that
// joined it to its neighbour; removing the only element leaves an empty array.
func removeElement(raw []byte, start, end int, elements []jsontext.Value, ends []int, index int) []byte {
	elementStart := start + ends[index] - len(elements[index])
	switch {
	case len(elements) == 1:
		return splice(raw, start, end, []byte("[]"))
	case index < len(elements)-1:
		next := start + ends[index+1] - len(elements[index+1])
		return splice(raw, elementStart, next, nil)
	}
	return splice(raw, start+ends[index-1], start+ends[index], nil)
}

// decodeElements returns the elements of the one JSON array raw holds and, for each, the offset
// in raw just past it. The loop reads at most one element per input byte.
func decodeElements(raw []byte) ([]jsontext.Value, []int, error) {
	decoder := jsontext.NewDecoder(bytes.NewReader(raw))
	if open, err := decoder.ReadToken(); err != nil || open.Kind() != '[' {
		return nil, nil, errNoArray
	}
	elements, ends := []jsontext.Value{}, []int{}
	for i := 0; i <= len(raw); i++ {
		if decoder.PeekKind() == ']' {
			return elements, ends, nil
		}
		value, err := decoder.ReadValue()
		if err != nil {
			return nil, nil, errNoArray
		}
		elements = append(elements, value.Clone())
		ends = append(ends, int(decoder.InputOffset()))
	}
	return nil, nil, errors.New("JSON token bound exceeded")
}

// expandOneLine renders out, a document written on one line, with the engine's two-space
// indentation (Object.Encode): one line has no indentation to keep, and an edit rewrites that
// line anyway. Whitespace before and after the object, a final newline included or not, stays
// as found; literals and escapes stay as written (jsontext.Value.Indent).
func expandOneLine(out []byte) ([]byte, error) {
	start, end := bytes.IndexByte(out, '{'), bytes.LastIndexByte(out, '}')+1
	if start < 0 || end <= start {
		return nil, errNotObject
	}
	object := jsontext.Value(bytes.Clone(out[start:end]))
	if err := object.Indent(jsontext.WithIndent(emptyIndent)); err != nil {
		return nil, errors.Join(errEncode, err)
	}
	return splice(out, start, end, object), nil
}

// splice returns raw with raw[start:end] replaced by insert.
func splice(raw []byte, start, end int, insert []byte) []byte {
	out := make([]byte, 0, len(raw)-(end-start)+len(insert))
	out = append(out, raw[:start]...)
	out = append(out, insert...)
	return append(out, raw[end:]...)
}
