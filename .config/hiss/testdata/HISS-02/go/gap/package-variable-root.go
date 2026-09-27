package p

import "context"

// A package variable may be reassigned by any function of the package, so its value at a
// call is not decided without whole-package data flow.
var root = context.Background()

func Load(url string) ([]byte, error) {
	return fetch(root, url)
}

func fetch(ctx context.Context, url string) ([]byte, error) { return nil, ctx.Err() }
