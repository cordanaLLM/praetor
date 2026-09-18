package config

import (
	"encoding/hex"
	"errors"
	"strings"
	"unicode"
)

// VerifyDigest checks a retained snapshot's bounded metadata against its digest
// without mutating it or reading files. It detects inconsistent metadata, not
// source authenticity: it does not verify a signature or reread source bytes.
func (p *EffectivePolicy) VerifyDigest() error {
	if p == nil || len(p.SHA256) != 64 {
		return errors.New("effective policy requires a SHA-256 identity")
	}
	if err := p.verifyDigestMetadata(); err != nil {
		return err
	}
	snapshot := *p
	if err := snapshot.seal(); err != nil {
		return err
	}
	if snapshot.SHA256 != p.SHA256 {
		return errors.New("effective policy digest does not match retained metadata")
	}
	return nil
}

func (p *EffectivePolicy) verifyDigestMetadata() error {
	names := complexityNames()
	if len(p.Sources) == 0 || len(p.Sources) > maxPolicyLayers+1 || len(p.Fields) != len(names) {
		return errors.New("effective policy sources or fields exceed their bounds")
	}
	seen := make(map[string]bool, len(p.Sources))
	for i := 0; i < len(p.Sources) && i <= maxPolicyLayers; i++ {
		if err := verifyDigestSource(p.Sources[i], seen); err != nil {
			return err
		}
	}
	if err := verifyDigestFields(names, p.Fields, seen); err != nil {
		return err
	}
	if err := verifyOperatorFields(p.Operator, p.OperatorFields, seen); err != nil {
		return err
	}
	return errors.Join(verifyDigestNames(p.Policy.Linters), verifyDigestNames(p.Policy.DevFeatures))
}

// verifyOperatorFields checks that operator settings and their contributors appear together,
// stay within the settings bound and name only retained sources.
func verifyOperatorFields(operator *OperatorSettings, fields map[string][]string, seen map[string]bool) error {
	if (operator == nil) != (len(fields) == 0) || len(fields) > maxOperatorSettings {
		return errors.New("effective operator settings and contributors must appear together and stay bounded")
	}
	for key, contributors := range fields {
		if len(key) > 512 || len(contributors) == 0 || len(contributors) > len(seen) {
			return errors.New("effective operator contributors exceed their bounds")
		}
		for i := 0; i < len(contributors) && i <= maxPolicyLayers; i++ {
			if !seen[contributors[i]] {
				return errors.New("effective operator contributor has no source")
			}
		}
	}
	return nil
}

func verifyDigestFields(names [4]string, fields map[string][]string, seen map[string]bool) error {
	for _, field := range names {
		contributors := fields[field]
		if len(contributors) == 0 || len(contributors) > len(seen) {
			return errors.New("effective policy contributors exceed their bounds")
		}
		for i := 0; i < len(contributors) && i <= maxPolicyLayers; i++ {
			if !seen[contributors[i]] {
				return errors.New("effective policy contributor has no source")
			}
		}
	}
	return nil
}

func verifyDigestSource(source PolicySource, seen map[string]bool) error {
	if source.ID == "" || len(source.ID) > 512 || seen[source.ID] || strings.ContainsFunc(source.ID, unicode.IsControl) {
		return errors.New("effective policy source IDs must be bounded and unique")
	}
	if len(source.Path) > 4096 || len(source.SHA256) != 64 {
		return errors.New("effective policy source path or digest exceeds its bound")
	}
	if _, err := hex.DecodeString(source.SHA256); err != nil {
		return errors.New("effective policy source requires a SHA-256 digest")
	}
	seen[source.ID] = true
	return nil
}

func verifyDigestNames(names []string) error {
	if len(names) > maxPolicyLayers+1 {
		return errors.New("effective policy names exceed their count bound")
	}
	for i := 0; i < len(names) && i <= maxPolicyLayers; i++ {
		if len(names[i]) > 512 {
			return errors.New("effective policy name exceeds its byte bound")
		}
	}
	return nil
}
