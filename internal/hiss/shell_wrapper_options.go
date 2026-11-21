// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package hiss

import "slices"

// ShellWrapperOptionSpec describes option arity shared by shell readers.
// Unproven values retain the forge reader's arity knowledge without admitting
// directory, argument-vector or command transformations in here-documents.
type ShellWrapperOptionSpec struct {
	ValueShort, FlagShort                  string
	ValueLong, FlagLong, OptionalValueLong []string
	UnprovenValueShort                     string
	UnprovenValueLong                      []string
}

// ShellWrapperOptions is the common option table for privilege and environment
// wrappers. Each entry follows its named manual; it is an allow-list, not a claim
// to model every option in that manual. FlagLong also holds standalone flag words.
var ShellWrapperOptions = map[string]ShellWrapperOptionSpec{
	// sudo(8): options retained from both readers; --preserve-env takes an
	// optional value only when attached with '='.
	"sudo": {
		ValueShort: "CDghpRTUu", FlagShort: "ABEHkKnPS",
		ValueLong: []string{"--close-from", "--chdir", "--group", "--host", "--prompt",
			"--chroot", "--command-timeout", "--other-user", "--user"},
		FlagLong: []string{"--askpass", "--background", "--set-home", "--reset-timestamp",
			"--remove-timestamp", "--non-interactive", "--preserve-groups", "--stdin"},
		OptionalValueLong: []string{"--preserve-env"},
	},
	// doas(1): preserve the existing reader's -n/-L and -u/-C/-a shapes.
	"doas": {ValueShort: "uCa", FlagShort: "nL"},
	// env(1): only unset and environment/output flags prove a here-document
	// owner. chdir, split-string and argv0 remain known only by their arity.
	"env": {
		ValueShort: "u", ValueLong: []string{"--unset"}, FlagShort: "i0",
		FlagLong:           []string{"-", "--ignore-environment", "--null"},
		UnprovenValueShort: "CSa",
		UnprovenValueLong:  []string{"--chdir", "--split-string", "--argv0"},
	},
}

// ValueOptions returns every option whose separate next word is its value.
// Readers that only need arity include the unproven here-document options.
func (spec ShellWrapperOptionSpec) ValueOptions() map[string]bool {
	options := make(map[string]bool)
	short := spec.ValueShort + spec.UnprovenValueShort
	for i := 0; i < len(short) && i < MaxShellFields; i++ {
		options["-"+short[i:i+1]] = true
	}
	long := slices.Concat(spec.ValueLong, spec.UnprovenValueLong)
	for i := 0; i < len(long) && i < MaxShellFields; i++ {
		options[long[i]] = true
	}
	return options
}
