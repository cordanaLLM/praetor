package forge

import (
	"cmp"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// A matrix job reports one check per leg, and the name of each check is what a required context
// has to match exactly. The rules below expand a literal matrix the way the workflow syntax
// reference states (jobs.<job_id>.strategy.matrix, .include, .exclude) and name each leg the way
// GitHub reports it; every shape whose legs or names only a workflow run knows is refused with
// that shape named, because a required context no run reports leaves every pull request
// "expected" forever (#324).
const (
	// maxMatrixLegs is GitHub's documented ceiling on the jobs one matrix generates in a
	// workflow run. A matrix beyond it is refused rather than truncated.
	maxMatrixLegs = 256
	// maxMatrixProduct bounds the axis combinations built before exclude narrows them (HISS-02).
	maxMatrixProduct = 4096
	// maxMatrixKeys bounds the variables of one matrix, include or exclude entry, or leg.
	maxMatrixKeys = 64
	// maxNameExpressions bounds the ${{ }} expressions evaluated in one job name.
	maxNameExpressions = 16
	matrixIncludeKey   = "include"
	matrixExcludeKey   = "exclude"
	matrixContextName  = "matrix."
	expressionOpen     = "${{"
	expressionClose    = "}}"
)

// matrixValue is one matrix variable's value as a job name renders it. unknown is empty for a
// value that renders as its text; otherwise it says why the file alone cannot tell what the value
// renders as (newMatrixValue).
type matrixValue struct {
	text    string
	unknown string
}

// matrixCell is one matrix variable, keyed as the workflow spells it.
type matrixCell struct {
	key   string
	value matrixValue
}

// matrixLeg is one job a matrix expands into.
//
// base holds the variables GitHub appends to a job name that holds no expression, in the order it
// appends them: the axis values of a combination of the axes, or every variable of a leg an
// include entry added. added holds the variables an include entry merged into a combination of
// the axes, which GitHub does not append: axis `name: [a]` with include `{name: a, os: x}` reports
// `build (a)`.
type matrixLeg struct {
	base  []matrixCell
	added []matrixCell
}

// matrixAxis is one literal axis: a variable and its values in declaration order.
type matrixAxis struct {
	key    string
	values []matrixValue
}

// matrixSpec is a literal matrix: its axes in declaration order and its include and exclude
// entries in list order.
type matrixSpec struct {
	axes    []matrixAxis
	include [][]matrixCell
	exclude [][]matrixCell
}

// lookup returns the leg's value of key, matched without regard to case as GitHub matches matrix
// variable names, and false when the leg has no such variable.
func (leg matrixLeg) lookup(key string) (matrixValue, bool) {
	if index := cellIndex(leg.base, key); index >= 0 {
		return leg.base[index].value, true
	}
	if index := cellIndex(leg.added, key); index >= 0 {
		return leg.added[index].value, true
	}
	return matrixValue{}, false
}

// cellIndex returns the position of key in cells, compared without regard to case, or -1.
func cellIndex(cells []matrixCell, key string) int {
	for i := 0; i < len(cells) && i < maxMatrixKeys; i++ {
		if strings.EqualFold(cells[i].key, key) {
			return i
		}
	}
	return -1
}

// matrixJobContexts returns the context of every leg of a matrix job: name is the job's name, or
// its id when it has none (legContext).
func matrixJobContexts(id, name string, matrix *yaml.Node) ([]string, error) {
	legs, err := expandMatrix(matrix)
	if err != nil {
		return nil, fmt.Errorf("job %q: %w; a required check context that no run reports blocks the branch permanently", id, err)
	}
	contexts := make([]string, 0, len(legs))
	for i := 0; i < len(legs) && i < maxMatrixLegs; i++ {
		reported, err := legContext(name, legs[i])
		if err != nil {
			return nil, fmt.Errorf("job %q: leg %d leaves %q unresolved: %w; a required check context that no run reports blocks the branch permanently", id, i, name, err)
		}
		contexts = append(contexts, reported)
	}
	return contexts, nil
}

// expandMatrix returns the legs a literal matrix runs: every combination of the axes, the first
// axis varying slowest, less each one an exclude entry partially matches; then each include
// entry is merged into every such combination none of whose axis values it would overwrite, and
// becomes a leg of its own when it fits none. An include entry never merges into a leg an earlier
// entry added, and a matrix of include entries alone runs one leg per entry.
func expandMatrix(node *yaml.Node) ([]matrixLeg, error) {
	spec, err := parseMatrix(node)
	if err != nil {
		return nil, err
	}
	legs, err := axisCombinations(spec.axes)
	if err != nil {
		return nil, err
	}
	if legs, err = excludeLegs(legs, spec.axes, spec.exclude); err != nil {
		return nil, err
	}
	if legs, err = includeLegs(legs, spec.include); err != nil {
		return nil, err
	}
	if len(legs) == 0 {
		return nil, errors.New("strategy.matrix yields no leg")
	}
	if len(legs) > maxMatrixLegs {
		return nil, fmt.Errorf("matrix exceeds %d legs", maxMatrixLegs)
	}
	return legs, nil
}

// parseMatrix reads a strategy.matrix mapping. A matrix, axis, or include or exclude list that is
// one expression is evaluated only when the workflow runs, so it is refused with that shape named.
func parseMatrix(node *yaml.Node) (matrixSpec, error) {
	if node.Kind != yaml.MappingNode {
		return matrixSpec{}, fmt.Errorf("strategy.matrix is %s, not a mapping of axes", matrixShape(node))
	}
	if len(node.Content) > 2*maxMatrixKeys {
		return matrixSpec{}, fmt.Errorf("strategy.matrix exceeds %d variables", maxMatrixKeys)
	}
	var spec matrixSpec
	for i := 0; i+1 < len(node.Content) && i < 2*maxMatrixKeys; i += 2 {
		if err := spec.add(node.Content[i].Value, node.Content[i+1]); err != nil {
			return matrixSpec{}, err
		}
	}
	return spec, nil
}

// add reads one strategy.matrix entry: the include or exclude list, or an axis.
func (spec *matrixSpec) add(key string, value *yaml.Node) error {
	var err error
	switch key {
	case matrixIncludeKey:
		spec.include, err = parseMatrixEntries(key, value)
	case matrixExcludeKey:
		spec.exclude, err = parseMatrixEntries(key, value)
	default:
		var axis matrixAxis
		axis, err = parseMatrixAxis(key, value)
		spec.axes = append(spec.axes, axis)
	}
	return err
}

// parseMatrixAxis reads one axis, a non-empty list of values.
func parseMatrixAxis(key string, node *yaml.Node) (matrixAxis, error) {
	if node.Kind != yaml.SequenceNode {
		return matrixAxis{}, fmt.Errorf("matrix axis %q is %s, not a list", key, matrixShape(node))
	}
	if len(node.Content) == 0 {
		return matrixAxis{}, fmt.Errorf("matrix axis %q has no values", key)
	}
	if len(node.Content) > maxMatrixProduct {
		return matrixAxis{}, fmt.Errorf("matrix axis %q exceeds %d values", key, maxMatrixProduct)
	}
	axis := matrixAxis{key: key, values: make([]matrixValue, 0, len(node.Content))}
	for i := 0; i < len(node.Content) && i < maxMatrixProduct; i++ {
		axis.values = append(axis.values, newMatrixValue(node.Content[i]))
	}
	return axis, nil
}

// parseMatrixEntries reads the include or exclude list: a list of variable mappings.
func parseMatrixEntries(key string, node *yaml.Node) ([][]matrixCell, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("strategy.matrix.%s is %s, not a list", key, matrixShape(node))
	}
	if len(node.Content) > maxMatrixLegs {
		return nil, fmt.Errorf("strategy.matrix.%s exceeds %d entries", key, maxMatrixLegs)
	}
	entries := make([][]matrixCell, 0, len(node.Content))
	for i := 0; i < len(node.Content) && i < maxMatrixLegs; i++ {
		entry := node.Content[i]
		if entry.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("strategy.matrix.%s entry %d is %s, not a mapping", key, i, matrixShape(entry))
		}
		if len(entry.Content) > 2*maxMatrixKeys {
			return nil, fmt.Errorf("strategy.matrix.%s entry %d exceeds %d variables", key, i, maxMatrixKeys)
		}
		cells := make([]matrixCell, 0, len(entry.Content)/2)
		for j := 0; j+1 < len(entry.Content) && j < 2*maxMatrixKeys; j += 2 {
			cells = append(cells, matrixCell{key: entry.Content[j].Value, value: newMatrixValue(entry.Content[j+1])})
		}
		entries = append(entries, cells)
	}
	return entries, nil
}

