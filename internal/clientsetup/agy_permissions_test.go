package clientsetup

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

func TestPlanAGYPermissionsAppendsMissingAllowRules(t *testing.T) {
	existing := []byte(`{"theme":"dark","permissions":{"allow":["command(git status)"],"ask":["read_url(example.test)"],"deny":["write_file(.git/)"],"future":{"number":9007199254740993}}}`)
	permissions := config.ClientPermissions{Manage: true, Allow: []string{
		"command(git status)",
		"mcp(hindsight/hindsight_list_knowledge_pages)",
		"command(go test)",
	}}

	plan, err := PlanAGYPermissions(t.Context(), existing, permissions)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Managed || !plan.Changed || plan.BeforeCount != 1 || plan.AfterCount != 3 {
		t.Fatalf("unexpected plan metadata: %+v", plan)
	}
	wantAdded := []string{"mcp(hindsight/hindsight_list_knowledge_pages)", "command(go test)"}
	if !slices.Equal(plan.Added, wantAdded) {
		t.Fatalf("added = %q, want %q", plan.Added, wantAdded)
	}
	var root map[string]jsontext.Value
	if err := json.Unmarshal(plan.Content, &root); err != nil {
		t.Fatal(err)
	}
	if string(root["theme"]) != `"dark"` {
		t.Fatalf("unknown root key lost: %s", plan.Content)
	}
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(root["permissions"], &fields); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string][]string{
		"allow": append([]string{"command(git status)"}, wantAdded...),
		"ask":   {"read_url(example.test)"},
		"deny":  {"write_file(.git/)"},
	} {
		var got []string
		if err := json.Unmarshal(fields[key], &got); err != nil || !slices.Equal(got, want) {
			t.Fatalf("permissions.%s = %q, want %q: %v", key, got, want, err)
		}
	}
	if !bytes.Contains(fields["future"], []byte("9007199254740993")) {
		t.Fatalf("unknown permissions key lost: %s", fields["future"])
	}
	if bytes.Equal(plan.Content, existing) {
		t.Fatal("changed plan returned original content")
	}
}

func TestPlanAGYPermissionsRejectsHigherPrecedenceRulesThatOverlapAllow(t *testing.T) {
	tests := []struct {
		name     string
		list     string
		blocking string
		declared string
	}{
		{name: "deny exact", list: "deny", blocking: "read_file(/srv/log)", declared: "read_file(/srv/log)"},
		{name: "ask action wildcard", list: "ask", blocking: "mcp(*)", declared: "mcp(hindsight/list)"},
		{name: "declared action wildcard", list: "ask", blocking: "mcp(hindsight/list)", declared: "mcp(*)"},
		{name: "deny command literal prefix", list: "deny", blocking: "command(git)", declared: "command(git status --short)"},
		{name: "ask command token prefix", list: "ask", blocking: "command(git status)", declared: "command(git   status --short)"},
		{name: "ask command overlaps broader allow", list: "ask", blocking: "command(git status)", declared: "command(git)"},
		{name: "ask mcp server wildcard", list: "ask", blocking: "mcp(hindsight/*)", declared: "mcp(hindsight/list)"},
		{name: "deny mcp tool overlaps server allow", list: "deny", blocking: "mcp(hindsight/list)", declared: "mcp(hindsight/*)"},
		{name: "deny recursive file parent", list: "deny", blocking: "read_file(/srv/log)", declared: "read_file(/srv/log/app/current.log)"},
		{name: "ask file child overlaps recursive allow", list: "ask", blocking: "write_file(src/generated)", declared: "write_file(src)"},
		{name: "deny normalized Windows file parent", list: "deny", blocking: `read_file(C:\srv\log)`, declared: `read_file(D:\srv\log\app)`},
		{name: "ask url parent domain", list: "ask", blocking: "read_url(example.test)", declared: "read_url(api.example.test)"},
		{name: "ask URL ignores blocking path and case", list: "ask", blocking: "read_url(EXAMPLE.TEST/admin)", declared: "read_url(api.example.test)"},
		{name: "deny URL ignores blocking scheme and port", list: "deny", blocking: "read_url(https://Example.test:8443/admin)", declared: "read_url(api.example.test)"},
		{name: "deny URL folds uppercase scheme and host", list: "deny", blocking: "read_url(HTTPS://API.EXAMPLE.TEST/X)", declared: "read_url(example.test)"},
		{name: "ask URL drops a single trailing root dot", list: "ask", blocking: "read_url(https://example.test./x)", declared: "read_url(api.example.test)"},
		{name: "deny URL reduces IPv6 literal with port and path", list: "deny", blocking: "read_url(https://[2001:DB8::1]:443/x)", declared: "read_url(2001:db8::1)"},
		{name: "deny URL ignores bracketed IPv6 port", list: "deny", blocking: "read_url([::1]:8080)", declared: "read_url(::1)"},
		{name: "deny URL strips IPv6 brackets without port", list: "deny", blocking: "read_url([::1])", declared: "read_url(::1)"},
		{name: "ask URL strips IPv6 brackets inside URL", list: "ask", blocking: "read_url(https://[::1]/x)", declared: "read_url(::1)"},
		{name: "deny url subdomain overlaps parent allow", list: "deny", blocking: "execute_url(admin.example.test)", declared: "execute_url(example.test)"},
		{name: "deny read implicitly denies write", list: "deny", blocking: "read_file(/srv/private)", declared: "write_file(/srv/private/report.txt)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			existing := []byte(fmt.Sprintf(`{"unrelated":{"keep":true},"permissions":{"%s":[%q],"future":["keep"]}}`,
				test.list, test.blocking))
			plan, err := PlanAGYPermissions(t.Context(), existing, config.ClientPermissions{
				Manage: true,
				Allow:  []string{test.declared},
			})
			if err == nil || plan != nil {
				t.Fatalf("shadowed grant accepted: plan=%+v err=%v", plan, err)
			}
			for _, want := range []string{test.list, strconv.Quote(test.blocking), strconv.Quote(test.declared)} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not surface %q", err, want)
				}
			}
		})
	}
}

