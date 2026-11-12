// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

//go:build !linux

package sandbox

import (
	"context"
	"errors"
	"io"
)

// runSession rejects session launches on non-Linux systems because session identity requires Linux.
func runSession(context.Context, Invocation, string, []string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("session identity requires Linux")
}
