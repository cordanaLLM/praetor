// Package deploychart holds the render tests for the Helm chart under
// deploy/helm/praetor. The chart is a shipped artefact with no Go code, so the
// package carries tests only; they drive the real helm binary because a
// hand-rolled template renderer would be a second implementation of it.
package deploychart

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// renderTimeout bounds the helm invocation (HISS-02).
	renderTimeout = 60 * time.Second
	// maxDocuments bounds the decode loop over a rendered manifest stream.
	maxDocuments = 64
	// maxRootWalk bounds the walk from this package to the repository root.
	maxRootWalk = 8
)

// document is one decoded manifest. yaml.v3 carries the named map type into
// nested mappings, so every nested object is a document too.
type document map[string]any

// helmChart returns the chart directory, skipping the test where the helm
// binary is absent. Windows, macOS and Linux all run this test when helm is on
// PATH; the skip reason names the missing binary rather than the platform.
func helmChart(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("helm binary not on PATH: chart rendering cannot be verified here")
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for i := 0; i < maxRootWalk; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "deploy", "helm", "praetor")
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("no go.mod within %d parents of the test directory", maxRootWalk)
	return ""
}

// render templates the chart for one release and decodes the manifest stream.
func render(t *testing.T, release string, args ...string) []document {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), renderTimeout)
	defer cancel()
	argv := append([]string{"template", release, helmChart(t)}, args...)
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "helm", argv...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm %v failed: %v: %s", argv, err, stderr.String())
	}
	return decode(t, out)
}

func decode(t *testing.T, manifest []byte) []document {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(manifest))
	documents := make([]document, 0, maxDocuments)
	for i := 0; i < maxDocuments; i++ {
		var doc document
		err := decoder.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return documents
		}
		if err != nil {
			t.Fatalf("decode rendered manifest: %v", err)
		}
		if len(doc) > 0 {
			documents = append(documents, doc)
		}
	}
	t.Fatalf("rendered manifest holds more than %d documents", maxDocuments)
	return nil
}

func ofKind(docs []document, kind string) []document {
	matched := make([]document, 0, len(docs))
	for i := 0; i < len(docs); i++ {
		if docs[i]["kind"] == kind {
			matched = append(matched, docs[i])
		}
	}
	return matched
}

// name reads metadata.name, reporting the empty string when it is absent.
func name(doc document) string {
	metadata, ok := doc["metadata"].(document)
	if !ok {
		return ""
	}
	value, _ := metadata["name"].(string)
	return value
}

// podSpec reads spec.template.spec of a Deployment.
func podSpec(t *testing.T, docs []document) document {
	t.Helper()
	deployments := ofKind(docs, "Deployment")
	if len(deployments) != 1 {
		t.Fatalf("expected exactly one Deployment, got %d", len(deployments))
	}
	spec, ok := deployments[0]["spec"].(document)
	if !ok {
		t.Fatal("Deployment has no spec mapping")
	}
	template, ok := spec["template"].(document)
	if !ok {
		t.Fatal("Deployment has no spec.template mapping")
	}
	pod, ok := template["spec"].(document)
	if !ok {
		t.Fatal("Deployment has no spec.template.spec mapping")
	}
	return pod
}

func names(docs []document) map[string]bool {
	seen := make(map[string]bool, len(docs))
	for i := 0; i < len(docs); i++ {
		seen[name(docs[i])] = true
	}
	return seen
}

// Positive: the default values create one release-scoped ServiceAccount, the
// Deployment runs as it, and the pod mounts no token it cannot use.
func TestDefaultValuesRenderServiceAccountTheDeploymentUses(t *testing.T) {
	docs := render(t, "alpha")
	accounts := ofKind(docs, "ServiceAccount")
	if len(accounts) != 1 {
		t.Fatalf("expected one ServiceAccount, got %d", len(accounts))
	}
	if got := name(accounts[0]); got != "alpha-praetor" {
		t.Fatalf("ServiceAccount name = %q, want alpha-praetor", got)
	}
	pod := podSpec(t, docs)
	if got := pod["serviceAccountName"]; got != "alpha-praetor" {
		t.Fatalf("serviceAccountName = %v, want alpha-praetor", got)
	}
	if got := pod["automountServiceAccountToken"]; got != false {
		t.Fatalf("automountServiceAccountToken = %v, want false", got)
	}
}

