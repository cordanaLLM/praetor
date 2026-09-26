package hiss

import (
	"regexp"
	"strings"
)

// Direct recursion in the line-scanned languages (HISS-01).
//
// Go decides recursion from its AST (go_ast.go checkSelfRecursion) and its package call graph
// (go_callgraph.go). Rust and Python have no parser here, so the line scanners decide the one
// shape a single function's text can show: its body calling it by a name that actually
// resolves to it. Matching the name anywhere would report every delegation, so each
// language's resolution rule is followed instead:
//
//   - A Python method is reached through its receiver or its class (self.f, cls.f, Owner.f);
//     a bare f() inside a method resolves to the module-level f. A plain function is reached
//     by its bare name.
//   - A Rust method in an inherent impl or a trait body is reached through self.f or Self::f,
//     and an associated function there through Self::f; a bare f() there resolves to a free
//     function. A free function is reached by its bare name, never through a path, because
//     other::f is a different item.
//   - Inside `impl Trait for T`, self.f and Self::f resolve to an inherent T::f first, which
//     may sit in any file of the crate, and Self::f picks among T's impls by argument type
//     (Self::from(b) in From<A> reaches From<B>). One function's text cannot tell forwarding
//     from recursion there, so those functions are not decided.
//   - A local binding of the same name (a parameter, let, assignment, loop target, import or
//     nested definition) shadows the function, so a bare call then reaches the local. A
//     Python binding makes the name local for the whole body of the function that owns the
//     line, and no other. Rust scopes are lexical (rustscope.go): a let shadows from the end
//     of its statement to the end of its block, a for, if-let, while-let, match-arm or closure
//     pattern only inside its construct, while an item (fn, use, const, static) covers the
//     whole body.
//   - A Rust macro other than the standard expression macros may rewrite its input into a
//     call of something else (syscall!(recv(..)) calls libc::recv), so a call written there
//     is not decided.
//
// Mutual and indirect recursion need a call graph across functions and are not decided here;
// .config/hiss/coverage.yaml holds them as gap fixtures.

const (
	// maxSelfCallSites bounds the recursive call sites held for one open function (HISS-02).
	maxSelfCallSites = 32
	// maxSignatureBytes bounds the signature text gathered across wrapped header lines.
	maxSignatureBytes = 4096
	// maxRustImplNesting bounds the stack of open impl and trait bodies (HISS-02).
	maxRustImplNesting = 16
	// maxRelayedCalls bounds the calls one Python scope holds for its enclosing functions
	// (HISS-02).
	maxRelayedCalls = 2 * maxSelfCallSites
)

// pythonFunc.outerBinds is a bit set over stack indices; this fails to compile if the Python
// nesting bound ever outgrows it.
const _ = uint64(1) << (maxPythonNesting - 1)

// rustImplHeader matches a line that opens an impl or trait body, whose functions resolve a
// bare call to a free function rather than to themselves. Attributes may precede it on the
// same line (`#[derive(Clone)] impl Trait for T {`).
var rustImplHeader = regexp.MustCompile(`^(?:#\[[^\]]*\]\s*)*(?:pub(?:\s*\([^)]*\))?\s+)?(?:unsafe\s+)?(?:impl\b|trait\s)`)

// rustBodyKind is the kind of body a Rust function is declared in, which decides the
// spellings that reach it from its own body.
type rustBodyKind int

const (
	// rustFreeBody is outside any impl or trait body: the bare name reaches the function.
	rustFreeBody rustBodyKind = iota
	// rustInherentBody is `impl T`: self.f and Self::f reach the inherent method itself.
	rustInherentBody
	// rustTraitBody is `trait T`: a default method is reached through self.f and Self::f.
	rustTraitBody
	// rustTraitImplBody is `impl Trait for T`: self.f and Self::f may reach an inherent T::f
	// or another impl of the trait, so no spelling is decided.
	rustTraitImplBody
)

