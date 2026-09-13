package planning

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

type jsonShapeKind uint8

const (
	jsonString jsonShapeKind = iota
	jsonInteger
	jsonBoolean
	jsonObject
	jsonArray
)

type jsonField struct {
	name  string
	shape *jsonShape
}

type jsonShape struct {
	kind    jsonShapeKind
	fields  []jsonField
	element *jsonShape
}

type jsonShapeWork struct {
	raw   json.RawMessage
	shape *jsonShape
	path  string
	depth int
}

var (
	stringJSONShape  = &jsonShape{kind: jsonString}
	integerJSONShape = &jsonShape{kind: jsonInteger}
	booleanJSONShape = &jsonShape{kind: jsonBoolean}
	stringArrayShape = arrayJSONShape(stringJSONShape)
	projectJSONShape = objectJSONShape(
		fieldJSONShape("id", stringJSONShape), fieldJSONShape("title", stringJSONShape),
		fieldJSONShape("repository", stringJSONShape), fieldJSONShape("revision", stringJSONShape),
	)
	sourceJSONShape = objectJSONShape(
		fieldJSONShape("id", stringJSONShape), fieldJSONShape("kind", stringJSONShape),
		fieldJSONShape("locator", stringJSONShape), fieldJSONShape("revision", stringJSONShape),
		fieldJSONShape("sha256", stringJSONShape), fieldJSONShape("provenance", stringJSONShape),
		fieldJSONShape("verified", booleanJSONShape),
	)
	citationJSONShape = objectJSONShape(
		fieldJSONShape("source_id", stringJSONShape), fieldJSONShape("source_sha256", stringJSONShape),
		fieldJSONShape("quote", stringJSONShape),
	)
	requirementJSONShape = objectJSONShape(
		fieldJSONShape("id", stringJSONShape), fieldJSONShape("detail", stringJSONShape),
		fieldJSONShape("disposition", stringJSONShape),
		fieldJSONShape("source_refs", arrayJSONShape(citationJSONShape)),
	)
	acceptanceJSONShape = objectJSONShape(
		fieldJSONShape("positive", stringArrayShape), fieldJSONShape("negative", stringArrayShape),
		fieldJSONShape("boundary", stringArrayShape),
	)
	milestoneJSONShape = objectJSONShape(
		fieldJSONShape("id", stringJSONShape), fieldJSONShape("title", stringJSONShape),
		fieldJSONShape("outcome", stringJSONShape), fieldJSONShape("depends_on", stringArrayShape),
		fieldJSONShape("acceptance", acceptanceJSONShape),
	)
	outputJSONShape = objectJSONShape(
		fieldJSONShape("id", stringJSONShape), fieldJSONShape("description", stringJSONShape),
	)
	linkJSONShape = objectJSONShape(
		fieldJSONShape("todo_id", stringJSONShape), fieldJSONShape("roadmap_id", stringJSONShape),
		fieldJSONShape("milestone_id", stringJSONShape),
	)
	stepJSONShape = objectJSONShape(
		fieldJSONShape("id", stringJSONShape), fieldJSONShape("title", stringJSONShape),
		fieldJSONShape("detail", stringJSONShape), fieldJSONShape("kind", stringJSONShape),
		fieldJSONShape("status", stringJSONShape), fieldJSONShape("requirement_ids", stringArrayShape),
		fieldJSONShape("milestone_id", stringJSONShape), fieldJSONShape("depends_on", stringArrayShape),
		fieldJSONShape("actions", stringArrayShape),
		fieldJSONShape("expected_outputs", arrayJSONShape(outputJSONShape)),
		fieldJSONShape("acceptance", acceptanceJSONShape), fieldJSONShape("link", linkJSONShape),
	)
	draftJSONShape = objectJSONShape(
		fieldJSONShape("schema_version", integerJSONShape), fieldJSONShape("id", stringJSONShape),
		fieldJSONShape("project", projectJSONShape), fieldJSONShape("sources", arrayJSONShape(sourceJSONShape)),
		fieldJSONShape("requirements", arrayJSONShape(requirementJSONShape)),
		fieldJSONShape("milestones", arrayJSONShape(milestoneJSONShape)),
		fieldJSONShape("steps", arrayJSONShape(stepJSONShape)),
	)
)