func TestPlanAGYPermissionsRejectsUnprovableRegexOverlap(t *testing.T) {
	for _, test := range []struct {
		name     string
		blocking string
		declared string
	}{
		{name: "blocking regex", blocking: "command(regex:git .*)", declared: "command(git status)"},
		{name: "declared regex", blocking: "command(git status)", declared: "command(regex:git (status|diff))"},
	} {
		t.Run(test.name, func(t *testing.T) {
			existing := []byte(fmt.Sprintf(`{"permissions":{"ask":[%q]}}`, test.blocking))
			plan, err := PlanAGYPermissions(t.Context(), existing, config.ClientPermissions{
				Manage: true,
				Allow:  []string{test.declared},
			})
			if err == nil || plan != nil {
				t.Fatalf("unprovable regex overlap accepted: plan=%+v err=%v", plan, err)
			}
			for _, want := range []string{"cannot prove non-overlap", "permissions.ask", test.blocking, test.declared} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not surface %q", err, want)
				}
			}
		})
	}
}

// An existing ask/deny URL rule that does not reduce to a canonical host could still
// match at runtime, so planning fails closed rather than treating it as disjoint.
func TestPlanAGYPermissionsRejectsUnprovableURLOverlap(t *testing.T) {
	for _, test := range []struct {
		name     string
		list     string
		blocking string
		declared string
	}{
		{name: "scheme-relative URL", list: "deny", blocking: "read_url(//example.test/x)", declared: "read_url(api.example.test)"},
		{name: "empty host after scheme", list: "deny", blocking: "read_url(https://)", declared: "read_url(example.test)"},
		{name: "backslash after host", list: "ask", blocking: `read_url(https://example.test\admin)`, declared: "read_url(api.example.test)"},
		{name: "backslash before userinfo", list: "deny", blocking: `execute_url(https://evil.test\@example.test)`, declared: "execute_url(evil.test)"},
		{name: "wildcard label", list: "deny", blocking: "read_url(*.example.test)", declared: "read_url(api.example.test)"},
		{name: "non-canonical IPv6 spelling", list: "ask", blocking: "read_url(0:0:0:0:0:0:0:1)", declared: "read_url(::1)"},
		{name: "unbalanced IPv6 bracket", list: "deny", blocking: "read_url([::1)", declared: "read_url(::1)"},
		// WHATWG URL parsing reads each of these as host example.test (or fails), while a
		// hand cut of scheme, port or userinfo reduced them to another host and let the
		// declared grant through.
		{name: "special scheme without slashes", list: "deny", blocking: "read_url(https:example.test)", declared: "read_url(example.test)"},
		{name: "special scheme with one slash", list: "deny", blocking: "read_url(https:/example.test)", declared: "read_url(example.test)"},
		{name: "special scheme without slashes and path", list: "ask", blocking: "read_url(https:example.test/x)", declared: "read_url(example.test)"},
		{name: "special scheme with three slashes", list: "deny", blocking: "read_url(https:///example.test)", declared: "read_url(example.test)"},
		{name: "slash inside userinfo password", list: "deny", blocking: "execute_url(https://user:pa/ss@example.test)", declared: "execute_url(example.test)"},
		{name: "userinfo without scheme", list: "ask", blocking: "execute_url(user@example.test)", declared: "execute_url(example.test)"},
		{name: "userinfo naming another host", list: "deny", blocking: "read_url(https://example.test@other.test)", declared: "read_url(example.test)"},
		{name: "non-numeric port", list: "deny", blocking: "read_url(other.test:example.test)", declared: "read_url(example.test)"},
		{name: "DNS host with bare port reads as a scheme", list: "ask", blocking: "read_url(other.test:443)", declared: "read_url(example.test)"},
		{name: "empty port", list: "deny", blocking: "read_url(https://other.test:/x)", declared: "read_url(example.test)"},
		{name: "port above range", list: "deny", blocking: "read_url(https://other.test:65536/x)", declared: "read_url(example.test)"},
		{name: "percent-encoded host", list: "ask", blocking: "read_url(https://%65xample.test)", declared: "read_url(example.test)"},
		{name: "non-ASCII host", list: "deny", blocking: "read_url(https://\u212Aexample.test)", declared: "read_url(kexample.test)"},
		{name: "hexadecimal IPv4 host", list: "deny", blocking: "read_url(0x7f.0.0.1)", declared: "read_url(127.0.0.1)"},
		{name: "whitespace-only target", list: "deny", blocking: "read_url( )", declared: "read_url(example.test)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			existing, err := json.Marshal(map[string]any{"permissions": map[string][]string{test.list: {test.blocking}}})
			if err != nil {
				t.Fatal(err)
			}
			plan, err := PlanAGYPermissions(t.Context(), existing, config.ClientPermissions{
				Manage: true,
				Allow:  []string{test.declared},
			})
			if err == nil || plan != nil {
				t.Fatalf("unprovable URL overlap accepted: plan=%+v err=%v", plan, err)
			}
			for _, want := range []string{"cannot prove non-overlap", "permissions." + test.list + " URL rule", strconv.Quote(test.blocking), strconv.Quote(test.declared)} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not surface %q", err, want)
				}
			}
		})
	}
}

