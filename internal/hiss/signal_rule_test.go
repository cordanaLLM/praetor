// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"testing"
)

type signalTestCase struct {
	name       string
	file       string
	code       string
	wantHISS08 int
}

func runSignalTests(t *testing.T, tests []signalTestCase) {
	t.Helper()
	for i := 0; i < len(tests); i++ {
		tc := tests[i]
		t.Run(tc.name, func(t *testing.T) {
			rep := scanFixtureFile(t, tc.file, tc.code)
			got := rep.Breakdown["HISS-08"]
			if got != tc.wantHISS08 {
				t.Fatalf("file %s: got HISS-08 count %d, want %d; violations: %+v",
					tc.file, got, tc.wantHISS08, rep.Violations)
			}
		})
	}
}

func TestHISS08_SignalCall_Go(t *testing.T) {
	tests := []signalTestCase{
		{
			name:       "syscall.Kill negative pid unguarded positive",
			file:       "kill.go",
			code:       "package main\nimport \"syscall\"\nfunc f(pid int) error { return syscall.Kill(-pid, syscall.SIGKILL) }\n",
			wantHISS08: 1,
		},
		{
			name:       "unix.Kill pid unguarded positive",
			file:       "unix.go",
			code:       "package main\nimport \"golang.org/x/sys/unix\"\nfunc f(pid int) error { return unix.Kill(pid, unix.SIGTERM) }\n",
			wantHISS08: 1,
		},
		{
			name:       "syscall.Kill probe signal 0 exempt negative",
			file:       "probe.go",
			code:       "package main\nimport \"syscall\"\nfunc f(pid int) error { return syscall.Kill(-pid, 0) }\n",
			wantHISS08: 0,
		},
		{
			name:       "syscall.Kill Process.Pid exempt negative",
			file:       "proc.go",
			code:       "package main\nimport (\n\t\"os/exec\"\n\t\"syscall\"\n)\nfunc f(cmd *exec.Cmd) error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }\n",
			wantHISS08: 0,
		},
		{
			name:       "syscall.Kill os.Getpid exempt negative",
			file:       "getpid.go",
			code:       "package main\nimport (\n\t\"os\"\n\t\"syscall\"\n)\nfunc f() error { return syscall.Kill(os.Getpid(), syscall.SIGTERM) }\n",
			wantHISS08: 0,
		},
		{
			name:       "syscall.Kill derived pid exempt negative",
			file:       "derived.go",
			code:       "package main\nimport (\n\t\"os/exec\"\n\t\"syscall\"\n)\nfunc f(cmd *exec.Cmd, grp bool) error {\n\tt := cmd.Process.Pid\n\tif grp {\n\t\tt = -t\n\t}\n\treturn syscall.Kill(t, syscall.SIGTERM)\n}\n",
			wantHISS08: 0,
		},
		{
			name:       "syscall.Kill guarded comparison negative boundary",
			file:       "guarded.go",
			code:       "package main\nimport \"syscall\"\nfunc f(pid int) error {\n\tif pid <= 1 {\n\t\treturn nil\n\t}\n\treturn syscall.Kill(-pid, syscall.SIGKILL)\n}\n",
			wantHISS08: 0,
		},
		{
			name:       "syscall.Kill guarded call negative boundary",
			file:       "guardcall.go",
			code:       "package main\nimport \"syscall\"\nfunc guardPID(pid int) {}\nfunc f(pid int) error {\n\tguardPID(pid)\n\treturn syscall.Kill(-pid, syscall.SIGKILL)\n}\n",
			wantHISS08: 0,
		},
		{
			name:       "syscall.Kill inside string literal negative",
			file:       "str.go",
			code:       "package main\nconst s = \"syscall.Kill(-pid, syscall.SIGKILL)\"\n",
			wantHISS08: 0,
		},
		// Bound comparison tests (flagged)
		{
			name:       "syscall.Kill weak guard pid > 0 positive",
			file:       "guard_gt0.go",
			code:       "package main\nimport \"syscall\"\nfunc f(pid int) error {\n\tif pid > 0 {\n\t\treturn syscall.Kill(-pid, syscall.SIGKILL)\n\t}\n\treturn nil\n}\n",
			wantHISS08: 1,
		},
		{
			name:       "syscall.Kill weak guard pid >= 1 positive",
			file:       "guard_ge1.go",
			code:       "package main\nimport \"syscall\"\nfunc f(pid int) error {\n\tif pid >= 1 {\n\t\treturn syscall.Kill(-pid, syscall.SIGKILL)\n\t}\n\treturn nil\n}\n",
			wantHISS08: 1,
		},
		{
			name:       "syscall.Kill weak guard pid != 0 positive",
			file:       "guard_ne0.go",
			code:       "package main\nimport \"syscall\"\nfunc f(pid int) error {\n\tif pid != 0 {\n\t\treturn syscall.Kill(-pid, syscall.SIGKILL)\n\t}\n\treturn nil\n}\n",
			wantHISS08: 1,
		},
		{
			name:       "syscall.Kill weak guard pid == 1 positive",
			file:       "guard_eq1.go",
			code:       "package main\nimport \"syscall\"\nfunc f(pid int) error {\n\tif pid == 1 {\n\t\treturn syscall.Kill(-pid, syscall.SIGKILL)\n\t}\n\treturn nil\n}\n",
			wantHISS08: 1,
		},
		{
			name:       "syscall.Kill weak guard 0 < pid positive",
			file:       "guard_0_lt.go",
			code:       "package main\nimport \"syscall\"\nfunc f(pid int) error {\n\tif 0 < pid {\n\t\treturn syscall.Kill(-pid, syscall.SIGKILL)\n\t}\n\treturn nil\n}\n",
			wantHISS08: 1,
		},
		// Bound comparison tests (passes)
		{
			name:       "syscall.Kill strong guard pid > 1 negative",
			file:       "guard_gt1.go",
			code:       "package main\nimport \"syscall\"\nfunc f(pid int) error {\n\tif pid > 1 {\n\t\treturn syscall.Kill(-pid, syscall.SIGKILL)\n\t}\n\treturn nil\n}\n",
			wantHISS08: 0,
		},
		{
			name:       "syscall.Kill strong guard pid >= 2 negative",
			file:       "guard_ge2.go",
			code:       "package main\nimport \"syscall\"\nfunc f(pid int) error {\n\tif pid >= 2 {\n\t\treturn syscall.Kill(-pid, syscall.SIGKILL)\n\t}\n\treturn nil\n}\n",
			wantHISS08: 0,
		},
		{
			name:       "syscall.Kill strong guard pid <= 1 refusing negative",
			file:       "guard_le1.go",
			code:       "package main\nimport \"syscall\"\nfunc f(pid int) error {\n\tif pid <= 1 {\n\t\treturn nil\n\t}\n\treturn syscall.Kill(-pid, syscall.SIGKILL)\n}\n",
			wantHISS08: 0,
		},
		{
			name:       "syscall.Kill strong guard pid < 2 negative",
			file:       "guard_lt2.go",
			code:       "package main\nimport \"syscall\"\nfunc f(pid int) error {\n\tif pid < 2 {\n\t\treturn nil\n\t}\n\treturn syscall.Kill(-pid, syscall.SIGKILL)\n}\n",
			wantHISS08: 0,
		},
		{
			name:       "syscall.Kill strong guard 1 < pid negative",
			file:       "guard_1_lt.go",
			code:       "package main\nimport \"syscall\"\nfunc f(pid int) error {\n\tif 1 < pid {\n\t\treturn syscall.Kill(-pid, syscall.SIGKILL)\n\t}\n\treturn nil\n}\n",
			wantHISS08: 0,
		},
		{
			name:       "syscall.Kill strong guard 2 <= pid negative",
			file:       "guard_2_le.go",
			code:       "package main\nimport \"syscall\"\nfunc f(pid int) error {\n\tif 2 <= pid {\n\t\treturn syscall.Kill(-pid, syscall.SIGKILL)\n\t}\n\treturn nil\n}\n",
			wantHISS08: 0,
		},
	}
	runSignalTests(t, tests)
}

