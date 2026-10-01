// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"strings"
	"testing"
)

// Positive (#182): a oneshot service with no start timeout, a disabled timeout, a restart loop
// with its rate limit off and a command whose failure is ignored are reported, in a service and
// a socket unit. Before, unit files were outside every scanner.
func TestSystemdScanner_ReportsEachInvariant(t *testing.T) {
	rep := assertScriptFindings(t, "backup.service",
		"[Unit]\nDescription=Nightly backup\nStartLimitIntervalSec=0\n\n[Service]\nType=oneshot\n"+
			"ExecStartPre=-/usr/bin/mkdir -p /var/backups\nExecStart=/usr/local/bin/backup\nTimeoutStopSec=infinity\nRestart=on-failure\n",
		"HISS-02@3", "HISS-02@6", "HISS-07@7", "HISS-02@9")
	if rep.Coverage.FilesRead != 1 || rep.Coverage.LanguagesRead["systemd"] != 1 {
		t.Errorf("a service unit must be read as systemd, got %+v", rep.Coverage)
	}
	assertScriptFindings(t, "listen.socket", "[Socket]\nListenStream=8080\nTimeoutSec=0\nExecStartPre=@-/bin/prepare prepare\n",
		"HISS-02@3", "HISS-07@4")
	assertScriptFindings(t, "worker.service", "[Service]\nExecStart=/usr/bin/worker\nTimeoutStartSec=0s\nRestart=always\n"+
		"StartLimitInterval=0\nExecStopPost=/bin/cleanup ; -/bin/notify\n",
		"HISS-02@3", "HISS-02@5", "HISS-07@6")
}

// Negative: a bounded oneshot, a restarting daemon under the default rate limit, commented
// settings and a file with a unit extension that is not a unit are not reported, and the last
// file is not counted as systemd.
func TestSystemdScanner_LegitimateUnitsAreClean(t *testing.T) {
	assertScriptFindings(t, "job.service", "[Service]\nType=oneshot\nTimeoutStartSec=5min\nExecStart=/usr/bin/job\n")
	assertScriptFindings(t, "daemon.service", "[Unit]\nStartLimitIntervalSec=30\n[Service]\nType=simple\n"+
		"ExecStart=/usr/bin/daemon --flag=-x\nRestart=on-failure\nRestartSec=5\n# TimeoutStopSec=infinity\n; ExecStart=-/bin/x\n")
	assertScriptFindings(t, "norestart.service", "[Unit]\nStartLimitIntervalSec=0\n[Service]\nExecStart=/usr/bin/once\nRestart=no\n")
	rep := assertScriptFindings(t, "client.service", "endpoint: https://example.com\nretries: 3\n")
	if rep.Coverage.FilesRead != 0 || len(rep.Coverage.UnscannedLanguages) != 0 || rep.Coverage.UnscannedByExtension[".service"] != 1 {
		t.Errorf("a non-unit .service file must stay unscanned, non-source: %+v", rep.Coverage)
	}
}

// Negative and positive: only the settings systemd parses as command lines carry a "-" prefix
// that ignores a failure. ExecPaths=, NoExecPaths= and ExecSearchPath= are path lists, where "-"
// ignores a missing path (systemd.exec(5)), and a command key the section does not define is
// never run; ExecReloadPost= (systemd 259) and the socket's ExecStopPre= are commands.
func TestSystemdScanner_OnlyCommandSettingsAreExec(t *testing.T) {
	assertScriptFindings(t, "sandbox.service", "[Service]\nExecPaths=-/opt/app\nNoExecPaths=-/srv/data\n"+
		"ExecSearchPath=/opt/app/bin\nExecStart=/usr/bin/app\nExecReloadPost=-/usr/bin/app-notify\n",
		"HISS-07@6")
	assertScriptFindings(t, "sandbox.socket", "[Socket]\nListenStream=8080\nExecPaths=-/opt/app\n"+
		"ExecReload=-/bin/reload\nExecStopPre=-/bin/drain\n",
		"HISS-07@5")
}

// Boundary: the last assignment wins and an empty one resets, a backslash continues a setting
// across lines (skipping a comment line inside it), and a CRLF checkout reads like LF (HISS-21).
func TestSystemdScanner_Boundaries(t *testing.T) {
	assertScriptFindings(t, "reset.service", "[Service]\nType=oneshot\nTimeoutStartSec=30\nTimeoutStartSec=\nExecStart=/bin/x\n",
		"HISS-02@2")
	assertScriptFindings(t, "override.service", "[Service]\nTimeoutStopSec=infinity\nTimeoutStopSec=30\nExecStart=/bin/x\n")
	// TimeoutAbortSec=0 aborts at once, which is bounded; only infinity disables it.
	assertScriptFindings(t, "abort.service", "[Service]\nExecStart=/bin/x\nTimeoutAbortSec=0\n")
	assertScriptFindings(t, "abort-forever.service", "[Service]\nExecStart=/bin/x\nTimeoutAbortSec=infinity\n", "HISS-02@3")
	assertScriptFindings(t, "wrapped.service", "[Service]\nExecStart=/bin/run \\\n# comment\n  --long-option\nExecStartPre=- \\\n  /bin/prepare\n",
		"HISS-07@5")
	body := "[Service]\nType=oneshot\nExecStart=-/bin/x\n"
	assertScriptFindings(t, "lf.service", body, "HISS-02@2", "HISS-07@3")
	assertScriptFindings(t, "crlf.service", strings.ReplaceAll(body, "\n", "\r\n"), "HISS-02@2", "HISS-07@3")
}

// Positive and negative: a zero time span is any spelling whose numbers are all zero.
func TestIsZeroSpan(t *testing.T) {
	for value, want := range map[string]bool{
		"0": true, "0s": true, "0min": true, "00": true, "0.0": true,
		"": false, "5": false, "10s": false, "0.5s": false, "infinity": false, "-0": false,
	} {
		if got := isZeroSpan(value); got != want {
			t.Errorf("isZeroSpan(%q) = %t, want %t", value, got, want)
		}
	}
}
