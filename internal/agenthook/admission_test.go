// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func admissionPolicy(t *testing.T, commands ...string) *Policy {
	t.Helper()
	policy, err := BuildPolicy(config.HookSettings{Admission: config.AdmissionSettings{Enforce: true, Commands: commands}})
	if err != nil {
		t.Fatal(err)
	}
	return policy
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
	if v := admissionPolicy(t).Command(`echo "unclosed && make`); v.Outcome != Deny || !strings.Contains(v.Reason, "unreadable") {
		t.Fatalf("an unreadable command: %v %q", v.Outcome, v.Reason)
	}
	if _, err := BuildPolicy(config.HookSettings{Admission: config.AdmissionSettings{Enforce: true, Commands: []string{"("}}}); err == nil {
		t.Fatal("a shape that does not compile built a policy")
	}
}
