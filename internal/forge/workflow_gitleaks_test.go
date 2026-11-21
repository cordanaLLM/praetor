// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package forge

import "testing"

// TestGitleaksHereDocs sees executing history scans and ignores text written as data.
func TestGitleaksHereDocs(t *testing.T) {
	checkHereDocReader(t, "gitleaks detect --log-opts=--all", stepRunsGitleaksWithoutMinusM)
}
