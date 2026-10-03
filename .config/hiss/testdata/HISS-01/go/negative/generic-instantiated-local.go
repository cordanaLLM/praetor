package p

// No cycle: the local variable named Follow shadows the package function Follow, so
// calling Follow[T] reaches the local function value in scope rather than the package function.
func Lead[T any](x T) T {
	Follow := func(v T) T { return v }
	_ = Follow
	return Follow[T](x)
}

func Follow[T any](x T) T {
	return Lead[T](x)
}
