Start service:

```go
app.New(service.Module) // health probes and metrics
```

```yaml
port: 8080 # health probes
# retries: 5
```

```json
{"timeout": 30}
```

```bash
make serve
```

```console
$ make check
ok  58 claims checked against 424 fixtures
```
