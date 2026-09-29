package clientjson

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
)

// Member is one name and value of a JSON object. Value holds the member's bytes as read, so a
// rewrite keeps every number literal and nested order it did not touch.
type Member struct {
	Name  string
	Value jsontext.Value
}

// Object is a JSON object in document order. Decoding into a Go map would sort the members on
// the way out and round large numbers through float64; an Object does neither.
type Object []Member

var errNotObject = errors.New("client config section must be an object")

// DecodeObject returns the members of the one JSON object raw holds, in document order. A
// duplicate member name is refused, as Validate refuses it.
func DecodeObject(raw []byte) (Object, error) {
	object, _, err := decodeMembers(raw)
	return object, err
}

// decodeMembers returns the members of the one JSON object raw holds, in document order, and
// for each member the offset in raw just past its value. The loop reads at most one member per
// input byte.
func decodeMembers(raw []byte) (Object, []int, error) {
	decoder := jsontext.NewDecoder(bytes.NewReader(raw))
	open, err := decoder.ReadToken()
	if err != nil || open.Kind() != '{' {
		return nil, nil, errNotObject
	}
	object, ends := Object{}, []int{}
	for i := 0; i <= len(raw); i++ {
		if decoder.PeekKind() == '}' {
			return object, ends, nil
		}
		name, err := decoder.ReadToken()
		if err != nil {
			return nil, nil, errNotObject
		}
		key := name.String()
		value, err := decoder.ReadValue()
		if err != nil {
			return nil, nil, errNotObject
		}
		object = append(object, Member{Name: key, Value: value.Clone()})
		ends = append(ends, int(decoder.InputOffset()))
	}
	return nil, nil, errors.New("JSON token bound exceeded")
}

// ReplaceMember returns raw, the text of one JSON object, with the value of its top-level member
// called name replaced by value where it stands. Every other byte of raw stays as written:
// indentation, member order, string escapes such as \u003c, and line endings. Unlike
// Object.Encode, which re-renders the whole object, a replace touches only the value's bytes.
// raw must be one valid JSON object without a duplicate member name (jsontext.Value.IsValid),
// value one valid JSON value, and the member must exist: an absent member has no place whose
// layout the text could keep, so it is refused.
func ReplaceMember(raw []byte, name string, value jsontext.Value) ([]byte, error) {
	if !value.IsValid() {
		return nil, fmt.Errorf("replacement for member %q is not one valid JSON value", name)
	}
	if !jsontext.Value(raw).IsValid() {
		return nil, errNotObject
	}
	object, ends, err := decodeMembers(raw)
	if err != nil {
		return nil, err
	}
	for i, member := range object {
		if member.Name != name {
			continue
		}
		start := ends[i] - len(member.Value)
		out := make([]byte, 0, len(raw)-len(member.Value)+len(value))
		out = append(out, raw[:start]...)
		out = append(out, value...)
		return append(out, raw[ends[i]:]...), nil
	}
	return nil, fmt.Errorf("%w: %q", errNoMember, name)
}

// errNoMember refuses a ReplaceMember of a member the object does not hold.
var errNoMember = errors.New("JSON object has no such member")

// Get returns the value of the member called name.
func (o Object) Get(name string) (jsontext.Value, bool) {
	for _, member := range o {
		if member.Name == name {
			return member.Value, true
		}
	}
	return nil, false
}

// With returns o with the member called name set to value: replaced where it stands, or
// appended after the last member when absent.
func (o Object) With(name string, value jsontext.Value) Object {
	for i, member := range o {
		if member.Name == name {
			o[i].Value = value
			return o
		}
	}
	return append(o, Member{Name: name, Value: value})
}

// Encode renders o with two-space indentation and a final newline, members in order and every
// number literal as read; string escapes come out in the encoder's normal form.
func (o Object) Encode() ([]byte, error) {
	var buf bytes.Buffer
	encoder := jsontext.NewEncoder(&buf, jsontext.WithIndent("  "))
	if err := encoder.WriteToken(jsontext.BeginObject); err != nil {
		return nil, errEncode
	}
	for _, member := range o {
		if err := encoder.WriteToken(jsontext.String(member.Name)); err != nil {
			return nil, errEncode
		}
		if err := encoder.WriteValue(member.Value); err != nil {
			return nil, errEncode
		}
	}
	if err := encoder.WriteToken(jsontext.EndObject); err != nil {
		return nil, errEncode
	}
	return buf.Bytes(), nil
}

var errEncode = errors.New("could not encode client configuration")

// StringValue returns the JSON string raw holds, or "" when raw is absent or not a string.
func StringValue(raw jsontext.Value) string {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return value
}
