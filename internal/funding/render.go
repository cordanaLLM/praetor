package funding

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	fundingFile    = ".github/FUNDING.yml"
	maxSurfaceSize = 1 << 20
	surfacePerm    = 0o644
	badgeURL       = "https://img.shields.io/badge/%s?style=for-the-badge&logo=%s&logoColor=white"
	generatedNote  = "# Rendered by `praetorctl docs funding` from " + ConfigFile + ". Do not edit by hand.\n"
)

// surface is one rendered region. A surface without markers owns its whole file; a marked
// surface owns the lines between its start and end markers, markers included, and is left
// alone in a file that carries no markers.
type surface struct {
	name   string
	path   string
	start  string
	end    string
	indent string
	render func(*Config, string) []string
}

var surfaces = [...]surface{
	{name: "funding file", path: fundingFile, render: renderFundingFile},
	{name: "README badges", path: "README.md", start: "<!-- praetor:funding-badges:start -->", end: "<!-- praetor:funding-badges:end -->", indent: "  ", render: renderBadges},
	{name: "README support section", path: "README.md", start: "<!-- praetor:funding-support:start -->", end: "<!-- praetor:funding-support:end -->", render: renderSupport},
	{name: "MkDocs announcement", path: "mkdocs.yml", start: "# praetor:funding-announcement:start", end: "# praetor:funding-announcement:end", indent: "  ", render: renderAnnouncement},
	{name: "MkDocs social links", path: "mkdocs.yml", start: "# praetor:funding-social:start", end: "# praetor:funding-social:end", indent: "    ", render: renderSocial},
}

// Result reports one Apply run: the surfaces whose content differs from the rendering (and
// were rewritten unless the run only checked), and the surfaces with no file or no markers.
type Result struct {
	Configured bool
	Drifted    []string
	Skipped    []string
}

// Apply renders every funding surface below root from cfg; a nil cfg is "not configured"
// and renders nothing. With write false it only reports drift. Files are written once, after
// every surface rendered, so a failed surface leaves the tree unchanged.
func Apply(ctx context.Context, root string, cfg *Config, write bool) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("funding apply requires a context")
	}
	result := Result{Configured: cfg.Configured()}
	files := map[string]*document{}
	for _, s := range surfaces {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		doc, err := loadDocument(root, s.path, files)
		if err != nil {
			return Result{}, err
		}
		drifted, skipped, err := s.apply(doc, cfg)
		if err != nil {
			return Result{}, fmt.Errorf("%s (%s): %w", s.name, s.path, err)
		}
		result.Drifted = appendIf(result.Drifted, drifted, s.name+" ("+s.path+")")
		result.Skipped = appendIf(result.Skipped, skipped, s.name+" ("+s.path+")")
	}
	if !write {
		return result, nil
	}
	return result, writeDocuments(root, files)
}

// document is one file's working copy across the surfaces that share it.
type document struct {
	content string
	present bool
	dirty   bool
}

func loadDocument(root, rel string, files map[string]*document) (*document, error) {
	if doc, ok := files[rel]; ok {
		return doc, nil
	}
	data, err := util.ReadConfinedLimited(root, rel, maxSurfaceSize)
	doc := &document{content: string(data), present: err == nil}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	files[rel] = doc
	return doc, nil
}

// apply renders s into doc and reports whether the content drifted or the surface was skipped.
func (s surface) apply(doc *document, cfg *Config) (drifted, skipped bool, err error) {
	body := strings.Join(s.render(cfg, s.indent), "\n")
	if s.start == "" {
		// A whole-file surface is created only when there is something to publish.
		if !doc.present && !cfg.Configured() {
			return false, true, nil
		}
		return doc.set(body + "\n"), false, nil
	}
	first, _, err := util.FindMarkedBlock(doc.content, s.start, s.end)
	if err != nil || !doc.present || first < 0 {
		return false, err == nil, err
	}
	block := s.indent + s.start + "\n" + body
	if body != "" {
		block += "\n"
	}
	block += s.indent + s.end
	next, _, err := util.ReplaceMarkedBlock(doc.content, s.start, s.end, block, util.MaxMarkedBlockLines)
	if err != nil {
		return false, false, err
	}
	return doc.set(next), false, nil
}

