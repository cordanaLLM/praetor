package devcontainer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func preparedBootstrap(t *testing.T) *Bundle {
	t.Helper()
	bundle, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestBootstrapIdentityIsStableAndIncludesTests(t *testing.T) {
	root := bootstrapSourceFixture(t)
	options := BootstrapOptions{SourceRoot: root}
	first, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("unchanged capture changed bootstrap identity")
	}
	writeBootstrapFile(t, root, "cmd/standardsctl/main_test.go", "package main\nimport \"testing\"\nfunc TestSource(t *testing.T) {}\n")
	third, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	if third.Spec().SourceSHA256 == first.Spec().SourceSHA256 || third.Spec().ArchiveSHA256 == first.Spec().ArchiveSHA256 {
		t.Fatal("test edit omitted from snapshot identity")
	}
}

func TestBootstrapSourceChangeDuringCaptureRejected(t *testing.T) {
	root := bootstrapSourceFixture(t)
	files, err := captureBootstrapSource(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	writeBootstrapFile(t, root, "cmd/standardsctl/main_test.go", "package main\n")
	bundle := &Bundle{}
	err = bundle.prepareSource(t.Context(), files, BootstrapOptions{SourceRoot: root}, &BootstrapSpec{})
	if err == nil || !strings.Contains(err.Error(), "changed during") {
		t.Fatalf("unstable source was archived: %v", err)
	}
	if len(bundle.Artifacts) != 0 {
		t.Fatal("unstable source published artifacts")
	}
}

func TestBootstrapRejectsUnsupportedSource(t *testing.T) {
	for name, change := range map[string]func(*testing.T, string){
		"embedded-assets": func(t *testing.T, root string) {
			writeBootstrapFile(t, root, "cmd/standardsctl/assets.go", "package main\nimport _ \"embed\"\n//go:embed secret.txt\nvar asset string\n")
		},
		"wrong-module": func(t *testing.T, root string) {
			writeBootstrapFile(t, root, "go.mod", "module attacker.invalid/tool\n// module github.com/cordanaLLM/praetor\n")
		},
		"duplicate-module": func(t *testing.T, root string) {
			writeBootstrapFile(t, root, "go.mod", "module github.com/cordanaLLM/praetor\nmodule other\n")
		},
		"missing-cli": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "cmd/standardsctl/main.go")); err != nil {
				t.Fatal(err)
			}
		},
		"invalid-go": func(t *testing.T, root string) {
			writeBootstrapFile(t, root, "cmd/standardsctl/main.go", "not go source")
		},
		"symlink-go": func(t *testing.T, root string) {
			target := filepath.Join(t.TempDir(), "outside.go")
			writeBootstrapFile(t, filepath.Dir(target), filepath.Base(target), "package main\n")
			if err := os.Symlink(target, filepath.Join(root, "outside.go")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := bootstrapSourceFixture(t)
			change(t, root)
			if _, err := PrepareBundle(t.Context(), "app", nil, nil, BootstrapOptions{SourceRoot: root}); err == nil {
				t.Fatal("unsupported source accepted")
			}
		})
	}
}

func TestBootstrapRejectsMutableAndInjectedImages(t *testing.T) {
	for _, image := range []string{"golang:latest", "golang:1.27", "golang@sha256:" + strings.Repeat("A", 64), "golang\nRUN touch injected@sha256:" + strings.Repeat("a", 64), "$(touch injected)@sha256:" + strings.Repeat("a", 64)} {
		if _, err := PrepareBundle(t.Context(), "app", nil, nil, BootstrapOptions{BuilderImage: image}); err == nil {
			t.Fatalf("unsafe image accepted: %q", image)
		}
	}
	for _, options := range []BootstrapOptions{{BaseImage: "unverified:tag"}, {SourceRoot: filepath.Join(t.TempDir(), "missing")}} {
		if _, err := PrepareBundle(t.Context(), "app", nil, nil, options); err == nil {
			t.Fatal("invalid explicit selection silently fell back")
		}
	}
}

func TestBootstrapUnavailableIsNotVerified(t *testing.T) {
	bundle, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, BootstrapOptions{SourceRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), ".devcontainer", "devcontainer.json")
	if err := WriteBundle(t.Context(), path, bundle, false); err != nil {
		t.Fatal(err)
	}
	if err := Verify(t.Context(), path, mustBaseContainer(t)); !errors.Is(err, ErrBootstrapUnavailable) {
		t.Fatalf("unavailable config was certified: %v", err)
	}
}

