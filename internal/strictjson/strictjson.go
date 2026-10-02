// Package strictjson reads one bounded JSON document strictly. It is the one reader for every
// Praetor input that encoding/json decodes and that must not be reinterpreted on the way in
// (HISS-19): notebook artifacts, planning drafts, repair configurations, repair provider
// responses, dogfood repair reports, editor configuration, the JSON files a flavor audit
// validates and operational sync derived files.
// It performs no I/O. Each caller reads its bytes under its own bound and passes its own bounds
// and wording through Options.
//
// Validate scans the document with encoding/json/jsontext and refuses input that is empty, over
// MaxBytes or not UTF-8; a token the scanner cannot read; an escaped UTF-16 surrogate without
// its pair, which encoding/json would replace with U+FFFD; nesting deeper than MaxDepth; more
// than MaxTokens tokens; a null under RejectNull; one object repeating a member name; and
// anything after the first document. Decode validates and then decodes into a Go value with
// unknown fields disallowed.
//
// Two member names are, by default, the same name when strings.EqualFold matches them, because
// that is how encoding/json matches a member to a struct field: {"name":"a","Name":"b"} decodes
// into one field and the last spelling silently wins. ExactNames narrows the rule for a document
// whose member names are case-sensitive data decoded into a Go map, which keeps both spellings.
//
// A document is strict JSON unless its caller names another Dialect. JSONC (issue #316) adds
// line comments, block comments and trailing commas for the files VS Code documents as JSON
// with Comments (DialectOf), and nothing else: newDecoder, the only place the token source is
// built, hands the scanner the document with those bytes blanked (dialect.go), so every bound
// and every refusal above applies to a JSONC document exactly as it does to a strict one, and
// a comment nothing closes is a syntax refusal. No second parser reads either dialect.
package strictjson

import (
	"bytes"
	"cmp"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// The kind of every refusal. An error from Validate or Decode matches exactly one of them with
// errors.Is, whatever wording the caller chose.
var (
	ErrSize      = errors.New("JSON input is empty, over its byte bound or not UTF-8")
	ErrSyntax    = errors.New("JSON input is not valid JSON")
	ErrSurrogate = errors.New("JSON string holds an unpaired UTF-16 surrogate escape")
	ErrDepth     = errors.New("JSON nesting exceeds its bound")
	ErrTokens    = errors.New("JSON token count exceeds its bound")
	ErrNull      = errors.New("JSON null is not allowed")
	ErrDuplicate = errors.New("JSON object repeats a member name")
	ErrTrailing  = errors.New("JSON input holds more than one document")
	ErrDecode    = errors.New("JSON document does not decode into its target")
)

// NameMatch decides when two member names of one object are the same name.
type NameMatch uint8

const (
	// FoldedNames treats names that strings.EqualFold matches as one name, the rule
	// encoding/json applies when it matches a member to a struct field. It is the zero value.
	FoldedNames NameMatch = iota
	// ExactNames treats only identical names as one name. It is for a document decoded into a
	// Go map, which keeps every spelling, whose names are case-sensitive data such as the glob
	// patterns of an editor setting.
	ExactNames
)

// Options holds one caller's bounds and wording.
type Options struct {
	MaxBytes   int       // input bytes accepted; a bound below 1 accepts nothing
	MaxDepth   int       // array and object nesting accepted; a bound below 1 accepts no container
	MaxTokens  int       // tokens accepted; below 1 the input length bounds them
	Names      NameMatch // when two member names of one object are the same name
	Dialect    Dialect   // the syntax accepted; the zero value is strict JSON
	RejectNull bool      // refuse every null literal
	UseNumber  bool      // Decode keeps a number held in an interface value as json.Number
	Messages   Messages
}

// Messages is the wording of each refusal; an empty field takes the default named beside it.
// A message may carry one formatting verb for the detail its refusal supplies: the byte bound
// (Size, %d), the scanner error (Syntax and Surrogate, %w), the nesting bound (Depth, %d), the
// token bound (Count, %d), the repeated member name as written (Duplicate, %q) and the decoder
// error (Decode, %w). Null and Trailing supply no detail, so their messages carry no verb.
type Messages struct {
	Size      string // "JSON requires 1..%d UTF-8 bytes"
	Syntax    string // "%w"
	Surrogate string // "JSON string holds an unpaired UTF-16 surrogate escape"
	Depth     string // "JSON nesting exceeds %d"
	Count     string // "JSON token bound exceeded"
	Null      string // "JSON null is not allowed"
	Duplicate string // "invalid or duplicate JSON key"
	Trailing  string // "expected exactly one JSON document"
	Decode    string // "decode JSON: %w"
}

func (m Messages) withDefaults() Messages {
	m.Size = cmp.Or(m.Size, "JSON requires 1..%d UTF-8 bytes")
	m.Syntax = cmp.Or(m.Syntax, "%w")
	m.Surrogate = cmp.Or(m.Surrogate, ErrSurrogate.Error())
	m.Depth = cmp.Or(m.Depth, "JSON nesting exceeds %d")
	m.Count = cmp.Or(m.Count, "JSON token bound exceeded")
	m.Null = cmp.Or(m.Null, ErrNull.Error())
	m.Duplicate = cmp.Or(m.Duplicate, "invalid or duplicate JSON key")
	m.Trailing = cmp.Or(m.Trailing, "expected exactly one JSON document")
	m.Decode = cmp.Or(m.Decode, "decode JSON: %w")
	return m
}

// Decode validates raw under opts and decodes it into value with unknown fields disallowed.
func Decode(raw []byte, value any, opts Options) error {
	text, err := validate(raw, opts)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(text))
	decoder.DisallowUnknownFields()
	if opts.UseNumber {
		decoder.UseNumber()
	}
	if err := decoder.Decode(value); err != nil {
		return refuse(ErrDecode, opts.Messages.withDefaults().Decode, err)
	}
	return nil
}

