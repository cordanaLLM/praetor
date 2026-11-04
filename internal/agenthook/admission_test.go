// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/admission"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/sentinel"
	"github.com/cordanaLLM/praetor/internal/util"
)

// testToday fixes the reference time for driver test fixtures.
var testToday = time.Date(2026, time.October, 31, 10, 0, 0, 0, time.UTC)

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

func initAdmissionRepo(t *testing.T, dir, branch, originURL string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := util.RunGit(ctx, dir, "init", "-q", "-b", branch); err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(ctx, dir, "-c", "user.name=t", "-c", "user.email=t@e", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-q", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	if originURL != "" {
		if _, err := util.RunGit(ctx, dir, "remote", "add", "origin", originURL); err != nil {
			t.Fatal(err)
		}
		if _, err := util.RunGit(ctx, dir, "update-ref", "refs/remotes/origin/"+branch, "HEAD"); err != nil {
			t.Fatal(err)
		}
		if _, err := util.RunGit(ctx, dir, "config", "branch."+branch+".remote", "origin"); err != nil {
			t.Fatal(err)
		}
		if _, err := util.RunGit(ctx, dir, "config", "branch."+branch+".merge", "refs/heads/"+branch); err != nil {
			t.Fatal(err)
		}
	}
}

func defaultAdmissionRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	initAdmissionRepo(t, repo, "main", "https://github.com/acme/widgets.git")
	initAdmissionRepo(t, filepath.Join(repo, "dir"), "main", "https://github.com/acme/widgets.git")
	return repo
}

func admissionPolicy(t *testing.T, commands ...string) *Policy {
	t.Helper()
	policy, err := BuildPolicy(config.HookSettings{Admission: config.AdmissionSettings{Enforce: true, Commands: commands}})
	if err != nil {
		t.Fatal(err)
	}
	repo := defaultAdmissionRepo(t)
	return policy.WithAdmissionHost(stubHost(30, 80), 32).WithAdmissionPullRequests(repo, func(context.Context, PushTarget) ([]forge.PullRequestHead, error) {
		return nil, nil
	})
}

