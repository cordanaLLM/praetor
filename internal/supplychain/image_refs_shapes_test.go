// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"testing"
)

type shapeExpectation struct {
	line         int
	ref          string
	unresolvable bool
}

type imageRefShapeCase struct {
	name string
	file string
	body string
	want []shapeExpectation
}

func workflowShapeCases(digest string) []imageRefShapeCase {
	return []imageRefShapeCase{
		{
			name: "matrix include env and step with image keys produce no finding",
			file: ".github/workflows/matrix.yml",
			body: "name: CI\non: push\njobs:\n  train:\n    runs-on: ubuntu-latest\n    strategy:\n      matrix:\n        include:\n          - image: tiny-gemma-trainer\n    env:\n      image: runner-env-name\n    steps:\n      - uses: actions/checkout@v4\n        with:\n          image: step-input-name\n",
		},
		{
			name: "unpinned workflow schema positions report with exact line numbers",
			file: ".github/workflows/containers.yml",
			body: "on: push\njobs:\n  scalar_container:\n    runs-on: ubuntu-latest\n    container: node:24\n    services:\n      db:\n        image: postgres:18\n    steps:\n      - uses: docker://alpine:3\n  mapping_container:\n    runs-on: ubuntu-latest\n    container: {image: node:24}\n",
			want: []shapeExpectation{
				{line: 5, ref: "node:24"},
				{line: 8, ref: "postgres:18"},
				{line: 10, ref: "docker://alpine:3"},
				{line: 13, ref: "node:24"},
			},
		},
		{
			name: "pinned workflow schema positions produce no finding",
			file: ".github/workflows/pinned.yml",
			body: "on: push\njobs:\n  scalar_container:\n    runs-on: ubuntu-latest\n    container: node:24@sha256:" + digest + "\n    services:\n      db:\n        image: postgres:18@sha256:" + digest + "\n    steps:\n      - uses: docker://alpine:3@sha256:" + digest + "\n  mapping_container:\n    runs-on: ubuntu-latest\n    container: {image: node:24@sha256:" + digest + "}\n",
		},
		{
			name: "matrix expression in container.image is unresolvable",
			file: ".github/workflows/expr.yml",
			body: "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    container:\n      image: ${{ matrix.image }}\n",
			want: []shapeExpectation{
				{line: 6, ref: "${{ matrix.image }}", unresolvable: true},
			},
		},
	}
}

func tokenAndInvalidHostCases(digest string) []imageRefShapeCase {
	return []imageRefShapeCase{
		{
			name: "deploy substitution tokens are unresolvable while sibling unpinned image stays blocking",
			file: "deploy/cnpg.yaml",
			body: "spec:\n  image: \"@CNPG_POSTGRES_IMAGE@\"\n  sidecar:\n    image: postgres:18\n  templated:\n    image: repo/@NAME@:1\n  pinned:\n    image: name:1@sha256:" + digest + "\n",
			want: []shapeExpectation{
				{line: 2, ref: "@CNPG_POSTGRES_IMAGE@", unresolvable: true},
				{line: 4, ref: "postgres:18", unresolvable: false},
				{line: 6, ref: "repo/@NAME@:1", unresolvable: true},
			},
		},
		{
			name: "substitution tokens in Dockerfile and devcontainer are unresolvable",
			file: "Dockerfile",
			body: "FROM @BASE_IMAGE@\n",
			want: []shapeExpectation{
				{line: 1, ref: "@BASE_IMAGE@", unresolvable: true},
			},
		},
		{
			name: "invalid TLD in Go source ignored while counter-hosts stay findings",
			file: "emulator.go",
			body: "package p\nconst (\n\ta = \"example.invalid/emulator:nonexistent\"\n\tb = \"FOO.INVALID:5000/y:1\"\n\tc = \"registry.example/x:1\"\n\td = \"invalid.example.com/x:1\"\n\te = \"notinvalid/x:1\"\n)\n",
			want: []shapeExpectation{
				{line: 5, ref: "registry.example/x:1"},
				{line: 6, ref: "invalid.example.com/x:1"},
			},
		},
		{
			name: "invalid TLD in Python source ignored while counter-hosts stay findings",
			file: "emulator.py",
			body: "A = \"example.invalid/emulator:nonexistent\"\nB = \"registry.test/x:1\"\n",
			want: []shapeExpectation{
				{line: 2, ref: "registry.test/x:1"},
			},
		},
		{
			name: "invalid TLD in YAML ignored while counter-hosts stay findings",
			file: "deploy/app.yaml",
			body: "a:\n  image: x.invalid/y:1\nb:\n  image: FOO.INVALID:5000/y:1\nc:\n  image: invalid/y:1\nd:\n  image: registry.example/x:1\ne:\n  image: invalid.example.com/x:1\nf:\n  image: notinvalid/x:1\n",
			want: []shapeExpectation{
				{line: 8, ref: "registry.example/x:1"},
				{line: 10, ref: "invalid.example.com/x:1"},
				{line: 12, ref: "notinvalid/x:1"},
			},
		},
	}
}

