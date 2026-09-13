package adopt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerificationMetadataBounds(t *testing.T) {
	for _, tc := range []struct {
		name         string
		count, bytes int
		wantError    bool
	}{
		{"file-exact", 1, maxVerificationInputBytes, false}, {"file-over", 1, maxVerificationInputBytes + 1, true},
		{"files-exact", maxVerificationInputs, 1, false}, {"files-over", maxVerificationInputs + 1, 1, true},
		{"total-exact", 32, maxVerificationInputBytes, false}, {"total-over", 33, maxVerificationInputBytes, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for i := 0; i < tc.count; i++ {
				mustWrite(t, filepath.Join(root, fmt.Sprintf("Project%d.csproj", i)), strings.Repeat(" ", tc.bytes))
			}
			inputs, err := loadVerificationInputs(t.Context(), root)
			if (err != nil) != tc.wantError {
				t.Fatalf("bound %s: %v", tc.name, err)
			}
			if err == nil && len(inputs.files) != tc.count {
				t.Fatal("successful bounded discovery truncated inputs")
			}
		})
	}
}

func TestVerificationDirectoryBounds(t *testing.T) {
	for _, count := range []int{maxVerificationEntries, maxVerificationEntries + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			root := t.TempDir()
			for i := 0; i < count; i++ {
				mustWrite(t, filepath.Join(root, fmt.Sprintf("unrelated-%d", i)), "")
			}
			_, err := loadVerificationInputs(t.Context(), root)
			if (err != nil) != (count > maxVerificationEntries) {
				t.Fatalf("raw entries not bounded before interpretation: %v", err)
			}
		})
	}
	for _, depth := range []int{maxVerificationDepth, maxVerificationDepth + 1} {
		t.Run(fmt.Sprintf("depth-%d", depth), func(t *testing.T) {
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, strings.Repeat("d/", depth), "Test.csproj"), "<Project/>")
			_, err := loadVerificationInputs(t.Context(), root)
			if (err != nil) != (depth > maxVerificationDepth) {
				t.Fatalf("meaningful marker depth bound: %v", err)
			}
		})
	}
	t.Run("deep-recognized-manifest-captured", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, strings.Repeat("d/", maxVerificationDepth), "Test.csproj")
		mustWrite(t, path, "<Project/>")
		rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		inputs, err := loadVerificationInputs(t.Context(), root)
		if err != nil || !inputs.has(rel) {
			t.Fatalf("deep manifest was not captured: %v", err)
		}
	})
}

func TestVerificationInputsRejectLinksNewlinesAndCancellation(t *testing.T) {
	for _, rel := range []string{"package.json", "Project.csproj", "tests/test_external.py"} {
		t.Run(rel, func(t *testing.T) {
			root := t.TempDir()
			outside := filepath.Join(t.TempDir(), "secret")
			mustWrite(t, outside, "{}")
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, rel)); err != nil {
				t.Fatal(err)
			}
			if _, err := loadVerificationInputs(t.Context(), root); err == nil {
				t.Fatal("symlinked command metadata accepted")
			}
		})
	}
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "injected\nrecipe.csproj"), "<Project/>")
	if _, err := loadVerificationInputs(t.Context(), root); err == nil {
		t.Fatal("line break in command path accepted")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := loadVerificationInputs(cancelled, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation cause: %v", err)
	}
	var absent context.Context
	if _, err := loadVerificationInputs(absent, t.TempDir()); err == nil {
		t.Fatal("nil context accepted")
	}
}

func TestVerificationIgnoredBuildTreesDoNotContributeMarkers(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "go.mod"), "module fixture\n")
	for _, dir := range []string{"node_modules", ".git", "obj", "bin", ".workingdir", ".workingdir2", "target"} {
		mustWrite(t, filepath.Join(root, dir, "Generated.Tests.csproj"), "invalid generated metadata")
	}
	plan, err := resolveVerificationPlan(t.Context(), root)
	if err != nil || plan.Status != verificationDeclared || strings.Join(plan.Runtimes, ",") != "go" {
		t.Fatalf("generated content affected plan: %+v %v", plan, err)
	}
}

func TestVerificationDotnetMetadataTestIdentity(t *testing.T) {
	for name, data := range map[string]string{
		"property": `<Project><PropertyGroup><IsTestProject>true</IsTestProject></PropertyGroup></Project>`,
		"sdk":      `<Project><ItemGroup><PackageReference Include="Microsoft.NET.Test.Sdk"/></ItemGroup></Project>`,
	} {
		test, err := dotnetTestProject([]byte(data))
		if err != nil || !test {
			t.Fatalf("%s valid marker rejected: %v", name, err)
		}
	}
	if test, err := dotnetTestProject([]byte("\xef\xbb\xbf<Project><PropertyGroup><IsTestProject>true</IsTestProject></PropertyGroup></Project>")); err != nil || !test {
		t.Fatalf("UTF-8 BOM should be accepted: test=%v err=%v", test, err)
	}
	for name, data := range map[string]string{
		"production":           `<Project/>`,
		"explicit-false":       `<Project><PropertyGroup><IsTestProject>false</IsTestProject></PropertyGroup><ItemGroup><PackageReference Include="Microsoft.NET.Test.Sdk"/></ItemGroup></Project>`,
		"conditional-property": `<Project><PropertyGroup Condition="'$(Test)' == 'true'"><IsTestProject>true</IsTestProject></PropertyGroup></Project>`,
		"conditional-sdk":      `<Project><ItemGroup><PackageReference Include="Microsoft.NET.Test.Sdk" Condition="false"/></ItemGroup></Project>`,
		"expression":           `<Project><PropertyGroup><IsTestProject>$(TestEnabled)</IsTestProject></PropertyGroup></Project>`,
		"conflicting":          `<Project><PropertyGroup><IsTestProject>true</IsTestProject><IsTestProject>false</IsTestProject></PropertyGroup></Project>`,
	} {
		test, err := dotnetTestProject([]byte(data))
		if err != nil || test {
			t.Fatalf("%s ambiguous/disabled project claimed test: %v", name, err)
		}
	}
	for _, data := range []string{"", "<wrong/>", "<Project/><Project/>", "<Project>", "text<Project/>", "\xef\xbb\xbf\xef\xbb\xbf<Project/>", "<Project>" + strings.Repeat("<X/>", 8192) + "</Project>"} {
		if _, err := dotnetTestProject([]byte(data)); err == nil {
			t.Fatalf("invalid/beyond-bound XML accepted: %.50s", data)
		}
	}
}
