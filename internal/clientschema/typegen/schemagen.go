// Package typegen generates Go types from the vendored upstream schemas (#909): the Codex hook
// input and output events and the MCP messages Praetor serves. It reads only internal/clientschema
// (the pinned bytes), writes only the files Targets names, and is checked by Check: a committed
// file that differs from what the vendored schemas generate fails the gate, so a schema bump
// without a regeneration cannot land.
//
// The generator covers the JSON Schema subset those schemas use: objects with properties,
// string enums, string constants, arrays, nullable and optional members, local references, and
// allOf with one reference. An object with no named members maps to map[string]V (V from a
// schema-valued additionalProperties, any otherwise), never to an empty struct that would drop
// every member. An object with named members is a struct; unless its schema says
// additionalProperties false, the struct also carries Extra, the members it does not name, and
// encodes through internal/clientschema/wirejson so a decode and encode round trip drops
// nothing. Anything else (a union) maps to json.RawMessage or any, and a construct the generator
// cannot place (an external reference, two properties that collide on one Go name) fails the
// generation instead of guessing.
package typegen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Bounds (HISS-02).
const (
	maxTypes      = 4096
	maxFields     = 1024
	maxCommentLen = 100
)

// Root names one type to generate: a definition of the schema (Def, such as "Tool"), or the whole
// document when Def is empty.
type Root struct {
	Def  string
	Name string
}

type pending struct {
	name string
	node map[string]any
}

type generator struct {
	defs    map[string]any
	emitted map[string]string // Go name -> canonical JSON of the schema it came from
	queue   []pending
	decls   []decl
}

type decl struct {
	name string
	text string
}

// Generate renders the Go source of the roots of one schema document into package pkg. header
// is the comment block placed above the package clause. The output is gofmt-formatted.
func Generate(pkg, header string, schema []byte, roots []Root) ([]byte, error) {
	document, err := decodeObject(schema)
	if err != nil {
		return nil, err
	}
	g := &generator{defs: definitions(document), emitted: map[string]string{}}
	for _, root := range roots {
		node := document
		if root.Def != "" {
			if node, err = g.definition(root.Def); err != nil {
				return nil, err
			}
		}
		g.enqueue(root.Name, node)
	}
	if err := g.run(); err != nil {
		return nil, err
	}
	return g.source(pkg, header)
}

// GenerateMany renders several documents into one package: each document contributes its roots,
// and a definition two documents share must be identical (a different one is prefixed with the
// root's name by the caller's choice of names, never merged silently).
func GenerateMany(pkg, header string, sources []Source) ([]byte, error) {
	merged := &generator{emitted: map[string]string{}}
	for _, source := range sources {
		document, err := decodeObject(source.Schema)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", source.Name, err)
		}
		g := &generator{defs: definitions(document), emitted: merged.emitted}
		g.decls = merged.decls
		g.enqueue(source.Root, document)
		if err := g.run(); err != nil {
			return nil, fmt.Errorf("%s: %w", source.Name, err)
		}
		merged.decls = g.decls
	}
	return merged.source(pkg, header)
}

// Source is one whole-document root of GenerateMany.
type Source struct {
	Name   string // the file it came from, for errors
	Root   string // the Go type name of the document
	Schema []byte
}

func decodeObject(raw []byte) (map[string]any, error) {
	var document map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&document); err != nil || document == nil {
		return nil, errors.New("schema is not a JSON object")
	}
	return document, nil
}

func definitions(document map[string]any) map[string]any {
	merged := map[string]any{}
	for _, key := range []string{"definitions", "$defs"} {
		if defs, ok := document[key].(map[string]any); ok {
			for name, node := range defs {
				merged[name] = node
			}
		}
	}
	return merged
}

func (g *generator) definition(name string) (map[string]any, error) {
	node, ok := g.defs[name].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema has no definition %q", name)
	}
	return node, nil
}

func (g *generator) enqueue(name string, node map[string]any) {
	g.queue = append(g.queue, pending{name: name, node: node})
}