// selfCalls collects the recursive call sites of one open function and decides them when the
// function closes, once every local binding in its body has been seen: a later binding in
// Python makes the name local for the whole body.
type selfCalls struct {
	name string
	// callees are the spellings that reach this function from its own body.
	callees []string
	// excluded reports a byte that, preceding a callee, means the call reaches something else.
	excluded func(byte) bool
	// binds reports whether a line binds name as a local for the whole body.
	binds func(code, name string) bool
	// shadowable is true when the callee is the bare name, which a local binding can capture.
	shadowable bool
	bound      bool
	sites      []int
}

// observeBinding records a local binding of the name on this line.
func (c *selfCalls) observeBinding(code string) {
	if c.shadowable && !c.bound && c.binds != nil && c.binds(code, c.name) {
		c.bound = true
	}
}

// observeCall records a call on one of the function's own lines that reaches it.
func (c *selfCalls) observeCall(code string, line int) {
	if c.reaches(code) {
		c.addSite(line)
	}
}

// reaches reports whether code calls the function by a spelling that reaches it.
func (c *selfCalls) reaches(code string) bool {
	for i := 0; i < len(c.callees); i++ {
		if hasCall(code, c.callees[i], c.excluded) {
			return true
		}
	}
	return false
}

// callsAt reports whether code calls the function at byte at by a spelling that reaches it.
func (c *selfCalls) callsAt(code string, at int) bool {
	for i := 0; i < len(c.callees); i++ {
		if callAt(code, at, c.callees[i], c.excluded) {
			return true
		}
	}
	return false
}

func (c *selfCalls) addSite(line int) {
	n := len(c.sites)
	if n < maxSelfCallSites && (n == 0 || c.sites[n-1] != line) {
		c.sites = append(c.sites, line)
	}
}

// report records one HISS-01 finding per recursive call site, unless a local binding of the
// name means the bare calls never reached the function.
func (c *selfCalls) report(rep *ScanReport, rel string) {
	if c.shadowable && c.bound {
		return
	}
	msg := "Direct recursion in " + c.name + "; the call graph must form an acyclic DAG"
	for i := 0; i < len(c.sites); i++ {
		recordViolation(rep, "HISS-01", rel, c.sites[i], c.name, msg)
	}
}

// appendSignature joins one more header line onto sig within maxSignatureBytes.
func appendSignature(sig, code string) string {
	joined := sig + " " + code
	if len(joined) > maxSignatureBytes {
		return joined[:maxSignatureBytes]
	}
	return joined
}

// leadingIdent returns the identifier text begins with, after leading blanks.
func leadingIdent(text string) string {
	text = strings.TrimSpace(text)
	end := 0
	for end < len(text) && isIdentByte(text[end]) {
		end++
	}
	return text[:end]
}

// ---------------------------------------------------------------------------
// Rust
// ---------------------------------------------------------------------------

// rustLine is what the Rust scanner knows about one line once the brace tracker has seen it.
type rustLine struct {
	code    string
	num     int
	header  bool
	name    string
	body    rustBodyKind
	entered bool // the function body opened on this line
	open    bool // the body is still open after this line
	pending bool // a header is waiting for its opening brace
}

// rustSelfCalls follows the one function the brace tracker has open.
type rustSelfCalls struct {
	rel    string
	rep    *ScanReport
	active bool
	inSig  bool
	body   rustBodyKind
	sig    string
	calls  selfCalls
	walk   rustWalk
}

func (r *rustSelfCalls) observe(l rustLine) {
	if l.header {
		*r = rustSelfCalls{rel: r.rel, rep: r.rep, active: true, inSig: true, body: l.body, calls: selfCalls{name: l.name}}
	}
	if !r.active {
		return
	}
	body := l.code
	switch {
	case l.entered:
		brace := strings.IndexByte(l.code, '{')
		if brace < 0 {
			brace = len(l.code) - 1
		}
		r.sig = appendSignature(r.sig, l.code[:brace])
		r.startBody()
		body = l.code[brace+1:]
	case r.inSig && l.pending:
		r.sig = appendSignature(r.sig, l.code)
		return
	case r.inSig:
		r.active = false // the header declared a function without a body
		return
	}
	r.observeBody(body, l.num)
	if !l.open {
		r.calls.report(r.rep, r.rel)
		r.active = false
	}
}