func TestPlanAGYPermissionsRejectsUnprovableMixedRootFileOverlap(t *testing.T) {
	for _, test := range []struct {
		name     string
		list     string
		blocking string
		declared string
	}{
		{name: "ask absolute deny relative read", list: "ask", blocking: "read_file(/workspace/src)", declared: "read_file(src)"},
		{name: "deny relative allow absolute write", list: "deny", blocking: "write_file(src)", declared: "write_file(/workspace/src)"},
		{name: "deny absolute read implies relative write", list: "deny", blocking: "read_file(/workspace/src)", declared: "write_file(src)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			existing := []byte(fmt.Sprintf(`{"permissions":{"%s":[%q]}}`, test.list, test.blocking))
			plan, err := PlanAGYPermissions(t.Context(), existing, config.ClientPermissions{
				Manage: true,
				Allow:  []string{test.declared},
			})
			if err == nil || plan != nil {
				t.Fatalf("workspace-dependent file overlap accepted: plan=%+v err=%v", plan, err)
			}
			for _, want := range []string{"cannot prove non-overlap", "runtime workspace resolution", "permissions." + test.list, test.blocking, test.declared} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not surface %q", err, want)
				}
			}
		})
	}
}

func TestPlanAGYPermissionsKeepsDisjointHigherPrecedenceRules(t *testing.T) {
	for _, test := range []struct {
		name     string
		blocking string
		declared string
	}{
		{name: "command token boundary", blocking: "command(git)", declared: "command(gitlab status)"},
		{name: "file sibling", blocking: "read_file(/srv/log)", declared: "read_file(/srv/logger)"},
		{name: "domain label boundary", blocking: "read_url(example.test)", declared: "read_url(notexample.test)"},
		{name: "blocking URL spelling keeps label boundary", blocking: "read_url(https://example.test:443/x)", declared: "read_url(notexample.test)"},
		{name: "bracketed IPv6 host stays distinct", blocking: "read_url([::2])", declared: "read_url(::1)"},
		{name: "bracketed IPv6 URL with port stays distinct", blocking: "read_url(https://[::2]:8080/x)", declared: "read_url(::1)"},
		{name: "canonical URL with port query and fragment stays distinct", blocking: "read_url(https://other.test:8443/x?y#z)", declared: "read_url(example.test)"},
		{name: "at sign in URL path is not userinfo", blocking: "read_url(https://other.test/@example.test)", declared: "read_url(example.test)"},
		{name: "unprovable URL rule on another action", blocking: "read_url(*.example.test)", declared: "execute_url(api.example.test)"},
		{name: "mcp server boundary", blocking: "mcp(hindsight/*)", declared: "mcp(hindsight2/list)"},
		{name: "ask read does not imply ask write", blocking: "read_file(/srv/private)", declared: "write_file(/srv/private/report.txt)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			existing := []byte(fmt.Sprintf(`{"permissions":{"ask":[%q]}}`, test.blocking))
			plan, err := PlanAGYPermissions(t.Context(), existing, config.ClientPermissions{
				Manage: true,
				Allow:  []string{test.declared},
			})
			if err != nil {
				t.Fatal(err)
			}
			if !plan.Changed || !slices.Equal(plan.Added, []string{test.declared}) {
				t.Fatalf("disjoint rule rejected: %+v", plan)
			}
		})
	}
}

