// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// The environment the fake jscpd reads. A test binary whose TestMain calls
// RunFakeJscpdIfRequested acts as jscpd when FakeJscpdEnv is "1", so a dedupe scan can run its
// foreign clone detector on every platform without Node or a shell script (order
// engine-dedupe-jscpd-skip-fails-closed-20261027).
const (
	// FakeJscpdEnv turns the test binary into the fake jscpd.
	FakeJscpdEnv = "PRAETOR_TEST_FAKE_JSCPD"
	// FakeJscpdVersionEnv is the line the fake prints for --version; empty means the default
	// RunFakeJscpdIfRequested is given.
	FakeJscpdVersionEnv = "PRAETOR_TEST_FAKE_JSCPD_VERSION"
	// FakeJscpdReportEnv is the report the fake writes; empty means no duplicates.
	FakeJscpdReportEnv = "PRAETOR_TEST_FAKE_JSCPD_REPORT_CONTENT"
	// FakeJscpdOversizedEnv set to "1" makes the fake write a 17 MiB report, above the scanner's bound.
	FakeJscpdOversizedEnv = "PRAETOR_TEST_FAKE_JSCPD_OVERSIZED"
	// jscpdBinaryEnv is the variable the dedupe scanner resolves jscpd from.
	jscpdBinaryEnv = "PRAETOR_JSCPD_BIN"
	// fakeJscpdOversizedBytes exceeds the scanner's 16 MiB report bound.
	fakeJscpdOversizedBytes = 17 << 20
)

// UseFakeJscpd makes this test binary the jscpd a dedupe scan runs, answering --version with
// version and writing a report without duplicates unless FakeJscpdReportEnv says otherwise. The
// package's TestMain calls RunFakeJscpdIfRequested.
func UseFakeJscpd(t testing.TB, version string) {
	t.Helper()
	t.Setenv(FakeJscpdEnv, "1")
	t.Setenv(jscpdBinaryEnv, os.Args[0])
	t.Setenv(FakeJscpdVersionEnv, version)
}

// RunFakeJscpdIfRequested acts as jscpd when FakeJscpdEnv is "1" and reports ran with the exit
// status, which TestMain passes to os.Exit; otherwise ran is false and nothing ran.
// defaultVersion answers --version when FakeJscpdVersionEnv is empty.
func RunFakeJscpdIfRequested(defaultVersion string) (code int, ran bool) {
	if os.Getenv(FakeJscpdEnv) != "1" {
		return 0, false
	}
	return RunFakeJscpd(os.Args[1:], defaultVersion, os.Stdout, os.Stderr), true
}

// RunFakeJscpd is the fake's body: --version prints the version line, and a scan reads the
// output folder from the -c configuration and writes jscpd-report.json there. It returns the
// exit status.
func RunFakeJscpd(args []string, defaultVersion string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "--version" {
		version := os.Getenv(FakeJscpdVersionEnv)
		if version == "" {
			version = defaultVersion
		}
		if _, err := fmt.Fprintln(stdout, version); err != nil {
			return 1
		}
		return 0
	}
	outDir, err := fakeJscpdOutDir(args)
	if err != nil {
		if _, werr := fmt.Fprintln(stderr, "fake jscpd:", err); werr != nil {
			return 2
		}
		return 1
	}
	if err := writeInDir(outDir, "jscpd-report.json", fakeJscpdReport()); err != nil {
		if _, werr := fmt.Fprintln(stderr, "fake jscpd: write report:", err); werr != nil {
			return 2
		}
		return 1
	}
	return 0
}

// readInDir reads the file name names inside dir, confined to dir through os.Root.
func readInDir(dir, name string) (data []byte, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	return root.ReadFile(name)
}

// writeInDir writes data to the file name names inside dir, confined to dir through os.Root.
func writeInDir(dir, name string, data []byte) (err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	return root.WriteFile(name, data, 0o600)
}

// fakeJscpdOutDir reads the output field of the configuration file the -c argument names.
func fakeJscpdOutDir(args []string) (string, error) {
	cfgPath := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-c" {
			cfgPath = args[i+1]
			break
		}
	}
	if cfgPath == "" {
		return "", fmt.Errorf("missing -c flag")
	}
	data, err := readInDir(filepath.Dir(cfgPath), filepath.Base(cfgPath))
	if err != nil {
		return "", fmt.Errorf("read config %s: %w", cfgPath, err)
	}
	var cfg struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("parse config %s: %w", cfgPath, err)
	}
	if cfg.Output == "" {
		return "", fmt.Errorf("config %s names no output folder", cfgPath)
	}
	return cfg.Output, nil
}

// fakeJscpdReport is the report the fake writes: oversized, the canned content, or no duplicates.
func fakeJscpdReport() []byte {
	if os.Getenv(FakeJscpdOversizedEnv) == "1" {
		oversized := make([]byte, fakeJscpdOversizedBytes)
		for i := range oversized {
			oversized[i] = ' '
		}
		return oversized
	}
	if canned := os.Getenv(FakeJscpdReportEnv); canned != "" {
		return []byte(canned)
	}
	return []byte(`{"duplicates":[]}`)
}
