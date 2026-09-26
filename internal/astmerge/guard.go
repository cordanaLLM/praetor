package astmerge

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"path"
	"regexp"
	"sort"
	"strings"
)

// kindSemantic is the Conflict kind of a merged file the post-merge guard rejects.
const kindSemantic = "semantic"

// guardFile is the name every guarded source is type-checked under, so the positions
// go/types writes into the continuation lines of a multi-part message are stripped by one
// pattern and the four versions of a file report the same text for the same error.
const guardFile = "guard.go"

// maxGuardInitializers bounds the pairwise init-order comparison. A file with more
// initialized package-level variables than this is not verified, so it fails closed.
const maxGuardInitializers = 4096

// absentValue stands for a constant a version does not declare, and unknownValue for one
// whose value go/types cannot compute, such as a constant defined through an import or a
// sibling file of the package.
const (
	absentValue  = "(absent)"
	unknownValue = "(unknown)"
)

var guardPosition = regexp.MustCompile(regexp.QuoteMeta(guardFile) + `:\d+:\d+: `)

// semanticFacts is what the guard compares across base, ours, theirs and the merged file:
// the value and type of every package-level constant, the kind of every other
// package-level declaration and method, the order the package-level variable initializers
// run in, and the type errors. layout and places record where the declarations and the
// initializers are written (see layoutOf), which orders the initializers each side adds.
type semanticFacts struct {
	consts map[string]string
	decls  map[string]string
	init   []string
	errs   map[string]bool
	layout []layoutEntry
	places map[string]initPlace
}

// guardResult re-checks a clean merge before it is reported and turns it into a conflict
// when the merged file does not type-check where both sides did, or when a constant value
// or a variable initialization order departs from what the three inputs imply. The
// structural merge resolves each declaration on its own, so a combination that is valid
// per declaration can still change what the file means: a spec inherits the expression of
// the spec before it, iota counts positions, and variable initializers run in declaration
// order. The guard is the fail-closed backstop for every such case the structure misses.
func guardResult(res *MergeResult, baseSrc, oursSrc, theirsSrc string) *MergeResult {
	conflicts := guardConflicts(baseSrc, oursSrc, theirsSrc, res.MergedCode)
	if len(conflicts) == 0 {
		return res
	}
	return &MergeResult{
		MergedCode:    "",
		Conflicts:     conflicts,
		Clean:         false,
		ResolvedCount: res.ResolvedCount,
	}
}

// guardConflicts returns one conflict per way the merged file departs from the inputs.
func guardConflicts(baseSrc, oursSrc, theirsSrc, merged string) []Conflict {
	base, ours, theirs := collectFacts(baseSrc), collectFacts(oursSrc), collectFacts(theirsSrc)
	got := collectFacts(merged)
	conflicts := typeErrorConflicts(ours, theirs, got)
	conflicts = append(conflicts, constConflicts(base, ours, theirs, got)...)
	conflicts = append(conflicts, declConflicts(base, ours, theirs, got)...)
	return append(conflicts, initOrderConflicts(base, ours, theirs, got)...)
}

// collectFacts type-checks one source on its own. Every error is recorded rather than
// returned: a file of a larger package refers to declarations in its sibling files, so an
// input that fails to type-check is normal, and what matters is whether the merge adds an
// error neither side has.
func collectFacts(src string) semanticFacts {
	facts := semanticFacts{consts: make(map[string]string), decls: make(map[string]string), errs: make(map[string]bool)}
	if strings.TrimSpace(src) == "" {
		return facts
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, guardFile, src, parser.SkipObjectResolution)
	if err != nil {
		facts.errs["parse: "+normalizeTypeError(err)] = true
		return facts
	}
	info := &types.Info{}
	conf := types.Config{
		Importer: offlineImporter{},
		Error:    func(err error) { facts.errs[normalizeTypeError(err)] = true },
	}
	pkg, err := conf.Check(file.Name.Name, fset, []*ast.File{file}, info)
	if err != nil {
		facts.errs[normalizeTypeError(err)] = true
	}
	if pkg == nil {
		return facts
	}
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		facts.record(scope.Lookup(name))
	}
	facts.init = initializerKeys(info.InitOrder)
	facts.layout, facts.places = layoutOf(file, scope, info.InitOrder)
	return facts
}

