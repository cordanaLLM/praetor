// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/readmepresentation"
)

// auditReadmePresentation runs the shared README presentation standard gate (README-01..README-05).
func auditReadmePresentation(ctx context.Context, manifest *config.Manifest, rootDir string) error {
	line, err := readmepresentation.Audit(ctx, readmepresentation.Options{
		Root:       rootDir,
		Exceptions: manifest.Exceptions,
		Today:      time.Now(),
	})
	if err != nil {
		return err
	}
	fmt.Println(line)
	return nil
}
