// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"errors"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/sentinel"
)

const testGiB = 1 << 30

func stubHost(availGiB, swapGiB uint64) func() (sentinel.HostStats, error) {
	return func() (sentinel.HostStats, error) {
		return sentinel.HostStats{
			RAMTotalBytes:  64 * testGiB,
			RAMFreeBytes:   availGiB * testGiB,
			SwapMeasured:   true,
			SwapTotalBytes: 100 * testGiB,
			SwapFreeBytes:  swapGiB * testGiB,
		}, nil
	}
}

func admissionPolicy(t *testing.T, commands ...string) *Policy {
	t.Helper()
	policy, err := BuildPolicy(config.HookSettings{Admission: config.AdmissionSettings{Enforce: true, Commands: commands}})
	if err != nil {
		t.Fatal(err)
	}
	return policy.WithAdmissionHost(stubHost(30, 80), 32)
}

func scopeAdmissionPolicy(t *testing.T, availGiB, swapGiB uint64, cores int) *Policy {
	t.Helper()
	policy, err := BuildPolicy(config.HookSettings{
		Admission: config.AdmissionSettings{Enforce: true, ScopeForm: true, MaxJobsDivisor: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	return policy.WithAdmissionHost(stubHost(availGiB, swapGiB), cores)
}

// Positive: light commands, heavy steps run through praetorctl admit, and heavy words that are
// only quoted text, comments or here-document bodies all pass.
func TestAdmission_Positive_AdmittedAndLightCommandsPass(t *testing.T) {
	policy := admissionPolicy(t)
	for _, command := range []string{
		"git status && go vet ./internal/admission/",
		"praetorctl admit --label verify-all -- make verify-all",
		"cd /work/repo && GOFLAGS=-p=4 praetorctl admit --label go-test -- go test -race ./...",
		"env PRAETOR_TEST_STAGE_TIMEOUT=25m /usr/local/bin/standardsctl admit -- git push -u origin topic",
		`git commit -s -m "make verify-all passes; go test ./... too"`,
		"grep -n 'go test' Makefile # make test",
		"cat > msg.txt <<'EOF'\nmake verify-all\ngit push\nEOF\ngit commit -F msg.txt",
		"timeout 60 grep make Makefile",
	} {
		if v := policy.Command(command); v.Outcome != Allow {
			t.Errorf("%q refused: %s", command, v.Reason)
		}
	}
	if v, err := CheckAdmission(config.AdmissionSettings{Enforce: true}, "praetorctl admit -- make"); err != nil || v.Outcome != Allow {
		t.Fatalf("CheckAdmission positive: %v %v", v, err)
	}
}

// Negative (planted): a heavy step started outside praetorctl admit is refused, through lists,
// assignments, wrappers and substitutions, and the refusal names the admitted form.
func TestAdmission_Negative_HeavyStepsRefused(t *testing.T) {
	policy := admissionPolicy(t, `^just build( |$)`)
	for command, label := range map[string]string{
		"make verify-all":                                                         "make-verify-all",
		"cd x && GOFLAGS=-p=6 go test -race ./...":                                "go-test",
		"git -C ../repo push -u origin topic":                                     "git-push",
		"systemd-run --user --scope -q -p MemoryHigh=8G -- cargo build --release": "cargo-build",
		"nice -n 10 timeout 600 /usr/bin/make -j4 all":                            "make-all",
		"praetorctl admit -- make lint && go build ./...":                         "go-build",
		"x=$(go test ./internal/...)":                                             "go-test",
		"if pytest -q; then echo ok; fi":                                          "pytest",
		"just build":                                                              "just-build",
	} {
		v := policy.Command(command)
		if v.Outcome != Deny || !strings.Contains(v.Reason, "praetorctl admit --label "+label+" -- <command>") {
			t.Errorf("%q: %v %q, want refused with label %s", command, v.Outcome, v.Reason, label)
		}
	}
	if v, err := CheckAdmission(config.AdmissionSettings{Enforce: true}, "make"); err != nil || v.Outcome != Deny {
		t.Fatalf("CheckAdmission negative: %v %v", v, err)
	}
}

// Boundary: without enforcement nothing is judged, an unreadable command is refused while
// enforcement is on, and a shape that does not compile fails the policy build.
func TestAdmission_Boundary_OffUnreadableAndBadShape(t *testing.T) {
	off, err := BuildPolicy(config.HookSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if v := off.Command("make verify-all"); v.Outcome != Allow {
		t.Fatalf("admission off refused make: %s", v.Reason)
	}
	if v, err := CheckAdmission(config.AdmissionSettings{}, "make verify-all"); err != nil || v.Outcome != Allow {
		t.Fatalf("CheckAdmission off: %v %v", v, err)
	}
	if v := admissionPolicy(t).Command(`echo "unclosed && make`); v.Outcome != Deny || !strings.Contains(v.Reason, "unreadable") {
		t.Fatalf("an unreadable command: %v %q", v.Outcome, v.Reason)
	}
	if _, err := BuildPolicy(config.HookSettings{Admission: config.AdmissionSettings{Enforce: true, Commands: []string{"("}}}); err == nil {
		t.Fatal("a shape that does not compile built a policy")
	}
	if _, err := CheckAdmission(config.AdmissionSettings{Enforce: true, Commands: []string{"("}}, "make"); err == nil {
		t.Fatal("CheckAdmission accepted bad shape")
	}
}

func requireDeniedContaining(t *testing.T, label string, v Verdict, wants ...string) {
	t.Helper()
	if v.Outcome != Deny {
		t.Errorf("%s: got %v, want Deny (%q)", label, v.Outcome, v.Reason)
		return
	}
	for _, want := range wants {
		if !strings.Contains(v.Reason, want) {
			t.Errorf("%s: %q lacks %q", label, v.Reason, want)
		}
	}
}

// Positive and negative (planted, order engine-hook-build-scope-gate-20261027): with scope_form
// enabled, unwrapped make is refused, a complete systemd-run scope passes when host readings
// meet the 12 GiB / 15 GiB floor, and low available memory or swap free is refused naming the
// readings. praetorctl admit passes regardless of readings.
func TestAdmission_ScopeFormAndMemoryReadings(t *testing.T) {
	const scopedMake = "systemd-run --user --scope -p MemoryHigh=8G --expand-environment=no make"
	healthy := scopeAdmissionPolicy(t, 30, 80, 32)
	requireDeniedContaining(t, "unwrapped make", healthy.Command("make"), "praetorctl admit")
	for _, cmd := range []string{scopedMake, "systemd-run --user --scope --property=MemoryHigh=8G --expand-environment=no -- go test ./..."} {
		if v := healthy.Command(cmd); v.Outcome != Allow {
			t.Errorf("healthy scope %q refused: %s", cmd, v.Reason)
		}
	}
	lowMem := scopeAdmissionPolicy(t, 8, 80, 32)
	requireDeniedContaining(t, "low memory scope", lowMem.Command(scopedMake), "8.0 GiB", "80.0 GiB")
	lowSwap := scopeAdmissionPolicy(t, 30, 10, 32)
	requireDeniedContaining(t, "low swap scope", lowSwap.Command(scopedMake), "swap free 10.0 GiB", "30.0 GiB")
	if v := lowMem.Command("praetorctl admit -- make"); v.Outcome != Allow {
		t.Errorf("praetorctl admit under low memory refused: %s", v.Reason)
	}
	readFail := scopeAdmissionPolicy(t, 30, 80, 32).WithAdmissionHost(func() (sentinel.HostStats, error) {
		return sentinel.HostStats{}, errors.New("procfs unavailable")
	}, 32)
	requireDeniedContaining(t, "failed host reading", readFail.Command(scopedMake), "procfs unavailable")
}

// Negative (planted, order engine-hook-build-scope-gate-20261027): a partial systemd-run wrapper
// is refused naming the missing required option or property.
func TestAdmission_ScopeFormMissingWrapperPartsRefused(t *testing.T) {
	policy := scopeAdmissionPolicy(t, 30, 80, 32)
	for command, missing := range map[string]string{
		"systemd-run --user --scope -p MemoryHigh=8G make":                        "--expand-environment=no",
		"systemd-run --user --scope --expand-environment=no make":                 "MemoryHigh",
		"systemd-run --user --scope -p MemoryMax=8G --expand-environment=no make": "MemoryHigh",
		"systemd-run --scope -p MemoryHigh=8G --expand-environment=no make":       "--user",
		"systemd-run --user -p MemoryHigh=8G --expand-environment=no make":        "--scope",
	} {
		requireDeniedContaining(t, command, policy.Command(command), missing)
	}
}

// Positive and negative (planted, order engine-hook-build-scope-gate-20261027): heavy steps with
// job counts above floor(cores/divisor) or bare -j/--jobs are refused naming N, cores and limit;
// job counts within the limit pass when admitted.
func TestAdmission_JobCountCap(t *testing.T) {
	policy := scopeAdmissionPolicy(t, 30, 80, 32)
	for command, substrings := range map[string][]string{
		"make -j32":                     {"32", "32 cores", "limit 10"},
		"make -j 32":                    {"32", "32 cores", "limit 10"},
		"make -j":                       {"-j", "32 cores", "limit 10"},
		"make -j all":                   {"-j", "32 cores", "limit 10"},
		"cargo build --jobs=12":         {"12", "32 cores", "limit 10"},
		"cargo build --jobs 12":         {"12", "32 cores", "limit 10"},
		"cargo build --jobs":            {"--jobs", "32 cores", "limit 10"},
		"praetorctl admit -- make -j32": {"32", "32 cores", "limit 10"},
		"systemd-run --user --scope -p MemoryHigh=8G --expand-environment=no make -j32": {"32", "32 cores", "limit 10"},
	} {
		requireDeniedContaining(t, command, policy.Command(command), substrings...)
	}
	for _, allowed := range []string{
		"systemd-run --user --scope -p MemoryHigh=8G --expand-environment=no make -j8",
		"systemd-run --user --scope -p MemoryHigh=8G --expand-environment=no make -j 10",
		"praetorctl admit -- make -j8",
		`git commit -s -m "make -j32 verify-all"`,
	} {
		if v := policy.Command(allowed); v.Outcome != Allow {
			t.Errorf("%q refused: %s", allowed, v.Reason)
		}
	}
}