// run drains the queue; emitting a type may enqueue the types it references.
func (g *generator) run() error {
	for steps := 0; len(g.queue) > 0; steps++ {
		if steps > maxTypes {
			return fmt.Errorf("more than %d types", maxTypes)
		}
		item := g.queue[0]
		g.queue = g.queue[1:]
		canonical, err := json.Marshal(item.node)
		if err != nil {
			return fmt.Errorf("type %s: %w", item.name, err)
		}
		if previous, seen := g.emitted[item.name]; seen {
			if previous != string(canonical) {
				return fmt.Errorf("type %s is defined twice with different schemas", item.name)
			}
			continue
		}
		g.emitted[item.name] = string(canonical)
		text, err := g.declare(item.name, item.node)
		if err != nil {
			return fmt.Errorf("type %s: %w", item.name, err)
		}
		g.decls = append(g.decls, decl{name: item.name, text: text})
	}
	return nil
}

func (g *generator) source(pkg, header string) ([]byte, error) {
	sort.Slice(g.decls, func(i, j int) bool { return g.decls[i].name < g.decls[j].name })
	var out strings.Builder
	var body strings.Builder
	for _, d := range g.decls {
		body.WriteString("\n" + d.text)
	}
	out.WriteString(header)
	out.WriteString("\npackage " + pkg + "\n")
	switch {
	case strings.Contains(body.String(), "wirejson."):
		out.WriteString("\nimport (\n\t\"encoding/json\"\n\n\t\"" + wirejsonImport + "\"\n)\n")
	case strings.Contains(body.String(), "json."):
		out.WriteString("\nimport \"encoding/json\"\n")
	}
	out.WriteString(body.String())
	formatted, err := format.Source([]byte(out.String()))
	if err != nil {
		return nil, fmt.Errorf("format generated source: %w", err)
	}
	return formatted, nil
}

// declare renders one named type.
func (g *generator) declare(name string, node map[string]any) (string, error) {
	node = g.settle(node)
	if values := stringEnum(node); len(values) > 0 {
		return enumDecl(name, node, values), nil
	}
	if hasMembers(node) {
		return g.structDecl(name, node)
	}
	expression, nullable, err := g.expr(node, name)
	if err != nil {
		return "", err
	}
	if nullable && pointerable(expression) {
		expression = "*" + expression
	}
	return comment(name, node) + "type " + name + " = " + expression + "\n", nil
}

// settle looks through allOf with one member so a wrapped reference reads as the reference.
func (g *generator) settle(node map[string]any) map[string]any {
	if parts, ok := node["allOf"].([]any); ok && len(parts) == 1 {
		if inner, ok := parts[0].(map[string]any); ok {
			merged := map[string]any{}
			for key, value := range inner {
				merged[key] = value
			}
			for key, value := range node {
				if key != "allOf" {
					merged[key] = value
				}
			}
			return merged
		}
	}
	return node
}

