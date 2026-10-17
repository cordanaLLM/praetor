package forge

// The allow-listed GitHub Actions expression subset a called job's name is evaluated with
// (#1334): string literals, inputs.<id>, == and !=, && and ||, format(), parentheses.

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

// exprTokenKind identifies one allow-listed token in an expression.
type exprTokenKind int

const (
	tokString exprTokenKind = iota + 1
	tokInput
	tokFormat
	tokEqual
	tokNotEqual
	tokAnd
	tokOr
	tokLParen
	tokRParen
	tokComma
)

type exprToken struct {
	kind exprTokenKind
	val  string
}

type exprValue struct {
	text   string
	isBool bool
	bval   bool
	// isNum marks a number input; GitHub reads 0 as false, every other number as true.
	isNum bool
}

func (v exprValue) isTruthy() bool {
	switch {
	case v.isBool:
		return v.bval
	case v.isNum:
		n, err := strconv.ParseFloat(v.text, 64)
		return err == nil && n != 0
	default:
		return v.text != ""
	}
}

func (v exprValue) String() string {
	if v.isBool {
		return strconv.FormatBool(v.bval)
	}
	return v.text
}

// workflowCallDefaults extracts default values of inputs from on.workflow_call.inputs.
func workflowCallDefaults(spec *workflowSpec) map[string]exprValue {
	defaults := make(map[string]exprValue)
	triggerValue, declared := eventTrigger(&spec.On, workflowCallEvent)
	if !declared || triggerValue == nil {
		return defaults
	}
	inputsNode := util.YAMLMappingValue(triggerValue, "inputs")
	if inputsNode == nil || inputsNode.Kind != yaml.MappingNode {
		return defaults
	}
	for i := 0; i+1 < len(inputsNode.Content) && i < 2*maxMatrixKeys; i += 2 {
		keyNode := inputsNode.Content[i]
		if keyNode.Kind != yaml.ScalarNode {
			continue
		}
		if val, ok := defaultExprValue(inputsNode.Content[i+1]); ok {
			defaults[keyNode.Value] = val
		}
	}
	return defaults
}

// defaultExprValue extracts a literal default value from one input's spec mapping.
func defaultExprValue(specNode *yaml.Node) (exprValue, bool) {
	defNode := util.YAMLMappingValue(specNode, "default")
	if defNode == nil || defNode.Kind != yaml.ScalarNode || strings.Contains(defNode.Value, expressionOpen) {
		return exprValue{}, false
	}
	if tag := defNode.ShortTag(); tag == "!!int" || tag == "!!float" {
		return exprValue{text: defNode.Value, isNum: true}, true
	}
	if defNode.ShortTag() == "!!bool" {
		if b, err := strconv.ParseBool(defNode.Value); err == nil {
			return exprValue{isBool: true, bval: b, text: defNode.Value}, true
		}
	}
	return exprValue{text: defNode.Value}, true
}

// callerInputs maps the caller job's with values to literal exprValues. Any value holding
// an expression or of an unsupported type returns ok=false so the expression is unresolved.
func callerInputs(with map[string]any) (map[string]exprValue, bool) {
	if len(with) == 0 {
		return make(map[string]exprValue), true
	}
	if len(with) > maxMatrixKeys {
		return nil, false
	}
	res := make(map[string]exprValue, len(with))
	for k, v := range with {
		val, ok := callerLiteralValue(v)
		if !ok {
			return nil, false
		}
		res[k] = val
	}
	return res, true
}

func callerLiteralValue(v any) (exprValue, bool) {
	switch val := v.(type) {
	case string:
		if strings.Contains(val, expressionOpen) {
			return exprValue{}, false
		}
		return exprValue{text: val}, true
	case bool:
		return exprValue{isBool: true, bval: val, text: strconv.FormatBool(val)}, true
	case int:
		return exprValue{text: strconv.Itoa(val), isNum: true}, true
	case int64:
		return exprValue{text: strconv.FormatInt(val, 10), isNum: true}, true
	case float64:
		return exprValue{text: strconv.FormatFloat(val, 'f', -1, 64), isNum: true}, true
	default:
		return exprValue{}, false
	}
}

// resolveInputs combines the called workflow's input defaults overlaid by the caller's
// literal with values. A with value holding an expression causes ok=false.
func resolveInputs(spec *workflowSpec, with map[string]any) (map[string]exprValue, bool) {
	callerMap, ok := callerInputs(with)
	if !ok {
		return nil, false
	}
	inputs := workflowCallDefaults(spec)
	for k, v := range callerMap {
		inputs[k] = v
	}
	return inputs, true
}

