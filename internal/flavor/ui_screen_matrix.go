// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path"
	"regexp"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	screenMatrixUnit    = "sveltesentio-a11y-matrix-check-20261028"
	screenMatrixPending = "schema validation pending A11Y-CHECK1 pin (" + screenMatrixUnit + ")"
	maxMatrixSpecs      = 128
)

// screenMatrixPin identifies engine-owned schema bytes; audited repositories cannot replace it.
// A11Y-CHECK1 has not published its schema path, version or digest yet.
type screenMatrixPin struct {
	Source, Version, SHA256, Unit string
	Schema                        []byte
}

var screenMatrixSchemaPin = screenMatrixPin{
	Source:  "golusoris/sveltesentio (A11Y-CHECK1 schema path unpublished)",
	Version: "unpinned",
	SHA256:  "unpinned",
	Unit:    screenMatrixUnit,
}

// Only literal spec paths selected by ui-gates count; unrelated tests cannot satisfy the rule.
var matrixSpecPath = regexp.MustCompile(`\btests/[A-Za-z0-9_./-]+\.spec\.(?:ts|js)\b`)

func auditUIScreenMatrix(root string, manifest *config.Manifest, pin screenMatrixPin) (string, error) {
	line := ""
	if pin.Version == "unpinned" {
		line = screenMatrixPending
	}
	policy := manifest.DeclaredUI()
	if policy == nil || policy.ScreenMatrix == "" {
		return line, fmt.Errorf("missing required ui.screen_matrix; declare the repository-relative A11Y-CHECK1 data file in .standards.yaml")
	}
	if err := policy.Validate(); err != nil {
		return line, err
	}
	rel := policy.ScreenMatrix
	data, err := util.ReadConfinedLimited(root, rel, maxSettingBytes)
	if err != nil {
		return line, fmt.Errorf("ui.screen_matrix %q missing or unreadable: %w", rel, err)
	}
	schema, err := pinnedScreenMatrixSchema(pin)
	if err != nil {
		return line, err
	}
	if err := ValidateUIScreenMatrix(data, schema); err != nil {
		return line, fmt.Errorf("ui.screen_matrix %q: %w", rel, err)
	}
	if err := checkScreenMatrixSpec(root, rel); err != nil {
		return line, err
	}
	if schema != nil {
		line = "[PASS] A11Y-CHECK1 schema: " + pin.Source + "@" + pin.Version + " sha256:" + pin.SHA256
	}
	return line, nil
}

func pinnedScreenMatrixSchema(pin screenMatrixPin) ([]byte, error) {
	if pin.Version == "unpinned" {
		return nil, nil
	}
	if pin.Source == "" || pin.Version == "" || len(pin.Schema) == 0 || len(pin.Schema) > maxSettingBytes {
		return nil, fmt.Errorf("A11Y-CHECK1 schema pin incomplete (%s)", pin.Unit)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(pin.Schema)); got != pin.SHA256 {
		return nil, fmt.Errorf("A11Y-CHECK1 schema pin sha256 mismatch (%s)", pin.Unit)
	}
	return pin.Schema, nil
}

// ValidateUIScreenMatrix validates JSON, then the supplied A11Y-CHECK1 schema when non-nil.
// Nil means unpublished: only JSON syntax is checked, never schema conformance.
// Schema references outside the supplied document are refused without filesystem or network I/O.
func ValidateUIScreenMatrix(data, schema []byte) error {
	if len(data) > maxSettingBytes || len(schema) > maxSettingBytes {
		return fmt.Errorf("A11Y-CHECK1 document or schema exceeds %d bytes", maxSettingBytes)
	}
	if !json.Valid(data) {
		return fmt.Errorf("data is not valid JSON")
	}
	if schema == nil {
		return nil
	}
	compiled, err := compileScreenMatrixSchema(schema)
	if err != nil {
		return err
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("decode A11Y-CHECK1 data: %w", err)
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Errorf("A11Y-CHECK1 schema validation: %w", err)
	}
	return nil
}

func compileScreenMatrixSchema(raw []byte) (*jsonschema.Schema, error) {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("decode A11Y-CHECK1 schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(jsonschema.SchemeURLLoader{})
	const identity = "https://schemas.praetor.invalid/a11y-check1.json"
	if err := compiler.AddResource(identity, document); err != nil {
		return nil, fmt.Errorf("add A11Y-CHECK1 schema: %w", err)
	}
	compiled, err := compiler.Compile(identity)
	if err != nil {
		return nil, fmt.Errorf("compile A11Y-CHECK1 schema: %w", err)
	}
	return compiled, nil
}

func checkScreenMatrixSpec(root, rel string) error {
	makefile, err := util.ReadConfinedLimited(root, "Makefile", maxSettingBytes)
	if err != nil {
		return fmt.Errorf("read ui-gates recipe for ui.screen_matrix: %w", err)
	}
	normalized, _ := util.NormalizeLineEndings(string(makefile))
	recipe, _ := util.MakefileTargetRecipe(normalized, "ui-gates")
	specs := matrixSpecPath.FindAllString(recipe, maxMatrixSpecs+1)
	if len(specs) > maxMatrixSpecs {
		return fmt.Errorf("ui-gates exceeds %d literal Playwright spec paths", maxMatrixSpecs)
	}
	for i := 0; i < len(specs) && i < maxMatrixSpecs; i++ {
		content, err := util.ReadConfinedLimited(root, specs[i], maxSettingBytes)
		if err != nil {
			return fmt.Errorf("read ui-gates matrix spec %q: %w", specs[i], err)
		}
		if referencesScreenMatrix(string(content), rel) {
			return nil
		}
	}
	return fmt.Errorf("ui-gates Playwright *.spec.ts or *.spec.js must reference ui.screen_matrix %q (or basename %q) as a path string; add the A11Y-CHECK1 spec to the ui-gates run", rel, path.Base(rel))
}

func referencesScreenMatrix(content, rel string) bool {
	// Basenames also cover paths relative to a nested spec directory.
	for quotes, i := []string{"'", "\"", "`"}, 0; i < len(quotes) && i < 3; i++ {
		quote := regexp.QuoteMeta(quotes[i])
		pattern := quote + "(?:[^\"'`\\r\\n]*/)?" + regexp.QuoteMeta(path.Base(rel)) + quote
		if regexp.MustCompile(pattern).MatchString(content) {
			return true
		}
	}
	return false
}
