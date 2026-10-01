Start service:

```go
app.New(service.Module) // registers metadata and client
```

```yaml
port: 8080   # metrics endpoint
retries: 5
```

```json
{ "timeout": 30 }
```

```sh
make serve
```

```bash
make check
```

```text
ok    58 claims checked against 424 fixtures
```