// resolveCalledJobName evaluates a called job's name expression against its resolved inputs.
func resolveCalledJobName(name string, spec *workflowSpec, with map[string]any) (string, bool) {
	inputs, ok := resolveInputs(spec, with)
	if !ok {
		return "", false
	}
	evaluated, err := evaluateCalledJobName(name, inputs)
	if err != nil {
		return "", false
	}
	evaluated = strings.TrimRightFunc(evaluated, unicode.IsSpace)
	if reportableName(evaluated) != nil {
		return "", false
	}
	return evaluated, true
}

// evaluateCalledJobName evaluates each ${{ ... }} expression in name against inputs.
func evaluateCalledJobName(name string, inputs map[string]exprValue) (string, error) {
	var evaluated strings.Builder
	rest := name
	for i := 0; i <= maxNameExpressions; i++ {
		before, expression, found := strings.Cut(rest, expressionOpen)
		evaluated.WriteString(before)
		if !found {
			return evaluated.String(), nil
		}
		end := expressionEnd(expression)
		if end < 0 {
			return "", errors.New("an expression is not closed")
		}
		body, after := expression[:end], expression[end+len(expressionClose):]
		value, err := evalAllowListExpression(body, inputs)
		if err != nil {
			return "", err
		}
		evaluated.WriteString(value)
		rest = after
	}
	return "", fmt.Errorf("the name holds more than %d expressions", maxNameExpressions)
}

// expressionEnd returns the index of the }} that closes an expression body, skipping single-quoted
// string literals (a doubled quote stays inside the literal), or -1 when none closes it.
func expressionEnd(body string) int {
	quoted := false
	for i := 0; i < len(body); i++ {
		switch {
		case body[i] == '\'':
			quoted = !quoted
		case !quoted && strings.HasPrefix(body[i:], expressionClose):
			return i
		}
	}
	return -1
}

// evalAllowListExpression evaluates one expression string within an allow-listed grammar.
func evalAllowListExpression(body string, inputs map[string]exprValue) (string, error) {
	tokens, err := tokenizeExpression(body)
	if err != nil {
		return "", err
	}
	if len(tokens) == 0 {
		return "", errors.New("empty expression")
	}
	val, err := evaluateTokens(tokens, inputs)
	if err != nil {
		return "", err
	}
	return val.String(), nil
}

// tokenizeExpression tokenizes an allow-listed expression string.
func tokenizeExpression(expr string) ([]exprToken, error) {
	var tokens []exprToken
	pos := 0
	for pos < len(expr) && len(tokens) < maxExprTokens {
		if unicode.IsSpace(rune(expr[pos])) {
			pos++
			continue
		}
		tok, next, err := nextExpressionToken(expr, pos)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, tok)
		pos = next
	}
	if pos < len(expr) {
		return nil, errors.New("expression exceeds maximum token bound")
	}
	return tokens, nil
}

func nextExpressionToken(expr string, pos int) (exprToken, int, error) {
	if expr[pos] == '\'' {
		str, next, err := scanStringLiteral(expr, pos)
		if err != nil {
			return exprToken{}, 0, err
		}
		return exprToken{kind: tokString, val: str}, next, nil
	}
	return nextSymbolToken(expr, pos)
}

func nextSymbolToken(expr string, pos int) (exprToken, int, error) {
	if tok, next, ok := nextOperatorToken(expr, pos); ok {
		return tok, next, nil
	}
	if tok, next, ok, err := nextNamedToken(expr, pos); ok || err != nil {
		if err != nil {
			return exprToken{}, 0, err
		}
		return tok, next, nil
	}
	return exprToken{}, 0, fmt.Errorf("unexpected token at position %d", pos)
}

func nextOperatorToken(expr string, pos int) (exprToken, int, bool) {
	rem := expr[pos:]
	switch {
	case strings.HasPrefix(rem, "=="):
		return exprToken{kind: tokEqual, val: "=="}, pos + 2, true
	case strings.HasPrefix(rem, "!="):
		return exprToken{kind: tokNotEqual, val: "!="}, pos + 2, true
	case strings.HasPrefix(rem, "&&"):
		return exprToken{kind: tokAnd, val: "&&"}, pos + 2, true
	case strings.HasPrefix(rem, "||"):
		return exprToken{kind: tokOr, val: "||"}, pos + 2, true
	case expr[pos] == '(':
		return exprToken{kind: tokLParen, val: "("}, pos + 1, true
	case expr[pos] == ')':
		return exprToken{kind: tokRParen, val: ")"}, pos + 1, true
	case expr[pos] == ',':
		return exprToken{kind: tokComma, val: ","}, pos + 1, true
	default:
		return exprToken{}, 0, false
	}
}

