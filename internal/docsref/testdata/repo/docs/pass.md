# References that resolve

Every reference below exists in the fixture, so the check reports nothing for this file.

## Commands

- `praetorctl state sync --verify --log "done" .`
- `praetorctl state task add`
- `praetorctl state task [add|list]`
- `praetorctl state sync [--verify]`
- `praetorctl hook claude pre-tool`
- `praetorctl hook <client> <event>`
- `praetorctl audit --strict --config=.standards.yaml`
- `praetorctl audit --config .standards.yaml --strict`
- `praetorctl audit -h`
- `praetorctl --help`
- `praetorctl <command>`
- `./bin/praetorctl audit`
- `standardsctl state task list`
- `go run ./cmd/standardsctl audit --strict`

A sentence quoted in a span is not a call: `this praetorctl nosuch mention`.

```yaml
# Not a shell fence, so this is data: praetorctl nosuch
command: praetorctl nosuch
```

```bash
# praetorctl nosuch in a comment is not a call
praetorctl audit \
  --strict
FOO=1 praetorctl state task list | head -1
praetorctl audit --strict > report.txt 2>&1 # praetorctl nosuch
```

## Paths

- `cmd/standardsctl/main.go` and `cmd/standardsctl/main.go:12-20`
- `internal/events/` and `internal/events`
- `internal/events.PreTool`, `internal/events.Registry` and `internal/events.Lookup`
- `internal/*/events.go` and `internal/<package>/doc.go`
- `deploy/arc/` and `deploy/k8s/app/kustomization.yaml`, operator-owned
- `docs/private/notes.md`, ignored by the fixture's `.gitignore`
- `docs/adr/0000-template.md`, named by the engine's source
- `https://example.com/internal/missing.go` is a URL
- `lusoris/praetor` names another repository

<!-- praetor:docs-references:off an illustrative path no repository carries -->

`internal/illustrative/example.go` and `praetorctl nosuch` are suppressed here.

<!-- praetor:docs-references:on -->
