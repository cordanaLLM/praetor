package forge

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

const (
	// maxReusableLevels is GitHub's nesting limit for reusable workflows: "You can connect up
	// to ten levels of workflows" (docs.github.com, Reusing workflow configurations, read
	// 2026-10-13), the calling workflow and nine called below it. A call deeper than that is
	// refused, as GitHub refuses to run it.
	maxReusableLevels = 10
	// maxReusableCalls bounds the calls one job's expansion follows, nested ones included:
	// GitHub allows "a maximum of 50 unique reusable workflows from a single workflow file"
	// (same page), and one workflow may be called by several of its jobs (HISS-02).
	maxReusableCalls = 50 * maxJobsPerFile
	// maxReusableContexts bounds the contexts one calling job expands to (HISS-02).
	maxReusableContexts = maxJobsPerFile * maxMatrixLegs
	// reusableContextSeparator joins the calling job's context and the called job's: the forge
	// reports one check per job of the called workflow, named "<caller> / <called job>"
	// (#1212, check runs of plurano/shared-ci pull request 5: "Licence / REUSE lint").
	reusableContextSeparator = " / "
	maxExprTokens            = 64
	maxExprStack             = 32
)

// calledWorkflows holds the repository's workflow documents by file name, so a job that calls
// a reusable workflow of this repository (localWorkflowPrefix) reads the jobs it runs. Each
// document is parsed once, on its first call.
type calledWorkflows struct {
	data   map[string][]byte
	parsed map[string]*workflowSpec
}

// newCalledWorkflows indexes files by name. A caller that selects some workflows indexes all of
// them, so a selected workflow can call one that is not selected.
func newCalledWorkflows(files []workflowFile) calledWorkflows {
	called := calledWorkflows{data: make(map[string][]byte, len(files)), parsed: map[string]*workflowSpec{}}
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		called.data[files[i].Name] = files[i].Data
	}
	return called
}

// spec returns the reusable workflow uses names, which job, the calling job, calls. A workflow
// of another repository is refused: its jobs, and with them the contexts the forge reports,
// cannot be read here, and a required check context that no run reports blocks the branch
// permanently. So is a local name that is no workflow of this repository declaring
// workflow_call.
func (c calledWorkflows) spec(job, uses string) (*workflowSpec, error) {
	name, local := strings.CutPrefix(strings.TrimSpace(uses), localWorkflowPrefix)
	if !local {
		return nil, fmt.Errorf("job %q calls the reusable workflow %s of another repository; its jobs, and so the "+
			"check contexts \"<job> / <called job>\" the forge reports for it, cannot be read here, and a required "+
			"check context that no run reports blocks the branch permanently", job, uses)
	}
	if spec, ok := c.parsed[name]; ok {
		return spec, nil
	}
	// Files are indexed by name, so a call into a subdirectory, where GitHub runs no
	// reusable workflow, finds none.
	data, ok := c.data[name]
	if !ok {
		return nil, fmt.Errorf("job %q calls %s, which is no workflow in %s", job, uses, plannedWorkflowDir)
	}
	var spec workflowSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("job %q calls %s: parse: %w", job, uses, err)
	}
	if _, reusable := eventTrigger(&spec.On, workflowCallEvent); !reusable {
		return nil, fmt.Errorf("job %q calls %s, which declares no %s trigger", job, uses, workflowCallEvent)
	}
	if len(spec.Jobs) > maxJobsPerFile {
		return nil, fmt.Errorf("job %q calls %s, which exceeds %d jobs", job, uses, maxJobsPerFile)
	}
	c.parsed[name] = &spec
	return &spec, nil
}

// reusableCall is one call still to expand: the contexts of the job that makes it, the
// workflow it calls, the calling job's id for messages, the level the called workflow
// runs at (the workflow a trigger starts is level 1), and the with inputs passed by the caller.
type reusableCall struct {
	prefixes []string
	uses     string
	job      string
	level    int
	with     map[string]any
}

