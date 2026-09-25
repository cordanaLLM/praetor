package difftest

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"github.com/cordanaLLM/praetor/internal/hiss"
)

// typeArgument instantiates every type parameter the planner accepts. int satisfies each
// accepted constraint (any, interface{} and comparable).
const typeArgument = "int"

// sourceInfo carries the file-level facts a signature is planned against: the names the
// file's imports bind and the type parameters each declared type carries.
type sourceInfo struct {
	fset       *token.FileSet
	imports    hiss.GoImports
	typeParams map[string]*ast.FieldList
}

func newSourceInfo(fset *token.FileSet, file *ast.File) *sourceInfo {
	info := &sourceInfo{fset: fset, imports: hiss.FileImports(file), typeParams: make(map[string]*ast.FieldList)}
	for i := 0; i < len(file.Decls); i++ {
		gen, ok := file.Decls[i].(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for j := 0; j < len(gen.Specs); j++ {
			if spec, isType := gen.Specs[j].(*ast.TypeSpec); isType && spec.TypeParams != nil {
				info.typeParams[spec.Name.Name] = spec.TypeParams
			}
		}
	}
	return info
}

// argPlan is the argument one parameter receives in each test dimension. omit drops the
// parameter from every call, which only a variadic parameter allows.
type argPlan struct {
	positive, negative, lower, upper string
	omit                             bool
}

// funcMetadata is the synthesis plan for one function or method: what to construct, what
// to call and with which arguments, derived from the declared signature so the generated
// test type-checks against it.
type funcMetadata struct {
	Name     string
	Receiver string
	// Callee is the expression each test calls: Name, Name[int] or obj.Name.
	Callee string
	// RecvInit constructs the receiver, such as new(Set[int]); empty for a function.
	RecvInit    string
	Params      []argPlan
	ResultCount int
	// ErrorLast reports that the final result is an error.
	ErrorLast   bool
	UsesContext bool
	UsesStrings bool
	// Imports maps each package name the arguments spell to its import path.
	Imports    map[string]string
	SkipReason string
}

var (
	nilArgPlan     = argPlan{positive: "nil", negative: "nil", lower: "nil", upper: "nil"}
	contextArgPlan = argPlan{positive: "ctx", negative: "ctx", lower: "ctx", upper: "ctx"}
)

// basicArgPlans holds per-kind literals. The negative input of an unsigned kind is 0
// because it has no negative value, and each upper bound fits its kind so the literal
// compiles.
var basicArgPlans = map[string]argPlan{
	"string":     {positive: `"test-positive"`, negative: `""`, lower: `""`, upper: `strings.Repeat("A", 1024)`},
	"bool":       {positive: "true", negative: "false", lower: "false", upper: "true"},
	"int":        {positive: "42", negative: "-1", lower: "0", upper: "100000"},
	"int8":       {positive: "42", negative: "-1", lower: "0", upper: "127"},
	"int16":      {positive: "42", negative: "-1", lower: "0", upper: "32767"},
	"int32":      {positive: "42", negative: "-1", lower: "0", upper: "2147483647"},
	"rune":       {positive: "42", negative: "-1", lower: "0", upper: "2147483647"},
	"int64":      {positive: "42", negative: "-1", lower: "0", upper: "9223372036854775807"},
	"uint":       {positive: "42", negative: "0", lower: "0", upper: "100000"},
	"uint8":      {positive: "42", negative: "0", lower: "0", upper: "255"},
	"byte":       {positive: "42", negative: "0", lower: "0", upper: "255"},
	"uint16":     {positive: "42", negative: "0", lower: "0", upper: "65535"},
	"uint32":     {positive: "42", negative: "0", lower: "0", upper: "4294967295"},
	"uint64":     {positive: "42", negative: "0", lower: "0", upper: "18446744073709551615"},
	"uintptr":    {positive: "42", negative: "0", lower: "0", upper: "100000"},
	"float32":    {positive: "1.5", negative: "-1", lower: "0", upper: "1e30"},
	"float64":    {positive: "1.5", negative: "-1", lower: "0", upper: "1e300"},
	"complex64":  {positive: "1.5", negative: "-1", lower: "0", upper: "1e30"},
	"complex128": {positive: "1.5", negative: "-1", lower: "0", upper: "1e300"},
}

// inspectFunction plans the tests for one declaration. A function the planner cannot call
// type-correctly carries a SkipReason instead of a guessed call.
func inspectFunction(info *sourceInfo, fn *ast.FuncDecl) funcMetadata {
	meta := funcMetadata{Name: fn.Name.Name, Callee: fn.Name.Name, Imports: make(map[string]string)}
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		meta.Receiver = extractReceiverTypeName(fn.Recv.List[0].Type)
	}
	typeParams, reason := planCallee(info, fn, &meta)
	if reason == "" {
		reason = planParameters(info, fn.Type.Params, typeParams, &meta)
	}
	if reason != "" {
		meta.SkipReason = reason
		return meta
	}
	meta.ResultCount, meta.ErrorLast = resultShape(fn.Type.Results)
	return meta
}

