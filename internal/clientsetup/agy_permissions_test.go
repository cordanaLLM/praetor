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
		{name: "ask URL ignores blocking userinfo", list: "ask", blocking: "execute_url(user@example.test)", declared: "execute_url(example.test)"},
		{name: "deny URL ignores bracketed IPv6 port", list: "deny", blocking: "read_url([::1]:8080)", declared: "read_url(::1)"},
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
