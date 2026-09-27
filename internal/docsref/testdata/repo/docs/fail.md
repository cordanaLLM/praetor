# References that do not resolve

Each reference below is broken in exactly one way.

- `praetorctl provider doctor`
- `praetorctl state frobnicate`
- `praetorctl state task purge`
- `praetorctl audit --nope`
- `praetorctl state sync --bogus=1`
- `praetorctl state task [add|zap]`
- `praetorctl --version`
- `go run ./cmd/standardsctl audit --gone`
- `praetorctl hook claude pre-nothing`

```bash
praetorctl audit \
  --strict \
  --missing
```

- `internal/removed/file.go`
- `internal/gone/`
- `internal/events.Missing`
- `deploy/k8sx/a.yaml`