// planCallee sets how the function is reached and returns the type parameter names its
// parameters may spell, each instantiated with typeArgument.
func planCallee(info *sourceInfo, fn *ast.FuncDecl, meta *funcMetadata) (map[string]bool, string) {
	switch {
	case fn.Name.Name == "init" || fn.Name.Name == "_":
		return nil, fmt.Sprintf("%s cannot be referenced, so it cannot be called", fn.Name.Name)
	case fn.Recv != nil:
		return planReceiver(info, fn, meta)
	case fn.Type.TypeParams != nil:
		names, constraints := typeParamList(fn.Type.TypeParams)
		args, reason := instantiate(names, constraints)
		meta.Callee = fn.Name.Name + "[" + args + "]"
		return typeParamSet(names), reason
	}
	return nil, ""
}

// planReceiver constructs the receiver with new, which yields an addressable value for a
// pointer or value receiver of any underlying type. A generic receiver is instantiated
// from the constraints its type declaration carries in the same source.
func planReceiver(info *sourceInfo, fn *ast.FuncDecl, meta *funcMetadata) (map[string]bool, string) {
	if meta.Receiver == "" {
		return nil, "the receiver expression names no declared type"
	}
	meta.Callee = "obj." + fn.Name.Name
	names := receiverTypeParams(fn.Recv.List[0].Type)
	if len(names) == 0 {
		meta.RecvInit = "new(" + meta.Receiver + ")"
		return nil, ""
	}
	declared, ok := info.typeParams[meta.Receiver]
	if !ok {
		return nil, fmt.Sprintf("the type parameters of %s are not declared in this source", meta.Receiver)
	}
	declaredNames, constraints := typeParamList(declared)
	if len(declaredNames) != len(names) {
		return nil, fmt.Sprintf("receiver %s names %d type parameters, its declaration %d", meta.Receiver, len(names), len(declaredNames))
	}
	args, reason := instantiate(names, constraints)
	meta.RecvInit = "new(" + meta.Receiver + "[" + args + "])"
	return typeParamSet(names), reason
}

// receiverTypeParams returns the type parameter names a generic receiver binds, in order.
func receiverTypeParams(expr ast.Expr) []string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	var indices []ast.Expr
	switch t := ast.Unparen(expr).(type) {
	case *ast.IndexExpr:
		indices = []ast.Expr{t.Index}
	case *ast.IndexListExpr:
		indices = t.Indices
	}
	names := make([]string, 0, len(indices))
	for i := 0; i < len(indices); i++ {
		if ident, ok := indices[i].(*ast.Ident); ok {
			names = append(names, ident.Name)
		}
	}
	return names
}

// typeParamList flattens a type parameter list into one name and constraint per parameter.
func typeParamList(list *ast.FieldList) ([]string, []ast.Expr) {
	var names []string
	var constraints []ast.Expr
	for i := 0; i < len(list.List); i++ {
		field := list.List[i]
		for j := 0; j < len(field.Names); j++ {
			names = append(names, field.Names[j].Name)
			constraints = append(constraints, field.Type)
		}
	}
	return names, constraints
}

// instantiate returns the type argument list for the parameters, or why no single type
// argument is known to satisfy one of their constraints.
func instantiate(names []string, constraints []ast.Expr) (string, string) {
	args := make([]string, 0, len(names))
	for i := 0; i < len(names) && i < len(constraints); i++ {
		if !acceptsTypeArgument(constraints[i]) {
			return "", fmt.Sprintf("type parameter %s has a constraint no known type argument satisfies", names[i])
		}
		args = append(args, typeArgument)
	}
	return strings.Join(args, ", "), ""
}

// acceptsTypeArgument reports whether typeArgument satisfies the constraint: any,
// comparable or an empty interface.
func acceptsTypeArgument(constraint ast.Expr) bool {
	switch c := constraint.(type) {
	case *ast.Ident:
		return c.Name == "any" || c.Name == "comparable"
	case *ast.InterfaceType:
		return c.Methods == nil || len(c.Methods.List) == 0
	}
	return false
}

func typeParamSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for i := 0; i < len(names); i++ {
		set[names[i]] = true
	}
	return set
}

// planParameters plans one argument per declared parameter, so a (string, string, int)
// signature receives three arguments rather than one per kind.
func planParameters(info *sourceInfo, params *ast.FieldList, typeParams map[string]bool, meta *funcMetadata) string {
	if params == nil {
		return ""
	}
	for i := 0; i < len(params.List); i++ {
		field := params.List[i]
		plan, reason := planArgument(info, field.Type, typeParams, meta)
		if reason != "" {
			return reason
		}
		for n := 0; n < max(1, len(field.Names)); n++ {
			meta.Params = append(meta.Params, plan)
		}
	}
	return ""
}

