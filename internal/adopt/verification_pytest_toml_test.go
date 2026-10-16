// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"strings"
	"testing"
)

func TestPytestConfigurationReadsDottedTOMLTable(t *testing.T) {
	inputs := verificationInputs{files: map[string][]byte{
		"pyproject.toml": []byte("tool.pytest.ini_options.testpaths = [\"tests\"]\n"),
	}}
	config, found, err := pytestConfiguration(inputs)
	if err != nil || !found || config != "pyproject.toml [tool.pytest.ini_options]" {
		t.Fatalf("pytestConfiguration = %q, %v, %v", config, found, err)
	}
}

func TestPytestConfigurationRejectsMalformedTOMLWithFileContext(t *testing.T) {
	inputs := verificationInputs{files: map[string][]byte{
		"pyproject.toml": []byte("[project]\nversion = \"1\"\nversion = \"2\"\n"),
	}}
	if _, _, err := pytestConfiguration(inputs); err == nil || !strings.Contains(err.Error(), "pyproject.toml") {
		t.Fatalf("pytestConfiguration error = %v, want file context", err)
	}
}