// startBody decides, from the finished signature, which spellings reach the function. A
// function in a trait impl gets none, so its calls are observed and never reported.
func (r *rustSelfCalls) startBody() {
	r.inSig = false
	params := rustParams(r.sig)
	name := r.calls.name
	r.calls.excluded = isPathByte
	switch {
	case r.body == rustTraitImplBody:
		r.calls.callees = nil
	case rustHasReceiver(params):
		r.calls.callees = []string{"self." + name, "Self::" + name}
	case r.body != rustFreeBody:
		r.calls.callees = []string{"Self::" + name}
	default:
		r.calls.callees = []string{name}
		r.calls.shadowable = true
		r.calls.binds = rustItemBinds
		r.calls.bound = containsIdent(params, name)
	}
}

// rustParams returns the parameter list of the signature: the first parenthesis after the
// function name and its generic parameters, up to its matching close.
func rustParams(sig string) string {
	fn := nextIdent(sig, "fn", 0)
	if fn < 0 {
		return ""
	}
	angle := 0
	for i := fn; i < len(sig); i++ {
		angle = rustAngleDepth(sig, i, angle)
		if sig[i] == '(' && angle == 0 {
			return enclosedParens(sig, i)
		}
	}
	return ""
}

// enclosedParens returns the text inside the parenthesis opening at open, or the rest of text
// when it never closes.
func enclosedParens(text string, open int) string {
	depth := 0
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return text[open+1 : i]
			}
		}
	}
	return text[open+1:]
}

// rustHasReceiver reports whether the first parameter is a self receiver in any of its forms:
// self, mut self, &self, &mut self, &'a self, self: Box<Self>.
func rustHasReceiver(params string) bool {
	first := params
	if comma := strings.IndexByte(first, ','); comma >= 0 {
		first = first[:comma]
	}
	first = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(first), "&"))
	if strings.HasPrefix(first, "'") {
		first = strings.TrimLeft(first, "'")
		first = strings.TrimSpace(first[len(leadingIdent(first)):])
	}
	first = strings.TrimSpace(strings.TrimPrefix(first, "mut "))
	return leadingIdent(first) == "self"
}

// rustItemBinds reports whether code declares an item of the name (a nested fn, a const or
// static, a use). An item is visible throughout its block, so it captures a bare call written
// before it as well as after.
func rustItemBinds(code, name string) bool {
	at := nextIdent(code, name, 0)
	for i := 0; i < len(code) && at >= 0; i++ {
		if rustDeclaresAt(code, at) {
			return true
		}
		at = nextIdent(code, name, at+1)
	}
	return false
}

// rustDeclaresAt reports an item or import declaring the name at at: a nested fn, a const or
// static, or a use.
func rustDeclaresAt(code string, at int) bool {
	before := code[:at]
	trimmed := strings.TrimSpace(code)
	return strings.HasSuffix(before, "fn ") || strings.HasSuffix(before, "const ") ||
		strings.HasSuffix(before, "static ") ||
		strings.HasPrefix(trimmed, "use ") || strings.HasPrefix(trimmed, "pub use ")
}

// rustCallText returns the part of a line that can hold a call. A match arm's pattern, such
// as `Variant(x) =>` or a macro arm like select!'s `recv(r) -> v =>`, names a path and never
// calls it, so the text up to the arm's guard or arrow is dropped. A line that opens the match
// or binds (`match f(n) {`, `let r = match f(n) { .. => .. }`) keeps all of its text.
func rustCallText(code string) string {
	arrow := strings.Index(code, "=>")
	if arrow < 0 {
		return code
	}
	pattern := code[:arrow]
	if nextIdent(pattern, "match", 0) >= 0 || nextIdent(pattern, "let", 0) >= 0 ||
		strings.ContainsAny(pattern, ";{") || strings.Contains(pattern, " = ") {
		return code
	}
	if guard := nextIdent(pattern, "if", 0); guard >= 0 {
		return code[guard:]
	}
	return code[arrow:]
}

// rustBlockScope tracks the brace depth of open impl and trait bodies across a file, and the
// kind of each.
type rustBlockScope struct {
	depth  int
	frames []rustBodyFrame
	// header gathers an impl or trait header until its opening brace, which rustfmt moves to a
	// later line when the header wraps (`impl<T> Trait\n    for Type<T>\nwhere ...\n{`).
	header  string
	pending bool
	nesting rustHeaderNesting
}

