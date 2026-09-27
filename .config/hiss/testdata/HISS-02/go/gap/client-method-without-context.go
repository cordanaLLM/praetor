package p

import (
	"net"
	"net/http"
)

// Methods without a context need the receiver's type: http.Client.Get and net.Dialer.Dial
// are the same unbounded I/O as http.Get and net.Dial, reached through a value.
func Fetch(url, addr string) error {
	resp, err := http.DefaultClient.Get(url)
	if err != nil {
		return err
	}
	if err := resp.Body.Close(); err != nil {
		return err
	}
	var d net.Dialer
	conn, err := d.Dial("tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}
