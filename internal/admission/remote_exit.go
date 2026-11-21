// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package admission

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// ErrExecutorLost is raised when the executor drops an SSH session before reporting the step status.
var ErrExecutorLost = errors.New("lost the step mid-run")

const remoteStepExitPrefix = "praetor-admit-step-complete "

type remoteStepStderr struct {
	inner        io.Writer
	tail         []byte
	stepFinished bool
}

// Write scans standard error output for the step completion marker and forwards bytes to the inner writer.
func (w *remoteStepStderr) Write(p []byte) (int, error) {
	combined := append(append([]byte(nil), w.tail...), p...)
	if hasStepExitMarker(combined) {
		w.stepFinished = true
	}
	if len(combined) > 64 {
		w.tail = append(w.tail[:0], combined[len(combined)-64:]...)
	} else {
		w.tail = append(w.tail[:0], combined...)
	}
	if w.inner == nil {
		return len(p), nil
	}
	return w.inner.Write(p)
}

// hasStepExitMarker reports whether buffer contains the step completion marker followed by a non-negative exit code.
func hasStepExitMarker(buf []byte) bool {
	idx := bytes.LastIndex(buf, []byte(remoteStepExitPrefix))
	if idx < 0 {
		return false
	}
	fields := strings.Fields(string(buf[idx+len(remoteStepExitPrefix):]))
	if len(fields) == 0 {
		return false
	}
	code, err := strconv.Atoi(fields[0])
	return err == nil && code >= 0
}

// isRemoteWrapperLossCode reports whether an exit status without a completion marker comes from
// an SSH transport disconnect (255) or the remote wrapper's SIGHUP, SIGINT, or SIGTERM traps (129, 130, 143).
func isRemoteWrapperLossCode(code int) bool {
	switch code {
	case 129, 130, 143, 255:
		return true
	default:
		return false
	}
}

// remoteRunFinished reports whether the remote execution completed its step or exited with a non-loss status.
func remoteRunFinished(stepFinished bool, err error) bool {
	if stepFinished {
		return true
	}
	var exit *exec.ExitError
	return errors.As(err, &exit) && !isRemoteWrapperLossCode(exit.ExitCode())
}

// remoteRunFailure evaluates an SSH execution failure, distinguishing between clean step completion and executor loss.
func remoteRunFailure(ctx context.Context, name string, stepFinished bool, err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var child *exec.ExitError
	if ctx.Err() != nil || !errors.As(err, &child) || child.ExitCode() <= 0 {
		return 0, fmt.Errorf("executor %s: SSH run: %w", name, err)
	}
	if remoteRunFinished(stepFinished, err) {
		return child.ExitCode(), nil
	}
	return 255, fmt.Errorf("executor %s %w (evicted or unreachable)", name, ErrExecutorLost)
}
