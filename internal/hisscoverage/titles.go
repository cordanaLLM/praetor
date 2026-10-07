package hisscoverage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// ErrTitleNotRewritable reports a stale title whose layout a one-line edit cannot rewrite in
// place: a block scalar, a title on a line of its own below its key, an alias or a flow-style
// rule. The file is left as it is, and the error names the catalog title to set by hand.
var ErrTitleNotRewritable = errors.New("hisscoverage: title cannot be rewritten in place")

// catalogFilePerm is the permission ceiling of a rewritten coverage file. A file with fewer
// bits keeps its own (util.WriteFileConfined).
const catalogFilePerm = 0o644

// TitleSync is the outcome of rewriting a coverage file's stale titles.
type TitleSync struct {
	// Stale lists, in declaration order, each title that differed from the HISS catalog's.
	Stale []StaleTitle
	// Content is the file text with every stale title rewritten; the input itself when no
	// title is stale.
	Content []byte
	// Written is true when Content replaced the file on disk (SyncTitlesFile with write).
	Written bool
}

// SyncTitlesFile rewrites the stale titles of the coverage file below rootDir (SyncTitles).
// It is a dry run unless write is set: the result names every title that would change and the
// file is left untouched. With write, a file holding a stale title is replaced and one without
// is not written at all.
func SyncTitlesFile(ctx context.Context, rootDir string, write bool) (*TitleSync, error) {
	data, err := readCatalogFile(ctx, rootDir)
	if err != nil {
		return nil, err
	}
	result, err := SyncTitles(data)
	if err != nil || !write || len(result.Stale) == 0 {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("sync coverage titles: %w", err)
	}
	if err := util.WriteFileConfined(rootDir, filepath.FromSlash(CatalogFile), result.Content, catalogFilePerm); err != nil {
		return nil, fmt.Errorf("write coverage catalog: %w", err)
	}
	result.Written = true
	return result, nil
}

// SyncTitles rewrites, in data (the text of a coverage file), every declared title that
// differs from the HISS catalog to the catalog's title. Only the value on each stale title's
// line changes: comments, key order, indentation, line endings and every other line stay as
// written. The result is decoded again and refused (ErrTitleNotRewritable) unless it holds
// exactly the input's evidence with the catalog's titles, so a layout the line edit does not
// address fails closed instead of producing a different file. A file that does not validate
// is refused as LoadCatalog refuses it, an unregistered rule id included.
func SyncTitles(data []byte) (*TitleSync, error) {
	catalog, err := parseCatalog(data)
	if err != nil {
		return nil, err
	}
	stale := catalog.StaleTitles()
	if len(stale) == 0 {
		return &TitleSync{Content: data}, nil
	}
	entries, err := titleEntries(data)
	if err != nil {
		return nil, err
	}
	lines := strings.SplitAfter(string(data), "\n")
	for i := 0; i < len(stale); i++ {
		if err := rewriteTitleLine(lines, entries[stale[i].ID], stale[i]); err != nil {
			return nil, err
		}
	}
	out := []byte(strings.Join(lines, ""))
	if err := confirmTitleSync(catalog, out); err != nil {
		return nil, err
	}
	return &TitleSync{Stale: stale, Content: out}, nil
}

// titleEntry is the key and value node of one rule's title. flow is true when the rule or the
// rules list is a flow collection, whose line holds more than the title.
type titleEntry struct {
	key, value *yaml.Node
	flow       bool
}

// titleEntries maps each rule id of a coverage file to its title entry. A rule without a title
// is left out.
func titleEntries(data []byte) (map[string]titleEntry, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("decode coverage catalog: %w", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return nil, fmt.Errorf("%w: catalog must contain exactly one document", ErrInvalidCatalog)
	}
	rules := util.YAMLMappingValue(document.Content[0], "rules")
	if rules == nil || rules.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%w: rules is not a list", ErrInvalidCatalog)
	}
	entries := make(map[string]titleEntry, len(rules.Content))
	for i := 0; i < len(rules.Content); i++ {
		id, entry, ok := ruleTitleEntry(rules.Content[i])
		if ok {
			entry.flow = entry.flow || rules.Style&yaml.FlowStyle != 0
			entries[id] = entry
		}
	}
	return entries, nil
}

