# Research and upstream radar

The radar watches public research and upstream sources for a repository: the releases of the
projects it builds on, and the feeds where papers and announcements appear. A registry file lists
the sources, `praetorctl radar collect` turns one window of their entries into a Markdown digest,
and every text a source supplied is neutralised before anyone reads it. Collection keeps no state
and reads no clock: the window is an input, so consecutive runs cover time without gap or overlap.

This is the offline part of the module proposed in
[#818](https://github.com/cordanaLLM/praetor/issues/818). It validates registries and collects
from planted source files. Reading sources over the network, the digest issue, the scheduled
workflow, the audit check, triage into the ledger and the licence and patent gate are later
slices ([below](#not-built-yet)).

## Quick start

Declare the radar in `.standards.yaml`. `registry` is optional and defaults to
`.config/radar.yaml`:

```yaml
radar:
  registry: ".config/radar.yaml"
```

List the sources in that file:

```yaml
---
version: 1
sources:
  - id: example-project
    kind: github_repo
    url: https://github.com/example-org/example-project
    why: Upstream of the tokenizer we port
  - id: example-papers
    kind: feed
    url: https://example.org/papers/tokenizers.rss
    why: Papers on tokenizer performance
```

Validate it, then collect the seven days before 7 October 2026 from a directory of planted
source files:

```bash
praetorctl radar validate
praetorctl radar collect --fixture=internal/radar/testdata/acceptance --now=2026-10-07 --days=7 --out=digest.md \
  --registry=internal/radar/testdata/acceptance/radar.yaml
```

The second command collects the acceptance fixture that ships with the engine
(`internal/radar/testdata/acceptance`): an organisation activity feed with a new repository and
a push, a paper feed and a releases listing. Its digest lists those four items and nothing older
(`TestCollect_Positive_AcceptanceFixture`).

## Commands

| Command | Does | Fails when |
| :--- | :--- | :--- |
| `praetorctl radar validate [--path=.] [--registry=<file>]` | Reads the registry `radar.registry` names in the manifest at `--path`, or the `--registry` file, and prints the source count by kind | the registry is invalid, or the manifest declares no `radar` section and no `--registry` is given |
| `praetorctl radar collect --fixture=<dir> --now=<time> --out=<file> [--days=7] [--path=.] [--registry=<file>]` | Collects the window `[now - days, now)` from the planted files in `--fixture` and writes the digest to `--out` | every source failed, or an argument is missing or invalid |

`--now` is an RFC 3339 timestamp or a calendar date, which means midnight UTC. `--days` runs from 1
to 366. `collect` prints the read path it used. Without `--fixture` it fails with
`network collection is not implemented yet (#818)` instead of collecting nothing. Both commands
live in `cmd/standardsctl/radar.go`; `TestRadarCommand_Negative_Refusals` covers each refusal.

## The registry

The registry is one YAML document, decoded strictly by `radar.ParseRegistry`
(`internal/radar/registry.go`): an unknown or misspelled key is an error, not a silently ignored
line.

| Field | Holds | Rule |
| :--- | :--- | :--- |
| `version` | the schema version | must be `1` |
| `sources[].id` | the name the digest uses | ASCII letters, digits, `.`, `_`, `-`, starting with a letter or digit; at most 64 bytes; unique without regard to case |
| `sources[].kind` | how the source is read | `feed` or `github_repo` |
| `sources[].url` | where the source lives | canonical HTTPS without credentials, query or fragment; at most 2048 bytes |
| `sources[].why` | what the source matters for | one line, at most 512 bytes, no `@` |

A registry lists between 1 and 256 sources and is at most 256 KiB. Every problem is reported at
once, each naming its field, such as `sources[2].url`. `TestRegistry_Negative_Refusals` and
`TestRegistry_Boundary_Limits` in `internal/radar/registry_test.go` pin these rules.

The two kinds read in this version:

- `feed` is an RSS 2.0, RSS 1.0 or Atom feed at any HTTPS address.
- `github_repo` is the releases of one repository, written `https://github.com/<owner>/<repository>`.

The kinds `github_owner`, `query` and `manual` are refused with `kind is not supported yet`; any
other kind is refused as unknown. A URL with a query is a search, which is the `query` kind.

The registry has no licence field. Licence data will come from the forge licence API in a later
slice. A source without licence data, such as a feed, is treated as unknown and never as
permitted.

## What the registry refuses

The registry and the digest name sources, never individuals. `radar.ParseRegistry` refuses a
source whose URL addresses one account (`internal/radar/person.go`):

- any page on a domain that serves profiles or person identifiers, such as `linkedin.com`, `x.com`,
  `orcid.org`, `scholar.google.com` and `gist.github.com`;
- a single path segment on a code host (`github.com`, `gitlab.com`, `codeberg.org`, `gitea.com`,
  `bitbucket.org`, `huggingface.co` and its short domain `hf.co`), which is a user or
  organisation page or an account's activity feed;
- a first path segment such as `user`, `users`, `u`, `people`, `profile`, `author` or `members`
  on any host, and arXiv author listings and dblp person pages;
- any path segment starting with `@` or `~`, the shape of fediverse handles and home pages;
- a `github_repo` whose name equals its owner, which is the owner's profile page;
- a `why` that contains `@`, which mentions an account or spells an address.

Each listed domain covers its subdomains, matched on whole labels: `www.x.com`,
`mobile.twitter.com` and `de.linkedin.com` are refused like the domain itself, while
`notx.com` and `x.com.example.org` are not. A subdomain a service uses for something other than
accounts is refused with the rest, so a single-segment feed on a code host's product subdomain,
such as `https://about.gitlab.com/atom.xml`, does not load; the check errs on the side of
refusing. `TestRegistry_Negative_Refusals` and `TestRegistry_Boundary_DomainLabels` list the
cases.

These refusals are structural. They catch the URL shapes listed here and nothing else: a page
about one person at an address of another shape passes. Review the registry like any other
change. Organisations cannot be told from users offline, so single-segment code host URLs are
refused for both until the `github_owner` kind can ask the forge.

## The window

`radar.NewWindow` (`internal/radar/window.go`) turns `--now` and `--days` into the half-open UTC
interval `[since, now)`, with `since` exactly `days` × 24 hours before `now`. An entry published
at `since` belongs to the window; one published at `now` belongs to the next. An entry that
carries only a date is placed by date: it belongs to the window whose `since` date is on or before
it and whose `now` date is after it. Two runs whose `--now` values are one window apart therefore
partition time and dates exactly (`TestWindow_Boundary_PartitionAndLimits`).

## Collecting from fixtures

`--fixture` names a directory holding one file per source, named after its id:

| Kind | File | Content |
| :--- | :--- | :--- |
| `feed` | `<id>.xml` | the feed document |
| `github_repo` | `<id>.json` | the JSON the GitHub REST API returns for `GET /repos/{owner}/{repo}/releases` |

A missing or unreadable file fails that source, as an unreachable source would. The readers treat
every document as untrusted (`internal/radar/feed.go`, `internal/radar/release.go`):

- a feed with a document type or entity declaration is refused;
- a feed is at most 4 MiB, nests at most 32 elements and carries at most 5000 entries; a releases
  listing is at most 4 MiB and 1000 releases. A larger document fails rather than being cut, so
  the digest never presents part of a source as all of it;
- malformed XML or JSON, a document that is not a feed and the API's error object are failures,
  never an empty source;
- a draft release is left out; an entry or release without a readable date is counted and named
  in the digest, because no window can place it;
- a feed date is read the same way on every host. An RSS date counts only when its zone is a
  numeric offset or an RFC 822 zone name with a fixed meaning (`UT`, `GMT`, `Z` and the US zones
  `EST` to `PDT`), read at the offsets RFC 5322 section 4.3 gives them. Any other zone name, such
  as `CEST` or a military letter other than `Z`, leaves the entry undated, because placing it
  would rest on a guess or on the host's own zone. A
  two-digit year takes the RFC 5322 century: `00` to `49` are 20xx, `50` to `99` are 19xx.
  `TestParseFeed_Positive_RFC822Zones`, `TestParseFeed_Negative_UnresolvedZones` and
  `TestParseFeed_Boundary_TwoDigitYear` run every case under three local zones.

## The digest

`radar.Render` (`internal/radar/digest.go`) writes the digest:

```markdown
# Radar digest 2026-09-30 to 2026-10-07

Window: 2026-09-30T00:00:00Z <= t < 2026-10-07T00:00:00Z (UTC). Sources read: 1, failed: 1.

## example-project (github_repo)

Source: <https://github.com/example-org/example-project>

- 2026-10-06 [v2.0.0](https://github.com/example-org/example-project/releases/tag/v2.0.0)

## Failed sources

- example-papers: fixture example-papers.xml is missing: file does not exist
```

- Each source with something new gets a section, in registry order, newest item first. A section
  lists at most 50 items and counts the rest.
- When nothing is new and no source failed, the digest is an empty file.
- Every failed source is named with its reason. The run exits non-zero only when every source
  failed, and it still writes the digest that names them.

Titles and failure reasons are untrusted. `radar.Neutralize` reduces each to one line of inert
Markdown, capped at 200 runes: it decodes HTML entities and removes HTML tags, drops control and
invisible format characters, removes `@` so no account is mentioned, breaks issue references
(`#12`, `GH-12`) so none links, replaces `|` so no table breaks, defangs URLs and escapes the
Markdown punctuation that opens emphasis, code, links or images. An item link renders only when
it is a plain `http` or `https` address; a link to an issue, pull request or discussion is printed
as code so the digest does not cross-reference it. `TestNeutralize_Positive_Table` lists each
case.

## Not built yet

These parts of [#818](https://github.com/cordanaLLM/praetor/issues/818) follow in later slices:

- reading feeds and GitHub releases over the network, with per-source timeouts and byte caps;
- publishing the digest to one forge issue per window, beside the `--out` file;
- the scheduled workflow template and an audit check that a declared radar has a registry that
  validates and a workflow that runs;
- the triage procedure for an agent lane, which lands outcomes in the ledger;
- the licence and patent gate, with licence data from the forge licence API.