// record files one package-level object: a constant under its value, anything else under
// its kind, and each method of a declared type under "T.M".
func (facts semanticFacts) record(obj types.Object) {
	switch o := obj.(type) {
	case *types.Const:
		facts.consts[o.Name()] = constFact(o)
	case *types.Var:
		facts.decls[o.Name()] = "var"
	case *types.Func:
		facts.decls[o.Name()] = "func"
	case *types.TypeName:
		facts.decls[o.Name()] = "type"
		named, ok := o.Type().(*types.Named)
		if !ok {
			return
		}
		for i := 0; i < named.NumMethods(); i++ {
			facts.decls[o.Name()+"."+named.Method(i).Name()] = "method"
		}
	}
}

// normalizeTypeError returns an error's message without the file positions go/types puts in
// front of each continuation line, so equal errors compare equal across versions.
func normalizeTypeError(err error) string {
	var typeErr types.Error
	msg := err.Error()
	if errors.As(err, &typeErr) {
		msg = typeErr.Msg
	}
	return guardPosition.ReplaceAllString(msg, "")
}

// constFact renders a constant's exact value and type, or unknownValue when go/types could
// not evaluate it.
func constFact(c *types.Const) string {
	if c.Val().Kind() == constant.Unknown {
		return unknownValue
	}
	return c.Val().ExactString() + ":" + c.Type().String()
}

// initializerKeys names each initializer in run order by the variables it assigns, or, for
// an initializer that assigns only blank identifiers, by its expression. A name that
// recurs cannot be matched across versions, so every occurrence of it is dropped.
func initializerKeys(order []*types.Initializer) []string {
	keys := make([]string, 0, len(order))
	count := make(map[string]int, len(order))
	for _, in := range order {
		key := initializerKey(in)
		keys = append(keys, key)
		count[key]++
	}
	unique := keys[:0]
	for _, key := range keys {
		if count[key] == 1 {
			unique = append(unique, key)
		}
	}
	return unique
}

func initializerKey(in *types.Initializer) string {
	names := make([]string, 0, len(in.Lhs))
	blank := true
	for _, v := range in.Lhs {
		names = append(names, v.Name())
		blank = blank && v.Name() == "_"
	}
	key := strings.Join(names, ",")
	if blank {
		key += " = " + types.ExprString(in.Rhs)
	}
	return key
}

// offlineImporter resolves every import path to an empty, complete package. The guard
// compares four versions of one file, so an import only has to behave the same in all of
// them, not resolve: loading real export data would run the go command without a deadline
// (HISS-02) and make the verdict depend on the host's toolchain and module cache (HISS-21).
// A reference into the package then reports the same "undefined" error in every version
// that makes it, and an import no version uses any more is still reported unused.
type offlineImporter struct{}

func (offlineImporter) Import(importPath string) (*types.Package, error) {
	pkg := types.NewPackage(importPath, importName(importPath))
	pkg.MarkComplete()
	return pkg, nil
}

// importName guesses the package name an import path declares: its last element, past a
// major-version suffix such as /v2, up to the first dot, with dashes made underscores.
func importName(importPath string) string {
	name := path.Base(importPath)
	if isMajorVersion(name) && path.Dir(importPath) != "." {
		name = path.Base(path.Dir(importPath))
	}
	if cut, _, found := strings.Cut(name, "."); found && cut != "" {
		name = cut
	}
	return strings.ReplaceAll(name, "-", "_")
}

func isMajorVersion(element string) bool {
	if len(element) < 2 || element[0] != 'v' {
		return false
	}
	return strings.Trim(element[1:], "0123456789") == ""
}

