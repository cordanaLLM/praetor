// Package schemacheck is the one JSON Schema oracle of the repository's test suites (#909, epic
// #910). It lives in its own module so the validator is a test dependency only: Praetor's
// production module has no JSON Schema library. The vendored schemas and their pins come from
// internal/clientschema; this package compiles them, validates documents against them and reports
// each violation with the JSON pointer of the offending field.
//
// API, kept small on purpose because units #913-#916, #918 and #920 reuse it:
//
//   - Compile / CompileVendored build a Schema from bytes or from a manifest path.
//   - Schema.Validate checks a JSON document, Schema.ValidateValue a decoded value, and both
//     return a *Violations that names every field, sorted, bounded.
//   - ReadYAML reads a YAML 1.2 document into a JSON-compatible value (yaml.go).
//   - ReadTOML reads a TOML document into a JSON-compatible value (toml.go).
//   - Fetch and SkipOffline serve the fetch-only path for schemas hosted in copyleft repositories
//     and the online pin check; offline runs skip with a stated reason (fetch.go).
//
// docs/guides/client-schemas.md is the guide.
package schemacheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/clientschema"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Bounds (HISS-02).
const (
	// MaxViolations bounds the violations one report carries.
	MaxViolations = 64
	// maxOutputNodes bounds the nodes of the validator's error tree one report walks.
	maxOutputNodes = 100000
	resourcePrefix = "https://schemas.praetor.invalid/"
)

// Schema is one compiled schema.
type Schema struct {
	name   string
	schema *jsonschema.Schema
}

// Violation is one failed check: the JSON pointer of the field and the validator's message.
type Violation struct {
	Field   string
	Message string
}

// Violations is the error Validate returns. Its text names every field.
type Violations struct {
	Schema string
	Items  []Violation
	// Truncated counts the violations beyond MaxViolations.
	Truncated int
}

// Error lists the schema and one line per violation.
func (v *Violations) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "document violates %s:", v.Schema)
	for _, item := range v.Items {
		fmt.Fprintf(&b, "\n  %s: %s", item.Field, item.Message)
	}
	if v.Truncated > 0 {
		fmt.Fprintf(&b, "\n  ... and %d more", v.Truncated)
	}
	return b.String()
}

// Names reports whether any violation sits at or below the field pointer, so a test can assert
// that a failure names the field it planted.
func (v *Violations) Names(field string) bool {
	want := "/" + strings.TrimPrefix(field, "/")
	for _, item := range v.Items {
		if item.Field == want || strings.HasPrefix(item.Field, want+"/") || strings.HasSuffix(item.Field, want) ||
			strings.Contains(item.Message, "'"+lastSegment(field)+"'") {
			return true
		}
	}
	return false
}

func lastSegment(pointer string) string {
	if i := strings.LastIndex(pointer, "/"); i >= 0 {
		return pointer[i+1:]
	}
	return pointer
}

// External maps the URL of a schema a vendored schema references to the text it is replaced
// with. A schema that points at a document Praetor neither vendors nor fetches (opencode's
// config references models.dev) compiles only with a stated replacement; Compile never reaches
// the network and never substitutes silently, so a caller names each replacement and its test
// reports it.
type External map[string]string

// Load serves a replacement, and refuses any other URL.
func (e External) Load(url string) (any, error) {
	text, ok := e[url]
	if !ok {
		return nil, fmt.Errorf("external schema %s has no stated replacement", url)
	}
	return jsonschema.UnmarshalJSON(strings.NewReader(text))
}

// Compile compiles raw as a schema named name. The schema's own $schema selects the draft;
// without one, draft 2020-12 applies. Every external reference needs a replacement in external.
func Compile(name string, raw []byte, external External) (*Schema, error) {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("read schema %s: %w", name, err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(external)
	url := resourcePrefix + name
	if err := compiler.AddResource(url, document); err != nil {
		return nil, fmt.Errorf("add schema %s: %w", name, err)
	}
	compiled, err := compiler.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("compile schema %s: %w", name, err)
	}
	return &Schema{name: name, schema: compiled}, nil
}