// Validate accepts exactly one JSON document that satisfies opts. Every token is read once, so
// the scan is bounded by MaxTokens, or by the input length when MaxTokens is below 1.
func Validate(raw []byte, opts Options) error {
	_, err := validate(raw, opts)
	return err
}

// validate is Validate, returning the strict JSON text it accepted: raw itself, or under the
// JSONC dialect raw with its comments and trailing commas blanked, which is what Decode hands
// to encoding/json.
func validate(raw []byte, opts Options) ([]byte, error) {
	messages := opts.Messages.withDefaults()
	if len(raw) == 0 || len(raw) > opts.MaxBytes || !utf8.Valid(raw) {
		return nil, refuse(ErrSize, messages.Size, opts.MaxBytes)
	}
	limit := opts.MaxTokens
	if limit < 1 {
		limit = len(raw)
	}
	decoder, text := newDecoder(raw, opts.Dialect)
	s := &scan{raw: text, opts: opts, messages: messages, decoder: decoder}
	// One read per accepted token, plus the read that must find the end of input.
	for i := 0; i <= limit; i++ {
		token, err := s.decoder.ReadToken()
		if s.complete {
			return text, s.finish(err)
		}
		if err != nil {
			return nil, s.syntax(err)
		}
		if err := s.accept(token); err != nil {
			return nil, err
		}
	}
	return nil, refuse(ErrTokens, messages.Count, limit)
}

// newDecoder builds the token source of one scan and returns it with the strict JSON text it
// reads, which is raw unless dialect blanks part of it (Dialect.strict). Duplicate names are
// left to the scan, which applies the caller's NameMatch; invalid UTF-8 and unpaired surrogate
// escapes stay refused.
func newDecoder(raw []byte, dialect Dialect) (*jsontext.Decoder, []byte) {
	text := dialect.strict(raw)
	return jsontext.NewDecoder(bytes.NewReader(text), jsontext.AllowDuplicateNames(true)), text
}

type scan struct {
	raw      []byte // the strict JSON text the decoder reads
	opts     Options
	messages Messages
	decoder  *jsontext.Decoder
	names    []map[string]bool // one entry per open container; nil for an array
	complete bool              // the first document has been read in full
}

