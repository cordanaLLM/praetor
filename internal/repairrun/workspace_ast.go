package repairrun

import (
	"bytes"
	"errors"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
)

type editStructure struct {
	declarations []string
	calls        map[string]int
	strings      map[string]bool
}

func validateASTEdit(original, candidate []byte) error {
	before, err := inspectEditStructure(original)
	if err != nil {
		return err
	}
	after, err := inspectEditStructure(candidate)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before.declarations, after.declarations) || !reflect.DeepEqual(before.calls, after.calls) {
		return errors.New("first-stage repair cannot change declarations, imports, signatures or call expressions")
	}
	for literal := range after.strings {
		if !before.strings[literal] && controlFraming(literal) {
			return errors.New("candidate introduces test-harness control framing")
		}
	}
	return nil
}

func inspectEditStructure(data []byte) (*editStructure, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "candidate.go", data, parser.AllErrors)
	if err != nil {
		return nil, errors.New("candidate must be valid Go source")
	}
	result := &editStructure{declarations: []string{}, calls: map[string]int{}, strings: map[string]bool{}}
	result.declarations = append(result.declarations, file.Name.Name)
	for _, declaration := range file.Decls {
		value, err := declarationShape(declaration)
		if err != nil {
			return nil, err
		}
		result.declarations = append(result.declarations, value)
	}
	count := 0
	ast.Inspect(file, func(node ast.Node) bool {
		if err != nil || node == nil {
			return false
		}
		count++
		if count > 32768 {
			err = errors.New("candidate AST exceeds node bound")
			return false
		}
		err = result.accept(node)
		return err == nil
	})
	return result, err
}

func declarationShape(node ast.Decl) (string, error) {
	function, ok := node.(*ast.FuncDecl)
	if !ok || function.Name.Name == "init" {
		return syntaxText(node)
	}
	copy := *function
	copy.Body = nil
	return syntaxText(&copy)
}

func (s *editStructure) accept(node ast.Node) error {
	switch value := node.(type) {
	case *ast.CallExpr:
		text, err := syntaxText(value)
		if err != nil {
			return err
		}
		s.calls[text]++
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return nil
		}
		text, err := strconv.Unquote(value.Value)
		if err != nil {
			return err
		}
		s.strings[text] = true
	}
	return nil
}

func syntaxText(node ast.Node) (string, error) {
	var output bytes.Buffer
	if err := format.Node(&output, token.NewFileSet(), node); err != nil {
		return "", err
	}
	return output.String(), nil
}

func controlFraming(text string) bool {
	return strings.ContainsRune(text, '\x16') || strings.Contains(text, "=== RUN") || strings.Contains(text, "--- PASS") || strings.Contains(text, "--- FAIL")
}
