// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import "fmt"

func printRepositoryAudit(line string, err error) error {
	if err != nil {
		return err
	}
	fmt.Println(line)
	return nil
}