func TestCanonicalAGYURLHost(t *testing.T) {
	for target, want := range map[string]string{
		"example.test":                 "example.test",
		" EXAMPLE.TEST/admin ":         "example.test",
		"example.test.":                "example.test",
		"https://Example.test:8443/x":  "example.test",
		"foo://api.example.test?q#f":   "api.example.test",
		"https://other.test/@x":        "other.test",
		"127.0.0.1:80":                 "127.0.0.1",
		"::1":                          "::1",
		"2001:DB8::1":                  "2001:db8::1",
		"[::1]":                        "::1",
		"[2001:db8::1]:443/x":          "2001:db8::1",
		"https://[::ffff:1.2.3.4]:1/x": "::ffff:1.2.3.4",
	} {
		if got := canonicalAGYURLHost(target); got != want {
			t.Fatalf("canonicalAGYURLHost(%q) = %q, want %q", target, got, want)
		}
	}
	for _, target := range []string{
		"", " ", "*", "*.example.test", "//example.test", "https://", "https:example.test", "https:/example.test",
		"https:///example.test", "https:443", "example.test:443", "localhost:8080", "other.test:example.test",
		"user@example.test", "https://user:pa/ss@example.test", "https://a@b@example.test", "https://example.test:",
		"https://example.test:0", "https://example.test:65536", "https://example.test:80:90", "https://example.test\\x",
		"https://%65xample.test", "https://ex\u212Aample.test", "exa\tmple.test", "example.test..", "0x7f.0.0.1",
		"0:0:0:0:0:0:0:1", "::1/x", "[::1", "[127.0.0.1]", "[fe80::1%25eth0]", "http:https://example.test", "a/b://example.test",
	} {
		if got := canonicalAGYURLHost(target); got != "" {
			t.Fatalf("ambiguous URL target %q reduced to host %q", target, got)
		}
	}
}

// A URL rule at the canonical-host bound still reduces to its host; one byte more,
// or a port past 65535, can no longer be reduced and planning fails closed.
func TestPlanAGYPermissionsURLHostBoundaries(t *testing.T) {
	label := strings.Repeat("a", 61)
	host := strings.Join([]string{label, label, label, label}, ".") + ".abcde"
	if len(host) != 253 {
		t.Fatalf("test host length = %d", len(host))
	}
	for _, test := range []struct {
		blocking string
		declared string
		want     string
	}{
		{blocking: "read_url(https://" + host + ":65535/x)", declared: "read_url(" + host + ")", want: "overlaps permissions.deny"},
		{blocking: "read_url(https://" + host + ":65536/x)", declared: "read_url(" + host + ")", want: "cannot prove non-overlap"},
		{blocking: "read_url(https://f" + host + "/x)", declared: "read_url(" + host + ")", want: "cannot prove non-overlap"},
	} {
		existing, err := json.Marshal(map[string]any{"permissions": map[string][]string{"deny": {test.blocking}}})
		if err != nil {
			t.Fatal(err)
		}
		plan, err := PlanAGYPermissions(t.Context(), existing, config.ClientPermissions{Manage: true, Allow: []string{test.declared}})
		if err == nil || plan != nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%s: plan=%+v err=%v, want %q", test.blocking, plan, err, test.want)
		}
	}
}