// set replaces the working copy and reports whether it changed.
func (d *document) set(next string) bool {
	if next == d.content {
		return false
	}
	d.content, d.present, d.dirty = next, true, true
	return true
}

func writeDocuments(root string, files map[string]*document) error {
	for rel, doc := range files {
		if !doc.dirty {
			continue
		}
		path, err := util.ConfinePath(root, rel)
		if err != nil {
			return err
		}
		// A repository directory keeps its mode: 0o755 is a ceiling that never tightens it.
		if err := util.MkdirSecure(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := util.WriteFileSecure(path, []byte(doc.content), surfacePerm); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

func appendIf(list []string, ok bool, item string) []string {
	if ok {
		return append(list, item)
	}
	return list
}

func renderFundingFile(cfg *Config, _ string) []string {
	lines := []string{strings.TrimSuffix(generatedNote, "\n")}
	if !cfg.Configured() {
		keys := make([]string, 0, len(channels)+1)
		for _, ch := range channels {
			keys = append(keys, ch.key)
		}
		return append(lines, "# Not configured: no funding channel is published. Supported keys: "+strings.Join(append(keys, "custom"), ", ")+".")
	}
	if len(cfg.GitHub) > 0 {
		lines = append(lines, "github: ["+strings.Join(cfg.GitHub, ", ")+"]")
	}
	for _, ch := range channels[1:] {
		if accounts := cfg.accounts(ch.key); len(accounts) > 0 {
			lines = append(lines, ch.key+": "+accounts[0])
		}
	}
	if len(cfg.Custom) > 0 {
		quoted := make([]string, 0, len(cfg.Custom))
		for _, raw := range cfg.Custom {
			quoted = append(quoted, fmt.Sprintf("%q", raw))
		}
		lines = append(lines, "custom: ["+strings.Join(quoted, ", ")+"]")
	}
	return lines
}

func badge(ch channel) string { return fmt.Sprintf(badgeURL, ch.badge, ch.logo) }

func renderBadges(cfg *Config, indent string) []string {
	var lines []string
	for _, l := range cfg.links() {
		lines = append(lines, fmt.Sprintf("%s[![%s](%s)](%s)", indent, l.channel.label, badge(l.channel), l.url()))
	}
	return lines
}

func renderSupport(cfg *Config, _ string) []string {
	links := cfg.links()
	if len(links) == 0 {
		return nil
	}
	message := cfg.Message
	if message == "" {
		message = "If this project saves you engineering time, you can support its development through the channels below."
	}
	lines := []string{"## 💖 Support & Sponsorship", "", message, "", `<div align="center">`, ""}
	for _, l := range links {
		lines = append(lines, fmt.Sprintf(`  <a href="%s"><img src="%s" alt="%s on %s" /></a>`, l.url(), badge(l.channel), l.account, l.channel.label))
	}
	return append(lines, "", "</div>", "", "---")
}

func renderAnnouncement(cfg *Config, indent string) []string {
	if cfg.accounts("polar") == nil {
		return nil
	}
	return []string{
		indent + "announcement: >",
		indent + "  🎯 <strong>Polar.sh Feature Bounties:</strong> Earn rewards for solved issues or sponsor upcoming roadmap features!",
		indent + `  <a href="sponsoring/"><strong>Learn more &amp; view bounties &rarr;</strong></a>`,
	}
}

func renderSocial(cfg *Config, indent string) []string {
	var lines []string
	for _, l := range cfg.links() {
		lines = append(lines,
			indent+"- icon: "+l.channel.icon,
			indent+"  link: "+l.url(),
			indent+"  name: Support on "+l.channel.label)
	}
	return lines
}
