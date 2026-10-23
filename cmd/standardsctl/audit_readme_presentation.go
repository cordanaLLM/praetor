// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/readmepresentation"
)

// auditReadmePresentation runs the shared README presentation standard gate (README-00..README-05).
func auditReadmePresentation(ctx context.Context, manifest *config.Manifest, opts *auditOptions) error {
	var maxStale *int
	if opts.maxStale >= 0 {
		maxStale = &opts.maxStale
	}
	line, err := readmepresentation.Audit(ctx, readmepresentation.Options{
		Root:     opts.rootDir,
		Baseline: opts.baseline,
		Enforce:  manifest != nil && manifest.Gate.EnforceReadmePresentation(),
		MaxStale: maxStale,
	})
	if err != nil {
		return err
	}
	fmt.Println(line)
	return nil
}
