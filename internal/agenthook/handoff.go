// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"bytes"
	"context"
)

// EngineHandoff decides, once the workspace root of a call is known, whether another engine
// judges it: the one the repository pins with PRAETOR_REF (#1173). Run hands it the call's
// payload so the other engine reads the same bytes on its standard input.
type EngineHandoff func(ctx context.Context, root string, row Registration, payload []byte) HandoffResult

// HandoffResult is an EngineHandoff's answer. Handled means Response is the other engine's
// own answer and is returned as it is. Otherwise Deny refuses the call with that reason (a pin
// that cannot be used fails closed, as the launcher refuses it), Skip answers with a stated
// skip (the pinned engine serves no such row, so it never blocks the client), and Note is
// printed on standard error while this engine judges the call.
type HandoffResult struct {
	Handled  bool
	Response Response
	Deny     string
	Skip     string
	Note     string
}

// handOff reads the payload once, resolves the workspace root the payload names and asks
// in.Handoff. It returns the response to give and true when the call is answered; otherwise
// the note to print and, through in, the payload for the evaluation that follows.
func handOff(ctx context.Context, dialect Dialect, row Registration, in *Invocation) (Response, string, bool) {
	payload, err := readBounded(ctx, in.Stdin)
	in.Stdin = bytes.NewReader(payload)
	if err != nil {
		return Response{}, "", false
	}
	canonical, err := dialect.Decode(row.Event, payload)
	if err != nil {
		return Response{}, "", false
	}
	// Only an adopted repository declares a pin; any other workspace is judged as before.
	root, _, err := callRoot(ctx, row, canonical, *in)
	if err != nil || root == "" || !Governed(root) {
		return Response{}, "", false
	}
	result := in.Handoff(ctx, root, row, payload)
	switch {
	case result.Handled:
		return result.Response, "", true
	case result.Deny != "":
		return dialect.Encode(canonical, Verdict{Outcome: Deny, Reason: "[BLOCKED BY HISS] " + result.Deny}), "", true
	case result.Skip != "":
		return dialect.Encode(canonical, Verdict{Outcome: Skip, Reason: result.Skip}), "", true
	}
	return Response{}, result.Note, false
}