func TestHISS08_SignalCall_Python(t *testing.T) {
	tests := []signalTestCase{
		{
			name:       "os.killpg incident shape positive",
			file:       "incident.py",
			code:       "import os, signal\ndef kill_process_group(process):\n    os.killpg(os.getpgid(process.pid), signal.SIGKILL)\n",
			wantHISS08: 1,
		},
		{
			name:       "os.killpg int raw positive",
			file:       "raw.py",
			code:       "import os, signal\ndef reap(raw):\n    os.killpg(int(raw), signal.SIGKILL)\n",
			wantHISS08: 1,
		},
		{
			name:       "bare killpg positive",
			file:       "bare.py",
			code:       "from os import killpg\nimport signal\ndef stop(pgid):\n    killpg(pgid, signal.SIGTERM)\n",
			wantHISS08: 1,
		},
		{
			name:       "os.kill negative pid positive",
			file:       "oskill.py",
			code:       "import os, signal\ndef stop_group(pid):\n    os.kill(-pid, signal.SIGTERM)\n",
			wantHISS08: 1,
		},
		{
			name:       "os.killpg probe signal 0 exempt negative",
			file:       "probe.py",
			code:       "import os\ndef probe(pgid):\n    os.killpg(pgid, 0)\n",
			wantHISS08: 0,
		},
		{
			name:       "os.killpg guarded comparison negative boundary",
			file:       "guarded.py",
			code:       "import os, signal\ndef reap(pgid):\n    if pgid <= 1:\n        return\n    os.killpg(pgid, signal.SIGKILL)\n",
			wantHISS08: 0,
		},
		{
			name:       "os.killpg guarded call negative boundary",
			file:       "guardcall.py",
			code:       "import os, signal\ndef reap(pgid):\n    assert_safe_pgid(pgid)\n    os.killpg(pgid, signal.SIGKILL)\n",
			wantHISS08: 0,
		},
		{
			name:       "os.killpg string literal negative",
			file:       "str.py",
			code:       "cmd = 'os.killpg(pgid, signal.SIGKILL)'\n",
			wantHISS08: 0,
		},
		// Bound comparison tests (flagged)
		{
			name:       "os.killpg weak guard pid > 0 positive",
			file:       "py_gt0.py",
			code:       "import os, signal\ndef f(pid):\n    if pid > 0:\n        os.killpg(pid, signal.SIGKILL)\n",
			wantHISS08: 1,
		},
		{
			name:       "os.killpg weak guard pid >= 1 positive",
			file:       "py_ge1.py",
			code:       "import os, signal\ndef f(pid):\n    if pid >= 1:\n        os.killpg(pid, signal.SIGKILL)\n",
			wantHISS08: 1,
		},
		{
			name:       "os.killpg weak guard pid != 0 positive",
			file:       "py_ne0.py",
			code:       "import os, signal\ndef f(pid):\n    if pid != 0:\n        os.killpg(pid, signal.SIGKILL)\n",
			wantHISS08: 1,
		},
		{
			name:       "os.killpg weak guard pid == 1 positive",
			file:       "py_eq1.py",
			code:       "import os, signal\ndef f(pid):\n    if pid == 1:\n        os.killpg(pid, signal.SIGKILL)\n",
			wantHISS08: 1,
		},
		{
			name:       "os.killpg weak guard 0 < pid positive",
			file:       "py_0_lt.py",
			code:       "import os, signal\ndef f(pid):\n    if 0 < pid:\n        os.killpg(pid, signal.SIGKILL)\n",
			wantHISS08: 1,
		},
		// Bound comparison tests (passes)
		{
			name:       "os.killpg strong guard pid > 1 negative",
			file:       "py_gt1.py",
			code:       "import os, signal\ndef f(pid):\n    if pid > 1:\n        os.killpg(pid, signal.SIGKILL)\n",
			wantHISS08: 0,
		},
		{
			name:       "os.killpg strong guard pid >= 2 negative",
			file:       "py_ge2.py",
			code:       "import os, signal\ndef f(pid):\n    if pid >= 2:\n        os.killpg(pid, signal.SIGKILL)\n",
			wantHISS08: 0,
		},
		{
			name:       "os.killpg strong guard pid <= 1 refusing negative",
			file:       "py_le1.py",
			code:       "import os, signal\ndef f(pid):\n    if pid <= 1:\n        return\n    os.killpg(pid, signal.SIGKILL)\n",
			wantHISS08: 0,
		},
		{
			name:       "os.killpg strong guard pid < 2 negative",
			file:       "py_lt2.py",
			code:       "import os, signal\ndef f(pid):\n    if pid < 2:\n        return\n    os.killpg(pid, signal.SIGKILL)\n",
			wantHISS08: 0,
		},
		{
			name:       "os.killpg strong guard 1 < pid negative",
			file:       "py_1_lt.py",
			code:       "import os, signal\ndef f(pid):\n    if 1 < pid:\n        os.killpg(pid, signal.SIGKILL)\n",
			wantHISS08: 0,
		},
		{
			name:       "os.killpg strong guard 2 <= pid negative",
			file:       "py_2_le.py",
			code:       "import os, signal\ndef f(pid):\n    if 2 <= pid:\n        os.killpg(pid, signal.SIGKILL)\n",
			wantHISS08: 0,
		},
	}
	runSignalTests(t, tests)
}