func TestCollectImageRefFindings_ThreeShapes_3D(t *testing.T) {
	const digest = "4a7b9f0e1c2d3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b"
	cases := append(workflowShapeCases(digest), tokenAndInvalidHostCases(digest)...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertImageRefShapeFindings(t, tc)
		})
	}
}

func assertImageRefShapeFindings(t *testing.T, tc imageRefShapeCase) {
	t.Helper()
	got := CollectImageRefFindings(tc.file, tc.body)
	if len(got) != len(tc.want) {
		t.Fatalf("got %d findings (%+v), want %d (%+v)", len(got), got, len(tc.want), tc.want)
	}
	for i, w := range tc.want {
		f := got[i]
		if f.File != tc.file || f.Line != w.line || f.Reference != w.ref || f.Unresolvable != w.unresolvable {
			t.Errorf("finding[%d] = %+v, want file=%q line=%d ref=%q unresolvable=%t",
				i, f, tc.file, w.line, w.ref, w.unresolvable)
		}
	}
}

func TestCollectImageRefFindings_UnparseableWorkflowIsUnresolvable(t *testing.T) {
	const file = ".github/workflows/broken.yml"
	got := CollectImageRefFindings(file, "jobs: [unclosed\n")
	if len(got) != 1 {
		t.Fatalf("unparseable workflow: got %d findings (%+v), want 1", len(got), got)
	}
	if !got[0].Unresolvable || got[0].File != file || got[0].Reference == "" {
		t.Fatalf("unparseable workflow finding = %+v, want Unresolvable=true on %s with parse error", got[0], file)
	}
}

// dockerfileContinuationCommentCases returns 3D test cases for continued Dockerfile FROM instructions with comments.
func dockerfileContinuationCommentCases(digest string) []imageRefShapeCase {
	return []imageRefShapeCase{
		{
			name: "continued FROM with full-line comments reports unpinned base and resolves stage alias",
			file: "Dockerfile",
			body: "FROM \\\n# comment inside continuation\nubuntu:latest AS base\nFROM \\\n  # comment with trailing backslash \\\n  base\n",
			want: []shapeExpectation{
				{line: 1, ref: "ubuntu:latest"},
			},
		},
		{
			name: "continued FROM with full-line comment and digest pin produces no finding",
			file: "Dockerfile",
			body: "FROM \\\n# comment\nubuntu:24.04@sha256:" + digest + "\n",
		},
		{
			name: "continued FROM with comments to scratch or incomplete EOF produces no finding",
			file: "Dockerfile",
			body: "FROM \\\n# comment 1\n# comment 2\nscratch\nFROM \\\n# trailing comment only\n",
		},
	}
}

// TestCollectImageRefFindings_DockerfileContinuedComments_3D verifies that full-line comments inside
// continued Dockerfile FROM instructions do not terminate the continuation early.
func TestCollectImageRefFindings_DockerfileContinuedComments_3D(t *testing.T) {
	const digest = "4a7b9f0e1c2d3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b"
	for _, tc := range dockerfileContinuationCommentCases(digest) {
		t.Run(tc.name, func(t *testing.T) {
			assertImageRefShapeFindings(t, tc)
		})
	}
}

