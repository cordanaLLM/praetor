// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"fmt"
	"os"
	"sync"
)

// privacyNoticeOnce bounds the notice to one emission per process. Repeating it for
// every artefact would bury the runs it matters in, and the statement is about the
// platform rather than about any one file.
var privacyNoticeOnce sync.Once

// NotePrivacyLimitation prints, once per process, the reason a privacy check could
// not be verified on this platform. An empty reason prints nothing, so a caller can
// pass the second return of ArtefactPrivacy unconditionally.
//
// HISS-21 allows a check to be skipped only with its reason stated. ArtefactPrivacy
// answers "private" on a platform that cannot prove it; without this the answer would
// be indistinguishable from a verified pass, which is the state the invariant forbids.
func NotePrivacyLimitation(reason string) {
	if reason == "" {
		return
	}
	privacyNoticeOnce.Do(func() {
		fmt.Fprintf(os.Stderr, "NOTE: artefact privacy not verified: %s\n", reason)
	})
}
