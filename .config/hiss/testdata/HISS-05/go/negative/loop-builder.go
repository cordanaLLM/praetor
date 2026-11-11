// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package p

import "strings"

func Build(xs []string) string {
	var sb strings.Builder
	for _, x := range xs {
		sb.WriteString(x)
		if sb.Len() > 10 {
			return sb.String()
		}
	}
	return ""
}
