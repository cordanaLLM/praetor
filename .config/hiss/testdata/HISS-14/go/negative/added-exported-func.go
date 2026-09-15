package p

// Append-only: a new exported function beside the existing surface. No caller breaks,
// so no breaking indicator and no Migration: footer are required.
func Ping() string { return "pong" }
