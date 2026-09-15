package p

import "os"

// The credential is read from the environment; nothing secret is in the file.
func Token() string { return os.Getenv("SERVICE_API_TOKEN") }
