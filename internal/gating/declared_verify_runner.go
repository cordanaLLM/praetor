// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/util"
)

// declaredVerifyCaptureKey requests exact streams from the production stage runner.
type declaredVerifyCaptureKey struct{}

// declaredVerifyCapture belongs to one synchronous command invocation, never shared stages.
type declaredVerifyCapture struct {
	output *util.CommandBytes
}

// runStageCommand preserves the string runner contract except when its caller requests
// byte capture. Both paths reuse util's bounded execution, deadline and environment handling.
func runStageCommand(ctx context.Context, dir, name string, args ...string) (string, error) {
	capture, ok := ctx.Value(declaredVerifyCaptureKey{}).(*declaredVerifyCapture)
	if !ok {
		return util.RunCommand(ctx, dir, name, args...)
	}
	output, err := util.RunCommandBytes(ctx, dir, name, util.MaxCommandOutputBytes, args...)
	capture.output = &output
	return "", err
}

// runDeclaredVerifyCommand always calls the configured runner. Production supplies exact
// bounded streams through the context; injected string runners supply stdout and stderr in errors.
// Oversized injected output is bounded too, and produces an explicit capture-limit error.
func runDeclaredVerifyCommand(ctx context.Context, cfg *stageConfig, dir string) (util.CommandBytes, error) {
	run := cfg.run
	if run == nil {
		run = runStageCommand
	}
	capture := &declaredVerifyCapture{}
	out, err := run(context.WithValue(ctx, declaredVerifyCaptureKey{}, capture), dir, "make", declaredVerifyTarget)
	if capture.output != nil {
		return *capture.output, err
	}
	if len(out) > util.MaxCommandOutputBytes {
		err = errors.Join(err, fmt.Errorf("command output exceeds %d bytes per stream", util.MaxCommandOutputBytes))
		out = util.UTF8Tail(out, util.MaxCommandOutputBytes)
	}
	output := util.CommandBytes{Stdout: []byte(out)}
	if err != nil {
		output.Stderr = []byte(util.UTF8Tail(err.Error(), util.MaxCommandOutputBytes))
	}
	return output, err
}
