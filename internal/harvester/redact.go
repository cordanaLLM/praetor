package harvester

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
)

const (
	// RedactedValue replaces every credential value in a bundled configuration file.
	RedactedValue = `"[REDACTED]"`
	// maxRedactedConfigBytes bounds a configuration file the bundler parses for redaction.
	// A larger file is skipped rather than copied unredacted.
	maxRedactedConfigBytes = 32 << 20
	// maxRedactTokens bounds the JSON token walk (HISS-02).
	maxRedactTokens = 1 << 22
)

// ErrMalformedConfig is returned for a configuration file in a redacted category that is
// not valid JSON. Such a file is skipped, never copied verbatim, because its secrets
// cannot be located.
var ErrMalformedConfig = errors.New("harvester: configuration file is not valid JSON; not copied because its credentials cannot be redacted")

// credentialKeySuffixes are the normalized key endings (lower case, with '-', '_', '.'
// and spaces removed) that name a credential: GITHUB_TOKEN, accessToken, client_secret,
// OPENAI_API_KEY, x-api-key, password.
var credentialKeySuffixes = []string{
	"token", "secret", "secrets", "password", "passwd", "credential", "credentials",
	"apikey", "accesskey", "privatekey", "secretkey", "sessionkey", "signingkey", "cookie",
}

// credentialKeys are normalized keys that name a credential only as a whole word.
var credentialKeys = map[string]bool{"authorization": true, "auth": true, "bearer": true, "pat": true}

// needsRedaction reports whether a bundled file must be redacted: a JSON file (by name,
// which also admits claude.json.backup) in one of the SensitiveBundleCategories. Shell
// history and TOML configuration are not JSON and are copied as written.
func needsRedaction(category, src string) bool {
	return slices.Contains(SensitiveBundleCategories, category) && strings.Contains(strings.ToLower(filepath.Base(src)), ".json")
}

