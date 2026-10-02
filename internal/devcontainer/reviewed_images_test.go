// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package devcontainer

import (
	"bytes"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// fixtureDigest returns a distinct lowercase sha256 digest per index.
func fixtureDigest(index int) string { return fmt.Sprintf("sha256:%064x", index) }

// Positive: the committed prior list parses, names an earlier default of each role, equals the
// copy compiled into the binary, and renders back to its exact bytes, so a bump that retires
// nothing leaves the file untouched.
func TestPriorImagesFileRoundTrips(t *testing.T) {
	data, err := os.ReadFile(priorImagesName)
	if err != nil {
		t.Fatal(err)
	}
	priors, err := ParsePriorImages(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(priors.Base) == 0 || len(priors.Builder) == 0 {
		t.Fatalf("prior list lost a role: %+v", priors)
	}
	embedded := shippedPriors(t)
	if !slices.Equal(embedded.Base, priors.Base) || !slices.Equal(embedded.Builder, priors.Builder) {
		t.Fatalf("compiled-in list %+v differs from %s %+v", embedded, priorImagesName, priors)
	}
	rendered, err := RenderPriorImages(priors)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rendered, data) {
		t.Fatalf("rendering does not reproduce %s:\n%s", priorImagesName, rendered)
	}
}

// Negative and boundary: a list a bump could not append to safely is refused; empty lists and
// a list exactly at the bound parse.
func TestParsePriorImagesRefusesMalformedLists(t *testing.T) {
	image := "docker.io/library/golang@" + fixtureDigest(1)
	full := make([]string, 0, maxPriorImages+1)
	for index := 0; index <= maxPriorImages; index++ {
		full = append(full, `"docker.io/library/golang@`+fixtureDigest(index)+`"`)
	}
	list := func(entries []string) string { return `{"base":[],"builder":[` + strings.Join(entries, ",") + `]}` }
	for name, data := range map[string]string{
		"not JSON":         "{",
		"unknown member":   `{"base":[],"builder":[],"runtime":[]}`,
		"duplicate member": `{"base":[],"base":[],"builder":[]}`,
		"not a list":       `{"base":"` + image + `","builder":[]}`,
		"tagged entry":     list([]string{`"docker.io/library/golang:1.27-alpine@` + fixtureDigest(1) + `"`}),
		"unpinned entry":   list([]string{`"docker.io/library/golang"`}),
		"uppercase digest": list([]string{`"docker.io/library/golang@sha256:` + strings.Repeat("A", 64) + `"`}),
		"repeated entry":   list([]string{`"` + image + `"`, `"` + image + `"`}),
		"past the bound":   list(full),
	} {
		if _, err := ParsePriorImages([]byte(data)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for name, data := range map[string]string{"empty lists": `{"base":[],"builder":[]}`, "at the bound": list(full[:maxPriorImages])} {
		if _, err := ParsePriorImages([]byte(data)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// pinSource renders a bootstrap.go fragment pinning both reviewed defaults.
func pinSource(builderTag, builderDigest, constantDigest string) string {
	return "const (\n\t// Reviewed at docker.io/library/golang:" + builderTag + "@" + builderDigest + "\n" +
		"\tDefaultBuilderImage = \"docker.io/library/golang@" + constantDigest + "\"\n" +
		"\t// Reviewed at mcr.microsoft.com/devcontainers/base:ubuntu26.04@" + fixtureDigest(9) + "\n" +
		"\tDefaultBaseImage = \"mcr.microsoft.com/devcontainers/base@" + fixtureDigest(9) + "\"\n)\n"
}

// Positive: the committed bootstrap.go yields one tagged pin per role whose digest-only form
// is the constant, so the reference Renovate reads is the one the bundle pulls.
func TestParseReviewedPinsReadsBootstrapSource(t *testing.T) {
	source, err := os.ReadFile("bootstrap.go")
	if err != nil {
		t.Fatal(err)
	}
	text, _, err := util.NormalizeLineEndingsStrict(string(source))
	if err != nil {
		t.Fatal(err)
	}
	pins, err := parseReviewedPins(text)
	if err != nil {
		t.Fatal(err)
	}
	for role, constant := range map[string]string{"base": DefaultBaseImage, "builder": DefaultBuilderImage} {
		if pin := pins[role]; pin.image() != constant || pin.tag == "" {
			t.Fatalf("%s pin %s does not carry a tag over %s", role, pin.reference(), constant)
		}
	}
}

// Negative and boundary: a pin whose comment and constant disagree, a missing, repeated or
// tagless pin, and a comment separated from its constant are refused, each naming its reason.
func TestParseReviewedPinsRefusesDriftedPins(t *testing.T) {
	builder := fixtureDigest(1)
	if _, err := parseReviewedPins(pinSource("1.27-alpine", builder, builder)); err != nil {
		t.Fatalf("agreeing pins refused: %v", err)
	}
	for name, tc := range map[string]struct{ source, reason string }{
		"constant digest differs": {pinSource("1.27-alpine", builder, fixtureDigest(2)), "but its comment was reviewed at"},
		"base missing":            {strings.SplitAfter(pinSource("1.27-alpine", builder, builder), "\"\n")[0], "directly above DefaultBaseImage"},
		"pinned twice":            {pinSource("1.27-alpine", builder, builder) + pinSource("1.27-alpine", builder, builder), "twice"},
		"tagless comment":         {strings.Replace(pinSource("1.27-alpine", builder, builder), "golang:1.27-alpine@", "golang@", 1), "directly above DefaultBuilderImage"},
		"line in between":         {strings.Replace(pinSource("1.27-alpine", builder, builder), "\n\tDefaultBuilderImage", "\n\t// note\n\tDefaultBuilderImage", 1), "directly above DefaultBuilderImage"},
	} {
		_, err := parseReviewedPins(tc.source)
		if err == nil || !strings.Contains(err.Error(), tc.reason) {
			t.Errorf("%s: error %v does not name %q", name, err, tc.reason)
		}
	}
}

// A bump moves a pin only to a tagged, digest-pinned, lowercase reference without a registry
// port: a tagless one is what made Renovate propose latest (#700, #703).
func TestParseTaggedPinRequiresTagAndDigest(t *testing.T) {
	digest := fixtureDigest(3)
	repository, tag, got, err := parseTaggedPin("docker.io/library/golang:1.28-alpine@" + digest)
	if err != nil || repository != "docker.io/library/golang" || tag != "1.28-alpine" || got != digest {
		t.Fatalf("tagged pin parsed as %q %q %q %v", repository, tag, got, err)
	}
	for _, reference := range []string{
		"docker.io/library/golang@" + digest,
		"docker.io/library/golang:1.28-alpine",
		"docker.io/library/golang:1.28-Alpine@" + digest,
		"registry.test:5000/golang:1.28@" + digest,
		"docker.io/library/golang:1.28@" + digest[:len(digest)-1],
		"",
	} {
		if _, _, _, err := parseTaggedPin(reference); err == nil {
			t.Errorf("%q accepted as a reviewed pin", reference)
		}
	}
}

// UpdateBotBundleFiles names the config and the Dockerfile beside it, in the config's own
// directory, with slashes on every platform; the source parts are never listed.
func TestUpdateBotBundleFilesNamesConfigAndDockerfile(t *testing.T) {
	for config, want := range map[string][]string{
		".devcontainer/devcontainer.json":     {".devcontainer/devcontainer.json", ".devcontainer/Dockerfile.praetor"},
		".devcontainer/api/devcontainer.json": {".devcontainer/api/devcontainer.json", ".devcontainer/api/Dockerfile.praetor"},
		"devcontainer.json":                   {"devcontainer.json", "Dockerfile.praetor"},
	} {
		if got := UpdateBotBundleFiles(config); !slices.Equal(got, want) {
			t.Errorf("UpdateBotBundleFiles(%q) = %v, want %v", config, got, want)
		}
	}
}

// RecordsBootstrap tells a config Praetor generated from any other. Positive: a ready bundle
// and an unavailable placeholder. Negative: an operator's own config, a managed-schema config
// without a specification, and a bootstrap member that is no object. Boundary: no bytes, and
// a generated config edited since, by a bot moving an image or a person adding a key or
// breaking the specification, which is still a generated config.
func TestRecordsBootstrapRecognisesGeneratedConfigs(t *testing.T) {
	read := func(path string) []byte {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	ready := read(writeRecordedBundle(t, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t)}))
	placeholder := read(writeRecordedBundle(t, BootstrapOptions{}))
	edited := bytes.ReplaceAll(ready, []byte(DefaultBaseImage), []byte("mcr.microsoft.com/devcontainers/base@"+fixtureDigest(2)))
	extended := bytes.Replace(ready, []byte("{"), []byte(`{"runArgs": ["--init"],`), 1)
	if bytes.Equal(edited, ready) || bytes.Equal(extended, ready) || decodeRecordedBootstrap(edited, "devcontainer.json") != nil {
		t.Fatal("the edited fixtures do not differ from the ready bundle in what verification reads")
	}
	for name, tc := range map[string]struct {
		data []byte
		want bool
	}{
		"ready bundle":            {ready, true},
		"unavailable placeholder": {placeholder, true},
		"image edited by a bot":   {edited, true},
		"key added by hand":       {extended, true},
		"broken specification":    {[]byte(`{"customizations": {"praetor": {"bootstrap": {"version": "one"}}}}`), true},
		"operator's own config":   {[]byte(`{"image": "ghcr.io/acme/dev:1", "runArgs": ["--init"]}`), false},
		"no specification":        {[]byte(`{"name": "app", "customizations": {"vscode": {"extensions": []}}}`), false},
		"bootstrap is no object":  {[]byte(`{"customizations": {"praetor": {"bootstrap": "source-bundle"}}}`), false},
		"null bootstrap":          {[]byte(`{"customizations": {"praetor": {"bootstrap": null}}}`), false},
		"not JSON":                {[]byte("{"), false},
		"no bytes":                {nil, false},
	} {
		if got := RecordsBootstrap(tc.data); got != tc.want {
			t.Errorf("%s: RecordsBootstrap = %v, want %v", name, got, tc.want)
		}
	}
}
