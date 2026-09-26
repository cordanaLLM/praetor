package forge

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// labelTaxonomy is the document shape of .config/labels.yaml.
type labelTaxonomy struct {
	Version int     `yaml:"version"`
	Labels  []Label `yaml:"labels"`
}

// ParseLabelTaxonomy decodes a label taxonomy such as .config/labels.yaml into the labels
// ReconcileLabels writes. The document must be exactly one YAML document with version 1
// and 1 to MaxLabelsLimit labels; unknown keys are rejected, names must be nonempty,
// trimmed and unique, and colors must be six hexadecimal digits. A taxonomy that passes
// is therefore one GitHub accepts label by label, and one sync can write in one batch.
func ParseLabelTaxonomy(data []byte) ([]Label, error) {
	var taxonomy labelTaxonomy
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&taxonomy); err != nil {
		return nil, fmt.Errorf("parse label taxonomy: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("label taxonomy requires exactly one document")
	}
	if taxonomy.Version != 1 || len(taxonomy.Labels) == 0 || len(taxonomy.Labels) > MaxLabelsLimit {
		return nil, fmt.Errorf("label taxonomy requires version 1 and 1-%d labels", MaxLabelsLimit)
	}
	if err := validateLabelEntries(taxonomy.Labels); err != nil {
		return nil, err
	}
	return taxonomy.Labels, nil
}

func validateLabelEntries(labels []Label) error {
	seen := make(map[string]bool, len(labels))
	for i := 0; i < len(labels) && i < MaxLabelsLimit; i++ {
		label := labels[i]
		name := strings.TrimSpace(label.Name)
		if name == "" || name != label.Name || seen[name] {
			return errors.New("label names must be nonempty, trimmed and unique")
		}
		if color, err := hex.DecodeString(label.Color); err != nil || len(color) != 3 {
			return errors.New("label colors must contain exactly six hexadecimal digits")
		}
		seen[name] = true
	}
	return nil
}