// rustHeaderNesting follows the brackets inside a pending impl or trait header. Within them a
// brace is a const generic argument (`W<{ N + 1 }>`) and a semicolon an array length
// (`[u8; N]`), not the body's opening brace or the end of the item.
type rustHeaderNesting struct {
	paren, angle, curly int
}

// consumes updates the depths for byte i and reports whether the byte belongs to a bracket of
// the header, so it neither opens the body nor ends the item.
func (n *rustHeaderNesting) consumes(code string, i int) bool {
	c := code[i]
	if n.curly > 0 {
		n.curly += bracketStep(c, "{", "}")
		return true
	}
	nested := n.paren > 0 || n.angle > 0
	switch {
	case c == '{' && nested:
		n.curly = 1
		return true
	case c == ';':
		return nested
	}
	n.paren = max(n.paren+bracketStep(c, "([", ")]"), 0)
	n.angle = rustAngleDepth(code, i, n.angle)
	return false
}

// bracketStep returns 1 when c is one of the opening bytes, -1 when one of the closing bytes,
// and 0 otherwise.
func bracketStep(c byte, opening, closing string) int {
	switch {
	case strings.IndexByte(opening, c) >= 0:
		return 1
	case strings.IndexByte(closing, c) >= 0:
		return -1
	}
	return 0
}

// rustBodyFrame is one open impl or trait body: the brace depth it opened at and its kind.
type rustBodyFrame struct {
	depth int
	kind  rustBodyKind
}

// kind returns the kind of the innermost open body.
func (s *rustBlockScope) kind() rustBodyKind {
	if n := len(s.frames); n > 0 {
		return s.frames[n-1].kind
	}
	return rustFreeBody
}

func (s *rustBlockScope) observe(code string) {
	if !s.pending && rustImplHeader.MatchString(strings.TrimSpace(code)) {
		s.pending, s.header, s.nesting = true, "", rustHeaderNesting{}
	}
	for i := 0; i < len(code); i++ {
		if !s.pending || !s.nesting.consumes(code, i) {
			s.bodyByte(code, i)
		}
	}
	if s.pending {
		s.header = appendSignature(s.header, code)
	}
}

// bodyByte follows the braces outside a header's brackets. A semicolon there ends a header
// that never opens a body (`trait Alias = Foo + Bar;`), so the next impl is read on its own.
func (s *rustBlockScope) bodyByte(code string, i int) {
	switch code[i] {
	case '{':
		s.depth++
		if s.pending {
			s.open(appendSignature(s.header, code[:i]))
		}
	case '}':
		s.closeBrace()
	case ';':
		s.pending, s.header = false, ""
	}
}

// closeBrace pops the innermost body when this brace closes it.
func (s *rustBlockScope) closeBrace() {
	if n := len(s.frames); n > 0 && s.frames[n-1].depth == s.depth {
		s.frames = s.frames[:n-1]
	}
	if s.depth > 0 {
		s.depth--
	}
}

// open pushes the body whose header just reached its opening brace.
func (s *rustBlockScope) open(header string) {
	s.pending = false
	s.header = ""
	if len(s.frames) < maxRustImplNesting {
		s.frames = append(s.frames, rustBodyFrame{depth: s.depth, kind: rustHeaderKind(header)})
	}
}

// rustHeaderKind classifies an impl or trait header: a trait, an inherent impl, or an impl of a
// trait for a type.
func rustHeaderKind(header string) rustBodyKind {
	trimmed := strings.TrimSpace(header)
	loc := rustImplHeader.FindStringIndex(trimmed)
	if loc == nil {
		return rustFreeBody
	}
	if !strings.HasSuffix(trimmed[:loc[1]], "impl") {
		return rustTraitBody
	}
	if rustNamesTrait(trimmed[loc[1]:]) {
		return rustTraitImplBody
	}
	return rustInherentBody
}