// ruleTitleEntry returns the id and title entry of one rule mapping; ok is false for a rule
// without either.
func ruleTitleEntry(rule *yaml.Node) (string, titleEntry, bool) {
	id := util.YAMLMappingValue(rule, "id")
	if id == nil {
		return "", titleEntry{}, false
	}
	for i := 0; i+1 < len(rule.Content); i += 2 {
		if rule.Content[i].Value == "title" {
			entry := titleEntry{key: rule.Content[i], value: rule.Content[i+1], flow: rule.Style&yaml.FlowStyle != 0}
			return id.Value, entry, true
		}
	}
	return "", titleEntry{}, false
}

// rewriteTitleLine replaces the value on stale's title line with the catalog's title, keeping
// everything before the value, the line's comment with the white space ahead of it (yamllint
// counts that gap), and the line ending.
func rewriteTitleLine(lines []string, entry titleEntry, stale StaleTitle) error {
	if !entry.rewritable() || entry.value.Line > len(lines) {
		return notRewritable(stale)
	}
	at := entry.value.Line - 1
	body := strings.TrimRight(lines[at], "\r\n")
	runes := []rune(body)
	start := entry.value.Column - 1
	tail, ok := commentTail(body, entry.lineComment())
	if start < 0 || start > len(runes) || !ok {
		return notRewritable(stale)
	}
	lines[at] = string(runes[:start]) + util.YAMLScalar(stale.Catalog) + tail + lines[at][len(body):]
	return nil
}

// commentTail is the end of line that follows a title's value: the comment and the white space
// ahead of it, or nothing for a line without a comment. ok is false when the comment the
// decoder reported does not end the line.
func commentTail(body, comment string) (string, bool) {
	if comment == "" {
		return "", true
	}
	trimmed := strings.TrimRight(body, " \t")
	if !strings.HasSuffix(trimmed, comment) {
		return "", false
	}
	value := strings.TrimRight(trimmed[:len(trimmed)-len(comment)], " \t")
	return trimmed[len(value):], true
}

// rewritable reports whether the title is a one-line scalar on its key's line, in a block
// mapping: the one layout whose value a line edit replaces without touching anything else.
func (e titleEntry) rewritable() bool {
	if e.key == nil || e.value == nil || e.flow || e.value.Kind != yaml.ScalarNode {
		return false
	}
	return e.value.Style&(yaml.LiteralStyle|yaml.FoldedStyle) == 0 && e.value.Line == e.key.Line && e.value.Line > 0
}

// lineComment is the comment that ends the title's line, which the decoder attaches to the key
// or to the value.
func (e titleEntry) lineComment() string {
	if e.key.LineComment != "" {
		return e.key.LineComment
	}
	return e.value.LineComment
}

// notRewritable is ErrTitleNotRewritable for stale, naming the title to set by hand.
func notRewritable(stale StaleTitle) error {
	return fmt.Errorf("%w: rule %s; set its title to %q or drop the title line by hand",
		ErrTitleNotRewritable, stale.ID, stale.Catalog)
}

// confirmTitleSync refuses a rewrite that does not decode to exactly before with the
// catalog's titles: a title whose value continued on the next line, or an edit that reached
// another key, decodes to something else.
func confirmTitleSync(before *Catalog, out []byte) error {
	after, err := parseCatalog(out)
	if err != nil {
		return fmt.Errorf("%w: the rewritten file does not load (%w); edit the titles by hand", ErrTitleNotRewritable, err)
	}
	want := Catalog{Version: before.Version, Rules: make([]Rule, len(before.Rules))}
	copy(want.Rules, before.Rules)
	for i := 0; i < len(want.Rules); i++ {
		if want.Rules[i].Title != "" {
			want.Rules[i].Title = want.Rules[i].CatalogTitle()
		}
	}
	if !reflect.DeepEqual(&want, after) {
		return fmt.Errorf("%w: the rewrite would change more than the titles; edit them by hand", ErrTitleNotRewritable)
	}
	return nil
}
