package p

// Mutual recursion through explicitly instantiated generic calls violates HISS-01.
// Explicit type arguments on calls must not hide the call-graph cycle.
func Ping[T any](x T) T {
	return Pong[T](x)
}

func Pong[T any](x T) T {
	return Ping[T](x)
}
