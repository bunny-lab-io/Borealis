// Package testsshpipe supplies independent control and data channels to SSH transport tests.
package testsshpipe

import (
	"context"
	"io"
	"net"
)

type Conn struct {
	net.Conn
	Data io.ReadWriteCloser
}

func Pipe() (*Conn, *Conn) {
	sender, receiver := net.Pipe()
	input, output := net.Pipe()
	return &Conn{sender, input}, &Conn{receiver, output}
}
func (c *Conn) Close() error                                        { c.Data.Close(); return c.Conn.Close() }
func (c *Conn) Open(context.Context, string) (io.ReadCloser, error) { return c.Data, nil }