// matrixShape names a matrix node by its shape, so a refusal says what it met: an expression is
// evaluated only when the workflow runs, and an alias is not resolved here.
func matrixShape(node *yaml.Node) string {
	switch {
	case node.Kind == yaml.ScalarNode && strings.Contains(node.Value, expressionOpen):
		return fmt.Sprintf("the expression %q (only a workflow run knows its value)", node.Value)
	case node.Kind == yaml.ScalarNode && node.ShortTag() == "!!null":
		return "null"
	case node.Kind == yaml.ScalarNode:
		return fmt.Sprintf("the scalar %q", node.Value)
	case node.Kind == yaml.MappingNode:
		return "a mapping"
	case node.Kind == yaml.SequenceNode:
		return "a list"
	case node.Kind == yaml.AliasNode:
		return "a YAML alias"
	default:
		return "an empty node"
	}
}

// newMatrixValue records one matrix value as a job name renders it: a string as written, and an
// integer, boolean or decimal whose YAML spelling is the text its value formats as. Anything else
// is unknown, with the reason: an expression, a mapping, list, alias or null, or a number spelled
// otherwise than it formats (3.10 is the number 3.1 to a YAML reader), which quoting settles.
func newMatrixValue(node *yaml.Node) matrixValue {
	if node.Kind != yaml.ScalarNode || strings.Contains(node.Value, expressionOpen) || node.ShortTag() == "!!null" {
		return matrixValue{unknown: "is " + matrixShape(node)}
	}
	if !canonicalScalar(node) {
		return matrixValue{unknown: fmt.Sprintf("is the %s %s, spelled otherwise than its value formats; quote it", node.ShortTag(), node.Value)}
	}
	return matrixValue{text: node.Value}
}