func TestBootstrapMutationsNeverWritePartialCompanions(t *testing.T) {
	for name, change := range map[string]func(*Bundle){
		"source-digest":       func(b *Bundle) { b.Spec().SourceSHA256 = "sha256:" + strings.Repeat("1", 64) },
		"archive-digest":      func(b *Bundle) { b.Spec().ArchiveSHA256 = "sha256:" + strings.Repeat("2", 64) },
		"dockerfile-digest":   func(b *Bundle) { b.Spec().DockerfileSHA256 = "sha256:" + strings.Repeat("3", 64) },
		"startup":             func(b *Bundle) { b.Config.PostCreateCommand = "touch unexpected" },
		"frame-content":       func(b *Bundle) { b.Artifacts[0].Content[0] = '!' },
		"dockerfile-content":  func(b *Bundle) { b.Artifacts[len(b.Artifacts)-1].Content = []byte("FROM untrusted:latest\n") },
		"duplicate-companion": func(b *Bundle) { b.Artifacts = append(b.Artifacts, b.Artifacts[0]) },
		"missing-companion":   func(b *Bundle) { b.Artifacts = b.Artifacts[1:] },
		"escaped-companion":   func(b *Bundle) { b.Artifacts[0].Name = "../outside" },
	} {
		t.Run(name, func(t *testing.T) {
			bundle := preparedBootstrap(t)
			change(bundle)
			root := t.TempDir()
			path := filepath.Join(root, ".devcontainer", "devcontainer.json")
			if err := WriteBundle(t.Context(), path, bundle, false); err == nil {
				t.Fatal("mutated bundle written")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("preflight failure wrote entries: %v %v", entries, err)
			}
		})
	}
}

func TestRecordedBootstrapUsesSelectedIdentityAndDetectsEdits(t *testing.T) {
	bundle := preparedBootstrap(t)
	path := filepath.Join(t.TempDir(), ".devcontainer", "devcontainer.json")
	if err := WriteBundle(t.Context(), path, bundle, false); err != nil {
		t.Fatal(err)
	}
	expected := mustBaseContainer(t)
	if err := Verify(t.Context(), path, expected); err != nil {
		t.Fatal(err)
	}
	if (&Bundle{Config: expected}).Spec() != nil {
		t.Fatal("verification mutated caller configuration")
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(original, []byte("\"name\":"), []byte("\"initializeCommand\":\"touch attacker\",\"name\":"), 1)
	if err := os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(t.Context(), path, expected); err == nil {
		t.Fatal("unknown executable edit was ignored")
	}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	companion := filepath.Join(filepath.Dir(path), bootstrapPartName(0))
	if err := os.Remove(companion); err != nil {
		t.Fatal(err)
	}
	if err := Verify(t.Context(), path, expected); err == nil {
		t.Fatal("missing recorded companion was verified")
	}
}

func TestLegacyAdoptedPathsFailAndSelfHostedInputsPass(t *testing.T) {
	dc := mustBaseContainer(t)
	root := t.TempDir()
	path := filepath.Join(root, ".devcontainer", "devcontainer.json")
	if err := WriteDevContainer(t.Context(), path, dc); err != nil {
		t.Fatal(err)
	}
	if err := Verify(t.Context(), path, dc); err == nil {
		t.Fatal("legacy matching strings hid absent Dockerfile")
	}
	writeBootstrapFile(t, root, "docker/dev/Dockerfile", "FROM fixture\n")
	if err := Verify(t.Context(), path, dc); err == nil {
		t.Fatal("legacy matching strings hid absent Praetor source")
	}
	writeBootstrapFile(t, root, "go.mod", "module github.com/cordanaLLM/praetor\n")
	writeBootstrapFile(t, root, "cmd/standardsctl/main.go", "package main\n")
	if err := Verify(t.Context(), path, dc); err != nil {
		t.Fatalf("actual legacy self-host inputs rejected: %v", err)
	}
}

func TestBootstrapBoundariesAndCancellation(t *testing.T) {
	for _, size := range []int{bootstrapPartBytes * maxBootstrapParts / 4 * 3, bootstrapPartBytes*maxBootstrapParts/4*3 + 1} {
		parts, err := frameBootstrapArchive(make([]byte, size))
		if (err != nil) != (size > bootstrapPartBytes*maxBootstrapParts/4*3) {
			t.Fatalf("archive framing bound: %v", err)
		}
		if err == nil && len(parts) != maxBootstrapParts {
			t.Fatal("exact boundary truncated frames")
		}
	}
	if _, err := PrepareBundle(t.Context(), strings.Repeat("x", 513), nil, nil, BootstrapOptions{}); err == nil {
		t.Fatal("oversized name accepted")
	}
	if _, err := PrepareBundle(t.Context(), "x", make([]string, MaxLoopLimit+1), nil, BootstrapOptions{}); err == nil {
		t.Fatal("oversized profiles truncated")
	}
	bundle := preparedBootstrap(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := WriteBundle(ctx, filepath.Join(t.TempDir(), "devcontainer.json"), bundle, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel lost: %v", err)
	}
}
