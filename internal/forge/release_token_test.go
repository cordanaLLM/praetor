package forge

import (
	"context"
	"strings"
	"testing"
)

// canonicalPassingReleaseWorkflow is the required release-please workflow shape.
const canonicalPassingReleaseWorkflow = `
jobs:
  release:
    runs-on: ubuntu-26.04
    steps:
      - name: Mint the release token
        id: app-token
        uses: actions/create-github-app-token@64ab729a6294000f5aa4a4bc0b73d1f32bc45510  # v3.3.0
        with:
          client-id: ${{ vars.RELEASE_APP_CLIENT_ID }}
          private-key: ${{ secrets.RELEASE_APP_PRIVATE_KEY }}
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.app-token.outputs.token }}
`

// assertReleaseWorkflowPasses verifies that a workflow yields 1 release-please step and 0 findings.
func assertReleaseWorkflowPasses(t *testing.T, ctx context.Context, content string) {
	t.Helper()
	root := t.TempDir()
	writeWorkflow(t, root, "release.yml", content)
	findings, count, err := MeasureReleaseTokens(ctx, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d; want 1", count)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %v; want none", findings)
	}
}

// TestMeasureReleaseTokens_Positive verifies that canonical and whitespace-variant workflows pass.
func TestMeasureReleaseTokens_Positive(t *testing.T) {
	ctx := context.Background()

	t.Run("canonical shape", func(t *testing.T) {
		assertReleaseWorkflowPasses(t, ctx, canonicalPassingReleaseWorkflow)
	})

	t.Run("legacy action prefix and app-id", func(t *testing.T) {
		content := `
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - id: custom-token
        uses: actions/create-github-app-token@v1
        with:
          app-id: 12345
          private-key: ${{ secrets.APP_KEY }}
      - uses: google-github-actions/release-please-action@v3
        with:
          token: ${{ steps.custom-token.outputs.token }}
`
		assertReleaseWorkflowPasses(t, ctx, content)
	})

	t.Run("whitespace variants in expressions", func(t *testing.T) {
		content := `
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - id: app-token
        uses: actions/create-github-app-token@v1
        with:
          client-id: ${{vars.APP_CLIENT_ID}}
          private-key: ${{  secrets.APP_KEY  }}
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{   steps.app-token.outputs.token   }}
`
		assertReleaseWorkflowPasses(t, ctx, content)
	})
}

type releaseTokenTestCase struct {
	name        string
	content     string
	wantProblem string
}

var negativeReleaseTokenCases = []releaseTokenTestCase{
	{
		name: "token missing with missing with block",
		content: `
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: googleapis/release-please-action@v4
`,
		wantProblem: problemNoToken,
	},
	{
		name: "token missing in with map",
		content: `
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: googleapis/release-please-action@v4
        with:
          release-type: node
`,
		wantProblem: problemNoToken,
	},
	{
		name: "token is github.token",
		content: `
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ github.token }}
`,
		wantProblem: problemWorkflowToken,
	},
	{
		name: "token is secrets.GITHUB_TOKEN",
		content: `
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ secrets.GITHUB_TOKEN }}
`,
		wantProblem: problemWorkflowToken,
	},
	{
		name: "token from a step that is not create-github-app-token (checkout)",
		content: `
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - id: checkout-step
        uses: actions/checkout@v4
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.checkout-step.outputs.token }}
`,
		wantProblem: problemNotAppToken,
	},
	{
		name: "token from a later step",
		content: `
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.app-token.outputs.token }}
      - id: app-token
        uses: actions/create-github-app-token@v1
        with:
          client-id: ${{ vars.CLIENT_ID }}
          private-key: ${{ secrets.KEY }}
`,
		wantProblem: problemNoEarlierStep,
	},
	{
		name: "fallback || in token",
		content: `
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - id: app-token
        uses: actions/create-github-app-token@v1
        with:
          client-id: ${{ vars.CLIENT_ID }}
          private-key: ${{ secrets.KEY }}
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.app-token.outputs.token || github.token }}
`,
		wantProblem: problemFallback,
	},
	{
		name: "app-token step conditional",
		content: `
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - id: app-token
        if: github.ref == 'refs/heads/main'
        uses: actions/create-github-app-token@v1
        with:
          client-id: ${{ vars.CLIENT_ID }}
          private-key: ${{ secrets.KEY }}
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.app-token.outputs.token }}
`,
		wantProblem: problemSkippable,
	},
	{
		name: "app-token step continue-on-error",
		content: `
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - id: app-token
        continue-on-error: true
        uses: actions/create-github-app-token@v1
        with:
          client-id: ${{ vars.CLIENT_ID }}
          private-key: ${{ secrets.KEY }}
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.app-token.outputs.token }}
`,
		wantProblem: problemSkippable,
	},
	{
		name: "private-key not a secrets expression literal",
		content: `
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - id: app-token
        uses: actions/create-github-app-token@v1
        with:
          client-id: ${{ vars.CLIENT_ID }}
          private-key: my-raw-key
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.app-token.outputs.token }}
`,
		wantProblem: problemKeyNotSecret,
	},
	{
		name: "private-key not a secrets expression env",
		content: `
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - id: app-token
        uses: actions/create-github-app-token@v1
        with:
          client-id: ${{ vars.CLIENT_ID }}
          private-key: ${{ env.PRIVATE_KEY }}
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.app-token.outputs.token }}
`,
		wantProblem: problemKeyNotSecret,
	},
	{
		name: problemNoAppID,
		content: `
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - id: app-token
        uses: actions/create-github-app-token@v1
        with:
          private-key: ${{ secrets.PRIVATE_KEY }}
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.app-token.outputs.token }}
`,
		wantProblem: problemNoAppID,
	},
}

