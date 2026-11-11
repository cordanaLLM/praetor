// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

import "testing"

// TestScannerInstallCommand_Shell verifies labeled Windows shells and literal path escaping.
func TestScannerInstallCommand_Shell(t *testing.T) {
	for _, tc := range []struct{ name, goos, path, want string }{
		{"posix", "linux", "/repo/tools/go", "GOWORK=off go -C '/repo/tools/go' install scanner"},
		{"windows", "windows", `C:\repo\tools\go`,
			"Command Prompt: set \"GOWORK=off\" && go -C \"C:\\repo\\tools\\go\" install scanner\n" +
				"PowerShell: $env:GOWORK='off'; go -C 'C:\\repo\\tools\\go' install scanner"},
		{"windows apostrophe", "windows", `C:\O'Brien space\tools\go`,
			"Command Prompt: set \"GOWORK=off\" && go -C \"C:\\O'Brien space\\tools\\go\" install scanner\n" +
				"PowerShell: $env:GOWORK='off'; go -C 'C:\\O''Brien space\\tools\\go' install scanner"},
		{"windows percent", "windows", `C:\%USERPROFILE%\tools\go`,
			"PowerShell: $env:GOWORK='off'; go -C 'C:\\%USERPROFILE%\\tools\\go' install scanner\n" +
				"The checkout path contains % or !, which cmd.exe expands; only the PowerShell form is given."},
		{"windows delayed expansion", "windows", `C:\!checkout!\tools\go`,
			"PowerShell: $env:GOWORK='off'; go -C 'C:\\!checkout!\\tools\\go' install scanner\n" +
				"The checkout path contains % or !, which cmd.exe expands; only the PowerShell form is given."},
		{"windows percent apostrophe", "windows", `C:\O'Brien %checkout%\tools\go`,
			"PowerShell: $env:GOWORK='off'; go -C 'C:\\O''Brien %checkout%\\tools\\go' install scanner\n" +
				"The checkout path contains % or !, which cmd.exe expands; only the PowerShell form is given."},
		{"windows substitutions", "windows", "C:\\$(touch x) `touch y`\\tools\\go",
			"Command Prompt: set \"GOWORK=off\" && go -C \"C:\\$(touch x) `touch y`\\tools\\go\" install scanner\n" +
				"PowerShell: $env:GOWORK='off'; go -C 'C:\\$(touch x) `touch y`\\tools\\go' install scanner"},
		{"empty path", "windows", "",
			"Command Prompt: set \"GOWORK=off\" && go -C \"\" install scanner\n" +
				"PowerShell: $env:GOWORK='off'; go -C '' install scanner"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := scannerInstallCommand(tc.path, "scanner", tc.goos); got != tc.want {
				t.Errorf("command = %q; want %q", got, tc.want)
			}
		})
	}
}