func scopeAdmissionPolicy(t *testing.T, availGiB, swapGiB uint64, cores int) *Policy {
	t.Helper()
	policy, err := BuildPolicy(config.HookSettings{
		Admission: config.AdmissionSettings{Enforce: true, ScopeForm: true, MaxJobsDivisor: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	repo := defaultAdmissionRepo(t)
	return policy.WithAdmissionHost(stubHost(availGiB, swapGiB), cores).WithAdmissionPullRequests(repo, func(context.Context, PushTarget) ([]forge.PullRequestHead, error) {
		return nil, nil
	})
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

// Positive and negative (planted): only gate run is a heavy step; other gate subcommands
// and bare gate usage run without admission, and admitted gate run passes.
func TestAdmission_GateRunOnly(t *testing.T) {
	policy := admissionPolicy(t)
	for command, label := range map[string]string{
		"praetorctl gate run --path=.": "praetorctl-run",
		"standardsctl gate run":        "standardsctl-run",
		"cd x && praetorctl gate run":  "praetorctl-run",
		"nice praetorctl gate run":     "praetorctl-run",
	} {
		v := policy.Command(command)
		if v.Outcome != Deny || !strings.Contains(v.Reason, "praetorctl admit --label "+label+" -- <command>") {
			t.Errorf("%q: %v %q, want refused with label %s", command, v.Outcome, v.Reason, label)
		}
	}
	for _, command := range []string{
		"praetorctl gate verify --path=. --receipt r.json",
		"praetorctl gate deadline --json",
		"praetorctl gate keygen",
		"praetorctl gate",
		"praetorctl admit --label g -- praetorctl gate run",
	} {
		if v := policy.Command(command); v.Outcome != Allow {
			t.Errorf("%q refused: %s", command, v.Reason)
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
		// admit's own flags (cmd/standardsctl/admit.go) end at `--`; the step after it is judged.
		"praetorctl admit --timeout 30m --max-load 40 --memory-swap-max 2G -- make -j32": {"32", "32 cores", "limit 10"},
		"systemd-run --user --scope -p MemoryHigh=8G --expand-environment=no make -j32":  {"32", "32 cores", "limit 10"},
	} {
		requireDeniedContaining(t, command, policy.Command(command), substrings...)
	}
	for _, allowed := range []string{
		"systemd-run --user --scope -p MemoryHigh=8G --expand-environment=no make -j8",
		"systemd-run --user --scope -p MemoryHigh=8G --expand-environment=no make -j 10",
		"praetorctl admit -- make -j8",
		"praetorctl admit --label verify-all --timeout 30m -- make -j8",
		"praetorctl admit --label idle",
		`git commit -s -m "make -j32 verify-all"`,
	} {
		if v := policy.Command(allowed); v.Outcome != Allow {
			t.Errorf("%q refused: %s", allowed, v.Reason)
		}
	}
}

func TestAdmission_IsolatedPush_Positive(t *testing.T) {
	policy := admissionPolicy(t)
	for _, command := range []string{
		"PRAETOR_ADMIT=git-push git push -u origin b",
		"PRAETOR_ADMIT=x git -C dir push",
		"PRAETOR_ADMIT=x git -c k=v push",
		"OTHER=value PRAETOR_ADMIT=git-push git push",
		"PRAETOR_ADMIT= PRAETOR_ADMIT=x git push",
		"PRAETOR_ADMIT=" + strings.Repeat("a", 31) + " git push",
		"praetorctl admit --label git-push -- git push",
	} {
		if v := policy.Command(command); v.Outcome != Allow {
			t.Errorf("%q refused: %s", command, v.Reason)
		}
	}
}

func TestAdmission_IsolatedPush_Negative(t *testing.T) {
	policy := admissionPolicy(t)
	for _, command := range []string{
		"PRAETOR_ADMIT=x make",
		"PRAETOR_ADMIT=x go test",
		"PRAETOR_ADMIT=Bad git push",
		"PRAETOR_ADMIT= git push",
		"PRAETOR_ADMIT=-bad git push",
		"PRAETOR_ADMIT=" + strings.Repeat("a", 32) + " git push",
		"PRAETOR_ADMIT=x PRAETOR_ADMIT= git push",
		"env PRAETOR_ADMIT=x git push",
		"git push PRAETOR_ADMIT=x",
		"PRAETOR_ADMIT=x git push; git push",
	} {
		requireDeniedContaining(t, command, policy.Command(command), "host admission")
	}
	requireDeniedContaining(t, "plain push", policy.Command("git push -u origin b"),
		"praetorctl admit --label git-push -- <command>", "PRAETOR_ADMIT=git-push git push")
	for _, command := range []string{
		"PRAETOR_ADMIT=git-push git push; make",
		"PRAETOR_ADMIT=git-push git push\nmake",
		"PRAETOR_ADMIT=git-push git push && make",
	} {
		requireDeniedContaining(t, command, policy.Command(command), "host admission: make")
	}
	if v := policy.Command("make"); strings.Contains(v.Reason, "PRAETOR_ADMIT") {
		t.Errorf("non-push refusal advertises push assignment: %s", v.Reason)
	}
}

func TestAdmission_IsolatedPush_HostReadings(t *testing.T) {
	healthy, err := stubHost(30, 80)()
	if err != nil {
		t.Fatal(err)
	}
	maxLoad := admission.HostCappedPolicy(healthy).MaxLoad1
	for _, tc := range []struct {
		name         string
		memory, swap uint64
		load         float64
		want         string
	}{
		{"memory", 8, 80, 0, "available 8.0 GiB"},
		{"swap", 30, 10, 0, "swap free 10.0 GiB"},
		{"load", 30, 80, maxLoad + 1, "load"},
		{"boundary", 12, 15, maxLoad, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stats := healthy
			stats.RAMFreeBytes, stats.SwapFreeBytes = tc.memory*testGiB, tc.swap*testGiB
			stats.CPULoadMeasured, stats.CPULoad1Min = true, tc.load
			policy := admissionPolicy(t).WithAdmissionHost(func() (sentinel.HostStats, error) {
				return stats, nil
			}, 32)
			v := policy.Command("PRAETOR_ADMIT=git-push git push")
			if tc.want == "" {
				if v.Outcome != Allow {
					t.Errorf("threshold refused: %s", v.Reason)
				}
				return
			}
			requireDeniedContaining(t, tc.name, v, tc.want, "readings:", "available", "swap free", "retry")
			if v := policy.Command("praetorctl admit --label git-push -- git push"); v.Outcome != Allow {
				t.Errorf("launcher path changed: %s", v.Reason)
			}
		})
	}
}

func TestAdmission_IsolatedPush_HostReadError(t *testing.T) {
	policy := admissionPolicy(t).WithAdmissionHost(func() (sentinel.HostStats, error) {
		return sentinel.HostStats{}, errors.New("procfs unavailable")
	}, 32)
	requireDeniedContaining(t, "failed reading", policy.Command("PRAETOR_ADMIT=x git push"),
		"host reading failed", "procfs unavailable", "retry")
}

// Planted negative: pushing to a branch headed by a MERGED or CLOSED pull request (with no OPEN
// pull request on that branch) is refused on both the PRAETOR_ADMIT and praetorctl admit paths,
// naming the pull request number, its state (MERGED or CLOSED), and instructing to push to a new branch.
func TestAdmission_GitPushHead_Planted_MergedAndClosedRefused(t *testing.T) {
	repo := defaultAdmissionRepo(t)
	policy := admissionPolicy(t).WithAdmissionPullRequests(repo, func(_ context.Context, target PushTarget) ([]forge.PullRequestHead, error) {
		switch target.Branch {
		case "merged-topic":
			return []forge.PullRequestHead{{Number: 42, State: forge.PullRequestStateMerged, HeadBranch: "merged-topic"}}, nil
		case "closed-topic":
			return []forge.PullRequestHead{{Number: 19, State: forge.PullRequestStateClosed, HeadBranch: "closed-topic"}}, nil
		default:
			return nil, nil
		}
	})
	for _, cmd := range []string{
		"PRAETOR_ADMIT=git-push git push -u origin merged-topic",
		"praetorctl admit --label git-push -- git push -u origin merged-topic",
		"PRAETOR_ADMIT=git-push git push --tags origin merged-topic",
		"PRAETOR_ADMIT=git-push git push origin tag v1 merged-topic",
	} {
		requireDeniedContaining(t, cmd, policy.Command(cmd), "#42", "MERGED", "push to a new branch")
	}
	for _, cmd := range []string{
		"PRAETOR_ADMIT=git-push git push -u origin closed-topic",
		"praetorctl admit --label git-push -- git push -u origin closed-topic",
	} {
		requireDeniedContaining(t, cmd, policy.Command(cmd), "#19", "CLOSED", "push to a new branch")
	}
}

// Planted positive: pushing to a branch headed by an OPEN pull request (even when an older
// MERGED or CLOSED pull request used the same branch name) or to a new branch with no pull
// request passes on both admission paths.
func TestAdmission_GitPushHead_Planted_OpenAndNewBranchPass(t *testing.T) {
	repo := defaultAdmissionRepo(t)
	policy := admissionPolicy(t).WithAdmissionPullRequests(repo, func(_ context.Context, target PushTarget) ([]forge.PullRequestHead, error) {
		if target.Branch == "open-topic" {
			return []forge.PullRequestHead{
				{Number: 42, State: forge.PullRequestStateMerged, HeadBranch: "open-topic"},
				{Number: 55, State: forge.PullRequestStateOpen, HeadBranch: "open-topic"},
			}, nil
		}
		return nil, nil
	})
	for _, cmd := range []string{
		"PRAETOR_ADMIT=git-push git push -u origin open-topic",
		"praetorctl admit --label git-push -- git push -u origin open-topic",
		"PRAETOR_ADMIT=git-push git push -u origin brand-new-branch",
		"praetorctl admit --label git-push -- git push -u origin brand-new-branch",
	} {
		if v := policy.Command(cmd); v.Outcome != Allow {
			t.Errorf("%q refused: %s", cmd, v.Reason)
		}
	}
}

// Planted negative: a forge read failure fails closed with a named refusal on both paths.
func TestAdmission_GitPushHead_Planted_ForgeReadFailureRefused(t *testing.T) {
	repo := defaultAdmissionRepo(t)
	policy := admissionPolicy(t).WithAdmissionPullRequests(repo, func(context.Context, PushTarget) ([]forge.PullRequestHead, error) {
		return nil, errors.New("forge api 503 unavailable")
	})
	for _, cmd := range []string{
		"PRAETOR_ADMIT=git-push git push -u origin topic",
		"praetorctl admit --label git-push -- git push -u origin topic",
	} {
		requireDeniedContaining(t, cmd, policy.Command(cmd),
			"git push forge pull request check failed", "forge api 503 unavailable")
	}
}

// Planted boundary: delete pushes (--delete, -d, :branch) and tag pushes (--tags, tag <name>,
// refs/tags/<name>) are not judged and never query the forge.
func TestAdmission_GitPushHead_Planted_DeleteAndTagPushesNotJudged(t *testing.T) {
	repo := defaultAdmissionRepo(t)
	calls := 0
	policy := admissionPolicy(t).WithAdmissionPullRequests(repo, func(context.Context, PushTarget) ([]forge.PullRequestHead, error) {
		calls++
		return []forge.PullRequestHead{{Number: 99, State: forge.PullRequestStateMerged, HeadBranch: "merged-topic"}}, nil
	})
	for _, cmd := range []string{
		"PRAETOR_ADMIT=git-push git push --delete origin merged-topic",
		"PRAETOR_ADMIT=git-push git push -d origin merged-topic",
		"PRAETOR_ADMIT=git-push git push -fd origin merged-topic",
		"PRAETOR_ADMIT=git-push git push origin :merged-topic",
		"PRAETOR_ADMIT=git-push git push origin +:merged-topic",
		"praetorctl admit --label git-push -- git push --delete origin merged-topic",
		"praetorctl admit --label git-push -- git push origin :merged-topic",
		"PRAETOR_ADMIT=git-push git push --tags origin",
		"PRAETOR_ADMIT=git-push git push origin tag v1",
		"PRAETOR_ADMIT=git-push git push origin tag v1.2.3",
		"PRAETOR_ADMIT=git-push git push origin refs/tags/v1.2.3",
		"PRAETOR_ADMIT=git-push git push origin HEAD:refs/tags/v1.2.3",
	} {
		if v := policy.Command(cmd); v.Outcome != Allow {
			t.Errorf("%q refused: %s", cmd, v.Reason)
		}
	}
	if calls != 0 {
		t.Fatalf("delete/tag pushes queried the forge %d times, want 0", calls)
	}
}

// Target resolution covers invocation working directory, git -C, named remote vs origin, and
// refspec destination branch vs HEAD resolved through git.
func TestAdmission_GitPushHead_TargetResolution_PositiveAndBoundary(t *testing.T) {
	repo := t.TempDir()
	initAdmissionRepo(t, repo, "current-head", "https://github.com/acme/widgets.git")
	if _, err := util.RunGit(context.Background(), repo, "remote", "add", "upstream", "git@codeberg.org:fork/widgets.git"); err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(context.Background(), repo, "update-ref", "refs/remotes/upstream/current-head", "HEAD"); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "sub")
	initAdmissionRepo(t, sub, "sub-branch", "https://forgejo.example.org/acme/subrepo.git")

	var got []PushTarget
	policy := admissionPolicy(t).WithAdmissionPullRequests(repo, func(_ context.Context, target PushTarget) ([]forge.PullRequestHead, error) {
		got = append(got, target)
		return nil, nil
	})

	cases := []struct {
		workDir    string
		cmd        string
		wantOwner  string
		wantRepo   string
		wantForge  config.Forge
		wantBranch string
	}{
		{repo, "PRAETOR_ADMIT=git-push git push", "acme", "widgets", config.ForgeGitHub, "current-head"},
		{repo, "PRAETOR_ADMIT=git-push git push origin HEAD", "acme", "widgets", config.ForgeGitHub, "current-head"},
		{repo, "PRAETOR_ADMIT=git-push git push --repo=upstream", "fork", "widgets", config.ForgeForgejo, "current-head"},
		{repo, "PRAETOR_ADMIT=git-push git push upstream HEAD:feature-dst", "fork", "widgets", config.ForgeForgejo, "feature-dst"},
		{repo, "PRAETOR_ADMIT=git-push git push upstream local:refs/heads/mapped", "fork", "widgets", config.ForgeForgejo, "mapped"},
		{repo, "PRAETOR_ADMIT=git-push git -C sub push origin +HEAD:refs/for/review-target", "acme", "subrepo", config.ForgeForgejo, "review-target"},
		{sub, "praetorctl admit --label git-push -- git push", "acme", "subrepo", config.ForgeForgejo, "sub-branch"},
	}
	for _, tc := range cases {
		got = nil
		if v := policy.CommandIn(t.Context(), tc.cmd, tc.workDir); v.Outcome != Allow {
			t.Fatalf("%q refused: %s", tc.cmd, v.Reason)
		}
		if len(got) != 1 || got[0].Remote.Owner != tc.wantOwner || got[0].Remote.Repo != tc.wantRepo ||
			got[0].Forge != tc.wantForge || got[0].Branch != tc.wantBranch {
			t.Errorf("%q resolved %+v, want owner=%s repo=%s forge=%s branch=%s",
				tc.cmd, got, tc.wantOwner, tc.wantRepo, tc.wantForge, tc.wantBranch)
		}
	}

	requireDeniedContaining(t, "missing -C dir",
		policy.CommandIn(t.Context(), "PRAETOR_ADMIT=git-push git -C nonexistent push origin main", repo),
		"git push target resolution failed")
	requireDeniedContaining(t, "missing remote",
		policy.CommandIn(t.Context(), "PRAETOR_ADMIT=git-push git push unknown-remote main", repo),
		"git push target resolution failed")
}

// End-to-end loopback HTTP check for GitHub and Forgejo drivers through Policy.
func TestAdmission_GitPushHead_GitHubAndForgejoLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		switch r.URL.Path {
		case "/repos/acme/widgets/pulls":
			if _, err := io.WriteString(w, fmt.Sprintf(`[{"number":77,"state":"closed","merged_at":%q,"head":{"ref":"gh-merged"}}]`, testToday.Format(time.RFC3339))); err != nil {
				t.Errorf("write github response: %v", err)
			}
		case "/api/v1/repos/acme/widgets/pulls":
			if _, err := io.WriteString(w, `[{"number":88,"state":"closed","merged":false,"head":{"ref":"fj-closed"}}]`); err != nil {
				t.Errorf("write forgejo response: %v", err)
			}
		default:
			if _, err := io.WriteString(w, `[]`); err != nil {
				t.Errorf("write default response: %v", err)
			}
		}
	}))
	defer srv.Close()

	repo := defaultAdmissionRepo(t)
	policy := admissionPolicy(t).WithAdmissionPullRequests(repo, func(ctx context.Context, target PushTarget) ([]forge.PullRequestHead, error) {
		if target.Forge == config.ForgeForgejo {
			d := forge.NewGiteaDriver("tok", srv.URL)
			d.SetRepository(target.Remote.Owner, target.Remote.Repo)
			return d.ListPullRequestsByHead(ctx, target.Branch)
		}
		d := forge.NewGitHubDriver("tok", srv.URL)
		d.SetRepository(target.Remote.Owner, target.Remote.Repo)
		return d.ListPullRequestsByHead(ctx, target.Branch)
	})

	requireDeniedContaining(t, "github merged",
		policy.Command("PRAETOR_ADMIT=git-push git push origin gh-merged"),
		"#77", "MERGED", "push to a new branch")

	if _, err := util.RunGit(context.Background(), repo, "remote", "add", "codeberg", "https://codeberg.org/acme/widgets.git"); err != nil {
		t.Fatal(err)
	}
	requireDeniedContaining(t, "forgejo closed",
		policy.Command("praetorctl admit --label git-push -- git push codeberg fj-closed"),
		"#88", "CLOSED", "push to a new branch")
}

