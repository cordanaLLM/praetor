// Package clientjson reads and rewrites agent client configuration JSON under one bound and
// without disturbing what it does not own. It performs no I/O: clientsetup merges MCP servers
// through Validate, adoption merges hook registrations through PlanHooks, and each caller
// reads, writes and reads back through its own confined filesystem path.
package clientjson

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
)

// ErrNotStrictJSON refuses a document that does not scan as strict JSON: a syntax error, a
// duplicate member name, or JSONC comments and trailing commas, which some clients strip before
// parsing (Gemini CLI does) but a merge cannot keep.
var ErrNotStrictJSON = errors.New("invalid or ambiguous client JSON")

const (
	// MaxBytes bounds one client configuration document.
	MaxBytes = 1 << 20
	// MaxDepth bounds the nesting of one client configuration document.
	MaxDepth = 32
)

// Validate accepts exactly one JSON object of 1..MaxBytes bytes, nested at most MaxDepth
// deep, with no duplicate member name. Every token is read, so the scan is bounded by the
// input length, and a cancelled ctx stops it between tokens.
func Validate(ctx context.Context, raw []byte) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if len(raw) == 0 || len(raw) > MaxBytes {
		return errors.New("JSON input requires 1..1048576 bytes")
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(raw))
	complete := false
	for i := 0; i <= len(raw); i++ {
		if err := contextErr(ctx); err != nil {
			return err
		}
		token, err := decoder.ReadToken()
		if errors.Is(err, io.EOF) && complete {
			return nil
		}
		if err != nil {
			return ErrNotStrictJSON
		}
		if err := validatePosition(token, i, complete, decoder.StackDepth()); err != nil {
			return err
		}
		complete = decoder.StackDepth() == 0
	}
	return errors.New("JSON token bound exceeded")
}

func validatePosition(token jsontext.Token, index int, complete bool, depth int) error {
	if complete || (index == 0 && token.Kind() != '{') {
		return errors.New("client JSON requires exactly one object")
	}
	if depth > MaxDepth {
		return errors.New("JSON nesting exceeds 32")
	}
	return nil
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return errors.New("client JSON requires a context")
	}
	return ctx.Err()
}
