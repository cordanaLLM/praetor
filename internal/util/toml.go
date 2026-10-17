// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"fmt"

	"github.com/pelletier/go-toml/v2"
)

// DecodeTOML parses data into target and includes the source name in syntax errors.
func DecodeTOML(name string, data []byte, target any) error {
	if err := toml.Unmarshal(data, target); err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}
	return nil
}
