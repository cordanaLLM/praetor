Start service:

```go
app.New(service.Module) // health probes and metrics
```

```json
{"timeout": 30}
```

```bash
make serve
```

```console
$ make check
ok  all tests passed
```
