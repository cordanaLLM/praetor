package main

import (
	"context"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
)

// auditBuildWarnings runs the HISS-10 build-warnings gate standards_audit runs too, with the
// manifest's HISS-10 exceptions: every workflow lane that compiles C, C++, Rust or Go code must
// build with its toolchain's warnings-as-errors form (#816).
func auditBuildWarnings(ctx context.Context, manifest *config.Manifest, rootDir string) error {
	line, err := adopt.AuditBuildWarnings(ctx, adopt.BuildWarningsOptions{
		Root: rootDir, Exceptions: manifest.Exceptions, Today: time.Now(),
	})
	if err != nil {
		return err
	}
	fmt.Println(line)
	return nil
}