func TestPlanAGYPermissionsValidatesDeclaredRuleGrammar(t *testing.T) {
	valid := []string{
		"read_file(/var/log/app)",
		"write_file(src/)",
		"read_url(example.test)",
		"execute_url(example.test)",
		"read_url(*)",
		"execute_url(127.0.0.1)",
		"command(git status)",
		"command(regex:npm run (build|lint|test))",
		"mcp(hindsight/hindsight_list_knowledge_pages)",
	}
	if _, err := PlanAGYPermissions(t.Context(), nil, config.ClientPermissions{Manage: true, Allow: valid}); err != nil {
		t.Fatalf("supported rules rejected: %v", err)
	}

	invalid := []string{
		"unsandboxed(git status)",
		"unknown(target)",
		"command()",
		"command(regex:)",
		"command(regex:   )",
		"command(regex:[)",
		"command(regex:(git)",
		"command(regex:git (?=status))",
		"command(regex:)|.*(?:)",
		"command(regex:)|(?s:.*)(?:)",
		"command(regex:)|(?P<captured>.*)(?:)",
		`command(regex:)|\Qliteral\E(?:)`,
		"command",
		" command(git)",
		"command(git) trailing",
		"Command(git)",
		"mcp(server/tool)\ncommand(git)",
		"command(echo $HOME)",
		"command(`whoami`)",
		"read_file({env:HOME})",
		"read_file({file:secret})",
		"read_file(/tmp/*)",
		"read_url(*.example.test)",
		"read_url(https://example.test)",
		"read_url(example.test:443)",
		"read_url(example.test/path)",
		"read_url(EXAMPLE.test)",
		"read_url(example.test.)",
		"execute_url(user@example.test)",
		"execute_url(bad_name.example)",
		"execute_url(0x7f.0.0.1)",
		"execute_url(example.1)",
		"execute_url()",
		"mcp(server)",
		"mcp(/tool)",
		"mcp(server/)",
		"mcp(server/query_*)",
		"mcp(*/*)",
	}
	for _, rule := range invalid {
		t.Run(rule, func(t *testing.T) {
			plan, err := PlanAGYPermissions(t.Context(), nil, config.ClientPermissions{Manage: true, Allow: []string{rule}})
			if err == nil || plan != nil {
				t.Fatalf("invalid rule accepted: %q, %+v", rule, plan)
			}
		})
	}
}

func TestPlanAGYPermissionsRejectsAmbiguousOrMalformedSettings(t *testing.T) {
	cases := []string{
		`{ // comment
          "permissions": {}}`,
		`{"permissions": {},}`,
		`{"permissions": {}, "permissions": {}}`,
		`[]`,
		`{} {}`,
		`{"permissions": []}`,
		`{"permissions": {"allow": null}}`,
		`{"permissions": {"allow": [1]}}`,
		`{"permissions": {"ask": {}}}`,
		`{"permissions": {"deny": "command(rm)"}}`,
	}
	for _, existing := range cases {
		t.Run(existing, func(t *testing.T) {
			plan, err := PlanAGYPermissions(t.Context(), []byte(existing), config.ClientPermissions{
				Manage: true,
				Allow:  []string{"command(git status)"},
			})
			if err == nil || plan != nil {
				t.Fatalf("malformed settings accepted: %+v", plan)
			}
		})
	}
}

