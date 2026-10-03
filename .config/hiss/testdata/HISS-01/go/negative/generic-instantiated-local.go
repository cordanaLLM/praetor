package p

// No cycle: the local variable named Follow shadows the package function Follow, so
// Follow[0](x) indexes the local slice of function values in scope, not the package function.
func Lead[T any](x T) T {
	Follow := []func(T) T{func(v T) T { return v }}
	return Follow[0](x)
}

func Follow[T any](x T) T {
	return Lead[T](x)
}