// Negative: creating no account must not leave the pod naming one that does not
// exist; it falls back to the namespace default.
func TestServiceAccountCreateFalseFallsBackToDefault(t *testing.T) {
	docs := render(t, "alpha", "--set", "serviceAccount.create=false")
	if accounts := ofKind(docs, "ServiceAccount"); len(accounts) != 0 {
		t.Fatalf("expected no ServiceAccount, got %d", len(accounts))
	}
	if got := podSpec(t, docs)["serviceAccountName"]; got != "default" {
		t.Fatalf("serviceAccountName = %v, want default", got)
	}
}

// Negative boundary: an explicit name is honoured even when the chart creates
// no account, because the operator created it out of band.
func TestExplicitServiceAccountNameWinsWithoutCreation(t *testing.T) {
	docs := render(t, "alpha", "--set", "serviceAccount.create=false", "--set", "serviceAccount.name=external-sa")
	if accounts := ofKind(docs, "ServiceAccount"); len(accounts) != 0 {
		t.Fatalf("expected no ServiceAccount, got %d", len(accounts))
	}
	if got := podSpec(t, docs)["serviceAccountName"]; got != "external-sa" {
		t.Fatalf("serviceAccountName = %v, want external-sa", got)
	}
}

// Boundary: two releases share a namespace, so no rendered name may repeat.
func TestTwoReleasesShareNoResourceName(t *testing.T) {
	first := names(render(t, "alpha"))
	second := names(render(t, "beta"))
	if len(first) == 0 || len(second) == 0 {
		t.Fatal("a release rendered no named object")
	}
	for objectName := range first {
		if second[objectName] {
			t.Fatalf("both releases render %q, so the second install collides", objectName)
		}
	}
}

// Boundary: fullnameOverride beats both the release name and nameOverride.
func TestFullnameOverrideWinsOverNameOverride(t *testing.T) {
	docs := render(t, "alpha", "--set", "nameOverride=alt", "--set", "fullnameOverride=custom")
	for _, kind := range []string{"ServiceAccount", "Service", "Deployment"} {
		objects := ofKind(docs, kind)
		if len(objects) != 1 {
			t.Fatalf("expected one %s, got %d", kind, len(objects))
		}
		if got := name(objects[0]); got != "custom" {
			t.Fatalf("%s name = %q, want custom", kind, got)
		}
	}
}

// Boundary: nameOverride alone renames within the release scope.
func TestNameOverrideKeepsReleaseScope(t *testing.T) {
	docs := render(t, "alpha", "--set", "nameOverride=alt")
	accounts := ofKind(docs, "ServiceAccount")
	if len(accounts) != 1 {
		t.Fatalf("expected one ServiceAccount, got %d", len(accounts))
	}
	if got := name(accounts[0]); got != "alpha-alt" {
		t.Fatalf("ServiceAccount name = %q, want alpha-alt", got)
	}
}

// Positive: imagePullSecrets reach the pod spec; empty by default it renders no
// key at all.
func TestImagePullSecretsReachThePodSpec(t *testing.T) {
	if _, present := podSpec(t, render(t, "alpha"))["imagePullSecrets"]; present {
		t.Fatal("empty imagePullSecrets rendered a key")
	}
	pod := podSpec(t, render(t, "alpha", "--set", "imagePullSecrets[0].name=ghcr-credentials"))
	secrets, ok := pod["imagePullSecrets"].([]any)
	if !ok || len(secrets) != 1 {
		t.Fatalf("imagePullSecrets = %v, want one entry", pod["imagePullSecrets"])
	}
	entry, ok := secrets[0].(document)
	if !ok || entry["name"] != "ghcr-credentials" {
		t.Fatalf("imagePullSecrets[0] = %v, want name ghcr-credentials", secrets[0])
	}
}