// typeErrorConflicts reports every type error of the merged file that neither side has.
// An error both inputs share, such as a reference to a sibling file, is not the merge's.
func typeErrorConflicts(ours, theirs, got semanticFacts) []Conflict {
	msgs := make([]string, 0, len(got.errs))
	for msg := range got.errs {
		if !ours.errs[msg] && !theirs.errs[msg] {
			msgs = append(msgs, msg)
		}
	}
	sort.Strings(msgs)
	conflicts := make([]Conflict, 0, len(msgs))
	for _, msg := range msgs {
		conflicts = append(conflicts, Conflict{
			Symbol: "typecheck",
			Kind:   kindSemantic,
			Reason: "the merged file has a type error neither side has: " + msg,
		})
	}
	return conflicts
}

// expect3 returns the state a 3-way merge must produce from a base, ours and theirs state:
// the state both sides agree on, or the one side's where the other kept base's. It reports
// false when both sides changed the state differently, which leaves nothing to hold the
// merge to.
func expect3(base, ours, theirs string) (string, bool) {
	switch {
	case ours == theirs:
		return ours, true
	case ours == base:
		return theirs, true
	case theirs == base:
		return ours, true
	}
	return "", false
}

// constConflicts reports every package-level constant whose merged value is not the 3-way
// expectation: the value both sides kept, or the value of the side that changed it.
func constConflicts(base, ours, theirs, got semanticFacts) []Conflict {
	var conflicts []Conflict
	for _, name := range constNames(base, ours, theirs, got) {
		b, o, t, m := constState(base, name), constState(ours, name), constState(theirs, name), constState(got, name)
		want, decided := expect3(b, o, t)
		if !decided || m == want {
			continue
		}
		conflicts = append(conflicts, Conflict{
			Symbol: "const:" + name,
			Kind:   kindSemantic,
			Reason: fmt.Sprintf("the merged value of const %s is %s, but the three-way expectation is %s", name, m, want),
			Ours:   o,
			Theirs: t,
			Base:   b,
		})
	}
	return conflicts
}

func constState(facts semanticFacts, name string) string {
	return stateOf(facts.consts, name)
}

func stateOf(states map[string]string, name string) string {
	if state, ok := states[name]; ok {
		return state
	}
	return absentValue
}

// constNames returns the sorted union of the constant names the versions declare.
func constNames(all ...semanticFacts) []string {
	maps := make([]map[string]string, 0, len(all))
	for _, facts := range all {
		maps = append(maps, facts.consts)
	}
	return unionKeys(maps...)
}

