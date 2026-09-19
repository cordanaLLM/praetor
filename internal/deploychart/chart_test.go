// Package deploychart holds the render tests for the Helm chart under
// deploy/helm/praetor. The chart is a shipped artefact with no Go code, so the
// package carries tests only; they drive the real helm binary because a
// hand-rolled template renderer would be a second implementation of it.
package deploychart

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// decodeDocuments decodes a manifest stream under a scalar bound (HISS-02). The
// loop runs one iteration past the bound so that a stream of exactly
// maxDocuments documents reaches io.EOF and is accepted; stopping at the bound
// itself would report an overflow for a stream that is within it.
func decodeDocuments(manifest []byte) ([]document, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(manifest))
	documents := make([]document, 0, maxDocuments)
	for i := 0; i < maxDocuments+1; i++ {
		var doc document
		err := decoder.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return documents, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode rendered manifest: %w", err)
		}
		if len(doc) > 0 {
			documents = append(documents, doc)
		}
	}
	return nil, fmt.Errorf("rendered manifest holds more than %d documents", maxDocuments)
}

func decode(t *testing.T, manifest []byte) []document {
	t.Helper()
	documents, err := decodeDocuments(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return documents
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

// only returns the single rendered object of a kind, failing the test when the
// chart renders any other number of them.
func only(t *testing.T, docs []document, kind string) document {
	t.Helper()
	matched := ofKind(docs, kind)
	if len(matched) != 1 {
		t.Fatalf("expected exactly one %s, got %d", kind, len(matched))
	}
	return matched[0]
}

// child walks a chain of nested mappings, failing the test at the first key that
// is absent or is not itself a mapping.
func child(t *testing.T, doc document, path ...string) document {
	t.Helper()
	current := doc
	for i := 0; i < len(path); i++ {
		next, ok := current[path[i]].(document)
		if !ok {
			t.Fatalf("no mapping at %s", strings.Join(path[:i+1], "."))
		}
		current = next
	}
	return current
}

// podSpec reads spec.template.spec of the rendered Deployment.
func podSpec(t *testing.T, docs []document) document {
	t.Helper()
	return child(t, only(t, docs, "Deployment"), "spec", "template", "spec")
}

// wantExactly asserts a label mapping holds those keys and no others, which is
// what a selector needs: an extra key changes what the selector matches.
func wantExactly(t *testing.T, where string, got document, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want exactly %v", where, got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("%s[%q] = %v, want %q", where, key, got[key], value)
		}
	}
}

// wantContains asserts a mapping carries every wanted key, which is what a
// mutable label set owes its readers: extra keys are allowed, missing ones are
// not.
func wantContains(t *testing.T, where string, got document, want map[string]string) {
	t.Helper()
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("%s[%q] = %v, want %q", where, key, got[key], value)
		}
	}
}

// selectorLabels is the label set praetor.selectorLabels renders for a release.
func selectorLabels(chartName, release string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":     chartName,
		"app.kubernetes.io/instance": release,
	}
}

// chartMetadata decodes the shipped Chart.yaml. The standard labels are derived
// from it, so the expectation reads the same file the template does and a chart
// version bump does not fail a test that is about the templates.
func chartMetadata(t *testing.T) document {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(helmChart(t), "Chart.yaml"))
	if err != nil {
		t.Fatalf("read Chart.yaml: %v", err)
	}
	documents, err := decodeDocuments(raw)
	if err != nil {
		t.Fatalf("decode Chart.yaml: %v", err)
	}
	if len(documents) != 1 {
		t.Fatalf("Chart.yaml holds %d documents, want 1", len(documents))
	}
	return documents[0]
}

// standardLabels is the release-independent half of praetor.labels, derived from
// Chart.yaml exactly as the helper derives it.
func standardLabels(t *testing.T) map[string]string {
	t.Helper()
	chart := chartMetadata(t)
	// The helper substitutes "_" for the "+" a semver build tag may carry and
	// truncates at 63 characters; "<name>-<version>" is well inside that bound here.
	return map[string]string{
		"helm.sh/chart": strings.ReplaceAll(
			fmt.Sprintf("%v-%v", chart["name"], chart["version"]), "+", "_"),
		"app.kubernetes.io/version":    fmt.Sprintf("%v", chart["appVersion"]),
		"app.kubernetes.io/managed-by": "Helm",
	}
}