func writeTestBody(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write test response: %v", err)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := util.RunGit(context.Background(), dir, args...); err != nil {
		t.Fatal(err)
	}
}

// TestAdmission_GitPushHead_Planted_ServerIgnoresHead checks that unrelated and fork
// pulls never fabricate a match, while a matching terminal pull on the first page refuses.
func TestAdmission_GitPushHead_Planted_ServerIgnoresHead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") == "open" {
			writeTestBody(t, w, `[]`)
			return
		}
		items := []string{
			`{"number":101,"state":"closed","merged":true,"head":{"ref":"other-branch","repo":{"full_name":"acme/widgets"}}}`,
			`{"number":102,"state":"closed","merged":true,"head":{"ref":"clean-topic","repo":{"full_name":"fork/widgets"}}}`,
			`{"number":105,"state":"closed","merged":true,"head":{"ref":"terminal-topic","repo":{"full_name":"acme/widgets"}}}`,
		}
		for i := len(items); i < forge.HeadPullRequestsPerPage; i++ {
			items = append(items, `{"number":199,"state":"closed","head":{"ref":"noise","repo":{"full_name":"acme/widgets"}}}`)
		}
		writeTestBody(t, w, "["+strings.Join(items, ",")+"]")
	}))
	defer srv.Close()
	repo := defaultAdmissionRepo(t)
	mustGit(t, repo, "remote", "add", "codeberg", "https://codeberg.org/acme/widgets.git")
	policy := admissionPolicy(t).WithAdmissionPullRequests(repo, func(ctx context.Context, target PushTarget) ([]forge.PullRequestHead, error) {
		d := forge.NewGiteaDriver("tok", srv.URL)
		d.SetRepository(target.Remote.Owner, target.Remote.Repo)
		return d.ListPullRequestsByHead(ctx, target.Branch)
	})
	if v := policy.Command("PRAETOR_ADMIT=git-push git push codeberg clean-topic"); v.Outcome != Allow {
		t.Fatalf("merged PR of another branch/fork should not refuse push: %s", v.Reason)
	}
	requireDeniedContaining(t, "first-page merged PR",
		policy.Command("PRAETOR_ADMIT=git-push git push codeberg terminal-topic"),
		"#105", "MERGED", "push to a new branch")
}