func TestHISS08_SignalCall_JavaScript(t *testing.T) {
	tests := []signalTestCase{
		{
			name:       "process.kill negative target positive",
			file:       "kill.js",
			code:       "export function stop(child) { process.kill(-child.pid, 'SIGKILL'); }\n",
			wantHISS08: 1,
		},
		{
			name:       "process.kill positive target negative",
			file:       "pos.js",
			code:       "export function stop(child) { process.kill(child.pid, 'SIGKILL'); }\n",
			wantHISS08: 0,
		},
		{
			name:       "process.kill probe 0 negative",
			file:       "probe.js",
			code:       "export function probe(pid) { process.kill(-pid, 0); }\n",
			wantHISS08: 0,
		},
		{
			name:       "process.kill guarded comparison negative boundary",
			file:       "guarded.js",
			code:       "export function stop(child) {\n  if (child.pid <= 1) return;\n  process.kill(-child.pid, 'SIGKILL');\n}\n",
			wantHISS08: 0,
		},
		{
			name:       "process.kill guarded call negative boundary",
			file:       "guardcall.js",
			code:       "export function stop(pid) {\n  assertSafePid(pid);\n  process.kill(-pid, 'SIGKILL');\n}\n",
			wantHISS08: 0,
		},
		{
			name:       "process.kill string literal negative",
			file:       "str.js",
			code:       "export const cmd = 'process.kill(-child.pid, \"SIGKILL\")';\n",
			wantHISS08: 0,
		},
		// Bound comparison tests (flagged)
		{
			name:       "process.kill weak guard pid > 0 positive",
			file:       "js_gt0.js",
			code:       "export function stop(pid) { if (pid > 0) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 1,
		},
		{
			name:       "process.kill weak guard pid >= 1 positive",
			file:       "js_ge1.js",
			code:       "export function stop(pid) { if (pid >= 1) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 1,
		},
		{
			name:       "process.kill weak guard pid != 0 positive",
			file:       "js_ne0.js",
			code:       "export function stop(pid) { if (pid != 0) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 1,
		},
		{
			name:       "process.kill weak guard pid == 1 positive",
			file:       "js_eq1.js",
			code:       "export function stop(pid) { if (pid == 1) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 1,
		},
		{
			name:       "process.kill weak guard 0 < pid positive",
			file:       "js_0_lt.js",
			code:       "export function stop(pid) { if (0 < pid) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 1,
		},
		// Bound comparison tests (passes)
		{
			name:       "process.kill strong guard pid > 1 negative",
			file:       "js_gt1.js",
			code:       "export function stop(pid) { if (pid > 1) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 0,
		},
		{
			name:       "process.kill strong guard pid >= 2 negative",
			file:       "js_ge2.js",
			code:       "export function stop(pid) { if (pid >= 2) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 0,
		},
		{
			name:       "process.kill strong guard pid <= 1 refusing negative",
			file:       "js_le1.js",
			code:       "export function stop(pid) { if (pid <= 1) return; process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 0,
		},
		{
			name:       "process.kill strong guard pid < 2 negative",
			file:       "js_lt2.js",
			code:       "export function stop(pid) { if (pid < 2) return; process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 0,
		},
		{
			name:       "process.kill strong guard 1 < pid negative",
			file:       "js_1_lt.js",
			code:       "export function stop(pid) { if (1 < pid) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 0,
		},
		{
			name:       "process.kill strong guard 2 <= pid negative",
			file:       "js_2_le.js",
			code:       "export function stop(pid) { if (2 <= pid) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 0,
		},
	}
	runSignalTests(t, tests)
}