// reusableCallContexts returns the check contexts a job that calls a reusable workflow reports
// inside the repository named identity (#1212). The forge reports none under the calling job's
// own name: it reports one check per job of the called workflow, "<caller> / <called job>". So
// the caller's contexts (jobCheckContexts: its name, its id, or one per matrix leg) are joined
// with those of every job of the called workflow that is itself required there (requiredJob,
// less what a proven aggregate covers, as for a workflow a trigger starts), each named the same
// way, matrix legs included. A called job that calls another workflow is followed through a
// work list, never by recursion (HISS-01), down to GitHub's limit of maxReusableLevels.
func reusableCallContexts(id string, job workflowJob, called calledWorkflows, identity string) ([]string, error) {
	callers, err := jobCheckContexts(id, job)
	if err != nil {
		return nil, err
	}
	queue := []reusableCall{{prefixes: callers, uses: job.Uses, job: id, level: 2, with: job.With}}
	var contexts []string
	for next := 0; next < len(queue); next++ {
		if next >= maxReusableCalls {
			return nil, fmt.Errorf("job %q: its reusable workflow calls exceed %d", id, maxReusableCalls)
		}
		found, calls, err := expandReusableCall(queue[next], called, identity)
		if err != nil {
			return nil, err
		}
		contexts = append(contexts, found...)
		if len(contexts) > maxReusableContexts {
			return nil, fmt.Errorf("job %q: its reusable workflow calls report more than %d contexts", id, maxReusableContexts)
		}
		queue = append(queue, calls...)
	}
	return contexts, nil
}

// expandReusableCall returns the contexts the jobs of one called workflow report and the calls
// its jobs make in turn, one level deeper.
func expandReusableCall(call reusableCall, called calledWorkflows, identity string) ([]string, []reusableCall, error) {
	spec, err := called.spec(call.job, call.uses)
	if err != nil {
		return nil, nil, err
	}
	ids := sortedJobIDs(spec.Jobs)
	covered := aggregateCoveredJobs(spec, ids)
	var contexts []string
	var calls []reusableCall
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		job := spec.Jobs[ids[i]]
		if covered[ids[i]] || !requiredJob(&job, identity) {
			continue
		}
		names, err := calledJobContexts(ids[i], job, spec, call.with)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", call.uses, err)
		}
		joined := joinReusableContexts(call.prefixes, names)
		if strings.TrimSpace(job.Uses) == "" {
			contexts = append(contexts, joined...)
			continue
		}
		if call.level+1 > maxReusableLevels {
			return nil, nil, fmt.Errorf("%s: job %q calls %s at level %d, deeper than the %d levels GitHub runs",
				call.uses, ids[i], job.Uses, call.level+1, maxReusableLevels)
		}
		calls = append(calls, reusableCall{prefixes: joined, uses: job.Uses, job: ids[i], level: call.level + 1, with: job.With})
	}
	return contexts, calls, nil
}

// calledJobContexts resolves a job's name expression before computing its check contexts.
func calledJobContexts(id string, job workflowJob, spec *workflowSpec, with map[string]any) ([]string, error) {
	if strings.Contains(job.Name, expressionOpen) && job.Strategy.Matrix.ShortTag() == "!!null" {
		if resolved, ok := resolveCalledJobName(job.Name, spec, with); ok {
			job.Name = resolved
		}
	}
	return jobCheckContexts(id, job)
}

// joinReusableContexts names every called job context under every calling one.
func joinReusableContexts(callers, names []string) []string {
	joined := make([]string, 0, len(callers)*len(names))
	for i := 0; i < len(callers) && i < maxMatrixLegs; i++ {
		for j := 0; j < len(names) && j < maxMatrixLegs; j++ {
			joined = append(joined, callers[i]+reusableContextSeparator+names[j])
		}
	}
	return joined
}

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
}

func (v exprValue) isTruthy() bool {
	if v.isBool {
		return v.bval
	}
	return v.text != ""
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
		return exprValue{text: strconv.Itoa(val)}, true
	case int64:
		return exprValue{text: strconv.FormatInt(val, 10)}, true
	case float64:
		return exprValue{text: strconv.FormatFloat(val, 'f', -1, 64)}, true
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
		body, after, closed := strings.Cut(expression, expressionClose)
		if !closed {
			return "", errors.New("an expression is not closed")
		}
		value, err := evalAllowListExpression(body, inputs)
		if err != nil {
			return "", err
		}
		evaluated.WriteString(value)
		rest = after
	}
	return "", fmt.Errorf("the name holds more than %d expressions", maxNameExpressions)
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
		*valStack = append(*valStack, inputs[tok.val])
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
	res := evalFormat(args)
	*valStack = append(*valStack, res)
	return nil
}

func evalFormat(args []exprValue) exprValue {
	fmtStr := args[0].String()
	for i := 1; i < len(args) && i < maxMatrixKeys; i++ {
		placeholder := fmt.Sprintf("{%d}", i-1)
		fmtStr = strings.ReplaceAll(fmtStr, placeholder, args[i].String())
	}
	return exprValue{text: fmtStr}
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