// canonicalScalar reports whether a scalar renders as it is spelled: any string, and an integer,
// boolean or decimal spelled the way its value formats.
func canonicalScalar(node *yaml.Node) bool {
	switch node.ShortTag() {
	case "!!str":
		return true
	case "!!int":
		value, err := strconv.ParseInt(node.Value, 10, 64)
		return err == nil && strconv.FormatInt(value, 10) == node.Value
	case "!!bool":
		return node.Value == "true" || node.Value == "false"
	case "!!float":
		value, err := strconv.ParseFloat(node.Value, 64)
		return err == nil && strconv.FormatFloat(value, 'f', -1, 64) == node.Value
	default:
		return false
	}
}

// axisCombinations returns every combination of the axes, the first axis varying slowest, or no
// leg for a matrix without axes. The count is bounded before any leg is built.
func axisCombinations(axes []matrixAxis) ([]matrixLeg, error) {
	if len(axes) == 0 {
		return nil, nil
	}
	total := 1
	for i := 0; i < len(axes) && i < maxMatrixKeys; i++ {
		total *= len(axes[i].values)
		if total > maxMatrixProduct {
			return nil, fmt.Errorf("matrix axes combine into more than %d legs", maxMatrixProduct)
		}
	}
	legs := make([]matrixLeg, 0, total)
	for index := 0; index < total && index < maxMatrixProduct; index++ {
		cells := make([]matrixCell, len(axes))
		remainder := index
		for i := len(axes) - 1; i >= 0 && i < maxMatrixKeys; i-- {
			size := len(axes[i].values)
			cells[i] = matrixCell{key: axes[i].key, value: axes[i].values[remainder%size]}
			remainder /= size
		}
		legs = append(legs, matrixLeg{base: cells})
	}
	return legs, nil
}