// unionKeys returns the sorted union of the maps' keys.
func unionKeys(all ...map[string]string) []string {
	seen := make(map[string]bool)
	var names []string
	for _, states := range all {
		for name := range states {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names
}

// declConflicts reports every package-level variable, function, type or method the merged
// file declares, or lacks, against the 3-way expectation: a declaration one side added
// must be there, one a side deleted while the other kept it must be gone. It is the
// backstop for a structural merge that drops or duplicates a declaration nothing refers to,
// which no type error would reveal.
func declConflicts(base, ours, theirs, got semanticFacts) []Conflict {
	var conflicts []Conflict
	for _, name := range unionKeys(base.decls, ours.decls, theirs.decls, got.decls) {
		b, o, t, m := stateOf(base.decls, name), stateOf(ours.decls, name), stateOf(theirs.decls, name), stateOf(got.decls, name)
		want, decided := expect3(b, o, t)
		if !decided || m == want {
			continue
		}
		conflicts = append(conflicts, Conflict{
			Symbol: "decl:" + name,
			Kind:   kindSemantic,
			Reason: fmt.Sprintf("the merged file has %s as %s, but the three-way expectation is %s", name, m, want),
			Ours:   o,
			Theirs: t,
			Base:   b,
		})
	}
	return conflicts
}

// initOrderConflicts reports the first pair of initializers the merged file runs in an
// order the inputs do not imply: both sides run them in one order, or one side kept
// base's order and the other changed it. A pair both sides order differently with no base
// order to decide by is ambiguous and fails closed too, and so is a pair of initializers
// each side added at the same place (see initPairs.crossAddedInOrder).
func initOrderConflicts(base, ours, theirs, got semanticFacts) []Conflict {
	if len(got.init) > maxGuardInitializers {
		return []Conflict{{
			Symbol: "init-order",
			Kind:   kindSemantic,
			Reason: fmt.Sprintf("the merged file has %d variable initializers, more than the %d the guard verifies", len(got.init), maxGuardInitializers),
		}}
	}
	pairs := initPairs{
		base: initRanks(got.init, base), ours: initRanks(got.init, ours), theirs: initRanks(got.init, theirs),
		oursSlot: initSlots(got.init, ours, base, theirs), theirsSlot: initSlots(got.init, theirs, base, ours),
	}
	for i := range got.init {
		for j := i + 1; j < len(got.init); j++ {
			if pairs.keptInOrder(i, j) {
				continue
			}
			return []Conflict{{
				Symbol: "init-order",
				Kind:   kindSemantic,
				Reason: fmt.Sprintf("the merged file initializes %s before %s, which neither the sides' agreement nor a one-sided change implies", got.init[i], got.init[j]),
			}}
		}
	}
	return nil
}

// initRanks maps each merged initializer, by merged index, to its rank in a version, or -1
// when the version has no such initializer.
func initRanks(merged []string, version semanticFacts) []int {
	rank := make(map[string]int, len(version.init))
	for i, key := range version.init {
		rank[key] = i
	}
	out := make([]int, len(merged))
	for i, key := range merged {
		out[i] = -1
		if r, ok := rank[key]; ok {
			out[i] = r
		}
	}
	return out
}

// initPairs holds, per merged initializer index, its rank in base, ours and theirs (see
// initRanks) and its slot in each side (see initSlots).
type initPairs struct {
	base, ours, theirs   []int
	oursSlot, theirsSlot []string
}

// keptInOrder reports whether running merged initializer i before j is what the inputs
// imply. A pair only one side holds keeps that side's order: the other side never saw one
// of the two, so it cannot have meant to reorder them. A pair neither side holds is
// constrained only when each side added one of the two (see crossAddedInOrder).
func (p initPairs) keptInOrder(i, j int) bool {
	oursHolds := p.ours[i] >= 0 && p.ours[j] >= 0
	theirsHolds := p.theirs[i] >= 0 && p.theirs[j] >= 0
	switch {
	case oursHolds && theirsHolds:
		return p.keptByBoth(i, j)
	case oursHolds:
		return p.ours[i] < p.ours[j]
	case theirsHolds:
		return p.theirs[i] < p.theirs[j]
	}
	return p.crossAddedInOrder(i, j)
}

// keptByBoth resolves a pair both sides hold 3-way: the order they agree on, or the order
// of the side that changed base's. Differing orders with no base order to decide by are
// ambiguous.
func (p initPairs) keptByBoth(i, j int) bool {
	oursFirst, theirsFirst := p.ours[i] < p.ours[j], p.theirs[i] < p.theirs[j]
	if oursFirst == theirsFirst {
		return oursFirst
	}
	if p.base[i] < 0 || p.base[j] < 0 {
		return false
	}
	baseFirst := p.base[i] < p.base[j]
	if oursFirst == baseFirst {
		return theirsFirst
	}
	return oursFirst
}

// crossAddedInOrder fails a pair of initializers one side added each when both sides
// added theirs at the same slot (see anchors.slot), such as a variable each side appends
// at the end of the file: no input implies an order for the two, and the merge used to run
// ours first, which no side wrote. The pair fails closed as it does inside a parenthesized
// var block. Initializers added at different slots stand where their sides put them.
func (p initPairs) crossAddedInOrder(i, j int) bool {
	switch {
	case p.addedBy(p.ours, p.theirs, i) && p.addedBy(p.theirs, p.ours, j):
		return p.oursSlot[i] != p.theirsSlot[j]
	case p.addedBy(p.theirs, p.ours, i) && p.addedBy(p.ours, p.theirs, j):
		return p.theirsSlot[i] != p.oursSlot[j]
	}
	return true
}

// addedBy reports whether merged initializer k is one side's addition: held by that side
// alone, not by base or the other side.
func (p initPairs) addedBy(side, other []int, k int) bool {
	return side[k] >= 0 && other[k] < 0 && p.base[k] < 0
}