func TestHISS08_SignalCall_TypeScript(t *testing.T) {
	tests := []signalTestCase{
		{
			name:       "process.kill negative target positive",
			file:       "kill.ts",
			code:       "export function stop(child: { pid: number }): void { process.kill(-child.pid, 'SIGKILL'); }\n",
			wantHISS08: 1,
		},
		{
			name:       "process.kill probe 0 negative",
			file:       "probe.ts",
			code:       "export function probe(pid: number): void { process.kill(-pid, 0); }\n",
			wantHISS08: 0,
		},
		{
			name:       "process.kill guarded comparison negative boundary",
			file:       "guarded.ts",
			code:       "export function stop(child: { pid: number }): void {\n  if (child.pid <= 1) return;\n  process.kill(-child.pid, 'SIGKILL');\n}\n",
			wantHISS08: 0,
		},
		// Bound comparison tests (flagged)
		{
			name:       "process.kill weak guard pid > 0 positive",
			file:       "ts_gt0.ts",
			code:       "export function stop(pid: number): void { if (pid > 0) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 1,
		},
		{
			name:       "process.kill weak guard pid >= 1 positive",
			file:       "ts_ge1.ts",
			code:       "export function stop(pid: number): void { if (pid >= 1) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 1,
		},
		{
			name:       "process.kill weak guard pid != 0 positive",
			file:       "ts_ne0.ts",
			code:       "export function stop(pid: number): void { if (pid != 0) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 1,
		},
		{
			name:       "process.kill weak guard pid == 1 positive",
			file:       "ts_eq1.ts",
			code:       "export function stop(pid: number): void { if (pid == 1) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 1,
		},
		{
			name:       "process.kill weak guard 0 < pid positive",
			file:       "ts_0_lt.ts",
			code:       "export function stop(pid: number): void { if (0 < pid) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 1,
		},
		// Bound comparison tests (passes)
		{
			name:       "process.kill strong guard pid > 1 negative",
			file:       "ts_gt1.ts",
			code:       "export function stop(pid: number): void { if (pid > 1) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 0,
		},
		{
			name:       "process.kill strong guard pid >= 2 negative",
			file:       "ts_ge2.ts",
			code:       "export function stop(pid: number): void { if (pid >= 2) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 0,
		},
		{
			name:       "process.kill strong guard pid <= 1 refusing negative",
			file:       "ts_le1.ts",
			code:       "export function stop(pid: number): void { if (pid <= 1) return; process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 0,
		},
		{
			name:       "process.kill strong guard pid < 2 negative",
			file:       "ts_lt2.ts",
			code:       "export function stop(pid: number): void { if (pid < 2) return; process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 0,
		},
		{
			name:       "process.kill strong guard 1 < pid negative",
			file:       "ts_1_lt.ts",
			code:       "export function stop(pid: number): void { if (1 < pid) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 0,
		},
		{
			name:       "process.kill strong guard 2 <= pid negative",
			file:       "ts_2_le.ts",
			code:       "export function stop(pid: number): void { if (2 <= pid) process.kill(-pid, 'SIGKILL'); }\n",
			wantHISS08: 0,
		},
	}
	runSignalTests(t, tests)
}
