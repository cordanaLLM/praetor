package p

import "net"

// net.Dial takes no context, so the dial and the connection it returns are unbounded.
func Connect(addr string) (net.Conn, error) {
	return net.Dial("tcp", addr)
}