func nextNamedToken(expr string, pos int) (exprToken, int, bool, error) {
	rem := expr[pos:]
	if strings.HasPrefix(rem, "inputs.") {
		id, next, err := scanInputIdentifier(expr, pos+len("inputs."))
		if err != nil {
			return exprToken{}, 0, false, err
		}
		return exprToken{kind: tokInput, val: id}, next, true, nil
	}
	if strings.HasPrefix(rem, "format") {
		after := rem[len("format"):]
		if len(after) == 0 || after[0] == '(' || unicode.IsSpace(rune(after[0])) {
			return exprToken{kind: tokFormat, val: "format"}, pos + len("format"), true, nil
		}
	}
	return exprToken{}, 0, false, nil
}

func scanStringLiteral(expr string, pos int) (string, int, error) {
	var sb strings.Builder
	pos++
	for pos < len(expr) && sb.Len() < 1024 {
		if expr[pos] == '\'' {
			if pos+1 < len(expr) && expr[pos+1] == '\'' {
				sb.WriteByte('\'')
				pos += 2
				continue
			}
			return sb.String(), pos + 1, nil
		}
		sb.WriteByte(expr[pos])
		pos++
	}
	return "", 0, errors.New("unclosed string literal")
}

func scanInputIdentifier(expr string, pos int) (string, int, error) {
	start := pos
	for pos < len(expr) && pos-start < maxMatrixKeys && isMatrixKeyRune(rune(expr[pos])) {
		pos++
	}
	if pos == start {
		return "", 0, errors.New("empty input identifier")
	}
	return expr[start:pos], pos, nil
}

func opPrecedence(tok exprTokenKind) int {
	switch tok {
	case tokEqual, tokNotEqual:
		return 3
	case tokAnd:
		return 2
	case tokOr:
		return 1
	default:
		return 0
	}
}

func evaluateTokens(tokens []exprToken, inputs map[string]exprValue) (exprValue, error) {
	valStack := make([]exprValue, 0, maxExprStack)
	opStack := make([]exprTokenKind, 0, maxExprStack)
	argCounts := make([]int, 0, maxExprStack)

	for i := 0; i < len(tokens) && i < maxExprTokens; i++ {
		if err := processToken(tokens[i], inputs, &valStack, &opStack, &argCounts); err != nil {
			return exprValue{}, err
		}
	}
	return finalizeStacks(&valStack, &opStack)
}

func processToken(tok exprToken, inputs map[string]exprValue, valStack *[]exprValue, opStack *[]exprTokenKind, argCounts *[]int) error {
	switch tok.kind {
	case tokString:
		*valStack = append(*valStack, exprValue{text: tok.val})
	case tokInput:
		value, declared := inputs[tok.val]
		if !declared {
			return fmt.Errorf("inputs.%s has no default and no literal caller value", tok.val)
		}
		*valStack = append(*valStack, value)
	case tokFormat:
		*opStack = append(*opStack, tokFormat)
		*argCounts = append(*argCounts, 1)
	case tokLParen:
		*opStack = append(*opStack, tokLParen)
	case tokComma:
		return handleComma(valStack, opStack, argCounts)
	case tokRParen:
		return handleRParen(valStack, opStack, argCounts)
	case tokEqual, tokNotEqual, tokAnd, tokOr:
		return handleBinaryOp(tok.kind, valStack, opStack)
	default:
		return errors.New("unknown token")
	}
	return nil
}

func handleBinaryOp(op exprTokenKind, valStack *[]exprValue, opStack *[]exprTokenKind) error {
	prec := opPrecedence(op)
	for len(*opStack) > 0 && opPrecedence((*opStack)[len(*opStack)-1]) >= prec {
		top := (*opStack)[len(*opStack)-1]
		*opStack = (*opStack)[:len(*opStack)-1]
		if err := applyBinaryOp(top, valStack); err != nil {
			return err
		}
	}
	*opStack = append(*opStack, op)
	return nil
}

func handleComma(valStack *[]exprValue, opStack *[]exprTokenKind, argCounts *[]int) error {
	for len(*opStack) > 0 && (*opStack)[len(*opStack)-1] != tokLParen {
		top := (*opStack)[len(*opStack)-1]
		*opStack = (*opStack)[:len(*opStack)-1]
		if err := applyBinaryOp(top, valStack); err != nil {
			return err
		}
	}
	if len(*argCounts) == 0 {
		return errors.New("comma outside function call")
	}
	(*argCounts)[len(*argCounts)-1]++
	return nil
}

