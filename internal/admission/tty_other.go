// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !unix

package admission

// ignoreBackgroundTTYSignals has nothing to ignore on systems without job-control terminal signals.
func ignoreBackgroundTTYSignals() {}
