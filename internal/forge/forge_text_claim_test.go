// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"bytes"
	"reflect"
	"testing"
	"time"
)

// TestUnwrapForgeText_Boundary_ClaimMarkerSurvives proves the send-time unwrap keeps a claim
// comment readable: the marker must stay the exact first line, or the claim desk loses the claim.
func TestUnwrapForgeText_Boundary_ClaimMarkerSurvives(t *testing.T) {
	now := time.Date(2026, 10, 15, 18, 0, 0, 0, time.UTC)
	claim := Claim{Session: "s-1", Lane: "lane-1", Branch: "fix/x", Started: now, Updated: now, Stage: "implementing",
		Note: "the note is one sentence that runs on long enough to be a paragraph of its own"}
	key := bytes.Repeat([]byte{0x42}, ClaimKeySize)
	for _, takeover := range []string{"", "s-0"} {
		body, err := renderClaimBodyWithKey(claim, takeover, key)
		if err != nil {
			t.Fatal(err)
		}
		before, okBefore := ParseClaimMarker(body)
		after, okAfter := ParseClaimMarker(UnwrapForgeText(body))
		if !okBefore || !okAfter || !reflect.DeepEqual(before, after) {
			t.Fatalf("takeover %q: marker changed by UnwrapForgeText: before %+v (%v), after %+v (%v)\n%s", takeover, before, okBefore, after, okAfter, body)
		}
	}
}