// planArgument chooses the argument literals for one parameter type.
func planArgument(info *sourceInfo, expr ast.Expr, typeParams map[string]bool, meta *funcMetadata) (argPlan, string) {
	switch t := expr.(type) {
	case *ast.Ellipsis:
		return argPlan{omit: true}, ""
	case *ast.Ident:
		if plan, ok := identArgPlan(t.Name, typeParams, meta); ok {
			return plan, ""
		}
	case *ast.SelectorExpr:
		if isContextType(info, t) {
			meta.UsesContext = true
			return contextArgPlan, ""
		}
	case *ast.StarExpr, *ast.MapType, *ast.ChanType, *ast.FuncType, *ast.InterfaceType:
		return nilArgPlan, ""
	case *ast.ArrayType:
		if t.Len == nil {
			return sliceArgPlan(info, t, typeParams, meta), ""
		}
	}
	return zeroArgPlan(info, expr, typeParams, meta)
}

// identArgPlan plans a parameter whose type is a bare identifier it recognises: a type
// parameter, a predeclared basic type, error or any.
func identArgPlan(name string, typeParams map[string]bool, meta *funcMetadata) (argPlan, bool) {
	if typeParams[name] {
		return basicArgPlans[typeArgument], true
	}
	if name == "error" || name == "any" {
		return nilArgPlan, true
	}
	plan, ok := basicArgPlans[name]
	if ok && name == "string" {
		meta.UsesStrings = true
	}
	return plan, ok
}

// isContextType reports whether sel names context.Context through the file's imports,
// under whatever name the file binds package context to.
func isContextType(info *sourceInfo, sel *ast.SelectorExpr) bool {
	pkg, ok := sel.X.(*ast.Ident)
	return ok && sel.Sel.Name == "Context" && info.imports.Binds(pkg.Name, "context")
}

// sliceArgPlan passes a populated slice on the positive and upper paths and nil on the
// others. A slice whose element type cannot be spelled in the test receives nil throughout,
// which every slice type accepts.
func sliceArgPlan(info *sourceInfo, t *ast.ArrayType, typeParams map[string]bool, meta *funcMetadata) argPlan {
	typ, reason := renderType(info, t, typeParams, meta)
	if reason != "" {
		return nilArgPlan
	}
	return argPlan{positive: "make(" + typ + ", 1)", negative: "nil", lower: "nil", upper: "make(" + typ + ", 100)"}
}

// zeroArgPlan passes the type's zero value, *new(T), which is valid for every type T the
// test can spell.
func zeroArgPlan(info *sourceInfo, expr ast.Expr, typeParams map[string]bool, meta *funcMetadata) (argPlan, string) {
	typ, reason := renderType(info, expr, typeParams, meta)
	if reason != "" {
		return argPlan{}, reason
	}
	zero := "*new(" + typ + ")"
	return argPlan{positive: zero, negative: zero, lower: zero, upper: zero}, ""
}

// renderType prints a parameter type as the source spells it and records the imports its
// package qualifiers need. A type that spells a type parameter or a package the file's
// imports do not bind cannot be written in the test.
func renderType(info *sourceInfo, expr ast.Expr, typeParams map[string]bool, meta *funcMetadata) (string, string) {
	reason := ""
	ast.Inspect(expr, func(n ast.Node) bool {
		if reason != "" {
			return false
		}
		switch node := n.(type) {
		case *ast.SelectorExpr:
			// A qualified name is resolved as a whole; its two identifiers are not type
			// parameters whatever they are spelled.
			reason = qualifierReason(info, node, meta)
			return false
		case *ast.Ident:
			if typeParams[node.Name] {
				reason = fmt.Sprintf("a parameter type spells type parameter %s", node.Name)
			}
		}
		return reason == ""
	})
	if reason != "" {
		return "", reason
	}
	typ := renderNode(info.fset, expr)
	if typ == "" {
		return "", "the parameter type cannot be printed"
	}
	return typ, ""
}

// qualifierReason resolves the package a qualified type name selects from and records its
// import, or says why the test cannot spell it.
func qualifierReason(info *sourceInfo, sel *ast.SelectorExpr, meta *funcMetadata) string {
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return "a parameter type selects from a non-package expression"
	}
	importPath, bound := info.imports.Path(pkg.Name)
	if !bound {
		return fmt.Sprintf("package %s in a parameter type is not bound by an import", pkg.Name)
	}
	meta.Imports[pkg.Name] = importPath
	return ""
}

// resultShape returns how many values the function returns and whether the last one is
// an error.
func resultShape(results *ast.FieldList) (int, bool) {
	if results == nil || len(results.List) == 0 {
		return 0, false
	}
	count := 0
	for i := 0; i < len(results.List); i++ {
		count += max(1, len(results.List[i].Names))
	}
	last, ok := results.List[len(results.List)-1].Type.(*ast.Ident)
	return count, ok && last.Name == "error"
}
