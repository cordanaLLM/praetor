// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import "strings"

// MaxCSVFields bounds the fields SplitCSV reads from one list (HISS-02).
const MaxCSVFields = 1024

// SplitCSV splits a comma-separated list into its trimmed, non-empty fields, at most
// MaxCSVFields of them, or nil for a blank list. The standardsctl list flags, the standards-mcp
// -allowed-origins flag and the allowed-failures and allowed-skips inputs of an alls-green step
// (internal/forge) read a list through it.
func SplitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	fields := make([]string, 0, len(parts))
	for i := 0; i < len(parts) && i < MaxCSVFields; i++ {
		if trimmed := strings.TrimSpace(parts[i]); trimmed != "" {
			fields = append(fields, trimmed)
		}
	}
	return fields
}
