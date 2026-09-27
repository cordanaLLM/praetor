import type { PraetorFigure } from "../../tools/figures/types"

export default {
    title: "Multi-Forge Federation",
    alt: "Architecture of praetorctl calling forge drivers for GitHub, GitLab, and Gitea",
    evidence: [
        "internal/forge/forge.go:NewForge",
        "internal/forge/forge.go:Forge",
        "internal/forge/github.go:GitHubDriver",
        "internal/forge/gitlab.go:GitLabDriver",
        "internal/forge/gitea.go:GiteaDriver"
    ],
    props: {
        layout: {
            direction: "row",
            children: [
                { id: "cli", label: "praetorctl", shape: "box" },
                { id: "factory", label: "forge.NewForge", shape: "box" },
                {
                    direction: "column",
                    children: [
                        { id: "github", label: "GitHub Driver", shape: "box" },
                        { id: "gitlab", label: "GitLab Driver", shape: "box" },
                        { id: "gitea", label: "Gitea Driver", shape: "box" }
                    ]
                }
            ]
        },
        edges: [
            { from: "cli", to: "factory", label: "Initializes" },
            { from: "factory", to: "github", label: "provider=github" },
            { from: "factory", to: "gitlab", label: "provider=gitlab" },
            { from: "factory", to: "gitea", label: "provider=gitea" },
            { from: "cli", to: "github", quiet: true },
            { from: "cli", to: "gitlab", quiet: true },
            { from: "cli", to: "gitea", quiet: true }
        ],
        steps: [
            {
                label: "GitHub",
                flow: [
                    { edges: "cli->factory", say: "Initialize factory" },
                    { edges: "factory->github", say: "Create GitHub driver" },
                    { edges: "cli->github", say: "Authenticate()" },
                    { edges: "cli->github", say: "ReconcileProtection()" },
                    { edges: "cli->github", say: "ReconcileLabels()" },
                    { edges: "cli->github", say: "PostStatusCheck()" },
                    { edges: "cli->github", say: "CreatePullRequest()" },
                    { edges: "cli->github", say: "CreateIssue()" },
                    { edges: "cli->github", say: "ListIssues()" },
                    { edges: "cli->github", say: "UpdateIssue()" }
                ]
            },
            {
                label: "GitLab",
                flow: [
                    { edges: "cli->factory", say: "Initialize factory" },
                    { edges: "factory->gitlab", say: "Create GitLab driver" },
                    { edges: "cli->gitlab", say: "Authenticate() OK" },
                    { edges: "cli->gitlab", say: "Enforcement -> ErrNotImplemented" }
                ]
            },
            {
                label: "Gitea",
                flow: [
                    { edges: "cli->factory", say: "Initialize factory" },
                    { edges: "factory->gitea", say: "Create Gitea driver" },
                    { edges: "cli->gitea", say: "Authenticate() OK" },
                    { edges: "cli->gitea", say: "Enforcement -> ErrNotImplemented" }
                ]
            }
        ]
    }
} satisfies PraetorFigure;
