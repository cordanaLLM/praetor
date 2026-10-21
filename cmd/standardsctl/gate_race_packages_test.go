// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"github.com/cordanaLLM/praetor/internal/util"
	"slices"
	"testing"
)

func TestGateRunRacePackagesFlag(t *testing.T) {
	for _, raw := range []string{"none", "example.test/a,example.test/b", "./..."} {
		t.Run(raw, func(t *testing.T) {
			call := recordPipeline(t)
			_, err := captureStdout(t, func() error {
				return runGateRun([]string{"--path=" + t.TempDir(), "--race-packages=" + raw})
			})
			if err != nil || !call.called {
				t.Fatalf("explicit race scope refused: called=%v err=%v", call.called, err)
			}
			want, parseErr := util.ParseRacePackages(raw)
			if parseErr != nil || call.racePackages == nil || !slices.Equal(call.racePackages, want) {
				t.Fatalf("scope did not reach pipeline: %v parse=%v", call.racePackages, parseErr)
			}
		})
	}
}

func TestGateRunRacePackagesDefaultAndInvalid(t *testing.T) {
	call := recordPipeline(t)
	if _, err := captureStdout(t, func() error { return runGateRun([]string{"--path=" + t.TempDir()}) }); err != nil || !call.called || call.racePackages != nil {
		t.Fatalf("omitted scope must keep full pipeline: call=%+v err=%v", call, err)
	}
	for _, raw := range []string{"", "a,", "-args", "./...,a", "a\tb"} {
		t.Run(raw, func(t *testing.T) {
			seen := recordPipeline(t)
			if err := runGateRun([]string{"--race-packages=" + raw}); err == nil || seen.called {
				t.Fatalf("bad scope reached pipeline: called=%v err=%v", seen.called, err)
			}
		})
	}
}
