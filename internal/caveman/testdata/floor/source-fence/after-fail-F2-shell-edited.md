Start service:

```go
app.New(service.Module) // health probes and metrics
```

```yaml
port: 8080 # health probes
retries: 5
```

```json
{"timeout": 30}
```

```bash
make run
```

```console
$ make check
ok  all tests passed
```