// isCredentialKey reports whether an object key names a credential value.
func isCredentialKey(key string) bool {
	normalized := strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || r == '.' || r == ' ' {
			return -1
		}
		return r
	}, strings.ToLower(key))
	if credentialKeys[normalized] {
		return true
	}
	for _, suffix := range credentialKeySuffixes {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

// redactFrame is one open JSON container on the walk's explicit stack.
type redactFrame struct {
	object    bool
	expectKey bool
	// env marks the members of an "env" object: every string value is redacted,
	// whatever the variable is called.
	env bool
}

// jsonRedactor walks a JSON document token by token without recursion (HISS-01) and
// records the byte span of every credential value.
type jsonRedactor struct {
	dec    *json.Decoder
	data   []byte
	stack  []redactFrame
	spans  [][2]int64
	tokens int
	// credential and envNext describe the member value about to be read.
	credential, envNext bool
}

// redactJSONSecrets replaces credential values in a JSON document with RedactedValue and
// returns the rewritten bytes and the number of values replaced. A value is a credential
// when its key names one (see isCredentialKey), when it is a string member of an "env"
// object, or when it is a string carrying a bearer token. Numbers, booleans and null are
// never replaced, so a key such as max_tokens keeps its value. Every byte outside a
// replaced value is copied unchanged.
func redactJSONSecrets(data []byte) ([]byte, int, error) {
	if !json.Valid(data) {
		return nil, 0, ErrMalformedConfig
	}
	r := &jsonRedactor{dec: json.NewDecoder(bytes.NewReader(data)), data: data}
	r.dec.UseNumber()
	for r.tokens <= maxRedactTokens {
		start := r.dec.InputOffset()
		tok, err := r.next()
		if errors.Is(err, io.EOF) {
			return spliceRedactions(data, r.spans), len(r.spans), nil
		}
		if err != nil {
			return nil, 0, fmt.Errorf("walk JSON for redaction: %w", err)
		}
		if err := r.consume(tok, start); err != nil {
			return nil, 0, err
		}
	}
	return nil, 0, fmt.Errorf("JSON document exceeds %d tokens", maxRedactTokens)
}

func (r *jsonRedactor) next() (json.Token, error) {
	r.tokens++
	if r.tokens > maxRedactTokens {
		return nil, fmt.Errorf("JSON document exceeds %d tokens", maxRedactTokens)
	}
	return r.dec.Token()
}

func (r *jsonRedactor) top() *redactFrame {
	if len(r.stack) == 0 {
		return nil
	}
	return &r.stack[len(r.stack)-1]
}

// consume advances the walk by one token that started at byte offset start.
func (r *jsonRedactor) consume(tok json.Token, start int64) error {
	d, isDelim := tok.(json.Delim)
	switch top := r.top(); {
	case isDelim && (d == '}' || d == ']'):
		r.stack = r.stack[:len(r.stack)-1]
		r.valueDone()
	case top != nil && top.object && top.expectKey:
		r.readKey(top, tok)
	case isDelim:
		return r.openValue(d, start)
	default:
		r.scalarValue(top, tok, start)
	}
	return nil
}

// readKey records what the member value after an object key must be treated as.
func (r *jsonRedactor) readKey(top *redactFrame, tok json.Token) {
	key, isString := tok.(string)
	top.expectKey = false
	r.credential = isString && isCredentialKey(key)
	r.envNext = isString && strings.EqualFold(key, "env")
}

// openValue handles an object or array value: redacted whole under a credential key,
// otherwise pushed onto the walk's stack.
func (r *jsonRedactor) openValue(d json.Delim, start int64) error {
	credential, env := r.credential, r.envNext
	r.credential, r.envNext = false, false
	if credential {
		return r.redactContainer(start)
	}
	r.stack = append(r.stack, redactFrame{object: d == '{', expectKey: d == '{', env: env && d == '{'})
	return nil
}

// scalarValue redacts a string value that is a credential; numbers, booleans and null are
// always kept.
func (r *jsonRedactor) scalarValue(top *redactFrame, tok json.Token, start int64) {
	credential := r.credential
	r.credential, r.envNext = false, false
	inEnv := top != nil && top.env
	if s, isString := tok.(string); isString && (credential || inEnv || carriesBearer(s)) {
		r.addSpan(start)
	}
	r.valueDone()
}

// redactContainer skips the object or array a credential key names and redacts it whole.
func (r *jsonRedactor) redactContainer(start int64) error {
	for depth := 1; depth > 0; {
		tok, err := r.next()
		if err != nil {
			return fmt.Errorf("walk JSON for redaction: %w", err)
		}
		if d, ok := tok.(json.Delim); ok {
			if d == '{' || d == '[' {
				depth++
			} else {
				depth--
			}
		}
	}
	r.addSpan(start)
	r.valueDone()
	return nil
}

// addSpan records the value that ends at the decoder's current offset. The token began
// after start once the separators before it are skipped.
func (r *jsonRedactor) addSpan(start int64) {
	end := r.dec.InputOffset()
	for start < end && strings.IndexByte(" \t\r\n:,", r.data[start]) >= 0 {
		start++
	}
	r.spans = append(r.spans, [2]int64{start, end})
}

// valueDone marks the current object member as complete.
func (r *jsonRedactor) valueDone() {
	if top := r.top(); top != nil && top.object {
		top.expectKey = true
	}
}

// carriesBearer reports whether a string value embeds an HTTP bearer or basic credential,
// such as an "Authorization: Bearer ..." header passed as an MCP server argument.
func carriesBearer(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "bearer ") || strings.HasPrefix(lower, "basic ")
}

// spliceRedactions copies data with every span replaced by RedactedValue. Spans are in
// document order and never overlap.
func spliceRedactions(data []byte, spans [][2]int64) []byte {
	if len(spans) == 0 {
		return data
	}
	var out bytes.Buffer
	out.Grow(len(data))
	prev := int64(0)
	for _, span := range spans {
		out.Write(data[prev:span[0]])
		out.WriteString(RedactedValue)
		prev = span[1]
	}
	out.Write(data[prev:])
	return out.Bytes()
}
