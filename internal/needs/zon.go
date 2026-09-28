package needs

import (
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// build.zig.zon is the package manifest of a Zig build (Zig 0.16.0, doc/build.zig.zon.md): one
// struct literal in Zig Object Notation whose optional .dependencies struct names each package
// the build fetches, by `url` and `hash`, or finds in the tree, by `path`. The two are mutually
// exclusive. The parser below reads that subset of ZON: struct and tuple literals, strings (with
// escapes and multiline `\\` lines), `@"quoted"` names, enum literals, numbers, characters and
// `//` comments. It reads only .dependencies and validates nothing else in the manifest.

// maxZonDepth bounds how deeply struct and tuple literals nest in one build.zig.zon (HISS-02).
// A manifest nests three levels to reach a dependency's fields.
const maxZonDepth = 64

// zonDependency is one entry of the .dependencies struct.
type zonDependency struct {
	url, path       string
	hasURL, hasPath bool
}

// thirdParty reports whether the dependency is a package the repository does not author: one
// the build fetches by url, or one vendored into a directory the discovery walk prunes as not the
// repository's own (notRepositorySource: vendor/, third_party/, zig-pkg/ and the like). Any other
// path dependency is a package of the repository, which discovery scans as a sub-project of its
// own when it holds a build.zig, like a Cargo path crate (cargoManifest.thirdPartyCrates).
func (d zonDependency) thirdParty() bool {
	if d.hasURL {
		return true
	}
	segments := strings.Split(path.Clean(strings.ReplaceAll(d.path, `\`, "/")), "/")
	return slices.ContainsFunc(segments, notRepositorySource)
}

type zonTokenKind uint8

const (
	zonEOF zonTokenKind = iota
	zonDot
	zonLBrace
	zonRBrace
	zonEqual
	zonComma
	zonIdent
	zonString
	zonScalar
)

// zonTokenNames names each token kind in a refusal.
var zonTokenNames = [...]string{"end of file", "'.'", "'{'", "'}'", "'='", "','", "a name", "a string", "a value"}

// zonPunctuation maps each single-byte punctuation token to its kind.
var zonPunctuation = map[byte]zonTokenKind{'.': zonDot, '{': zonLBrace, '}': zonRBrace, '=': zonEqual, ',': zonComma}

type zonToken struct {
	kind zonTokenKind
	// text is the decoded name or string, or the raw spelling of a scalar.
	text string
	line int
}

// zonLexer splits a build.zig.zon into tokens.
type zonLexer struct {
	src    string
	pos    int
	line   int
	tokens []zonToken
}

// lexZon tokenizes src, ending with an end-of-file token on the last line. Every step consumes
// at least one byte, so len(src) steps bound the loop.
func lexZon(src string) ([]zonToken, error) {
	lx := &zonLexer{src: src, line: 1}
	for steps := 0; lx.pos < len(lx.src); steps++ {
		if steps > len(src) {
			return nil, errors.New("tokenizer made no progress")
		}
		if err := lx.next(); err != nil {
			return nil, err
		}
	}
	lx.emit(zonEOF, "")
	return lx.tokens, nil
}

func (lx *zonLexer) errorf(format string, args ...any) error {
	return fmt.Errorf("line %d: %s", lx.line, fmt.Sprintf(format, args...))
}

func (lx *zonLexer) emit(kind zonTokenKind, text string) {
	lx.tokens = append(lx.tokens, zonToken{kind: kind, text: text, line: lx.line})
}

// next consumes one token, or one run of white space or one comment.
func (lx *zonLexer) next() error {
	if lx.skipTrivia() {
		return nil
	}
	c, rest := lx.src[lx.pos], lx.src[lx.pos:]
	if kind, ok := zonPunctuation[c]; ok {
		lx.emit(kind, string(c))
		lx.pos++
		return nil
	}
	switch {
	case c == '"':
		return lx.quoted(zonString)
	case strings.HasPrefix(rest, `@"`):
		lx.pos++
		return lx.quoted(zonIdent)
	case strings.HasPrefix(rest, `\\`):
		lx.multiline()
		return nil
	case c == '\'':
		return lx.character()
	case isZonWordByte(c) || c == '-':
		lx.word()
		return nil
	}
	return lx.errorf("unexpected character %q", c)
}

// skipTrivia consumes one white-space byte or one `//` comment and reports whether it did.
func (lx *zonLexer) skipTrivia() bool {
	switch c := lx.src[lx.pos]; {
	case c == '\n':
		lx.line++
		lx.pos++
	case c == ' ' || c == '\t' || c == '\r':
		lx.pos++
	case strings.HasPrefix(lx.src[lx.pos:], "//"):
		lx.pos += lineLength(lx.src[lx.pos:])
	default:
		return false
	}
	return true
}

// lineLength is the length of s up to, not including, its first line break.
func lineLength(s string) int {
	if end := strings.IndexByte(s, '\n'); end >= 0 {
		return end
	}
	return len(s)
}

func isZonWordByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// word consumes an identifier or a number. A number also takes a fraction point followed by a
// digit and an exponent sign (1.5e-3, 0x1.8p+3).
func (lx *zonLexer) word() {
	start, first := lx.pos, lx.src[lx.pos]
	lx.pos++
	numeric := first == '-' || first >= '0' && first <= '9'
	for lx.pos < len(lx.src) && lx.continuesWord(numeric) {
		lx.pos++
	}
	text := lx.src[start:lx.pos]
	if numeric {
		lx.emit(zonScalar, text)
		return
	}
	lx.emit(zonIdent, text)
}

// continuesWord reports whether the byte at lx.pos extends the word being consumed.
func (lx *zonLexer) continuesWord(numeric bool) bool {
	c := lx.src[lx.pos]
	if isZonWordByte(c) {
		return true
	}
	if !numeric {
		return false
	}
	prev := lx.src[lx.pos-1]
	if c == '.' {
		return lx.pos+1 < len(lx.src) && isZonWordByte(lx.src[lx.pos+1])
	}
	return (c == '+' || c == '-') && strings.IndexByte("eEpP", prev) >= 0
}

// quoted consumes a double-quoted string at lx.pos and emits it, decoded, as kind.
func (lx *zonLexer) quoted(kind zonTokenKind) error {
	lx.pos++
	var decoded strings.Builder
	for lx.pos < len(lx.src) {
		c := lx.src[lx.pos]
		switch c {
		case '"':
			lx.pos++
			lx.emit(kind, decoded.String())
			return nil
		case '\n':
			return lx.errorf("unterminated string")
		case '\\':
			if err := lx.escape(&decoded); err != nil {
				return err
			}
		default:
			decoded.WriteByte(c)
			lx.pos++
		}
	}
	return lx.errorf("unterminated string")
}

// zonSimpleEscapes maps the one-letter escapes of a Zig string to the byte each stands for.
var zonSimpleEscapes = map[byte]byte{'n': '\n', 'r': '\r', 't': '\t', '\\': '\\', '\'': '\'', '"': '"'}

// escape decodes the escape sequence at lx.pos into out: \n \r \t \\ \' \", \xNN and \u{N...}.
func (lx *zonLexer) escape(out *strings.Builder) error {
	if lx.pos+1 >= len(lx.src) {
		return lx.errorf("unterminated string")
	}
	letter := lx.src[lx.pos+1]
	if b, ok := zonSimpleEscapes[letter]; ok {
		out.WriteByte(b)
		lx.pos += 2
		return nil
	}
	switch letter {
	case 'x':
		return lx.hexEscape(out)
	case 'u':
		return lx.unicodeEscape(out)
	}
	return lx.errorf("invalid escape sequence \\%c", letter)
}

func (lx *zonLexer) hexEscape(out *strings.Builder) error {
	if lx.pos+4 > len(lx.src) {
		return lx.errorf("invalid \\x escape")
	}
	value, err := strconv.ParseUint(lx.src[lx.pos+2:lx.pos+4], 16, 8)
	if err != nil {
		return lx.errorf("invalid \\x escape %q", lx.src[lx.pos:lx.pos+4])
	}
	out.WriteByte(byte(value))
	lx.pos += 4
	return nil
}

func (lx *zonLexer) unicodeEscape(out *strings.Builder) error {
	rest := lx.src[lx.pos:]
	end := strings.IndexByte(rest, '}')
	if !strings.HasPrefix(rest, `\u{`) || end < 0 || end > lineLength(rest) {
		return lx.errorf("invalid \\u escape")
	}
	value, err := strconv.ParseUint(rest[3:end], 16, 32)
	if err != nil || value > utf8.MaxRune {
		return lx.errorf("invalid \\u escape %q", rest[:end+1])
	}
	r := rune(value)
	if !utf8.ValidRune(r) {
		return lx.errorf("invalid \\u escape %q: a surrogate half", rest[:end+1])
	}
	out.WriteRune(r)
	lx.pos += end + 1
	return nil
}

// multiline consumes a multiline string: consecutive lines, each starting with `\\` after white
// space, joined by line breaks. It is bounded by the length of the source.
func (lx *zonLexer) multiline() {
	var lines []string
	for lx.pos < len(lx.src) {
		rest := lx.src[lx.pos+2:]
		lines = append(lines, strings.TrimSuffix(rest[:lineLength(rest)], "\r"))
		lx.pos += 2 + lineLength(rest)
		following := strings.TrimLeft(lx.src[min(lx.pos+1, len(lx.src)):], " \t\r")
		if lx.pos >= len(lx.src) || !strings.HasPrefix(following, `\\`) {
			break
		}
		lx.line++
		lx.pos = len(lx.src) - len(following)
	}
	lx.emit(zonString, strings.Join(lines, "\n"))
}

// character consumes a character literal ('a', '\n', '\u{1F600}') as a scalar.
func (lx *zonLexer) character() error {
	var decoded strings.Builder
	start := lx.pos
	lx.pos++
	for lx.pos < len(lx.src) && lx.src[lx.pos] != '\'' && lx.src[lx.pos] != '\n' {
		if lx.src[lx.pos] != '\\' {
			lx.pos++
			continue
		}
		if err := lx.escape(&decoded); err != nil {
			return err
		}
	}
	if lx.pos >= len(lx.src) || lx.src[lx.pos] != '\'' {
		return lx.errorf("unterminated character literal")
	}
	lx.pos++
	lx.emit(zonScalar, lx.src[start:lx.pos])
	return nil
}

type zonState uint8

const (
	zonExpectValue zonState = iota
	zonExpectMember
	zonAfterValue
	zonDone
)

// zonParser walks the tokens with an explicit stack of open literals (no recursion, HISS-01).
type zonParser struct {
	tokens []zonToken
	i      int
	// stack holds, for every open struct or tuple literal, the field it is the value of: "" for
	// the top-level literal and for a tuple element.
	stack []string
	// field is the name of the field whose value comes next, "" for a tuple element.
	field string
	deps  map[string]*zonDependency
}

// parseZon reads the .dependencies of a build.zig.zon. A manifest that is not one struct
// literal, is not valid ZON, or declares a dependency that is not a struct with exactly one of
// url and path is refused with the line and reason.
func parseZon(src string) (map[string]zonDependency, error) {
	tokens, err := lexZon(src)
	if err != nil {
		return nil, err
	}
	p := &zonParser{tokens: tokens, deps: make(map[string]*zonDependency)}
	state := zonExpectValue
	for steps := 0; state != zonDone; steps++ {
		if steps > 2*len(tokens)+2 {
			return nil, errors.New("parser made no progress")
		}
		if state, err = p.step(state); err != nil {
			return nil, err
		}
	}
	return p.dependencies()
}

func (p *zonParser) step(state zonState) (zonState, error) {
	switch state {
	case zonExpectMember:
		return p.member()
	case zonAfterValue:
		return p.afterValue()
	default:
		return p.value()
	}
}

// peek returns the token n places ahead, or the end-of-file token lexZon ends every token list
// with once past it.
func (p *zonParser) peek(n int) zonToken {
	return p.tokens[min(p.i+n, len(p.tokens)-1)]
}

func (p *zonParser) errorf(tok zonToken, format string, args ...any) error {
	return fmt.Errorf("line %d: %s", tok.line, fmt.Sprintf(format, args...))
}

// value consumes one value: a struct or tuple literal, which it opens, or a scalar.
func (p *zonParser) value() (zonState, error) {
	tok, next := p.peek(0), p.peek(1)
	opens := tok.kind == zonDot && next.kind == zonLBrace
	if len(p.stack) == 0 && !opens {
		return 0, p.errorf(tok, "a build.zig.zon holds one struct literal .{ ... }, found %s", zonTokenNames[tok.kind])
	}
	switch {
	case opens:
		p.i += 2
		return p.open(tok)
	case tok.kind == zonDot && next.kind == zonIdent:
		p.i += 2
		return p.scalar(next)
	case tok.kind == zonString || tok.kind == zonScalar || tok.kind == zonIdent:
		p.i++
		return p.scalar(tok)
	}
	return 0, p.errorf(tok, "expected a value, found %s", zonTokenNames[tok.kind])
}

// open pushes a struct or tuple literal, creating the dependency it declares when it is an entry
// of .dependencies.
func (p *zonParser) open(tok zonToken) (zonState, error) {
	if len(p.stack) >= maxZonDepth {
		return 0, p.errorf(tok, "literals nest deeper than %d levels", maxZonDepth)
	}
	if p.inDependencies() {
		if p.field == "" {
			return 0, p.unnamedDependency(tok)
		}
		p.deps[p.field] = &zonDependency{}
	}
	p.stack = append(p.stack, p.field)
	p.field = ""
	return zonExpectMember, nil
}

// unnamedDependency refuses an entry of .dependencies that is a tuple element, not a field.
func (p *zonParser) unnamedDependency(tok zonToken) error {
	return p.errorf(tok, ".dependencies holds named fields (.name = .{ ... }), found an unnamed entry")
}

// inDependencies reports whether the value being parsed is an entry of the top-level
// .dependencies struct.
func (p *zonParser) inDependencies() bool {
	return len(p.stack) == 2 && p.stack[1] == "dependencies"
}

// scalar records a scalar value where it is a dependency field and refuses it where the manifest
// needs a struct.
func (p *zonParser) scalar(tok zonToken) (zonState, error) {
	switch {
	case len(p.stack) == 1 && p.field == "dependencies":
		return 0, p.errorf(tok, ".dependencies is %s, not a struct literal", zonTokenNames[tok.kind])
	case p.inDependencies() && p.field == "":
		return 0, p.unnamedDependency(tok)
	case p.inDependencies():
		return 0, p.errorf(tok, "dependency %q is %s, not a struct literal", p.field, zonTokenNames[tok.kind])
	case len(p.stack) == 3 && p.stack[1] == "dependencies":
		return zonAfterValue, p.dependencyField(tok)
	}
	return zonAfterValue, nil
}

// dependencyField records the url or path of the dependency being parsed; both are strings.
func (p *zonParser) dependencyField(tok zonToken) error {
	dep := p.deps[p.stack[2]]
	if p.field != "url" && p.field != "path" {
		return nil
	}
	if tok.kind != zonString {
		return p.errorf(tok, "dependency %q: .%s is %s, not a string", p.stack[2], p.field, zonTokenNames[tok.kind])
	}
	if p.field == "url" {
		dep.url, dep.hasURL = tok.text, true
		return nil
	}
	dep.path, dep.hasPath = tok.text, true
	return nil
}

// member consumes the start of the next entry of an open literal: `}`, a `.name =` field or,
// consuming nothing, a tuple element.
func (p *zonParser) member() (zonState, error) {
	if p.peek(0).kind == zonRBrace {
		p.i++
		return p.close(), nil
	}
	if p.peek(0).kind == zonDot && p.peek(1).kind == zonIdent && p.peek(2).kind == zonEqual {
		p.field = p.peek(1).text
		p.i += 3
		return zonExpectValue, nil
	}
	p.field = ""
	return zonExpectValue, nil
}

// close pops the innermost literal, which completes the value of its parent.
func (p *zonParser) close() zonState {
	p.stack = p.stack[:len(p.stack)-1]
	p.field = ""
	return zonAfterValue
}

// afterValue consumes what follows a complete value: `,` or `}` inside a literal, the end of the
// file after the top-level literal.
func (p *zonParser) afterValue() (zonState, error) {
	tok := p.peek(0)
	if len(p.stack) == 0 {
		if tok.kind != zonEOF {
			return 0, p.errorf(tok, "unexpected %s after the top-level struct literal", zonTokenNames[tok.kind])
		}
		return zonDone, nil
	}
	switch tok.kind {
	case zonComma:
		p.i++
		return zonExpectMember, nil
	case zonRBrace:
		p.i++
		return p.close(), nil
	}
	return 0, p.errorf(tok, "expected ',' or '}' after a value, found %s", zonTokenNames[tok.kind])
}

// dependencies returns the parsed dependencies, refusing one that sets both url and path or
// neither: a dependency provides a url and hash, or a path.
func (p *zonParser) dependencies() (map[string]zonDependency, error) {
	deps := make(map[string]zonDependency, len(p.deps))
	for _, name := range slices.Sorted(maps.Keys(p.deps)) {
		dep := p.deps[name]
		switch {
		case dep.hasURL && dep.hasPath:
			return nil, fmt.Errorf("dependency %q sets both url and path, which are mutually exclusive", name)
		case !dep.hasURL && !dep.hasPath:
			return nil, fmt.Errorf("dependency %q sets neither url nor path", name)
		}
		deps[name] = *dep
	}
	return deps, nil
}