// devcontainerMultilineCases returns 3D test cases for multiline devcontainer.json image fields.
func devcontainerMultilineCases(digest string) []imageRefShapeCase {
	return []imageRefShapeCase{
		{
			name: "devcontainer image field split across lines reports finding on value line",
			file: ".devcontainer/devcontainer.json",
			body: "{\n  \"name\": \"dev\",\n  \"image\":\n    \"ubuntu:latest\"\n}\n",
			want: []shapeExpectation{
				{line: 4, ref: "ubuntu:latest"},
			},
		},
		{
			name: "devcontainer pinned image field split across lines produces no finding",
			file: ".devcontainer/devcontainer.json",
			body: "{\n  \"image\":\n    \"ubuntu:24.04@sha256:" + digest + "\"\n}\n",
		},
		{
			name: "devcontainer multiline image with substitution token is unresolvable",
			file: ".devcontainer.json",
			body: "{\n  \"image\" \t\n : \n\t \"@DEVCONTAINER_IMAGE@\"\n}\n",
			want: []shapeExpectation{
				{line: 4, ref: "@DEVCONTAINER_IMAGE@", unresolvable: true},
			},
		},
	}
}

// TestCollectImageRefFindings_DevcontainerMultiline_3D verifies that devcontainer image fields
// spanning JSON whitespace and newlines are detected with their line numbers preserved.
func TestCollectImageRefFindings_DevcontainerMultiline_3D(t *testing.T) {
	const digest = "4a7b9f0e1c2d3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b"
	for _, tc := range devcontainerMultilineCases(digest) {
		t.Run(tc.name, func(t *testing.T) {
			assertImageRefShapeFindings(t, tc)
		})
	}
}

// yamlFlowMappingCases returns 3D test cases for YAML flow mappings and multiline image mappings.
func yamlFlowMappingCases(digest string) []imageRefShapeCase {
	return []imageRefShapeCase{
		{
			name: "flow mappings and multiline image mappings report unpinned images",
			file: "deploy/pod.yaml",
			body: "{image: nginx:latest}\nspec: {container: {image: redis:7}}\ncontainers: [{name: web, image: alpine:3.20}, {name: proxy, image: \"envoy:v1.31\"}]\nmultiline:\n  image:\n    postgres:18\n",
			want: []shapeExpectation{
				{line: 1, ref: "nginx:latest"},
				{line: 2, ref: "redis:7"},
				{line: 3, ref: "alpine:3.20"},
				{line: 3, ref: "envoy:v1.31"},
				{line: 6, ref: "postgres:18"},
			},
		},
		{
			name: "pinned flow mapping and structured image mapping produce no finding",
			file: "deploy/values.yaml",
			body: "{image: nginx:1.27@sha256:" + digest + ", name: image}\nimage:\n  repository: nginx\n  tag: latest\n",
		},
		{
			name: "flow mapping with template or substitution token is unresolvable",
			file: "deploy/template.yaml",
			body: "{image: \"${CONTAINER_IMAGE}\"}\nsidecar: {image: {{ .Values.sidecar.image }}}\n",
			want: []shapeExpectation{
				{line: 1, ref: "${CONTAINER_IMAGE}", unresolvable: true},
				{line: 2, ref: "{{ .Values.sidecar.image }}", unresolvable: true},
			},
		},
	}
}

// TestCollectImageRefFindings_YAMLFlowMappings_3D verifies that YAML flow mappings and multiline
// image mappings are inspected in non-workflow YAML files.
func TestCollectImageRefFindings_YAMLFlowMappings_3D(t *testing.T) {
	const digest = "4a7b9f0e1c2d3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b"
	for _, tc := range yamlFlowMappingCases(digest) {
		t.Run(tc.name, func(t *testing.T) {
			assertImageRefShapeFindings(t, tc)
		})
	}
}
