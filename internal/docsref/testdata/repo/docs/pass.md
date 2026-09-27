# References that resolve

Every reference below exists in the fixture, so the check reports nothing for this file.

## Commands

- `praetorctl state sync --verify --log "done" .`
- `praetorctl state task add`
- `praetorctl state task [add|list]`
- `praetorctl state task <add|list>`
- `praetorctl state sync [--verify]`
- `praetorctl state task add fix-build`, an operand below a leaf
- `praetorctl hook claude pre-tool` and `praetorctl hook claude any-event`, operands of a leaf
- `praetorctl hook <client> <event>`
- `praetorctl audit --json`, a flag the code compares by hand
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
praetorctl audit # see internal/nothere/notes.go
```

A terminal transcript reads only the lines after the prompt; the rest is output:

```console
$ praetorctl audit \
    --strict
praetorctl version dev
internal/removedx/file.go:3: an output line, not a reference
$ praetorctl state task list
```

## Paths

- `cmd/standardsctl/main.go` and `cmd/standardsctl/main.go:12-20`
- `internal/events/` and `internal/events`
- `internal/events.PreTool`, `internal/events.Registry` and `internal/events.Lookup`
- `internal/events.templatePath`, an unexported identifier
- `internal/*/events.go` and `internal/<package>/doc.go`
- `deploy/arc/` and `deploy/k8s/app/kustomization.yaml`, operator-owned
- `docs/private/notes.md`, ignored by the fixture's `.gitignore`
- `docs/adr/0000-template.md`, named by the engine's source
- `https://example.com/internal/missing.go` is a URL
- `acme/praetor` names another repository

<!-- praetor:docs-references:off an illustrative path no repository carries -->

`internal/illustrative/example.go` and `praetorctl nosuch` are suppressed here.

<!-- praetor:docs-references:on -->