func fieldJSONShape(name string, shape *jsonShape) jsonField {
	return jsonField{name: name, shape: shape}
}

func objectJSONShape(fields ...jsonField) *jsonShape {
	return &jsonShape{kind: jsonObject, fields: fields}
}

func arrayJSONShape(element *jsonShape) *jsonShape {
	return &jsonShape{kind: jsonArray, element: element}
}

func validateDraftJSONShape(raw []byte) error {
	work := []jsonShapeWork{{raw: raw, shape: draftJSONShape, path: "draft", depth: 1}}
	for processed := 0; len(work) > 0 && processed < len(raw); processed++ {
		last := len(work) - 1
		item := work[last]
		work = work[:last]
		children, err := inspectJSONShape(item)
		if err != nil {
			return err
		}
		if len(children) > 0 && item.depth >= 32 {
			return fmt.Errorf("planning JSON shape nesting exceeds 32")
		}
		for index := len(children) - 1; index >= 0; index-- {
			children[index].depth = item.depth + 1
			work = append(work, children[index])
		}
	}
	if len(work) != 0 {
		return fmt.Errorf("planning JSON shape work exceeds input byte bound")
	}
	return nil
}

func inspectJSONShape(item jsonShapeWork) ([]jsonShapeWork, error) {
	if bytes.Equal(bytes.TrimSpace(item.raw), []byte("null")) {
		return nil, fmt.Errorf("planning JSON %s must be present and non-null", item.path)
	}
	switch item.shape.kind {
	case jsonString:
		return nil, validateJSONPrimitive[string](item.raw, item.path, "string")
	case jsonInteger:
		return nil, validateJSONPrimitive[int](item.raw, item.path, "integer")
	case jsonBoolean:
		return nil, validateJSONPrimitive[bool](item.raw, item.path, "boolean")
	case jsonObject:
		return inspectJSONObject(item.raw, item.shape.fields, item.path)
	case jsonArray:
		return inspectJSONArray(item.raw, item.shape.element, item.path)
	default:
		return nil, fmt.Errorf("planning JSON %s has an unsupported schema shape", item.path)
	}
}

func validateJSONPrimitive[T any](raw json.RawMessage, path, kind string) error {
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("planning JSON %s must be a %s", path, kind)
	}
	return nil
}

func inspectJSONObject(raw json.RawMessage, fields []jsonField, path string) ([]jsonShapeWork, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, fmt.Errorf("planning JSON %s must be an object", path)
	}
	children := make([]jsonShapeWork, 0, len(fields))
	for _, field := range fields {
		value, exists := values[field.name]
		if !exists {
			return nil, fmt.Errorf("planning JSON %s.%s must be present", path, field.name)
		}
		children = append(children, jsonShapeWork{raw: value, shape: field.shape, path: path + "." + field.name})
		delete(values, field.name)
	}
	if len(values) != 0 {
		return nil, fmt.Errorf("planning JSON %s contains unknown field %s", path, firstJSONKey(values))
	}
	return children, nil
}

func inspectJSONArray(raw json.RawMessage, element *jsonShape, path string) ([]jsonShapeWork, error) {
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, fmt.Errorf("planning JSON %s must be an array", path)
	}
	children := make([]jsonShapeWork, 0, len(values))
	for index, value := range values {
		children = append(children, jsonShapeWork{raw: value, shape: element, path: fmt.Sprintf("%s[%d]", path, index)})
	}
	return children, nil
}

func firstJSONKey(values map[string]json.RawMessage) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys[0]
}