// containerName reads the single container name of the rendered pod spec.
func containerName(t *testing.T, docs []document) string {
	t.Helper()
	containers, ok := podSpec(t, docs)["containers"].([]any)
	if !ok || len(containers) != 1 {
		t.Fatalf("containers = %v, want exactly one", podSpec(t, docs)["containers"])
	}
	entry, ok := containers[0].(document)
	if !ok {
		t.Fatalf("containers[0] = %v, want a mapping", containers[0])
	}
	value, _ := entry["name"].(string)
	return value
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
	account := only(t, docs, "ServiceAccount")
	if got := name(account); got != "alpha-praetor" {
		t.Fatalf("ServiceAccount name = %q, want alpha-praetor", got)
	}
	// BUG-661 is fixed on both objects: the pod declines the mount and the
	// account declines to hand one out, so neither half can be dropped alone.
	if got := account["automountServiceAccountToken"]; got != false {
		t.Fatalf("ServiceAccount automountServiceAccountToken = %v, want false", got)
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

// Positive: with create=true an explicit serviceAccount.name wins over the
// release-scoped default, on the created object and on the pod alike. This is
// the migration path out of the breaking rename in this change: an existing
// release keeps its praetor-sa account by setting the name.
func TestServiceAccountCreateTrueHonoursExplicitName(t *testing.T) {
	docs := render(t, "alpha", "--set", "serviceAccount.name=praetor-sa")
	if got := name(only(t, docs, "ServiceAccount")); got != "praetor-sa" {
		t.Fatalf("ServiceAccount name = %q, want praetor-sa", got)
	}
	if got := podSpec(t, docs)["serviceAccountName"]; got != "praetor-sa" {
		t.Fatalf("serviceAccountName = %v, want praetor-sa", got)
	}
	// The other names stay release-scoped, so the opt-out is scoped to the account.
	if got := name(only(t, docs, "Deployment")); got != "alpha-praetor" {
		t.Fatalf("Deployment name = %q, want alpha-praetor", got)
	}
}

// Boundary: spec.selector cannot be edited after a Deployment exists, so the
// label set it matches on is pinned exactly here; renaming a selector label
// turns `helm upgrade` on a live release into a hard failure.
func TestSelectorLabelsPinTheImmutableFields(t *testing.T) {
	docs := render(t, "alpha")
	want := selectorLabels("praetor", "alpha")
	deployment := only(t, docs, "Deployment")
	wantExactly(t, "Deployment spec.selector.matchLabels",
		child(t, deployment, "spec", "selector", "matchLabels"), want)
	wantExactly(t, "Service spec.selector",
		child(t, only(t, docs, "Service"), "spec", "selector"), want)
	// The pod template labels are mutable and only have to be a superset of the
	// selector, so adding the standard chart labels there stays a safe change.
	wantContains(t, "pod template metadata.labels",
		child(t, deployment, "spec", "template", "metadata", "labels"), want)
	// The object labels are a superset too: the selector subset plus release metadata.
	wantContains(t, "Deployment metadata.labels", child(t, deployment, "metadata", "labels"), want)
}

// Positive: every rendered object carries the standard chart labels that
// `helm list` and `kubectl get -l` read. Dropping one from praetor.labels used to
// leave this suite green.
func TestEveryObjectCarriesTheStandardChartLabels(t *testing.T) {
	want := standardLabels(t)
	docs := render(t, "alpha")
	if len(docs) == 0 {
		t.Fatal("the chart rendered no object")
	}
	for i := 0; i < len(docs); i++ {
		where := fmt.Sprintf("%v %q metadata.labels", docs[i]["kind"], name(docs[i]))
		wantContains(t, where, child(t, docs[i], "metadata", "labels"), want)
	}
}

// Negative: the standard labels describe the chart, not the release, so
// nameOverride moves neither of them while it does move the selector label.
func TestStandardLabelsIgnoreTheNameOverride(t *testing.T) {
	want := standardLabels(t)
	labels := child(t, only(t, render(t, "alpha", "--set", "nameOverride=alt"), "Deployment"),
		"metadata", "labels")
	wantContains(t, "Deployment metadata.labels", labels, want)
	if labels["app.kubernetes.io/name"] != "alt" {
		t.Fatalf("app.kubernetes.io/name = %v, want alt", labels["app.kubernetes.io/name"])
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

// Boundary: nameOverride alone renames within the release scope, and it also
// moves app.kubernetes.io/name, which docs/guides/helm-chart.md documents and
// which the immutable Deployment selector matches on.
func TestNameOverrideKeepsReleaseScope(t *testing.T) {
	docs := render(t, "alpha", "--set", "nameOverride=alt")
	if got := name(only(t, docs, "ServiceAccount")); got != "alpha-alt" {
		t.Fatalf("ServiceAccount name = %q, want alpha-alt", got)
	}
	wantExactly(t, "Deployment spec.selector.matchLabels",
		child(t, only(t, docs, "Deployment"), "spec", "selector", "matchLabels"),
		selectorLabels("alt", "alpha"))
	// The container name follows the chart name too; a literal there would make
	// `kubectl logs -c` name a container the values no longer describe.
	if got := containerName(t, docs); got != "alt" {
		t.Fatalf("container name = %q, want alt", got)
	}
	if got := containerName(t, render(t, "alpha")); got != "praetor" {
		t.Fatalf("default container name = %q, want praetor", got)
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

// manifestOf builds a stream of n minimal documents, itself bounded by the
// constant the callers derive n from (HISS-02). A caller that asks for more than
// the bound fails here rather than silently receiving a shorter stream and
// asserting on it.
func manifestOf(t *testing.T, n int) []byte {
	t.Helper()
	if n > maxDocuments+1 {
		t.Fatalf("manifestOf(%d) exceeds the %d-document bound this helper builds under",
			n, maxDocuments+1)
	}
	var buf bytes.Buffer
	for i := 0; i < n && i <= maxDocuments+1; i++ {
		if i > 0 {
			buf.WriteString("---\n")
		}
		buf.WriteString("kind: Probe\n")
	}
	return buf.Bytes()
}

// Positive, boundary and negative for the decode bound: a short stream and a
// stream of exactly maxDocuments documents are both within the bound and decode
// whole; the first document past the bound is refused. The loop used to stop at
// the bound itself, so a 64-document manifest reported "more than 64 documents".
func TestDecodeBoundsTheDocumentStream(t *testing.T) {
	for _, count := range []int{1, maxDocuments} {
		documents, err := decodeDocuments(manifestOf(t, count))
		if err != nil {
			t.Fatalf("%d documents: %v", count, err)
		}
		if len(documents) != count {
			t.Fatalf("decoded %d documents from a %d-document manifest", len(documents), count)
		}
	}
	if _, err := decodeDocuments(manifestOf(t, maxDocuments+1)); err == nil {
		t.Fatalf("a %d-document manifest decoded without an overflow error", maxDocuments+1)
	}
}
