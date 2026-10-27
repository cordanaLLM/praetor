// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Order: engine-fr-rule-checks-20261027. Reuse the TOML decoder and bounded discovery.
var msrvVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+(?:\.[0-9]+)?$`)

type msrvPackage struct {
	RustVersion any `toml:"rust-version"`
}
type msrvDocument struct {
	Package   msrvPackage `toml:"package"`
	Workspace struct {
		Package msrvPackage `toml:"package"`
	} `toml:"workspace"`
}
type msrvDeclaration struct{ file, version string }

// AuditRustMSRV requires a pinned minimum or a workflow job building on the declared minimum.
// This checks repository declarations; it does not execute the workflow or compile Rust.
func AuditRustMSRV(ctx context.Context, root string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, contextopt.MaxDuration)
	defer cancel()
	inputs, err := loadVerificationInputs(ctx, root)
	if err != nil {
		return "", fmt.Errorf("[FAIL] rust-msrv: %w", err)
	}
	declarations, count, err := rustMSRVDeclarations(inputs.files)
	if err != nil {
		return "", fmt.Errorf("[FAIL] rust-msrv: %w", err)
	}
	if count == 0 {
		return "[SKIP] rust-msrv: no Cargo.toml found.", nil
	}
	if len(declarations) == 0 {
		return "[PASS] rust-msrv: No rust-version declared in Cargo.toml files.", nil
	}
	proofs, err := rustMSRVWorkflowProofs(inputs.files)
	if err != nil {
		return "", fmt.Errorf("[FAIL] rust-msrv: %w", err)
	}
	return judgeRustMSRV(inputs.files, declarations, proofs)
}

func judgeRustMSRV(files map[string][]byte, declarations []msrvDeclaration, proofs map[string]string) (string, error) {
	var evidence []string
	for i := 0; i < len(declarations) && i < maxVerificationInputs*2; i++ {
		declaration := declarations[i]
		pin, err := rustMSRVPin(files, path.Dir(declaration.file))
		if err != nil {
			return "", fmt.Errorf("[FAIL] rust-msrv: %s: %w", declaration.file, err)
		}
		proof := proofs[declaration.version]
		if !rustMSRVMatches(pin, declaration.version) && proof == "" {
			return "", fmt.Errorf("[FAIL] rust-msrv: %s declares rust-version %s; pinned channel %q does not prove it; pin that minimum or add a workflow job installing it and running cargo build, check or test", declaration.file, declaration.version, pin)
		}
		if proof == "" {
			proof = "pinned channel " + pin
		}
		evidence = append(evidence, declaration.file+" "+declaration.version+" ("+proof+")")
	}
	return "[PASS] rust-msrv: " + strings.Join(evidence, "; "), nil
}

func rustMSRVDeclarations(files map[string][]byte) ([]msrvDeclaration, int, error) {
	documents, names, err := rustMSRVDocuments(files)
	if err != nil {
		return nil, 0, err
	}
	var declarations []msrvDeclaration
	for i := 0; i < len(names) && i < maxVerificationInputs; i++ {
		name := names[i]
		document := documents[name]
		version, err := rustMSRVValue(document.Package.RustVersion, name, documents)
		if err != nil {
			return nil, 0, err
		}
		workspace, err := rustMSRVValue(document.Workspace.Package.RustVersion, name, nil)
		if err != nil {
			return nil, 0, err
		}
		if version != "" {
			declarations = append(declarations, msrvDeclaration{name, version})
		}
		if workspace != "" && workspace != version {
			declarations = append(declarations, msrvDeclaration{name, workspace})
		}
	}
	return declarations, len(names), nil
}

func rustMSRVDocuments(files map[string][]byte) (map[string]msrvDocument, []string, error) {
	documents := map[string]msrvDocument{}
	var names []string
	for name, data := range files {
		if path.Base(name) != "Cargo.toml" {
			continue
		}
		var document msrvDocument
		if err := util.DecodeTOML(name, data, &document); err != nil {
			return nil, nil, err
		}
		documents[name] = document
		names = append(names, name)
	}
	slices.Sort(names)
	return documents, names, nil
}

func rustMSRVValue(value any, file string, documents map[string]msrvDocument) (string, error) {
	if value == nil {
		return "", nil
	}
	if text, ok := value.(string); ok && msrvVersion.MatchString(text) {
		return text, nil
	}
	inherit, ok := value.(map[string]any)
	if !ok || inherit["workspace"] != true || len(inherit) != 1 || documents == nil {
		return "", fmt.Errorf("%s has invalid rust-version %v", file, value)
	}
	return inheritedRustMSRV(file, documents)
}

func inheritedRustMSRV(file string, documents map[string]msrvDocument) (string, error) {
	dir := path.Dir(file)
	for depth := 0; depth <= maxVerificationDepth; depth++ {
		document := documents[path.Join(dir, "Cargo.toml")]
		if text, ok := document.Workspace.Package.RustVersion.(string); ok && msrvVersion.MatchString(text) {
			return text, nil
		}
		if dir == "." {
			break
		}
		dir = path.Dir(dir)
	}
	return "", fmt.Errorf("%s inherits rust-version without a workspace.package.rust-version", file)
}

func rustMSRVPin(files map[string][]byte, dir string) (string, error) {
	for depth := 0; depth <= maxVerificationDepth; depth++ {
		// rustup prefers rust-toolchain over rust-toolchain.toml when both exist.
		for _, base := range []string{"rust-toolchain", "rust-toolchain.toml"} {
			name := path.Join(dir, base)
			data, found := files[name]
			if !found {
				continue
			}
			text := strings.TrimSpace(string(data))
			if base == "rust-toolchain" && !strings.HasPrefix(text, "[") {
				return text, nil
			}
			var document struct {
				Toolchain struct {
					Channel string `toml:"channel"`
				} `toml:"toolchain"`
			}
			if err := util.DecodeTOML(name, data, &document); err != nil {
				return "", err
			}
			return document.Toolchain.Channel, nil
		}
		if dir == "." {
			break
		}
		dir = path.Dir(dir)
	}
	return "<none>", nil
}

func rustMSRVMatches(channel, declared string) bool {
	if !msrvVersion.MatchString(channel) {
		return false
	}
	if strings.Count(declared, ".") == 2 {
		return channel == declared
	}
	parts := strings.Split(channel, ".")
	return parts[0]+"."+parts[1] == declared
}