func handleRParen(valStack *[]exprValue, opStack *[]exprTokenKind, argCounts *[]int) error {
	for len(*opStack) > 0 && (*opStack)[len(*opStack)-1] != tokLParen {
		top := (*opStack)[len(*opStack)-1]
		*opStack = (*opStack)[:len(*opStack)-1]
		if err := applyBinaryOp(top, valStack); err != nil {
			return err
		}
	}
	if len(*opStack) == 0 || (*opStack)[len(*opStack)-1] != tokLParen {
		return errors.New("unmatched right parenthesis")
	}
	*opStack = (*opStack)[:len(*opStack)-1]
	if len(*opStack) > 0 && (*opStack)[len(*opStack)-1] == tokFormat {
		*opStack = (*opStack)[:len(*opStack)-1]
		return applyFormat(valStack, argCounts)
	}
	return nil
}

func applyFormat(valStack *[]exprValue, argCounts *[]int) error {
	if len(*argCounts) == 0 {
		return errors.New("missing argument count for format")
	}
	argc := (*argCounts)[len(*argCounts)-1]
	*argCounts = (*argCounts)[:len(*argCounts)-1]
	if len(*valStack) < argc || argc < 1 {
		return errors.New("format requires at least 1 argument")
	}
	args := (*valStack)[len(*valStack)-argc:]
	*valStack = (*valStack)[:len(*valStack)-argc]
	res, err := evalFormat(args)
	if err != nil {
		return err
	}
	*valStack = append(*valStack, res)
	return nil
}

// evalFormat replaces {N} in the first argument with argument N+1 in one pass, so a value that
// holds a placeholder is not replaced again, and reads {{ and }} as literal braces, as GitHub's
// format() does; a placeholder without an argument or a lone brace refuses.
func evalFormat(args []exprValue) (exprValue, error) {
	pattern := args[0].String()
	var out strings.Builder
	for pos := 0; pos < len(pattern); {
		next, err := formatStep(pattern, pos, args[1:], &out)
		if err != nil {
			return exprValue{}, err
		}
		pos = next
	}
	return exprValue{text: out.String()}, nil
}

// formatStep writes the piece of pattern at pos to out and returns the position after it.
func formatStep(pattern string, pos int, args []exprValue, out *strings.Builder) (int, error) {
	rest := pattern[pos:]
	switch {
	case strings.HasPrefix(rest, "{{"), strings.HasPrefix(rest, "}}"):
		out.WriteByte(rest[0])
		return pos + 2, nil
	case rest[0] == '}':
		return 0, errors.New("format: a lone } in the pattern")
	case rest[0] != '{':
		out.WriteByte(rest[0])
		return pos + 1, nil
	}
	index, after, closed := strings.Cut(rest[1:], "}")
	n, err := strconv.Atoi(index)
	if !closed || err != nil || n < 0 || n >= len(args) {
		return 0, fmt.Errorf("format: placeholder {%s} names no argument", index)
	}
	out.WriteString(args[n].String())
	return len(pattern) - len(after), nil
}

func finalizeStacks(valStack *[]exprValue, opStack *[]exprTokenKind) (exprValue, error) {
	for len(*opStack) > 0 {
		top := (*opStack)[len(*opStack)-1]
		*opStack = (*opStack)[:len(*opStack)-1]
		if top == tokLParen || top == tokFormat {
			return exprValue{}, errors.New("unclosed parenthesis or function")
		}
		if err := applyBinaryOp(top, valStack); err != nil {
			return exprValue{}, err
		}
	}
	if len(*valStack) != 1 {
		return exprValue{}, errors.New("invalid expression syntax")
	}
	return (*valStack)[0], nil
}

func applyBinaryOp(op exprTokenKind, valStack *[]exprValue) error {
	if len(*valStack) < 2 {
		return errors.New("missing operands for binary operator")
	}
	right := (*valStack)[len(*valStack)-1]
	left := (*valStack)[len(*valStack)-2]
	*valStack = (*valStack)[:len(*valStack)-2]
	var res exprValue
	switch op {
	case tokEqual, tokNotEqual:
		res = evalCompare(op, left, right)
	case tokAnd, tokOr:
		res = evalLogical(op, left, right)
	default:
		return fmt.Errorf("unsupported operator: %v", op)
	}
	*valStack = append(*valStack, res)
	return nil
}

func evalCompare(op exprTokenKind, left, right exprValue) exprValue {
	var equal bool
	if left.isBool && right.isBool {
		equal = left.bval == right.bval
	} else {
		equal = strings.EqualFold(left.String(), right.String())
	}
	if op == tokNotEqual {
		equal = !equal
	}
	return exprValue{isBool: true, bval: equal, text: strconv.FormatBool(equal)}
}

func evalLogical(op exprTokenKind, left, right exprValue) exprValue {
	if op == tokAnd {
		if left.isTruthy() {
			return right
		}
		return left
	}
	if left.isTruthy() {
		return left
	}
	return right
}
