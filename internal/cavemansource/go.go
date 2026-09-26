package cavemansource

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
)

const maxGoSourceDeclarations = 4096

type goStringLiteral struct {
	text string
	line int
}

func extractGo(item discoveredInput, data []byte, packageGoverned map[string]bool) ([]Source, error) {
	if item.input.Selector == "mcp.descriptions" || item.input.Selector == "mcp.outputs" {
		return extractGoMCP(item, data, packageGoverned)
	}
	segments := strings.Split(item.input.Selector, ".")
	if len(segments) != 2 || segments[0] == "" {
		return nil, fmt.Errorf("caveman source %s: go selector requires table.*, table.index, mcp.descriptions, or mcp.outputs", item.path)
	}
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, item.path, data, 0)
	if err != nil {
		return nil, fmt.Errorf("caveman source %s: parse Go: %w", item.path, err)
	}
	literals, err := findGoStringTable(files, parsed, segments[0])
	if err != nil {
		return nil, fmt.Errorf("caveman source %s selector %s: %w", item.path, item.input.Selector, err)
	}
	indexes, err := selectGoStringIndexes(segments[1], len(literals))
	if err != nil {
		return nil, fmt.Errorf("caveman source %s selector %s: %w", item.path, item.input.Selector, err)
	}
	sources := make([]Source, 0, len(indexes))
	for sourceIndex := 0; sourceIndex < len(indexes) && sourceIndex < config.MaxRegisterSourceOutputs; sourceIndex++ {
		index := indexes[sourceIndex]
		selector := fmt.Sprintf("$.%s.%d", segments[0], index)
		sources = append(sources, sourceFrom(item, literals[index].text, selector, literals[index].line))
	}
	return sources, nil
}

func findGoStringTable(files *token.FileSet, parsed *ast.File, name string) ([]goStringLiteral, error) {
	if len(parsed.Decls) > maxGoSourceDeclarations {
		return nil, fmt.Errorf("go source exceeds %d declarations", maxGoSourceDeclarations)
	}
	specs, err := findGoStringSpecs(parsed, name)
	if err != nil {
		return nil, err
	}
	if len(specs) != 1 {
		return nil, fmt.Errorf("go string table %q requires exactly one declaration", name)
	}
	return decodeGoStringTable(files, specs[0])
}

func findGoStringSpecs(parsed *ast.File, name string) ([]*ast.ValueSpec, error) {
	var found []*ast.ValueSpec
	for declaration := 0; declaration < len(parsed.Decls); declaration++ {
		general, ok := parsed.Decls[declaration].(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		if len(general.Specs) > maxGoSourceDeclarations {
			return nil, fmt.Errorf("go declaration exceeds %d specs", maxGoSourceDeclarations)
		}
		for specIndex := 0; specIndex < len(general.Specs); specIndex++ {
			spec, ok := general.Specs[specIndex].(*ast.ValueSpec)
			if ok && goValueSpecNames(spec, name) {
				found = append(found, spec)
			}
		}
	}
	return found, nil
}

func goValueSpecNames(spec *ast.ValueSpec, name string) bool {
	return len(spec.Names) == 1 && spec.Names[0].Name == name
}

func decodeGoStringTable(files *token.FileSet, spec *ast.ValueSpec) ([]goStringLiteral, error) {
	if len(spec.Values) != 1 {
		return nil, fmt.Errorf("go string table %q requires one static composite literal", spec.Names[0].Name)
	}
	composite, ok := spec.Values[0].(*ast.CompositeLit)
	if !ok || !goStringArrayType(composite.Type) {
		return nil, fmt.Errorf("go string table %q requires [...]string or []string", spec.Names[0].Name)
	}
	if len(composite.Elts) == 0 || len(composite.Elts) > config.MaxRegisterSourceOutputs {
		return nil, fmt.Errorf("go string table %q requires 1..%d values", spec.Names[0].Name, config.MaxRegisterSourceOutputs)
	}
	values := make([]goStringLiteral, len(composite.Elts))
	for index := 0; index < len(composite.Elts) && index < config.MaxRegisterSourceOutputs; index++ {
		literal, ok := composite.Elts[index].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return nil, fmt.Errorf("go string table %q value %d requires a static string literal", spec.Names[0].Name, index)
		}
		text, err := strconv.Unquote(literal.Value)
		if err != nil {
			return nil, fmt.Errorf("go string table %q value %d: %w", spec.Names[0].Name, index, err)
		}
		values[index] = goStringLiteral{text: text, line: files.Position(literal.Pos()).Line}
	}
	return values, nil
}

func goStringArrayType(expression ast.Expr) bool {
	array, ok := expression.(*ast.ArrayType)
	if !ok {
		return false
	}
	element, ok := array.Elt.(*ast.Ident)
	return ok && element.Name == "string"
}

func selectGoStringIndexes(segment string, length int) ([]int, error) {
	if segment == "*" {
		return allGoStringIndexes(length), nil
	}
	index, ok := parseGoStringIndex(segment)
	if !ok || index >= length {
		return nil, fmt.Errorf("go string table index %q matched zero values", segment)
	}
	return []int{index}, nil
}

func allGoStringIndexes(length int) []int {
	indexes := make([]int, length)
	for index := 0; index < len(indexes) && index < config.MaxRegisterSourceOutputs; index++ {
		indexes[index] = index
	}
	return indexes
}

func parseGoStringIndex(segment string) (int, bool) {
	if segment == "" || segment != "0" && strings.HasPrefix(segment, "0") {
		return 0, false
	}
	for _, digit := range segment {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	index, err := strconv.Atoi(segment)
	return index, err == nil && index >= 0
}
