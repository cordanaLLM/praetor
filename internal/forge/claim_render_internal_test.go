// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRenderClaimBody_TagsPlainSessionAndLane(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, ClaimKeySize)
	c := Claim{
		Session: "secret-session-id-99",
		Lane:    "hidden-lane-alpha",
		Branch:  "feat/safe",
		Started: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC),
		Updated: time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC),
		Stage:   "implementing",
	}

	body, err := renderClaimBodyWithKey(c, "", key)
	if err != nil {
		t.Fatalf("renderClaimBodyWithKey: %v", err)
	}

	if strings.Contains(body, "secret-session-id-99") {
		t.Fatalf("rendered claim body leaked plain session: %s", body)
	}
	if strings.Contains(body, "hidden-lane-alpha") {
		t.Fatalf("rendered claim body leaked plain lane: %s", body)
	}

	wantSessionTag := ClaimTag(key, "session", "secret-session-id-99")
	wantLaneTag := ClaimTag(key, "lane", "hidden-lane-alpha")

	if !strings.Contains(body, wantSessionTag) {
		t.Fatalf("rendered claim body missing session tag %s: %s", wantSessionTag, body)
	}
	if !strings.Contains(body, wantLaneTag) {
		t.Fatalf("rendered claim body missing lane tag %s: %s", wantLaneTag, body)
	}
}
