package p

import web "net/http"

// The alias binds web to net/http; http.Get uses the default client with no context.
func Fetch(url string) (*web.Response, error) {
	return web.Get(url)
}
