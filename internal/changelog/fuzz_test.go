package changelog

import (
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
			return
		}

		frag := Fragment{
			Type:  FragmentType(typ),
			Title: title,
		}
		_, _ = CreateFragment(tmpDir, frag)
		_ = RenderRelease(tmpDir, version, date)
	})
}