// Planted negative and positive (finding 3): an omitted refspec is resolved via
// `git rev-parse --symbolic-full-name @{push}` (including the remote it names), and when git
// cannot tell (no upstream, push.default nothing, detached HEAD) the push is refused with the
// reason instead of guessing the current branch.
func TestAdmission_GitPushHead_Planted_OmittedRefspecViaGitPushRef(t *testing.T) {
	ctx := context.Background()
	noUpstream := t.TempDir()
	mustGit(t, noUpstream, "init", "-q", "-b", "untracked-branch")
	mustGit(t, noUpstream, "remote", "add", "origin", "https://github.com/acme/widgets.git")

	policy := admissionPolicy(t).WithAdmissionPullRequests(noUpstream, func(_ context.Context, target PushTarget) ([]forge.PullRequestHead, error) {
		if target.Remote.Owner == "fork" && target.Branch == "tracked-dst" {
			return []forge.PullRequestHead{{Number: 61, State: forge.PullRequestStateMerged, HeadBranch: "tracked-dst"}}, nil
		}
		return nil, nil
	})

	// No upstream configured: refused with git's reason rather than guessing untracked-branch.
	requireDeniedContaining(t, "no upstream bare push",
		policy.CommandIn(ctx, "PRAETOR_ADMIT=git-push git push", noUpstream),
		"git push target resolution failed", "@{push}")
	requireDeniedContaining(t, "no upstream remote push",
		policy.CommandIn(ctx, "PRAETOR_ADMIT=git-push git push origin", noUpstream),
		"git push target resolution failed", "@{push}")

	// Configure a commit and upstream pointing to upstream/tracked-dst.
	mustGit(t, noUpstream, "-c", "user.name=t", "-c", "user.email=t@e", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-q", "-m", "init")
	mustGit(t, noUpstream, "remote", "add", "upstream", "https://codeberg.org/fork/widgets.git")
	mustGit(t, noUpstream, "update-ref", "refs/remotes/upstream/tracked-dst", "HEAD")
	mustGit(t, noUpstream, "config", "branch.untracked-branch.remote", "upstream")
	mustGit(t, noUpstream, "config", "branch.untracked-branch.merge", "refs/heads/tracked-dst")
	mustGit(t, noUpstream, "config", "push.default", "upstream")

	// Bare `git push` now resolves via @{push} to remote "upstream" (fork/widgets) and branch "tracked-dst".
	requireDeniedContaining(t, "resolved via @{push} to upstream/tracked-dst",
		policy.CommandIn(ctx, "PRAETOR_ADMIT=git-push git push", noUpstream),
		"fork/widgets", "tracked-dst", "#61", "MERGED")

	// push.default=nothing: git cannot resolve @{push}, so the push is refused.
	mustGit(t, noUpstream, "config", "push.default", "nothing")
	requireDeniedContaining(t, "push.default nothing",
		policy.CommandIn(ctx, "PRAETOR_ADMIT=git-push git push", noUpstream),
		"git push target resolution failed", "@{push}")

	// Detached HEAD: git cannot resolve @{push}, so the push is refused.
	mustGit(t, noUpstream, "config", "push.default", "simple")
	mustGit(t, noUpstream, "checkout", "--detach", "-q", "HEAD")
	requireDeniedContaining(t, "detached HEAD bare push",
		policy.CommandIn(ctx, "PRAETOR_ADMIT=git-push git push", noUpstream),
		"git push target resolution failed", "@{push}")
}

// Planted negative and positive (finding 4): `git push --mirror` is refused with the reason,
// and `git push --all` judges every local branch under refs/heads (refusing when any local
// branch has a terminal PR or when the branch count exceeds the bound).
func TestAdmission_GitPushHead_Planted_MirrorAndAllBranches(t *testing.T) {
	ctx := context.Background()
	repo := defaultAdmissionRepo(t)
	mustGit(t, repo, "branch", "merged-feature")

	var queried []string
	policy := admissionPolicy(t).WithAdmissionPullRequests(repo, func(_ context.Context, target PushTarget) ([]forge.PullRequestHead, error) {
		queried = append(queried, target.Branch)
		if target.Branch == "merged-feature" {
			return []forge.PullRequestHead{{Number: 73, State: forge.PullRequestStateMerged, HeadBranch: "merged-feature"}}, nil
		}
		return nil, nil
	})

	for _, cmd := range []string{
		"PRAETOR_ADMIT=git-push git push --mirror origin",
		"praetorctl admit --label git-push -- git push --mirror",
	} {
		requireDeniedContaining(t, cmd, policy.CommandIn(ctx, cmd, repo),
			"--mirror", "push branches one by one")
	}

	requireDeniedContaining(t, "push --all with merged local branch",
		policy.CommandIn(ctx, "PRAETOR_ADMIT=git-push git push --all origin", repo),
		"merged-feature", "#73", "MERGED")

	mustGit(t, repo, "branch", "-D", "merged-feature")
	mustGit(t, repo, "branch", "clean-feature")
	queried = nil
	if v := policy.CommandIn(ctx, "PRAETOR_ADMIT=git-push git push --all origin", repo); v.Outcome != Allow {
		t.Fatalf("push --all with clean branches refused: %s", v.Reason)
	}
	if len(queried) != 2 {
		t.Fatalf("push --all queried %v, want 2 local branches", queried)
	}

	for i := 0; i < 35; i++ {
		mustGit(t, repo, "branch", "extra-"+strings.Repeat("a", i/10)+string(rune('a'+i%10)))
	}
	requireDeniedContaining(t, "push --all exceeding branch bound",
		policy.CommandIn(ctx, "PRAETOR_ADMIT=git-push git push --all origin", repo),
		"git push target resolution failed", "--all")
}

// Planted negative, positive and boundary: refspec tag detection inspects the source ref rather
// than the destination name, so a local tag sharing the destination branch name cannot hide a
// branch push (`HEAD:merged-topic`, `main:merged-topic`), while a local-tag source pushed to an
// unqualified destination (`v1:merged-topic`) is skipped as a tag push unless the destination
// explicitly names `refs/heads/`.
func TestAdmission_GitPushHead_Planted_RefspecSourceTagVsBranch(t *testing.T) {
	ctx := context.Background()
	repo := defaultAdmissionRepo(t)
	mustGit(t, repo, "tag", "merged-topic")
	mustGit(t, repo, "tag", "v1")
	mustGit(t, repo, "branch", "ambiguous-ref")
	mustGit(t, repo, "tag", "ambiguous-ref")

	calls := 0
	policy := admissionPolicy(t).WithAdmissionPullRequests(repo, func(_ context.Context, target PushTarget) ([]forge.PullRequestHead, error) {
		calls++
		switch target.Branch {
		case "merged-topic":
			return []forge.PullRequestHead{{Number: 42, State: forge.PullRequestStateMerged, HeadBranch: "merged-topic"}}, nil
		case "closed-topic":
			return []forge.PullRequestHead{{Number: 19, State: forge.PullRequestStateClosed, HeadBranch: "closed-topic"}}, nil
		default:
			return nil, nil
		}
	})

	for _, cmd := range []string{
		"PRAETOR_ADMIT=git-push git push origin HEAD:merged-topic",
		"PRAETOR_ADMIT=git-push git push origin +HEAD:merged-topic",
		"PRAETOR_ADMIT=git-push git push origin main:merged-topic",
		"PRAETOR_ADMIT=git-push git push origin ambiguous-ref:merged-topic",
		"PRAETOR_ADMIT=git-push git push origin v1:refs/heads/merged-topic",
	} {
		requireDeniedContaining(t, cmd, policy.CommandIn(ctx, cmd, repo),
			"#42", "MERGED", "push to a new branch")
	}

	calls = 0
	for _, cmd := range []string{
		"PRAETOR_ADMIT=git-push git push origin v1",
		"PRAETOR_ADMIT=git-push git push origin v1:merged-topic",
		"PRAETOR_ADMIT=git-push git push origin +v1:merged-topic",
		"PRAETOR_ADMIT=git-push git push origin v1:closed-topic",
		"PRAETOR_ADMIT=git-push git push origin +v1:closed-topic",
	} {
		if v := policy.CommandIn(ctx, cmd, repo); v.Outcome != Allow {
			t.Errorf("%q refused: %s", cmd, v.Reason)
		}
	}
	if calls != 0 {
		t.Fatalf("local-tag source pushes queried the forge %d times, want 0", calls)
	}
}

// Planted negative, positive and boundary: `git -c` keys that can redirect or alter the push
// target (remote.*, remotes.*, url.*, branch.*, push.*, include.*, includeIf.*) are refused
// before querying the forge, while unrelated `git -c` settings still pass target resolution
// and undergo the normal terminal-head check.
func TestAdmission_GitPushHead_Planted_ConfigTargetOverridesRefused(t *testing.T) {
	ctx := context.Background()
	repo := defaultAdmissionRepo(t)
	calls := 0
	policy := admissionPolicy(t).WithAdmissionPullRequests(repo, func(_ context.Context, target PushTarget) ([]forge.PullRequestHead, error) {
		calls++
		if target.Branch == "merged-topic" {
			return []forge.PullRequestHead{{Number: 42, State: forge.PullRequestStateMerged, HeadBranch: "merged-topic"}}, nil
		}
		return nil, nil
	})

	for _, cmd := range []string{
		"PRAETOR_ADMIT=git-push git -c remote.origin.url=https://github.com/other/widgets.git push origin merged-topic",
		"praetorctl admit --label git-push -- git -c remote.origin.url=https://github.com/other/widgets.git push origin main",
		"PRAETOR_ADMIT=git-push git -c remote.origin.pushurl=https://github.com/other/widgets.git push origin main",
		"PRAETOR_ADMIT=git-push git -c remote.origin.push=refs/heads/main:refs/heads/other push origin",
		"PRAETOR_ADMIT=git-push git -c remote.origin.mirror push origin",
		"PRAETOR_ADMIT=git-push git -c REMOTE.pushDefault=upstream push",
		"PRAETOR_ADMIT=git-push git -c remotes.group=origin push group main",
		"PRAETOR_ADMIT=git-push git -c url.https://github.com/other/.insteadOf=https://github.com/acme/ push origin main",
		"PRAETOR_ADMIT=git-push git -c URL.https://github.com/other/.pushInsteadOf=https://github.com/acme/ push origin main",
		"PRAETOR_ADMIT=git-push git -c branch.main.remote=upstream push",
		"PRAETOR_ADMIT=git-push git -c branch.main.pushRemote=upstream push",
		"PRAETOR_ADMIT=git-push git -c branch.main.merge=refs/heads/other push",
		"PRAETOR_ADMIT=git-push git -c push.default=matching push",
		"PRAETOR_ADMIT=git-push git -c push.autoSetupRemote=true push",
		"PRAETOR_ADMIT=git-push git -c include.path=/tmp/gitconfig push origin main",
		"PRAETOR_ADMIT=git-push git -c IncludeIf.onbranch:main.path=/tmp/gitconfig push origin main",
		"PRAETOR_ADMIT=git-push git -c http.lowSpeedLimit=1 -c remote.origin.url=https://github.com/other/widgets.git push origin main",
	} {
		requireDeniedContaining(t, cmd, policy.CommandIn(ctx, cmd, repo),
			"git push target resolution failed", "overrides the push target")
	}
	if calls != 0 {
		t.Fatalf("push-target -c overrides queried the forge %d times, want 0", calls)
	}

	for _, cmd := range []string{
		"PRAETOR_ADMIT=git-push git -c http.lowSpeedLimit=1 push origin main",
		"PRAETOR_ADMIT=git-push git -c core.sshCommand=ssh push origin main",
		"praetorctl admit --label git-push -- git -c credential.helper= push origin main",
	} {
		if v := policy.CommandIn(ctx, cmd, repo); v.Outcome != Allow {
			t.Errorf("%q refused: %s", cmd, v.Reason)
		}
	}
	requireDeniedContaining(t, "benign -c on merged branch still refused by forge check",
		policy.CommandIn(ctx, "PRAETOR_ADMIT=git-push git -c http.lowSpeedLimit=1 push origin merged-topic", repo),
		"#42", "MERGED", "push to a new branch")
}

// Planted negative, positive and boundary: wildcard, pattern and negative refspecs are refused
// during target resolution before any forge query, while explicit branch refspecs still work.
func TestAdmission_GitPushHead_Planted_WildcardRefspecsRefused(t *testing.T) {
	ctx := context.Background()
	repo := defaultAdmissionRepo(t)
	calls := 0
	policy := admissionPolicy(t).WithAdmissionPullRequests(repo, func(_ context.Context, target PushTarget) ([]forge.PullRequestHead, error) {
		calls++
		if target.Branch == "merged-topic" {
			return []forge.PullRequestHead{{Number: 42, State: forge.PullRequestStateMerged, HeadBranch: "merged-topic"}}, nil
		}
		return nil, nil
	})

	for _, cmd := range []string{
		"PRAETOR_ADMIT=git-push git push origin refs/heads/*:refs/heads/*",
		"praetorctl admit --label git-push -- git push origin refs/heads/*:refs/heads/*",
		"PRAETOR_ADMIT=git-push git push origin 'refs/heads/*:refs/heads/*'",
		"PRAETOR_ADMIT=git-push git push origin *:*",
		"PRAETOR_ADMIT=git-push git push origin '*:*'",
		"PRAETOR_ADMIT=git-push git push origin *",
		"PRAETOR_ADMIT=git-push git push origin refs/heads/*",
		"PRAETOR_ADMIT=git-push git push origin +refs/heads/feat/*:refs/heads/feat/*",
		"PRAETOR_ADMIT=git-push git push origin refs/heads/feat-?:refs/heads/feat-?",
		"PRAETOR_ADMIT=git-push git push origin refs/heads/feat-[a-z]:refs/heads/feat-[a-z]",
		"PRAETOR_ADMIT=git-push git push origin main ^refs/heads/merged-topic",
		"PRAETOR_ADMIT=git-push git push origin main refs/heads/*:refs/heads/*",
	} {
		requireDeniedContaining(t, cmd, policy.CommandIn(ctx, cmd, repo),
			"git push target resolution failed", "wildcard git push refspec", "push branches one by one")
	}
	if calls != 0 {
		t.Fatalf("wildcard refspec pushes queried the forge %d times, want 0", calls)
	}

	if v := policy.CommandIn(ctx, "PRAETOR_ADMIT=git-push git push origin refs/heads/main:refs/heads/main", repo); v.Outcome != Allow {
		t.Fatalf("explicit non-wildcard refspec refused: %s", v.Reason)
	}
	requireDeniedContaining(t, "explicit non-wildcard refspec to merged branch",
		policy.CommandIn(ctx, "PRAETOR_ADMIT=git-push git push origin refs/heads/merged-topic:refs/heads/merged-topic", repo),
		"#42", "MERGED", "push to a new branch")
}

// Planted negative, positive and boundary: resolvePushRemoteAndForge resolves the effective
// push URL (`remote.<name>.pushurl` and `url.<base>.pushInsteadOf`) rather than the fetch URL,
// while ReadOriginRemote and ReadGitRemote keep reading the fetch URL for their other callers.
func TestAdmission_GitPushHead_Planted_PushURLAndPushInsteadOf(t *testing.T) {
	ctx := context.Background()
	repo := defaultAdmissionRepo(t)
	mustGit(t, repo, "config", "remote.origin.pushurl", "https://codeberg.org/fork/widgets.git")
	mustGit(t, repo, "remote", "add", "rewritten", "https://github.com/acme/widgets.git")
	mustGit(t, repo, "config", "url.https://codeberg.org/fork/.pushInsteadOf", "https://github.com/acme/")

	policy := admissionPolicy(t).WithAdmissionPullRequests(repo, stubForkVsUpstreamPullRequests)
	for _, cmd := range []string{
		"PRAETOR_ADMIT=git-push git push origin merged-in-fork",
		"praetorctl admit --label git-push -- git push origin merged-in-fork",
		"PRAETOR_ADMIT=git-push git push rewritten merged-in-fork",
	} {
		requireDeniedContaining(t, cmd, policy.CommandIn(ctx, cmd, repo),
			"fork/widgets", "#84", "MERGED", "push to a new branch")
	}
	for _, cmd := range []string{
		"PRAETOR_ADMIT=git-push git push origin clean-in-fork",
		"PRAETOR_ADMIT=git-push git push rewritten clean-in-fork",
	} {
		if v := policy.CommandIn(ctx, cmd, repo); v.Outcome != Allow {
			t.Fatalf("%q refused when only upstream fetch URL had a merged PR: %s", cmd, v.Reason)
		}
	}
	assertUpstreamFetchRemotesUnchanged(t, ctx, repo)
}

func stubForkVsUpstreamPullRequests(_ context.Context, target PushTarget) ([]forge.PullRequestHead, error) {
	if target.Remote.Owner == "fork" && target.Forge == config.ForgeForgejo && target.Branch == "merged-in-fork" {
		return []forge.PullRequestHead{{Number: 84, State: forge.PullRequestStateMerged, HeadBranch: "merged-in-fork"}}, nil
	}
	if target.Remote.Owner == "acme" && target.Branch == "clean-in-fork" {
		return []forge.PullRequestHead{{Number: 85, State: forge.PullRequestStateMerged, HeadBranch: "clean-in-fork"}}, nil
	}
	return nil, nil
}

func assertUpstreamFetchRemotesUnchanged(t *testing.T, ctx context.Context, repo string) {
	t.Helper()
	want := util.GitRemote{Host: "github.com", Path: "acme/widgets", Owner: "acme", Repo: "widget" + "s"}
	if got, err := util.ReadOriginRemote(ctx, repo); err != nil || got != want {
		t.Fatalf("ReadOriginRemote fetch reader changed: %+v, %v", got, err)
	}
	if got, err := util.ReadGitRemote(ctx, repo, "rewritten"); err != nil || got != want {
		t.Fatalf("ReadGitRemote fetch reader changed: %+v, %v", got, err)
	}
}

func governedAdmissionRepo(t *testing.T, branch, originURL string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	initAdmissionRepo(t, dir, branch, originURL)
	if err := os.WriteFile(filepath.Join(dir, manifestName), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// Planted negative, positive and boundary: AGY workspacePaths keep their precedence for
// repository-root discovery while commandBase uses Invocation.WorkDir (and native dialects'
// CommandDir when present) across evaluateCommand, resolveCommandRoot, and evaluateForgeTextRefs.
func TestAdmission_GitPushHead_Planted_AgyWorkspaceVsCommandDir(t *testing.T) {
	addDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outerRepo := governedAdmissionRepo(t, "outer-merged", "https://github.com/acme/outer.git")
	nestedRepo := governedAdmissionRepo(t, "nested-topic", "https://github.com/fork/nested.git")

	pol := admissionPolicy(t).WithAdmissionPullRequests(outerRepo, func(_ context.Context, target PushTarget) ([]forge.PullRequestHead, error) {
		if target.Remote.Owner == "acme" && target.Remote.Repo == "outer" {
			return []forge.PullRequestHead{{Number: 90, State: forge.PullRequestStateMerged, HeadBranch: target.Branch}}, nil
		}
		if target.Remote.Owner == "fork" && target.Remote.Repo == "nested" && target.Branch == "nested-merged" {
			return []forge.PullRequestHead{{Number: 91, State: forge.PullRequestStateMerged, HeadBranch: "nested-merged"}}, nil
		}
		return nil, nil
	})

	allowResp := runAgyPreToolWithPolicy(t, pol, nestedRepo, []string{addDir, outerRepo, nestedRepo}, "PRAETOR_ADMIT=git-push git push")
	if !agyStdoutAllows(t, "pre-tool", allowResp) {
		t.Fatalf("agy push from nestedRepo refused by outer workspace: %s", allowResp.Stdout)
	}
	denyResp := runAgyPreToolWithPolicy(t, pol, nestedRepo, []string{addDir, nestedRepo}, "PRAETOR_ADMIT=git-push git push origin HEAD:nested-merged")
	if agyStdoutAllows(t, "pre-tool", denyResp) || !strings.Contains(string(denyResp.Stdout), "fork/nested") || !strings.Contains(string(denyResp.Stdout), "#91") {
		t.Fatalf("agy push to merged branch in nestedRepo not refused with fork/nested #91: %s", denyResp.Stdout)
	}
	assertAgyCommandBaseCallers(t, pol, addDir, outerRepo, nestedRepo)
}

func assertAgyCommandBaseCallers(t *testing.T, pol *Policy, addDir, outerRepo, nestedRepo string) {
	t.Helper()
	plainRepo := repository(t, false)
	relCmd := "git -C " + filepath.Base(nestedRepo) + " push --no-verify"
	relResp := runAgyPreToolWithPolicy(t, pol, filepath.Dir(nestedRepo), []string{addDir, plainRepo}, relCmd)
	if agyStdoutAllows(t, "pre-tool", relResp) || !strings.Contains(string(relResp.Stdout), evasionMessage) {
		t.Fatalf("agy relative command target from WorkDir allowed: %s", relResp.Stdout)
	}

	bodyPath := filepath.Join(nestedRepo, "pr-body.md")
	if err := os.WriteFile(bodyPath, []byte("Ref upstream/other#12\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ghResp := runAgyPreToolWithPolicy(t, pol, nestedRepo, []string{addDir, outerRepo}, "gh pr create --repo acme/outer --title t --body-file pr-body.md")
	if agyStdoutAllows(t, "pre-tool", ghResp) || !strings.Contains(string(ghResp.Stdout), "upstream/other#12") {
		t.Fatalf("agy relative gh --body-file in WorkDir not checked for foreign reference: %s", ghResp.Stdout)
	}

	claudePayload := commandPayload(t, map[string]any{
		"cwd":        nestedRepo,
		"tool_input": map[string]any{"command": "PRAETOR_ADMIT=git-push git push origin HEAD:nested-merged"},
	})
	claudeResp := Run(context.Background(), Invocation{
		Client: "claude", Event: "pre-tool", Stdin: strings.NewReader(string(claudePayload)),
		Getenv: noEnvironment, WorkDir: outerRepo, Policy: pol,
	})
	if claudeResp.ExitCode == 0 || !strings.Contains(string(claudeResp.Stderr), "fork/nested") || !strings.Contains(string(claudeResp.Stderr), "#91") {
		t.Fatalf("claude payload cwd did not take precedence over WorkDir: %s", claudeResp.Stderr)
	}
}

func runAgyPreToolWithPolicy(t *testing.T, pol *Policy, workDir string, workspaces []string, cmd string) Response {
	t.Helper()
	payload := commandPayload(t, map[string]any{
		"workspacePaths": workspaces,
		"toolCall":       map[string]any{"name": "run_command", "args": map[string]any{"CommandLine": cmd}},
	})
	return Run(context.Background(), Invocation{
		Client: "agy", Event: "pre-tool", Stdin: strings.NewReader(string(payload)),
		Getenv: noEnvironment, WorkDir: workDir, Policy: pol,
	})
}

var uncategorizedExceptionManifest = `version: 1
repository:
  forge: forgejo
exceptions:
  - rule: HISS-04
    path: internal/foo.go
    reason: legacy complexity pending split
    expires: "` + testToday.AddDate(0, 0, 62).Format(time.DateOnly) + `"
`

// Planted negative, positive and boundary (order engine-push-target-manifest-defect-20261102):
// a manifest with an exception missing its category does not block push target resolution while
// still refusing a push to a branch headed by a merged pull request; a malformed manifest falls
// back to the remote host and reports a non-blocking finding naming the manifest; and a valid
// manifest declaring forgejo still resolves forgejo.
func TestAdmission_GitPushHead_Planted_ManifestDefectDoesNotBlock(t *testing.T) {
	ctx := context.Background()
	repo := defaultAdmissionRepo(t)
	manifestFile := filepath.Join(repo, config.ManifestFileName)
	if err := os.WriteFile(manifestFile, []byte(uncategorizedExceptionManifest), 0o600); err != nil {
		t.Fatal(err)
	}

	var gotTargets []PushTarget
	pol := admissionPolicy(t).WithAdmissionPullRequests(repo, func(_ context.Context, target PushTarget) ([]forge.PullRequestHead, error) {
		gotTargets = append(gotTargets, target)
		if target.Branch == "merged-topic" {
			return []forge.PullRequestHead{{Number: 42, State: forge.PullRequestStateMerged, HeadBranch: "merged-topic"}}, nil
		}
		return nil, nil
	})

	for _, cmd := range []string{
		"PRAETOR_ADMIT=git-push git push -u origin feature-branch",
		"praetorctl admit --label git-push -- git push -u origin feature-branch",
	} {
		v := pol.CommandIn(ctx, cmd, repo)
		if v.Outcome != Allow || v.Notice != "" {
			t.Fatalf("%q with uncategorized exception: outcome=%v reason=%q notice=%q", cmd, v.Outcome, v.Reason, v.Notice)
		}
	}
	requireDeniedContaining(t, "uncategorized exception + merged PR branch",
		pol.CommandIn(ctx, "PRAETOR_ADMIT=git-push git push -u origin merged-topic", repo),
		"#42", "MERGED", "push to a new branch")

	assertMalformedManifestFallbackAndNotice(t, pol, repo, manifestFile)
	assertDeclaredForgejoResolution(t, pol, &gotTargets)
}

// assertMalformedManifestFallbackAndNotice verifies that a malformed .standards.yaml falls back to
// the remote hostname with a non-blocking notice while still refusing merged pull request heads.
func assertMalformedManifestFallbackAndNotice(t *testing.T, pol *Policy, repo, manifestFile string) {
	t.Helper()
	ctx := t.Context()
	if err := os.WriteFile(manifestFile, []byte("version: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	v := pol.CommandIn(ctx, "PRAETOR_ADMIT=git-push git push -u origin feature-branch", repo)
	if v.Outcome != Allow || !strings.Contains(v.Notice, config.ManifestFileName) {
		t.Fatalf("malformed manifest push: outcome=%v reason=%q notice=%q, want Allow with notice naming %s",
			v.Outcome, v.Reason, v.Notice, config.ManifestFileName)
	}
	requireDeniedContaining(t, "malformed manifest + merged PR branch",
		pol.CommandIn(ctx, "PRAETOR_ADMIT=git-push git push -u origin merged-topic", repo),
		"#42", "MERGED", "push to a new branch")

	claudePayload := commandPayload(t, map[string]any{
		"cwd":        repo,
		"tool_input": map[string]any{"command": "PRAETOR_ADMIT=git-push git push -u origin feature-branch"},
	})
	claudeResp := Run(ctx, Invocation{
		Client: "claude", Event: "pre-tool", Stdin: strings.NewReader(string(claudePayload)),
		Getenv: noEnvironment, WorkDir: repo, Policy: pol,
	})
	if claudeResp.ExitCode != 0 || !strings.Contains(string(claudeResp.Stderr), config.ManifestFileName) {
		t.Fatalf("claude pre-tool with malformed manifest: exit=%d stderr=%q", claudeResp.ExitCode, claudeResp.Stderr)
	}
	agyResp := runAgyPreToolWithPolicy(t, pol, repo, []string{repo}, "PRAETOR_ADMIT=git-push git push -u origin feature-branch")
	if !agyStdoutAllows(t, "pre-tool", agyResp) || !strings.Contains(string(agyResp.Stdout), config.ManifestFileName) {
		t.Fatalf("agy pre-tool with malformed manifest: stdout=%q", agyResp.Stdout)
	}
}

// assertDeclaredForgejoResolution verifies that a valid .standards.yaml declaring repository.forge:
// forgejo on a custom host resolves config.ForgeForgejo and its origin host.
func assertDeclaredForgejoResolution(t *testing.T, pol *Policy, gotTargets *[]PushTarget) {
	t.Helper()
	ctx := t.Context()
	customRepo := t.TempDir()
	initAdmissionRepo(t, customRepo, "feature-branch", "https://git.example.com/acme/widgets.git")
	validForgejoManifest := "version: 1\nrepository:\n  forge: forgejo\n"
	if err := os.WriteFile(filepath.Join(customRepo, config.ManifestFileName), []byte(validForgejoManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	*gotTargets = nil
	v := pol.CommandIn(ctx, "PRAETOR_ADMIT=git-push git push -u origin feature-branch", customRepo)
	if v.Outcome != Allow || v.Notice != "" {
		t.Fatalf("valid forgejo manifest push: outcome=%v reason=%q notice=%q", v.Outcome, v.Reason, v.Notice)
	}
	if len(*gotTargets) != 1 || (*gotTargets)[0].Forge != config.ForgeForgejo {
		t.Fatalf("valid forgejo manifest resolved %+v, want ForgeForgejo", *gotTargets)
	}
	if host := declaredForgejoHost(ctx, customRepo); host != "git.example.com" {
		t.Fatalf("declaredForgejoHost = %q, want git.example.com", host)
	}
}
