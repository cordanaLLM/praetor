// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/flavor"
)

// auditUIGates runs the ui-gates audit check on rootDir (order engine-fr-rule-checks-20261027).
func auditUIGates(rootDir string, manifest *config.Manifest) error {
	line, err := flavor.AuditUIGates(rootDir, manifest)
	if line != "" {
		fmt.Println(line)
	}
	if err != nil {
		return err
	}
	return nil
}