// rustNamesTrait reports whether the text after `impl` implements a trait: a `for` keyword
// outside every angle bracket and before any where clause. A higher-ranked `for<'a>` bound is
// not that keyword, and neither is a `for` inside the generic parameter list.
func rustNamesTrait(rest string) bool {
	angle := 0
	for i := 0; i < len(rest); i++ {
		angle = rustAngleDepth(rest, i, angle)
		if angle > 0 || !isIdentByte(rest[i]) || (i > 0 && isIdentByte(rest[i-1])) {
			continue
		}
		word := leadingIdent(rest[i:])
		if word == "where" {
			return false
		}
		if word == "for" && !strings.HasPrefix(strings.TrimLeft(rest[i+len(word):], " \t"), "<") {
			return true
		}
		i += len(word) - 1
	}
	return false
}

// rustAngleDepth returns the angle-bracket depth after the byte at i, where the arrow of a
// return type such as Fn() -> T closes nothing.
func rustAngleDepth(text string, i, depth int) int {
	switch {
	case text[i] == '<':
		return depth + 1
	case text[i] == '>' && depth > 0 && (i == 0 || text[i-1] != '-'):
		return depth - 1
	}
	return depth
}

// ---------------------------------------------------------------------------
// Python
// ---------------------------------------------------------------------------

// pythonBrackets scans one stripped line starting at the carried bracket depth. It returns
// the index of the first ':' at depth zero, which ends a def header, and the depth carried to
// the next line.
func pythonBrackets(code string, depth int) (int, int) {
	colon := -1
	for i := 0; i < len(code); i++ {
		switch code[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		case ':':
			if depth == 0 && colon < 0 {
				colon = i
			}
		}
	}
	return colon, depth
}

// relayedCall is a call written in a nested scope to the open function at stack index target.
type relayedCall struct {
	target int
	line   int
}

// bindName records the name a def or class statement binds in the scope it sits directly in.
// A class body binds in the class namespace, which no function's name resolution reads.
func (s *pythonScanner) bindName(name string) {
	j := len(s.open) - 1
	if j < 0 || s.open[j].class {
		return
	}
	for i := 0; i <= j; i++ {
		if s.open[i].name == name {
			s.markBound(j, i)
		}
	}
}

// markBound records that the scope at stack index j binds the name of the function at i: its
// own name makes its bare self-calls reach the local, and an enclosing function's name makes
// the calls from j to that name reach the local instead.
func (s *pythonScanner) markBound(j, i int) {
	switch {
	case i == j:
		s.open[j].calls.bound = true
	case s.open[i].calls.shadowable:
		s.open[j].outerBinds |= 1 << uint(i)
	}
}

// observeScopedBindings records what the line binds, in the function that owns it: a binding
// in a nested def is that def's local, so it never shadows the enclosing function.
func (s *pythonScanner) observeScopedBindings(body string) {
	j := len(s.open) - 1
	if j < 0 || s.open[j].class {
		return
	}
	s.open[j].calls.observeBinding(body)
	for i := 0; i < j; i++ {
		if s.open[i].calls.shadowable && pythonBinds(body, s.open[i].name) {
			s.open[j].outerBinds |= 1 << uint(i)
		}
	}
}

// observeScopedCalls records a call to each open function. A call on the function's own line
// is its call site; a call from a nested scope is held there until that scope closes, because
// a binding later in the nested scope makes the name its local for the whole of it.
func (s *pythonScanner) observeScopedCalls(body string, lineNum int) {
	j := len(s.open) - 1
	for i := 0; i <= j; i++ {
		switch {
		case s.open[i].class:
		case i == j:
			s.open[i].calls.observeCall(body, lineNum)
		case s.open[i].calls.reaches(body):
			s.open[j].relayed = appendRelayed(s.open[j].relayed, relayedCall{target: i, line: lineNum})
		}
	}
}

// passOutward hands the calls the closing scope at stack index j held to the scope around it,
// dropping those whose name j binds: the target's call site when that is the next scope out,
// otherwise that scope's own relay. A class body binds nothing a nested function reads, so its
// relayed calls pass through unchanged.
func (s *pythonScanner) passOutward(j int) {
	if j < 1 {
		return
	}
	from, to := &s.open[j], &s.open[j-1]
	for k := 0; k < len(from.relayed); k++ {
		r := from.relayed[k]
		switch {
		case from.outerBinds&(1<<uint(r.target)) != 0:
		case r.target == j-1:
			to.calls.addSite(r.line)
		default:
			to.relayed = appendRelayed(to.relayed, r)
		}
	}
}