// TestMeasureReleaseTokens_Negative tests each finding kind specified in section 3.
func TestMeasureReleaseTokens_Negative(t *testing.T) {
	ctx := context.Background()
	for _, tc := range negativeReleaseTokenCases {
		t.Run(tc.name, func(t *testing.T) {
			assertReleaseWorkflowProblem(t, ctx, tc.content, tc.wantProblem)
		})
	}
}

// assertReleaseWorkflowProblem verifies that a workflow yields 1 release-please step and a finding with wantProblem.
func assertReleaseWorkflowProblem(t *testing.T, ctx context.Context, content, wantProblem string) {
	t.Helper()
	root := t.TempDir()
	writeWorkflow(t, root, "release.yml", content)
	findings, count, err := MeasureReleaseTokens(ctx, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d; want 1", count)
	}
	if len(findings) == 0 {
		t.Fatalf("expected finding with problem %q, got none", wantProblem)
	}
	for _, f := range findings {
		if strings.Contains(f.Problem, wantProblem) {
			return
		}
	}
	t.Errorf("findings = %v; want problem containing %q", findings, wantProblem)
}

// TestMeasureReleaseTokens_Boundary tests edge conditions and boundaries.
func TestMeasureReleaseTokens_Boundary(t *testing.T) {
	ctx := context.Background()

	t.Run("no workflows dir", func(t *testing.T) {
		root := t.TempDir()
		findings, count, err := MeasureReleaseTokens(ctx, root)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if count != 0 || len(findings) != 0 {
			t.Errorf("got count=%d, findings=%d; want 0, 0", count, len(findings))
		}
	})

	t.Run("token step in first job used in second job", func(t *testing.T) {
		testBoundaryCrossJob(t, ctx)
	})

	t.Run("two release-please steps both valid", func(t *testing.T) {
		testBoundaryTwoStepsValid(t, ctx)
	})

	t.Run("two release-please steps one invalid", func(t *testing.T) {
		testBoundaryTwoStepsOneInvalid(t, ctx)
	})
}

func testBoundaryCrossJob(t *testing.T, ctx context.Context) {
	t.Helper()
	root := t.TempDir()
	content := `
jobs:
  mint:
    runs-on: ubuntu-latest
    steps:
      - id: app-token
        uses: actions/create-github-app-token@v1
        with:
          client-id: ${{ vars.CLIENT_ID }}
          private-key: ${{ secrets.KEY }}
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.app-token.outputs.token }}
`
	writeWorkflow(t, root, "release.yml", content)
	findings, count, err := MeasureReleaseTokens(ctx, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d; want 1", count)
	}
	if len(findings) == 0 {
		t.Fatal("expected finding for cross-job token step reference, got none")
	}
	wantProblem := problemNoEarlierStep
	if !strings.Contains(findings[0].Problem, wantProblem) {
		t.Errorf("finding[0].Problem = %q; want %q", findings[0].Problem, wantProblem)
	}
}

func testBoundaryTwoStepsValid(t *testing.T, ctx context.Context) {
	t.Helper()
	root := t.TempDir()
	content := `
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - id: app-token
        uses: actions/create-github-app-token@v1
        with:
          client-id: ${{ vars.CLIENT_ID }}
          private-key: ${{ secrets.KEY }}
      - name: release 1
        uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.app-token.outputs.token }}
      - name: release 2
        uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.app-token.outputs.token }}
`
	writeWorkflow(t, root, "release.yml", content)
	findings, count, err := MeasureReleaseTokens(ctx, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d; want 2", count)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %v; want none", findings)
	}
}

func testBoundaryTwoStepsOneInvalid(t *testing.T, ctx context.Context) {
	t.Helper()
	root := t.TempDir()
	content := `
jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - id: app-token
        uses: actions/create-github-app-token@v1
        with:
          client-id: ${{ vars.CLIENT_ID }}
          private-key: ${{ secrets.KEY }}
      - name: release 1
        uses: googleapis/release-please-action@v4
        with:
          token: ${{ steps.app-token.outputs.token }}
      - name: release 2
        uses: googleapis/release-please-action@v4
`
	writeWorkflow(t, root, "release.yml", content)
	findings, count, err := MeasureReleaseTokens(ctx, root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d; want 2", count)
	}
	if len(findings) != 1 {
		t.Fatalf("findings count = %d; want 1", len(findings))
	}
	if !strings.Contains(findings[0].Problem, problemNoToken) {
		t.Errorf("finding Problem = %q; want %q", findings[0].Problem, problemNoToken)
	}
}
