package dogfood

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
)

const (
	// MaxSuiteCases bounds all public and transcript cases in one invocation.
	MaxSuiteCases       = 8
	maxSuiteConfigBytes = 64 * 1024
)

var suiteID = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)
var suiteSHA = regexp.MustCompile(`^[0-9a-f]{64}$`)

// SuiteConfig declares immutable inputs only; execution stage is a separate option.
type SuiteConfig struct {
	Version            int               `json:"version"`
	PublicRepositories []string          `json:"public_repositories"`
	Transcripts        []SuiteTranscript `json:"transcripts"`
	InputLimits        *InputLimits      `json:"input_limits,omitempty"`
}

// SuiteTranscript explicitly identifies private local observations to replay.
type SuiteTranscript struct {
	ID         string `json:"id"`
	SourcePath string `json:"source_path"`
	SHA256     string `json:"sha256"`
	Format     string `json:"format"`
}

func loadSuiteConfig(ctx context.Context, path string) (*SuiteConfig, string, error) {
	data, err := readSuiteConfig(ctx, path)
	if err != nil {
		return nil, "", err
	}
	fields, err := suiteObject(data, []string{"version", "public_repositories", "transcripts"}, "input_limits")
	if err != nil {
		return nil, "", err
	}
	config, err := decodeSuiteConfig(fields)
	if err != nil {
		return nil, "", err
	}
	if err := validateSuiteConfig(config); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return config, hex.EncodeToString(sum[:]), nil
}

func decodeSuiteConfig(fields map[string]json.RawMessage) (*SuiteConfig, error) {
	var config SuiteConfig
	var err error
	config.InputLimits, err = decodeInputLimits(fields["input_limits"])
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(fields["version"], &config.Version); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(fields["public_repositories"], &config.PublicRepositories); err != nil {
		return nil, err
	}
	var transcripts []json.RawMessage
	if err := json.Unmarshal(fields["transcripts"], &transcripts); err != nil {
		return nil, err
	}
	if config.PublicRepositories == nil || transcripts == nil || len(transcripts) > MaxSuiteCases {
		return nil, errors.New("suite lists must be arrays with at most eight transcript cases")
	}
	for i := 0; i < len(transcripts); i++ {
		if _, err := suiteObject(transcripts[i], []string{"id", "source_path", "sha256", "format"}); err != nil {
			return nil, err
		}
		var source SuiteTranscript
		if err := json.Unmarshal(transcripts[i], &source); err != nil {
			return nil, err
		}
		config.Transcripts = append(config.Transcripts, source)
	}
	return &config, nil
}

// Decode each fixed object explicitly: struct decoding alone accepts duplicate and
// case-folded keys, which make a supposedly pinned configuration ambiguous.
func suiteObject(data []byte, names []string, optional ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("suite configuration requires JSON objects")
	}
	fields := make(map[string]json.RawMessage)
	allowed := append(append([]string(nil), names...), optional...)
	for i := 0; decoder.More() && i <= len(allowed); i++ {
		if err := decodeSuiteField(decoder, fields, allowed); err != nil {
			return nil, err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	for _, name := range names {
		if fields[name] == nil {
			return nil, errors.New("suite object is missing required fields")
		}
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing suite JSON content")
	}
	return fields, nil
}

func decodeSuiteField(decoder *json.Decoder, fields map[string]json.RawMessage, names []string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	key, ok := token.(string)
	if !ok || !suiteHasKey(names, key) || fields[key] != nil {
		return errors.New("unknown or duplicate suite field")
	}
	var value json.RawMessage
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	fields[key] = value
	return nil
}

func suiteHasKey(names []string, key string) bool {
	for i := 0; i < len(names) && i < 8; i++ {
		if names[i] == key {
			return true
		}
	}
	return false
}

func validateSuiteConfig(config *SuiteConfig) error {
	count := len(config.PublicRepositories) + len(config.Transcripts)
	if config.Version != 1 || count < 1 || count > MaxSuiteCases {
		return errors.New("suite version must be 1 with 1..8 total cases")
	}
	sources, err := parsePublicSources(config.PublicRepositories)
	if err != nil {
		return err
	}
	for i := 0; i < len(sources); i++ {
		if sources[i].sha == "" {
			return errors.New("suite public repositories require immutable commit pins")
		}
	}
	return validateSuiteSources(config)
}

func validateSuiteSources(config *SuiteConfig) error {
	seenID, seenPath := make(map[string]bool), make(map[string]bool)
	for i := 0; i < len(config.PublicRepositories); i++ {
		seenID[fmt.Sprintf("public-%02d", i+1)] = true
	}
	for i := 0; i < len(config.Transcripts); i++ {
		source := config.Transcripts[i]
		if err := validateSuiteTranscript(source); err != nil {
			return err
		}
		if seenID[source.ID] || seenPath[source.SourcePath] {
			return errors.New("suite requires unique case IDs and transcript paths")
		}
		seenID[source.ID], seenPath[source.SourcePath] = true, true
	}
	return nil
}

func validateSuiteTranscript(source SuiteTranscript) error {
	if !suiteID.MatchString(source.ID) || !suiteSHA.MatchString(source.SHA256) {
		return errors.New("transcript requires a valid ID and lowercase SHA256")
	}
	if err := validateSuitePath(source.SourcePath); err != nil {
		return err
	}
	if !filepath.IsAbs(source.SourcePath) || filepath.Clean(source.SourcePath) != source.SourcePath {
		return errors.New("suite transcript paths must be clean absolute paths")
	}
	if source.Format != "antigravity-jsonl-v1" && source.Format != "claude-code-jsonl-v1" {
		return errors.New("unsupported suite transcript format")
	}
	return nil
}