// excludeLegs drops every combination an exclude entry partially matches (entryMatches). An
// exclude entry naming a variable no axis declares is refused, as GitHub refuses the workflow
// ("Matrix exclude key ... does not match any key within the matrix").
func excludeLegs(legs []matrixLeg, axes []matrixAxis, exclude [][]matrixCell) ([]matrixLeg, error) {
	for i := 0; i < len(exclude) && i < maxMatrixLegs; i++ {
		for j := 0; j < len(exclude[i]) && j < maxMatrixKeys; j++ {
			if !declaresAxis(axes, exclude[i][j].key) {
				return nil, fmt.Errorf("strategy.matrix.exclude entry %d names %q, which no axis declares", i, exclude[i][j].key)
			}
		}
	}
	kept := make([]matrixLeg, 0, len(legs))
	for i := 0; i < len(legs) && i < maxMatrixProduct; i++ {
		excluded, err := matchesAnyEntry(legs[i].base, exclude)
		if err != nil {
			return nil, fmt.Errorf("strategy.matrix.exclude: %w", err)
		}
		if !excluded {
			kept = append(kept, legs[i])
		}
	}
	return kept, nil
}

// declaresAxis reports whether an axis of the matrix is the variable key, compared without regard
// to case.
func declaresAxis(axes []matrixAxis, key string) bool {
	for i := 0; i < len(axes) && i < maxMatrixKeys; i++ {
		if strings.EqualFold(axes[i].key, key) {
			return true
		}
	}
	return false
}

// matchesAnyEntry reports whether any entry matches base (entryMatches).
func matchesAnyEntry(base []matrixCell, entries [][]matrixCell) (bool, error) {
	for i := 0; i < len(entries) && i < maxMatrixLegs; i++ {
		matched, err := entryMatches(base, entries[i])
		if err != nil || matched {
			return matched, err
		}
	}
	return false, nil
}

// entryMatches reports whether every variable of entry that base holds has the same value there;
// a variable base lacks does not decide it. A comparison whose outcome the file cannot show
// (valuesEqual) is refused unless another variable already rules the match out.
func entryMatches(base, entry []matrixCell) (bool, error) {
	var undecided error
	for i := 0; i < len(entry) && i < maxMatrixKeys; i++ {
		index := cellIndex(base, entry[i].key)
		if index < 0 {
			continue
		}
		equal, err := valuesEqual(entry[i].key, base[index].value, entry[i].value)
		if err != nil {
			undecided = cmp.Or(undecided, err)
			continue
		}
		if !equal {
			return false, nil
		}
	}
	return undecided == nil, undecided
}

// valuesEqual compares two values of the matrix variable key. An unknown value, or two texts that
// differ only in case, cannot be compared from the file: which legs exist depends on the answer.
func valuesEqual(key string, left, right matrixValue) (bool, error) {
	for _, value := range [...]matrixValue{left, right} {
		if value.unknown != "" {
			return false, fmt.Errorf("matrix variable %q %s", key, value.unknown)
		}
	}
	if left.text != right.text && strings.EqualFold(left.text, right.text) {
		return false, fmt.Errorf("matrix variable %q compares %q with %q, which differ only in case", key, left.text, right.text)
	}
	return left.text == right.text, nil
}

// includeLegs applies each include entry in order to the combinations of the axes (mergeInclude),
// appending the entry as a leg of its own when it fits none of them.
func includeLegs(legs []matrixLeg, include [][]matrixCell) ([]matrixLeg, error) {
	originals := len(legs)
	for i := 0; i < len(include) && i < maxMatrixLegs; i++ {
		fitted, err := mergeInclude(legs[:originals], include[i])
		if err != nil {
			return nil, fmt.Errorf("strategy.matrix.include entry %d: %w", i, err)
		}
		if !fitted {
			legs = append(legs, matrixLeg{base: include[i]})
		}
	}
	return legs, nil
}

