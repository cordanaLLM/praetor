# Release publisher fixture

The workflows (`workflows/`, copied into `.github/workflows` of a temporary repository by the tests) of jellysin/.github at 1e92384 (#45) and its release-please
configuration before and after that commit. Before #45 the configuration lacks
`skip-github-release`, while `prepare-release.yml` and `release.yml` create and
publish the GitHub release themselves; release-please then published v0.1.0
without assets. Used by the release publisher audit tests.