func stringEnum(node map[string]any) []string {
	list, ok := node["enum"].([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	values := make([]string, 0, len(list))
	for _, item := range list {
		text, ok := item.(string)
		if !ok {
			return nil
		}
		values = append(values, text)
	}
	return values
}

func enumDecl(name string, node map[string]any, values []string) string {
	var b strings.Builder
	b.WriteString(comment(name, node) + "type " + name + " string\n\nconst (\n")
	for _, value := range values {
		b.WriteString("\t" + name + goName(value) + " " + name + " = " + quote(value) + "\n")
	}
	b.WriteString(")\n")
	return b.String()
}

func requiredFields(node map[string]any) map[string]bool {
	required := map[string]bool{}
	if list, ok := node["required"].([]any); ok {
		for _, item := range list {
			if text, ok := item.(string); ok {
				required[text] = true
			}
		}
	}
	return required
}

func sortedPropertyKeys(properties map[string]any) []string {
	keys := make([]string, 0, len(properties))
	for key := range properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (g *generator) structDecl(name string, node map[string]any) (string, error) {
	properties, ok := node["properties"].(map[string]any)
	if !ok || len(properties) > maxFields {
		return "", fmt.Errorf("no properties object, or more than %d properties", maxFields)
	}
	required := requiredFields(node)
	keys := sortedPropertyKeys(properties)
	var b strings.Builder
	b.WriteString(comment(name, node) + "type " + name + " struct {\n")
	used := map[string]string{}
	for _, key := range keys {
		line, err := g.field(name, key, properties[key], required[key], used)
		if err != nil {
			return "", err
		}
		b.WriteString(line)
	}
	if isOpen(node) {
		if other, clash := used[extraField]; clash {
			return "", fmt.Errorf("property %q collides with the %s member of an open object", other, extraField)
		}
		b.WriteString("\t// " + extraField + " holds the members the schema does not name, kept verbatim.\n")
		b.WriteString("\t" + extraField + " map[string]json.RawMessage `json:\"-\"`\n")
		b.WriteString("}\n" + openMethods(name))
		return b.String(), nil
	}
	b.WriteString("}\n")
	return b.String(), nil
}

// extraField is the struct member that holds what an open object's properties do not name.
const extraField = "Extra"

// wirejsonImport is the package the methods of open objects call.
const wirejsonImport = "github.com/cordanaLLM/praetor/internal/clientschema/wirejson"

// openMethods renders the JSON methods that keep Extra through a round trip. The local plain
// type has the fields without the methods, so the call cannot recurse.
func openMethods(name string) string {
	return "\n// UnmarshalJSON decodes the named members and keeps the others in " + extraField + ".\n" +
		"func (v *" + name + ") UnmarshalJSON(data []byte) error {\n\ttype plain " + name + "\n" +
		"\treturn wirejson.Decode(data, (*plain)(v), &v." + extraField + ")\n}\n" +
		"\n// MarshalJSON encodes the named members, then " + extraField + ".\n" +
		"func (v " + name + ") MarshalJSON() ([]byte, error) {\n\ttype plain " + name + "\n" +
		"\treturn wirejson.Encode(plain(v), v." + extraField + ")\n}\n"
}

func (g *generator) field(owner, key string, raw any, required bool, used map[string]string) (string, error) {
	prop, ok := raw.(map[string]any)
	if !ok {
		prop = map[string]any{}
	}
	field := goName(key)
	if other, clash := used[field]; clash {
		return "", fmt.Errorf("properties %q and %q both map to the Go name %s", other, key, field)
	}
	used[field] = key
	expression, nullable, err := g.expr(g.settle(prop), owner+field)
	if err != nil {
		return "", fmt.Errorf("property %q: %w", key, err)
	}
	if (!required || nullable) && pointerable(expression) {
		expression = "*" + expression
	}
	tag := key
	if !required {
		tag += ",omitempty"
	}
	return "\t" + field + " " + expression + " `json:" + quote(tag) + "`" + trail(prop) + "\n", nil
}

// pointerable reports whether an optional member is told apart from its absence by a pointer:
// scalars, generated structs and maps, not slices, raw messages or any. A map needs it because
// an empty object is a statement ("logging": {} advertises a capability) that omitempty would
// drop.
func pointerable(expression string) bool {
	if strings.HasPrefix(expression, "[]") || strings.HasPrefix(expression, "*") {
		return false
	}
	return expression != "json.RawMessage" && expression != "any"
}

// isObjectMap reports whether node represents a map with a schema-valued additionalProperties.
func isObjectMap(node map[string]any) (map[string]any, bool) {
	if hasMembers(node) {
		return nil, false
	}
	kind := typeOf(node)
	if kind != "" && kind != "object" {
		return nil, false
	}
	extra, ok := node["additionalProperties"].(map[string]any)
	if !ok || len(extra) == 0 {
		return nil, false
	}
	return extra, true
}

// expr resolves a schema node to a Go type expression, queueing the named types it needs. The
// second result reports a nullable member (type list with null, or anyOf with a null branch).
func (g *generator) expr(node map[string]any, hint string) (string, bool, error) {
	prefix := ""
	nullable := false
	for depth := 0; depth < 64; depth++ {
		node = g.settle(node)
		if branches, isNull := nullBranch(node); isNull {
			nullable = true
			node = branches
		}
		if ref, ok := node["$ref"].(string); ok {
			name, err := g.reference(ref)
			return prefix + name, nullable, err
		}
		if kind := typeOf(node); kind == "array" {
			items, isObject := node["items"].(map[string]any)
			if !isObject {
				return prefix + "[]json.RawMessage", nullable, nil
			}
			prefix += "[]"
			node = items
			continue
		}
		if extra, ok := isObjectMap(node); ok {
			prefix += "map[string]"
			hint += "Value"
			node = extra
			continue
		}
		expression, err := g.leaf(node, hint)
		return prefix + expression, nullable, err
	}
	return "", false, errors.New("type nesting too deep")
}

func primitiveType(kind string) (string, bool) {
	switch kind {
	case "string":
		return "string", true
	case "integer":
		return "int64", true
	case "number":
		return "float64", true
	case "boolean":
		return "bool", true
	default:
		return "", false
	}
}

func isObjectNode(node map[string]any) bool {
	if typeOf(node) == "object" {
		return true
	}
	if _, ok := node["properties"].(map[string]any); ok {
		return true
	}
	_, ok := node["additionalProperties"]
	return ok
}

func (g *generator) leaf(node map[string]any, hint string) (string, error) {
	if prim, ok := primitiveType(typeOf(node)); ok {
		return prim, nil
	}
	if isObjectNode(node) {
		return g.objectExpr(node, hint)
	}
	if _, ok := node["const"].(string); ok {
		return "string", nil
	}
	if _, ok := node["anyOf"]; ok {
		return "json.RawMessage", nil
	}
	if _, ok := node["oneOf"]; ok {
		return "json.RawMessage", nil
	}
	return "any", nil
}

// hasMembers reports whether node is an object with at least one named member.
func hasMembers(node map[string]any) bool {
	properties, ok := node["properties"].(map[string]any)
	return ok && len(properties) > 0
}

// isOpen reports whether node admits members its properties do not name: JSON Schema's default,
// unless additionalProperties is false.
func isOpen(node map[string]any) bool {
	extra, present := node["additionalProperties"]
	return !present || extra != false
}

func (g *generator) objectExpr(node map[string]any, hint string) (string, error) {
	if hasMembers(node) {
		g.enqueue(hint, node)
		return hint, nil
	}
	return "map[string]any", nil
}

func (g *generator) reference(ref string) (string, error) {
	for _, prefix := range []string{"#/$defs/", "#/definitions/"} {
		if name, ok := strings.CutPrefix(ref, prefix); ok {
			node, err := g.definition(name)
			if err != nil {
				return "", err
			}
			goType := goName(name)
			g.enqueue(goType, node)
			return goType, nil
		}
	}
	return "", fmt.Errorf("reference %q is not local to the document", ref)
}

// typeOf returns the one non-null type of a node ("" when it has none or several).
func typeOf(node map[string]any) string {
	switch kind := node["type"].(type) {
	case string:
		return kind
	case []any:
		var found string
		for _, item := range kind {
			if text, ok := item.(string); ok && text != "null" {
				if found != "" {
					return ""
				}
				found = text
			}
		}
		return found
	}
	return ""
}

// nullBranch looks through the two spellings of a nullable member and returns the node without
// its null alternative.
func nullBranch(node map[string]any) (map[string]any, bool) {
	if list, ok := node["type"].([]any); ok {
		for _, item := range list {
			if item == "null" {
				return node, true
			}
		}
	}
	branches, ok := node["anyOf"].([]any)
	if !ok || len(branches) != 2 {
		return node, false
	}
	if other, found := nonNullBranch(branches); found {
		return other, true
	}
	return node, false
}

// nonNullBranch returns the one object branch of a two-branch anyOf whose other branch is the
// null type.
func nonNullBranch(branches []any) (map[string]any, bool) {
	var other map[string]any
	null := false
	for _, branch := range branches {
		entry, isObject := branch.(map[string]any)
		switch {
		case !isObject:
			return nil, false
		case entry["type"] == "null":
			null = true
		default:
			other = entry
		}
	}
	return other, null && other != nil
}

func goName(text string) string {
	var b strings.Builder
	upper := true
	for _, r := range text {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if upper {
				r = unicode.ToUpper(r)
			}
			b.WriteRune(r)
			upper = false
		default:
			upper = true
		}
	}
	name := b.String()
	if name == "" || unicode.IsDigit(rune(name[0])) {
		name = "X" + name
	}
	return name
}

func quote(text string) string {
	return strconv.Quote(text)
}

func comment(name string, node map[string]any) string {
	if description := describe(node); description != "" {
		return "// " + name + " is generated from the vendored schema: " + description + "\n"
	}
	return "// " + name + " is generated from the vendored schema.\n"
}

func trail(prop map[string]any) string {
	if description := describe(prop); description != "" {
		return " // " + oneLine(description)
	}
	if constant, ok := prop["const"].(string); ok {
		return " // always " + quote(constant)
	}
	return ""
}

// describe returns the first sentence of the description member of node, or "" without one.
func describe(node map[string]any) string {
	if text, ok := node["description"].(string); ok {
		return oneLine(text)
	}
	return ""
}

// oneLine reduces a description to its first sentence on one line, bounded.
func oneLine(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if cut := strings.Index(text, ". "); cut > 0 {
		text = text[:cut+1]
	}
	if len(text) > maxCommentLen {
		text = text[:maxCommentLen] + "..."
	}
	return text
}
