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
// the value and type of every package-level constant, the order the package-level variable
// initializers run in, and the type errors.
type semanticFacts struct {
	consts map[string]string
	init   []string
	errs   map[string]bool
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
	return append(conflicts, initOrderConflicts(base, ours, theirs, got)...)
}

// collectFacts type-checks one source on its own. Every error is recorded rather than
// returned: a file of a larger package refers to declarations in its sibling files, so an
// input that fails to type-check is normal, and what matters is whether the merge adds an
// error neither side has.
func collectFacts(src string) semanticFacts {
	facts := semanticFacts{consts: make(map[string]string), errs: make(map[string]bool)}
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
		if c, ok := scope.Lookup(name).(*types.Const); ok {
			facts.consts[name] = constFact(c)
		}
	}
	facts.init = initializerKeys(info.InitOrder)
	return facts
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
	if value, ok := facts.consts[name]; ok {
		return value
	}
	return absentValue
}

// constNames returns the sorted union of the constant names the versions declare.
func constNames(all ...semanticFacts) []string {
	seen := make(map[string]bool)
	var names []string
	for _, facts := range all {
		for name := range facts.consts {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names
}

// initOrderConflicts reports the first pair of initializers the merged file runs in an
// order the inputs do not imply: both sides run them in one order, or one side kept
// base's order and the other changed it. A pair both sides order differently with no base
// order to decide by is ambiguous and fails closed too.
func initOrderConflicts(base, ours, theirs, got semanticFacts) []Conflict {
	if len(got.init) > maxGuardInitializers {
		return []Conflict{{
			Symbol: "init-order",
			Kind:   kindSemantic,
			Reason: fmt.Sprintf("the merged file has %d variable initializers, more than the %d the guard verifies", len(got.init), maxGuardInitializers),
		}}
	}
	b, o, t := initRanks(got.init, base), initRanks(got.init, ours), initRanks(got.init, theirs)
	for i := range got.init {
		for j := i + 1; j < len(got.init); j++ {
			if pairKeptInOrder(i, j, b, o, t) {
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

// pairKeptInOrder reports whether running merged initializer i before j is what the inputs
// imply. A pair only one side holds keeps that side's order: the other side never saw one
// of the two, so it cannot have meant to reorder them. A pair neither side holds is
// unconstrained.
func pairKeptInOrder(i, j int, base, ours, theirs []int) bool {
	oursHolds := ours[i] >= 0 && ours[j] >= 0
	theirsHolds := theirs[i] >= 0 && theirs[j] >= 0
	switch {
	case oursHolds && theirsHolds:
		return pairKeptByBoth(i, j, base, ours, theirs)
	case oursHolds:
		return ours[i] < ours[j]
	case theirsHolds:
		return theirs[i] < theirs[j]
	}
	return true
}

// pairKeptByBoth resolves a pair both sides hold 3-way: the order they agree on, or the
// order of the side that changed base's. Differing orders with no base order to decide by
// are ambiguous.
func pairKeptByBoth(i, j int, base, ours, theirs []int) bool {
	oursFirst, theirsFirst := ours[i] < ours[j], theirs[i] < theirs[j]
	if oursFirst == theirsFirst {
		return oursFirst
	}
	if base[i] < 0 || base[j] < 0 {
		return false
	}
	baseFirst := base[i] < base[j]
	if oursFirst == baseFirst {
		return theirsFirst
	}
	return oursFirst
}
