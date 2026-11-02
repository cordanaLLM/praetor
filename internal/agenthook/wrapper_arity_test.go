// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestWrapperOptionArityFindsProtectedPrivilegeCommand(t *testing.T) {
	data, err := os.ReadFile("testdata/privilege-wrappers.json")
	if err != nil {
		t.Fatal(err)
	}
	var replay struct {
		Commands []string `json:"commands"`
		DataOnly []string `json:"data_only"`
	}
	if err := json.Unmarshal(data, &replay); err != nil {
		t.Fatal(err)
	}
	matcher := regexp.MustCompile(privilegeCommandSource)
	for _, wrapper := range replay.Commands {
		t.Run(wrapper, func(t *testing.T) {
			words := strings.Fields(wrapper + " sudo praetorctl conform /srv/dev")
			if got := commandAfter(words, 0, admissionWrappers[words[0]]); got >= len(words) || words[got] != "sudo" {
				t.Errorf("wrapper option consumed incorrectly: command index %d in %q", got, words)
			}
			for _, escalator := range []string{"sudo", "doas"} {
				command := wrapper + " " + escalator + " praetorctl conform /srv/dev"
				if !matcher.MatchString(command) {
					t.Errorf("protected adoption escaped wrapper rule: %q", command)
				}
			}
		})
	}
	for _, command := range replay.DataOnly {
		if matcher.MatchString(command) {
			t.Errorf("option data treated as privilege invocation: %q", command)
		}
	}
}
