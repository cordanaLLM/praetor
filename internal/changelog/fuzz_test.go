package changelog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func FuzzChangelogRender(f *testing.F) {
	f.Add("Feature title", "added", "2026-09-11", "v1.0.0")
	f.Add("Security fix", "security", "2026-01-01", "v2.0.0-rc.1")

	f.Fuzz(func(t *testing.T, title, typ, date, version string) {
		if len(title) > 1000 || len(title) == 0 {
			title = "Default Title"
		}
		if len(version) > 64 || len(version) == 0 {
			version = "v1.0.0"
		}
		if len(date) > 32 || len(date) == 0 {
			date = "2026-09-11"
		}

		tmpDir := t.TempDir()
		initChangelog := "# Changelog\n\n## [Unreleased]\n\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "CHANGELOG.md"), []byte(initChangelog), 0644); err != nil {
			t.Fatal(err)
		}

		frag := Fragment{
			Type:  FragmentType(typ),
			Title: title,
		}
		if _, err := CreateFragment(tmpDir, frag); err != nil {
			return
		}

		// The contract is not "never errors": the version and date are recorded verbatim in
		// a JSON recovery journal, so input it cannot represent must be refused. What must
		// hold is that the refusal is a named validation error rather than a leaked
		// serialization failure, and that anything representable renders.
		err := RenderRelease(tmpDir, version, date)
		validText := RenderTextRepresentable(version) && RenderTextRepresentable(date)
		switch {
		case err == nil && !validText:
			t.Fatalf("invalid UTF-8 was accepted: version=%q date=%q", version, date)
		case err != nil && validText:
			t.Fatalf("representable input was refused: version=%q date=%q: %v", version, date, err)
		case err != nil && !errors.Is(err, ErrRenderTextUnrepresentable):
			t.Fatalf("refusal must name the offending argument, got a leaked error: %v", err)
		}
	})
}
