// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestOperatorHolds_LayersAppendAndDeduplicate(t *testing.T) {
	first := "{file: '~/lanes/hold/agy', commands: [agy, praetor-agy-wrapper], via: [agy-run.sh]}"
	second := "{file: '${LANE_ROOT}/hold/other', commands: [other], via: [other-run.sh]}"
	policy, err := loadAdmissionLayers(t, "hooks: {holds: ["+first+"]}\n", "hooks: {holds: ["+first+", "+second+"]}\n")
	if err != nil {
		t.Fatal(err)
	}
	holds := policy.OperatorSettings().Hooks.Holds
	if len(holds) != 2 || holds[0].File != "~/lanes/hold/agy" || holds[1].File != "${LANE_ROOT}/hold/other" {
		t.Fatalf("merged holds: %+v", holds)
	}
	if !reflect.DeepEqual(holds[0].Commands, []string{"agy", "praetor-agy-wrapper"}) ||
		!reflect.DeepEqual(policy.OperatorFields["hooks.holds"], []string{"fleet", "workstation"}) {
		t.Fatalf("lost fields/contributors: %+v, %v", holds, policy.OperatorFields)
	}
	if err := policy.VerifyDigest(); err != nil {
		t.Fatal(err)
	}
	policy.Operator.Hooks.Holds[0].File = "changed"
	if policy.VerifyDigest() == nil {
		t.Fatal("hold tampering accepted")
	}
}

func TestOperatorHolds_InvalidEntries(t *testing.T) {
	for _, entry := range []string{
		"{file: hold, commands: [agy]}", "{commands: [agy], via: [run.sh]}",
		"{file: '', commands: [agy], via: [run.sh]}",
		"{file: hold, commands: [], via: [run.sh]}",
		"{file: hold, commands: [agy], via: []}",
		"{file: hold, commands: [/bin/agy], via: [run.sh]}",
		"{file: hold, commands: [agy], via: [agy]}",
		"{file: hold, commands: [agy], via: [run.sh], typo: true}",
		"{file: hold, file: other, commands: [agy], via: [run.sh]}",
		"{file: 123, commands: [agy], via: [run.sh]}", "agy",
	} {
		if _, err := loadAdmissionLayers(t, "hooks: {holds: ["+entry+"]}\n", ""); err == nil {
			t.Errorf("invalid entry accepted: %s", entry)
		}
	}
	if _, err := loadAdmissionLayers(t, "hooks: {holds: {file: hold}}\n", ""); err == nil {
		t.Fatal("mapping accepted as a list")
	}
}

func holdDocument(from, count int) string {
	var body strings.Builder
	body.WriteString("hooks:\n  holds:\n")
	for i := from; i < from+count; i++ {
		fmt.Fprintf(&body, "    - {file: 'hold-%d', commands: [lane], via: [run.sh]}\n", i)
	}
	return body.String()
}

func TestOperatorHolds_BoundsAndDefault(t *testing.T) {
	if _, err := loadAdmissionLayers(t, holdDocument(0, 16), holdDocument(16, 16)); err != nil {
		t.Fatalf("merged limit refused: %v", err)
	}
	if _, err := loadAdmissionLayers(t, holdDocument(0, 16), holdDocument(16, 17)); err == nil {
		t.Fatal("merged list over limit accepted")
	}
	if _, err := loadAdmissionLayers(t, holdDocument(0, MaxHookHolds+1), ""); err == nil {
		t.Fatal("single list over limit accepted")
	}
	policy, err := loadAdmissionLayers(t, "hooks: {holds: []}\n", "")
	if err != nil || len(policy.OperatorSettings().Hooks.Holds) != 0 {
		t.Fatalf("empty holds: %+v, %v", policy, err)
	}
	if len(DefaultOperatorSettings().Hooks.Holds) != 0 {
		t.Fatal("holds enabled by default")
	}
}

func TestValidateHookHolds_ProgramAndPathBounds(t *testing.T) {
	valid := HoldSettings{File: "hold", Commands: []string{"lane"}, Via: []string{"run.sh"}}
	for _, field := range []string{"commands", "via"} {
		hold := valid
		names := make([]string, MaxHoldPrograms)
		for i := range names {
			names[i] = fmt.Sprintf("tool-%d", i)
		}
		if field == "commands" {
			hold.Commands = names
		} else {
			hold.Via = names
		}
		if err := ValidateHookHolds([]HoldSettings{hold}); err != nil {
			t.Fatalf("%s exact bound: %v", field, err)
		}
		if field == "commands" {
			hold.Commands = append(names, "extra")
		} else {
			hold.Via = append(names, "extra")
		}
		if ValidateHookHolds([]HoldSettings{hold}) == nil {
			t.Fatalf("%s over bound accepted", field)
		}
	}
	for _, file := range []string{"", "hold\n", string([]byte{0xff}), strings.Repeat("x", maxSettingValueBytes+1)} {
		hold := valid
		hold.File = file
		if ValidateHookHolds([]HoldSettings{hold}) == nil {
			t.Fatalf("invalid path accepted: %q", file)
		}
	}
}