// mergeInclude merges entry into every original leg none of whose axis values it overwrites, and
// reports whether it fit any. A variable the entry adds replaces what an earlier entry added.
func mergeInclude(originals []matrixLeg, entry []matrixCell) (bool, error) {
	fitted := false
	for i := 0; i < len(originals) && i < maxMatrixProduct; i++ {
		fits, err := entryMatches(originals[i].base, entry)
		if err != nil {
			return false, err
		}
		if !fits {
			continue
		}
		fitted = true
		if err := originals[i].addCells(entry); err != nil {
			return false, err
		}
	}
	return fitted, nil
}

// addCells sets each variable of entry the leg's axes do not hold, replacing one an earlier
// include entry added under the same name.
func (leg *matrixLeg) addCells(entry []matrixCell) error {
	for i := 0; i < len(entry) && i < maxMatrixKeys; i++ {
		if cellIndex(leg.base, entry[i].key) >= 0 {
			continue
		}
		if index := cellIndex(leg.added, entry[i].key); index >= 0 {
			leg.added[index].value = entry[i].value
			continue
		}
		if len(leg.added) >= maxMatrixKeys {
			return fmt.Errorf("a leg exceeds %d added variables", maxMatrixKeys)
		}
		leg.added = append(leg.added, entry[i])
	}
	return nil
}

// legContext returns the check context one leg of a matrix job reports under. A name holding an
// expression is evaluated against the leg and GitHub appends nothing to it; any other name, the
// job id included, gets the leg's base values appended (legSuffix).
func legContext(name string, leg matrixLeg) (string, error) {
	if strings.Contains(name, expressionOpen) {
		return evaluateMatrixName(name, leg)
	}
	suffix, err := legSuffix(leg)
	if err != nil {
		return "", err
	}
	return name + " (" + suffix + ")", nil
}

// legSuffix joins the leg's base values with ", ", leaving empty ones out: GitHub reports the leg
// {os: windows-latest, features: ""} of job test as `test (windows-latest)`. A leg whose every
// value is empty has no suffix the file can show.
func legSuffix(leg matrixLeg) (string, error) {
	values := make([]string, 0, len(leg.base))
	for i := 0; i < len(leg.base) && i < maxMatrixKeys; i++ {
		cell := leg.base[i]
		if cell.value.unknown != "" {
			return "", fmt.Errorf("matrix variable %q %s, and GitHub appends it to the job name", cell.key, cell.value.unknown)
		}
		if cell.value.text != "" {
			values = append(values, cell.value.text)
		}
	}
	if len(values) == 0 {
		return "", errors.New("every matrix value the job name would carry is empty")
	}
	return strings.Join(values, ", "), nil
}

// evaluateMatrixName replaces each ${{ matrix.<variable> }} of name with the leg's value. Any
// other expression, a variable the leg lacks, or a value whose text is unknown is refused.
func evaluateMatrixName(name string, leg matrixLeg) (string, error) {
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
		value, err := matrixReference(body, leg)
		if err != nil {
			return "", err
		}
		evaluated.WriteString(value)
		rest = after
	}
	return "", fmt.Errorf("the name holds more than %d expressions", maxNameExpressions)
}

// matrixReference evaluates one expression body that is a bare matrix.<variable> reference.
func matrixReference(body string, leg matrixLeg) (string, error) {
	expression := strings.TrimSpace(body)
	if len(expression) <= len(matrixContextName) || !strings.EqualFold(expression[:len(matrixContextName)], matrixContextName) {
		return "", fmt.Errorf("expression %q is not a matrix variable, so only a workflow run knows its value", expression)
	}
	key := expression[len(matrixContextName):]
	if strings.ContainsFunc(key, func(r rune) bool { return !isMatrixKeyRune(r) }) {
		return "", fmt.Errorf("expression %q is not a bare matrix variable, so only a workflow run knows its value", expression)
	}
	value, held := leg.lookup(key)
	if !held {
		return "", fmt.Errorf("the leg has no matrix variable %q", key)
	}
	if value.unknown != "" {
		return "", fmt.Errorf("matrix variable %q %s", key, value.unknown)
	}
	return value.text, nil
}

// isMatrixKeyRune reports whether r may appear in a matrix variable name read by property
// dereference: letters, digits, '_' and '-' (matrix.python-version).
func isMatrixKeyRune(r rune) bool {
	return r == '_' || r == '-' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9')
}