// accept checks one token the decoder has just read.
func (s *scan) accept(token jsontext.Token) error {
	depth := s.decoder.StackDepth()
	switch token.Kind() {
	case jsontext.KindBeginObject, jsontext.KindBeginArray:
		if depth > s.opts.MaxDepth {
			return refuse(ErrDepth, s.messages.Depth, s.opts.MaxDepth)
		}
		var names map[string]bool
		if token.Kind() == jsontext.KindBeginObject {
			names = map[string]bool{}
		}
		s.names = append(s.names, names)
	case jsontext.KindEndObject, jsontext.KindEndArray:
		s.names = s.names[:len(s.names)-1]
	case jsontext.KindNull:
		if s.opts.RejectNull {
			return refuse(ErrNull, s.messages.Null, nil)
		}
	case jsontext.KindString:
		if err := s.member(token.String(), depth); err != nil {
			return err
		}
	}
	s.complete = depth == 0
	return nil
}

// member records a string read at depth when it is a member name: an odd count of tokens in the
// enclosing object means the string just read is a name rather than a value.
func (s *scan) member(text string, depth int) error {
	kind, length := s.decoder.StackIndex(depth)
	if kind != jsontext.KindBeginObject || length%2 == 0 {
		return nil
	}
	name := text
	if s.opts.Names == FoldedNames {
		name = foldName(text)
	}
	names := s.names[depth-1]
	if names[name] {
		return refuse(ErrDuplicate, s.messages.Duplicate, text)
	}
	names[name] = true
	return nil
}

// finish reads what follows a complete document: only the end of input is accepted.
func (s *scan) finish(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}
	return refuse(ErrTrailing, s.messages.Trailing, nil)
}

// syntax words a scanner error. The end of input before any value is an unexpected end too.
func (s *scan) syntax(err error) error {
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	if unpairedSurrogate(s.raw, err) {
		return refuse(ErrSurrogate, s.messages.Surrogate, err)
	}
	return refuse(ErrSyntax, s.messages.Syntax, err)
}

// unpairedSurrogate reports whether err is the scanner refusing a well-formed \u escape of a
// UTF-16 surrogate, which it reports at the offset of the escape. A malformed escape and a
// document cut short inside one stay syntax errors.
func unpairedSurrogate(raw []byte, err error) bool {
	var syntax *jsontext.SyntacticError
	if !errors.As(err, &syntax) || errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}
	at := syntax.ByteOffset
	if at < 0 || at+6 > int64(len(raw)) || raw[at] != '\\' || raw[at+1] != 'u' {
		return false
	}
	unit, parseErr := strconv.ParseUint(string(raw[at+2:at+6]), 16, 16)
	return parseErr == nil && utf16.IsSurrogate(rune(unit))
}

// refusal is one refused document: its kind for errors.Is and the caller's wording, which wraps
// the scanner or decoder error when the wording carries %w.
type refusal struct {
	kind error
	err  error
}

func (r *refusal) Error() string { return r.err.Error() }

func (r *refusal) Unwrap() []error { return []error{r.kind, r.err} }

func refuse(kind error, message string, detail any) error {
	if !strings.Contains(message, "%") {
		return &refusal{kind: kind, err: errors.New(message)}
	}
	return &refusal{kind: kind, err: fmt.Errorf(message, detail)}
}

// maxFoldOrbit bounds the walk around a rune's case-folding orbit (HISS-02). Unicode orbits are
// at most four runes long; the bound exists so a future table cannot make the loop unbounded.
const maxFoldOrbit = 8

// foldName returns the spelling strings.EqualFold compares by, the rule encoding/json matches
// member names to struct fields with: ASCII letters upper-cased, every other rune folded to the
// smallest rune in its case-folding orbit. encoding/json keeps its own fold unexported.
func foldName(name string) string {
	return strings.Map(foldRune, name)
}

func foldRune(r rune) rune {
	if r < utf8.RuneSelf {
		if 'a' <= r && r <= 'z' {
			return r - ('a' - 'A')
		}
		return r
	}
	for i := 0; i < maxFoldOrbit; i++ {
		folded := unicode.SimpleFold(r)
		if folded <= r {
			return folded
		}
		r = folded
	}
	return r
}