// repoRoot is the repository root seen from this package's directory, where go test runs.
const repoRoot = "../.."

// vendorDir is the vendor directory of internal/clientschema in this checkout.
var vendorDir = clientschema.VendorDir(repoRoot)

// CompileVendored compiles the vendored file at relPath, a Path of the manifest, after the pin
// check of clientschema.Read. external is nil where the schema is self-contained.
func CompileVendored(relPath string, external External) (*Schema, error) {
	manifest, err := clientschema.LoadManifest()
	if err != nil {
		return nil, err
	}
	raw, err := manifest.Schema(vendorDir, relPath)
	if err != nil {
		return nil, err
	}
	return Compile(relPath, raw, external)
}

// CompileDefinition compiles one named definition ($defs or definitions) of the schema in raw as
// the root, keeping every definition reachable for its references: it is how a test validates a
// message against, say, MCP's CallToolResult.
func CompileDefinition(name string, raw []byte, def string, external External) (*Schema, error) {
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("read schema %s: %w", name, err)
	}
	wrapper := map[string]any{}
	for _, key := range []string{"$schema", "$defs", "definitions"} {
		if value, ok := document[key]; ok {
			wrapper[key] = value
		}
	}
	for _, home := range []string{"$defs", "definitions"} {
		if defs, ok := document[home].(map[string]any); ok {
			if _, found := defs[def]; found {
				wrapper["$ref"] = "#/" + home + "/" + def
			}
		}
	}
	if _, ok := wrapper["$ref"]; !ok {
		return nil, fmt.Errorf("schema %s has no definition %q", name, def)
	}
	encoded, err := json.Marshal(wrapper)
	if err != nil {
		return nil, fmt.Errorf("encode schema %s: %w", name, err)
	}
	return Compile(name+"~"+def, encoded, external)
}

// Name is the manifest path or the name the schema was compiled under.
func (s *Schema) Name() string { return s.name }

// Validate checks one JSON document. A document that is not JSON is an error of its own.
func (s *Schema) Validate(raw []byte) error {
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("document is not JSON: %w", err)
	}
	return s.ValidateValue(value)
}

// ValidateValue checks a decoded value (the types jsonschema.UnmarshalJSON and ReadYAML return).
func (s *Schema) ValidateValue(value any) error {
	err := s.schema.Validate(value)
	if err == nil {
		return nil
	}
	var failure *jsonschema.ValidationError
	if !errors.As(err, &failure) {
		return fmt.Errorf("validate against %s: %w", s.name, err)
	}
	return collect(s.name, failure)
}

// collect walks the validator's error tree with an explicit stack (HISS-01: no recursion) and
// keeps its leaves, the checks that failed.
func collect(name string, root *jsonschema.ValidationError) *Violations {
	report := &Violations{Schema: name}
	seen := map[string]bool{}
	detailed := root.DetailedOutput()
	stack := []jsonschema.OutputUnit{*detailed}
	for visited := 0; len(stack) > 0 && visited < maxOutputNodes; visited++ {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if len(node.Errors) > 0 {
			stack = append(stack, node.Errors...)
			continue
		}
		if node.Error == nil {
			continue
		}
		item := Violation{Field: "/" + strings.TrimPrefix(node.InstanceLocation, "/"), Message: node.Error.String()}
		key := item.Field + "\x00" + item.Message
		if seen[key] {
			continue
		}
		seen[key] = true
		report.Items = append(report.Items, item)
	}
	sort.Slice(report.Items, func(i, j int) bool {
		if report.Items[i].Field != report.Items[j].Field {
			return report.Items[i].Field < report.Items[j].Field
		}
		return report.Items[i].Message < report.Items[j].Message
	})
	if len(report.Items) > MaxViolations {
		report.Truncated = len(report.Items) - MaxViolations
		report.Items = report.Items[:MaxViolations]
	}
	return report
}
