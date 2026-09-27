package clientjson

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
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
// duplicate member name is refused, as Validate refuses it. The loop reads at most one member
// per input byte.
func DecodeObject(raw []byte) (Object, error) {
	decoder := jsontext.NewDecoder(bytes.NewReader(raw))
	open, err := decoder.ReadToken()
	if err != nil || open.Kind() != '{' {
		return nil, errNotObject
	}
	object := Object{}
	for i := 0; i <= len(raw); i++ {
		if decoder.PeekKind() == '}' {
			return object, nil
		}
		name, err := decoder.ReadToken()
		if err != nil {
			return nil, errNotObject
		}
		key := name.String()
		value, err := decoder.ReadValue()
		if err != nil {
			return nil, errNotObject
		}
		object = append(object, Member{Name: key, Value: value.Clone()})
	}
	return nil, errors.New("JSON token bound exceeded")
}

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
