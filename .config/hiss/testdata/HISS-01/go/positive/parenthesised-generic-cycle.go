package p

// Mutual recursion through parenthesised generic calls violates HISS-01.
// Explicit type arguments wrapped in parentheses must not hide the call-graph cycle.
func Ping[T any](x T) T {
	return (Pong[T])(x)
}

func Pong[T any](x T) T {
	return Ping[T](x)
}
