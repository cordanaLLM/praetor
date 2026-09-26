// Package funding renders a repository's funding and sponsorship surfaces (the GitHub
// FUNDING.yml file, the README badge row and support section, and the MkDocs social links)
// from operator configuration. Funding accounts are operator data: the engine reads them, it
// never embeds them, and without configuration it renders nothing rather than another
// operator's links (issue #222).
package funding

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ConfigFile is the operator-owned funding document, relative to the repository root. It
// sits under .config/operator/, which the engine ignores and the operational fork carries.
const ConfigFile = ".config/operator/funding.yaml"

const (
	maxConfigBytes = 64 << 10
	// maxAccounts mirrors GitHub's FUNDING.yml limits: four Sponsors accounts, four custom URLs.
	maxAccounts = 4
	// maxMessageBytes bounds the support-section sentence.
	maxMessageBytes = 1024
)

// ErrNotConfigured reports that the repository declares no funding configuration.
var ErrNotConfigured = errors.New("funding not configured")

// accountPattern admits the account slugs every supported platform uses.
var accountPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// Config is the operator's funding document. Its keys are the FUNDING.yml keys they render.
type Config struct {
	GitHub         []string `yaml:"github"`
	Polar          string   `yaml:"polar"`
	KoFi           string   `yaml:"ko_fi"`
	OpenCollective string   `yaml:"open_collective"`
	Custom         []string `yaml:"custom"`
	// Message replaces the default sentence of the README support section. It is operator
	// prose, one line of at most maxMessageBytes, and renders only when a channel does.
	Message string `yaml:"message"`
}

// channel describes one funding platform and how each surface presents it.
type channel struct {
	key   string // FUNDING.yml key
	label string // platform name
	url   string // account URL template
	badge string // shields.io label-message-colour segment
	logo  string // shields.io logo name
	icon  string // MkDocs Material icon
}

// channels is the one table of supported platforms, in rendering order.
var channels = [...]channel{
	{key: "github", label: "GitHub Sponsors", url: "https://github.com/sponsors/%s", badge: "Sponsor-GitHub_Sponsors-EA4AAA", logo: "githubsponsors", icon: "fontawesome/solid/heart"},
	{key: "polar", label: "Polar.sh", url: "https://polar.sh/%s", badge: "Bounties-Polar.sh-000000", logo: "polar", icon: "fontawesome/solid/bolt"},
	{key: "ko_fi", label: "Ko-fi", url: "https://ko-fi.com/%s", badge: "Support-Ko--fi-FF5E5B", logo: "kofi", icon: "fontawesome/solid/mug-hot"},
	{key: "open_collective", label: "Open Collective", url: "https://opencollective.com/%s", badge: "Donate-Open_Collective-7FADF2", logo: "opencollective", icon: "fontawesome/solid/hand-holding-dollar"},
}

// link is one configured account on one platform.
type link struct {
	channel channel
	account string
}

func (l link) url() string { return fmt.Sprintf(l.channel.url, l.account) }

// accounts returns the configured accounts for a channel key.
func (c *Config) accounts(key string) []string {
	if c == nil {
		return nil
	}
	single := map[string]string{"polar": c.Polar, "ko_fi": c.KoFi, "open_collective": c.OpenCollective}
	if key == "github" {
		return c.GitHub
	}
	if single[key] == "" {
		return nil
	}
	return []string{single[key]}
}

// links returns every configured platform account in channel order.
func (c *Config) links() []link {
	var out []link
	for _, ch := range channels {
		for _, account := range c.accounts(ch.key) {
			out = append(out, link{channel: ch, account: account})
		}
	}
	return out
}

// Configured reports whether the document names at least one funding destination. A nil
// config, an empty file and a file whose lists are empty all render nothing.
func (c *Config) Configured() bool {
	return c != nil && (len(c.links()) > 0 || len(c.Custom) > 0)
}

// Load reads the funding document at rel below root. An absent file returns
// ErrNotConfigured; a present file is decoded strictly and validated.
func Load(root, rel string) (*Config, error) {
	data, err := util.ReadConfinedLimited(root, rel, maxConfigBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s is absent", ErrNotConfigured, rel)
	}
	if err != nil {
		return nil, fmt.Errorf("read funding configuration %s: %w", rel, err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("funding configuration %s: %w", rel, err)
	}
	return cfg, nil
}

// Parse decodes a funding document, rejecting unknown keys, and validates every value.
func Parse(data []byte) (*Config, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	if len(c.GitHub) > maxAccounts || len(c.Custom) > maxAccounts {
		return fmt.Errorf("github and custom each accept at most %d entries", maxAccounts)
	}
	for _, ch := range channels {
		for _, account := range c.accounts(ch.key) {
			if !accountPattern.MatchString(account) {
				return fmt.Errorf("%s account %q is not a platform account name", ch.key, account)
			}
		}
	}
	for _, raw := range c.Custom {
		if err := validateCustomURL(raw); err != nil {
			return err
		}
	}
	if len(c.Message) > maxMessageBytes || strings.ContainsAny(c.Message, "\r\n") {
		return fmt.Errorf("message must be one line of at most %d bytes", maxMessageBytes)
	}
	return nil
}

func validateCustomURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || strings.ContainsAny(raw, "\"\\ \t\n") {
		return fmt.Errorf("custom funding URL %q must be an absolute https URL", raw)
	}
	return nil
}