// appendRelayed appends one relayed call within maxRelayedCalls.
func appendRelayed(relayed []relayedCall, r relayedCall) []relayedCall {
	if len(relayed) >= maxRelayedCalls {
		return relayed
	}
	return append(relayed, r)
}

// pythonCallees decides which spellings reach a function from its own body, given the class
// it is defined directly in (empty for a plain function) and its first parameter.
func pythonCallees(c *selfCalls, owner, receiver string) {
	c.excluded = isSelectorByte
	switch {
	case owner == "":
		c.callees = []string{c.name}
		c.shadowable = true
		c.binds = pythonBinds
	case receiver == "self" || receiver == "cls":
		c.callees = []string{receiver + "." + c.name, owner + "." + c.name}
	default:
		c.callees = []string{owner + "." + c.name}
	}
}

// pythonBinds reports whether code binds name as a local of the function it sits in.
func pythonBinds(code, name string) bool {
	trimmed := strings.TrimSpace(code)
	if !containsIdent(trimmed, name) {
		return false
	}
	if pythonStatementBinds(trimmed, name) {
		return true
	}
	target := pythonAssignTarget(trimmed)
	at := nextIdent(trimmed, name, 0)
	for i := 0; i < len(trimmed) && at >= 0; i++ {
		if pythonBindsAt(trimmed, target, at, len(name)) {
			return true
		}
		at = nextIdent(trimmed, name, at+1)
	}
	return false
}

// pythonStatementBinds covers the statements whose whole target list binds: for loops,
// imports and nested classes.
func pythonStatementBinds(trimmed, name string) bool {
	for _, prefix := range []string{"for ", "async for "} {
		if in := strings.Index(trimmed, " in "); strings.HasPrefix(trimmed, prefix) && in > 0 {
			return containsIdent(trimmed[:in], name)
		}
	}
	if imp := strings.Index(trimmed, "import "); imp >= 0 &&
		(strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "from ")) {
		return containsIdent(trimmed[imp+len("import "):], name)
	}
	return strings.HasPrefix(trimmed, "class ") && leadingIdent(strings.TrimPrefix(trimmed, "class ")) == name
}

// pythonBindsAt reports whether the occurrence at at is bound: an `as` target, a walrus
// target, or a bare name on the left of an assignment (not an attribute or subscript).
func pythonBindsAt(trimmed, target string, at, n int) bool {
	if strings.HasSuffix(trimmed[:at], " as ") {
		return true
	}
	rest := strings.TrimLeft(trimmed[at+n:], " \t")
	if strings.HasPrefix(rest, ":=") {
		return true
	}
	if at >= len(target) || (at > 0 && trimmed[at-1] == '.') {
		return false
	}
	return rest == "" || !strings.ContainsAny(rest[:1], ".[(")
}

// pythonAssignTarget returns the text left of a top-level assignment, or "" when the line
// assigns nothing. Comparisons (==, !=, <=, >=) and keyword arguments are not assignments.
func pythonAssignTarget(trimmed string) string {
	depth := 0
	for i := 0; i < len(trimmed); i++ {
		switch c := trimmed[i]; {
		case strings.IndexByte("([{", c) >= 0:
			depth++
		case strings.IndexByte(")]}", c) >= 0 && depth > 0:
			depth--
		case depth == 0 && pythonAssignsAt(trimmed, i):
			return trimmed[:i]
		}
	}
	return ""
}

// pythonAssignsAt reports whether byte i is an assigning =: neither half of == nor the end of
// !=, <= or >=.
func pythonAssignsAt(trimmed string, i int) bool {
	if trimmed[i] != '=' {
		return false
	}
	next := i+1 < len(trimmed) && trimmed[i+1] == '='
	prev := i > 0 && strings.IndexByte("=!<>", trimmed[i-1]) >= 0
	return !next && !prev
}
