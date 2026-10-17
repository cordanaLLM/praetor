// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package wirejson keeps the members a generated struct does not name through a JSON round trip
// (#909). The generated types of internal/clientschema/typegen call Decode and Encode from the
// UnmarshalJSON and MarshalJSON methods of every object whose schema leaves
// additionalProperties open, so a decode followed by an encode drops no member.
package wirejson

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// maxFields bounds the struct fields Decode inspects (HISS-02).
const maxFields = 1024

// Decode unmarshals data into target, a pointer to a struct type that has no JSON methods of its
// own, and stores in extra every member of the object that no field of the struct takes. Field
// names match the way encoding/json matches them, ignoring case. A null document leaves extra
// nil.
func Decode(data []byte, target any, extra *map[string]json.RawMessage) error {
	if err := json.Unmarshal(data, target); err != nil {
		return err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return fmt.Errorf("read members: %w", err)
	}
	for _, name := range fieldNames(reflect.TypeOf(target)) {
		for key := range members {
			if strings.EqualFold(key, name) {
				delete(members, key)
			}
		}
	}
	if len(members) == 0 {
		*extra = nil
		return nil
	}
	*extra = members
	return nil
}

// Encode marshals value, a struct without JSON methods, and adds the members of extra that its
// fields do not already write. Keys come out sorted, as encoding/json sorts a map.
func Encode(value any, extra map[string]json.RawMessage) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil || len(extra) == 0 {
		return data, err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return nil, fmt.Errorf("read members: %w", err)
	}
	for key, raw := range extra {
		if _, written := members[key]; !written {
			members[key] = raw
		}
	}
	return json.Marshal(members)
}

// fieldNames lists the JSON names of the exported fields of the struct that t points to.
func fieldNames(t reflect.Type) []string {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil
	}
	var names []string
	for i := 0; i < t.NumField() && i < maxFields; i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		switch name {
		case "-":
			continue
		case "":
			name = field.Name
		}
		names = append(names, name)
	}
	return names
}
