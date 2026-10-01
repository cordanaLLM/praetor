Start service:

```go
app.New(service.Module) // health probes and metrics
```

```json
{"timeout": 30}
```

```bash
make run
```

```console
$ make verify
ok  all tests passed
```
