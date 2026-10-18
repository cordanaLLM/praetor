// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"fmt"
	"os"

	"github.com/cordanaLLM/praetor/tools/secrets"
)

func main() {
	if err := secrets.WriteRulesGen("."); err != nil {
		fmt.Fprintf(os.Stderr, "generate rules_gen.go: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("generated tools/secrets/gate/rules_gen.go")
}
