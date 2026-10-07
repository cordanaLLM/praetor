// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package nodemanifest

import (
	"encoding/json"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/strictjson"
)

// MaxManifestBytes bounds the package.json ParseManifest reads (HISS-02): the bound every caller
// already reads the file under.
const MaxManifestBytes = 1 << 20

// maxManifestDepth bounds the nesting of a package.json ParseManifest accepts (HISS-02).
const maxManifestDepth = 64

// Manifest is the part of one package.json the dependency readers use: its name and its four
// direct dependency groups, each mapping a package name to the version range it declares.
type Manifest struct {
	Name                 string            `json:"name"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

// ParseManifest decodes one package.json. It is the one package.json dependency reader: the
// needs analyzer, the bump scanner, the documentation scanner and the credits inventory all
// call it. The text must be exactly one strict JSON document within MaxManifestBytes in which no
// object repeats a member name (strictjson.Validate), because npm keeps the last of two equal
// names while another reader may keep the first. A member the struct does not name is ignored.
func ParseManifest(data []byte) (Manifest, error) {
	opts := strictjson.Options{MaxBytes: MaxManifestBytes, MaxDepth: maxManifestDepth}
	if err := strictjson.Validate(data, opts); err != nil {
		return Manifest{}, fmt.Errorf("package.json: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("package.json: %w", err)
	}
	return manifest, nil
}

// Groups returns the four dependency groups in the order npm documents them: dependencies,
// devDependencies, peerDependencies, optionalDependencies. A group the file omits is nil.
func (m Manifest) Groups() []map[string]string {
	return []map[string]string{m.Dependencies, m.DevDependencies, m.PeerDependencies, m.OptionalDependencies}
}