func TestPlanAGYPermissionsUnmanagedIsAnUnparsedNoOpWithinTheInputBound(t *testing.T) {
	existing := []byte("not JSON and intentionally unparsed")
	plan, err := PlanAGYPermissions(t.Context(), existing, config.ClientPermissions{
		Manage: false,
		Allow:  []string{"not an AGY rule"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Managed || plan.Changed || plan.BeforeCount != 0 || plan.AfterCount != 0 || len(plan.Added) != 0 ||
		plan.SourceSHA256 != digest(existing) || !bytes.Equal(plan.Content, existing) {
		t.Fatalf("unmanaged plan is not a byte-identical no-op: %+v", plan)
	}
	plan.Content[0] = 'N'
	if existing[0] != 'n' {
		t.Fatal("plan content aliases caller input")
	}

	oversized := make([]byte, MaxConfigBytes+1)
	if got, err := PlanAGYPermissions(t.Context(), oversized, config.ClientPermissions{}); err == nil || got != nil {
		t.Fatalf("oversized unmanaged input accepted: plan_present=%t", got != nil)
	}
}

func TestPlanAGYPermissionsReplayIsByteIdenticalAboveThePolicyGrantBound(t *testing.T) {
	existingRules := make([]string, config.MaxPermissionGrants+2)
	for i := range existingRules {
		existingRules[i] = fmt.Sprintf("command(tool-%02d)", i)
	}
	encodedRules, err := json.Marshal(existingRules)
	if err != nil {
		t.Fatal(err)
	}
	existing := []byte(`{ "future" : 9007199254740993, "permissions" : { "allow" : ` + string(encodedRules) + `, "ask" : ["read_url(*)"], "deny" : ["unsandboxed(old-rule)"] } }`)
	plan, err := PlanAGYPermissions(t.Context(), existing, config.ClientPermissions{
		Manage: true,
		Allow:  []string{existingRules[len(existingRules)-1]},
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Changed || plan.BeforeCount != len(existingRules) || plan.AfterCount != len(existingRules) ||
		len(plan.Added) != 0 || !bytes.Equal(plan.Content, existing) {
		t.Fatalf("replay changed existing settings: changed=%t before=%d after=%d added=%q",
			plan.Changed, plan.BeforeCount, plan.AfterCount, plan.Added)
	}

	missing := "mcp(hindsight/hindsight_list_knowledge_pages)"
	changed, err := PlanAGYPermissions(t.Context(), existing, config.ClientPermissions{Manage: true, Allow: []string{missing, missing}})
	if err != nil {
		t.Fatal(err)
	}
	if !changed.Changed || changed.BeforeCount != len(existingRules) || changed.AfterCount != len(existingRules)+1 ||
		!slices.Equal(changed.Added, []string{missing}) || !bytes.Contains(changed.Content, []byte("9007199254740993")) ||
		!bytes.Contains(changed.Content, []byte(`"unsandboxed(old-rule)"`)) {
		t.Fatalf("merge above policy bound lost data: changed=%t before=%d after=%d added=%q content_bytes=%d",
			changed.Changed, changed.BeforeCount, changed.AfterCount, changed.Added, len(changed.Content))
	}
	if strings.Count(string(changed.Content), missing) != 1 {
		t.Fatalf("duplicate declaration appended more than once: %s", changed.Content)
	}
}

func TestPlanAGYPermissionsDeclaredBoundaries(t *testing.T) {
	maxRule := "command(" + strings.Repeat("x", maxAGYPermissionRuleBytes-len("command()")) + ")"
	if len(maxRule) != maxAGYPermissionRuleBytes {
		t.Fatalf("test rule length = %d", len(maxRule))
	}
	maxRegexRule := "command(regex:" + strings.Repeat("x", maxAGYPermissionRuleBytes-len("command(regex:)")) + ")"
	if len(maxRegexRule) != maxAGYPermissionRuleBytes {
		t.Fatalf("test regex rule length = %d", len(maxRegexRule))
	}
	if got, err := PlanAGYPermissions(t.Context(), nil, config.ClientPermissions{Manage: true, Allow: []string{maxRegexRule}}); err != nil || got == nil || !got.Changed {
		t.Fatalf("maximum regex declaration rejected: plan=%+v err=%v", got, err)
	}
	maximum := make([]string, config.MaxPermissionGrants)
	for i := range maximum {
		maximum[i] = maxRule
	}
	plan, err := PlanAGYPermissions(t.Context(), nil, config.ClientPermissions{Manage: true, Allow: maximum})
	if err != nil || !plan.Changed || plan.BeforeCount != 0 || plan.AfterCount != 1 || len(plan.Added) != 1 {
		t.Fatalf("maximum declaration rejected or duplicated: err=%v plan=%+v", err, plan)
	}

	tooMany := append(slices.Clone(maximum), "command(extra)")
	if got, err := PlanAGYPermissions(t.Context(), nil, config.ClientPermissions{Manage: true, Allow: tooMany}); err == nil || got != nil {
		t.Fatalf("declaration above %d grants accepted", config.MaxPermissionGrants)
	}
	tooLong := maxRule[:len(maxRule)-1] + "x)"
	if got, err := PlanAGYPermissions(t.Context(), nil, config.ClientPermissions{Manage: true, Allow: []string{tooLong}}); err == nil || got != nil {
		t.Fatalf("%d-byte permission accepted", len(tooLong))
	}
}

func TestPlanAGYPermissionsContext(t *testing.T) {
	var nilContext context.Context
	if plan, err := PlanAGYPermissions(nilContext, nil, config.ClientPermissions{}); err == nil || plan != nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if plan, err := PlanAGYPermissions(ctx, nil, config.ClientPermissions{}); err == nil || plan != nil {
		t.Fatal("canceled context accepted")
	}
}
