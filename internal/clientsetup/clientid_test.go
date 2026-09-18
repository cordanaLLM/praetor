// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package clientsetup

import (
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/clientid"
)

// The adapter table and the shared client list must name the same clients. Operator settings
// accept exactly the clientid list, so a client without an adapter row would be accepted in a
// settings file and then fail at reconcile time, and a row without an id could never be selected.
func TestAdapterTableMatchesSharedClientList(t *testing.T) {
	rows := make([]clientid.ID, 0, len(adapters))
	for client := range adapters {
		rows = append(rows, client)
	}
	slices.Sort(rows)
	if known := clientid.Known(); !slices.Equal(rows, known) {
		t.Fatalf("adapter rows %v differ from known clients %v", rows, known)
	}
}
