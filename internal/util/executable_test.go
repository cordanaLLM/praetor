// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"io/fs"
	"testing"
	"time"
)

// modeInfo is an fs.FileInfo with a chosen mode, so every platform's answer is tested on every
// host without depending on the file system that runs the test.
type modeInfo fs.FileMode

func (m modeInfo) Name() string       { return "tool" }
func (m modeInfo) Size() int64        { return 0 }
func (m modeInfo) Mode() fs.FileMode  { return fs.FileMode(m) }
func (m modeInfo) ModTime() time.Time { return time.Time{} }
func (m modeInfo) IsDir() bool        { return fs.FileMode(m).IsDir() }
func (m modeInfo) Sys() any           { return nil }

// Positive: an execute bit on Unix and an .exe or .com extension (any case) on Windows.
func TestExecutableFile_Positive(t *testing.T) {
	for _, c := range []struct {
		path, goos string
		mode       fs.FileMode
	}{
		{"/bin/tool", "linux", 0o755}, {"/bin/tool", "darwin", 0o100},
		{`C:\bin\tool.exe`, "windows", 0o666}, {`C:\bin\TOOL.COM`, "windows", 0o444},
	} {
		if !ExecutableFile(c.path, modeInfo(c.mode), c.goos) {
			t.Errorf("ExecutableFile(%q, %v, %s) = false, want true", c.path, c.mode, c.goos)
		}
	}
}

// Negative: no execute bit on Unix, a batch file or a plain file on Windows whatever its bits,
// and a directory or a link however executable its mode reads.
func TestExecutableFile_Negative(t *testing.T) {
	for _, c := range []struct {
		path, goos string
		mode       fs.FileMode
	}{
		{"/bin/tool", "linux", 0o644}, {`C:\bin\tool.bat`, "windows", 0o777}, {`C:\bin\tool`, "windows", 0o777},
		{"/bin", "linux", fs.ModeDir | 0o755}, {"/bin/link", "linux", fs.ModeSymlink | 0o777},
	} {
		if ExecutableFile(c.path, modeInfo(c.mode), c.goos) {
			t.Errorf("ExecutableFile(%q, %v, %s) = true, want false", c.path, c.mode, c.goos)
		}
	}
}

// Boundary: no file information is no executable, and a single group or other bit is enough on
// Unix.
func TestExecutableFile_Boundary(t *testing.T) {
	if ExecutableFile("/bin/tool", nil, "linux") {
		t.Error("nil info reported executable")
	}
	for _, mode := range []fs.FileMode{0o010, 0o001} {
		if !ExecutableFile("/bin/tool", modeInfo(mode), "linux") {
			t.Errorf("mode %v reported not executable", mode)
		}
	}
}
