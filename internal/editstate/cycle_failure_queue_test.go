package editstate

import (
	"io"
	"net"
	"testing"
	"time"
)

type stalledCloseConn struct {
	net.Conn
	closing chan struct{}
	release chan struct{}
}

func (c *stalledCloseConn) Close() error {
	close(c.closing)
	<-c.release
	return c.Conn.Close()
}

func TestCycleQueuedEventReturnsWhenPublisherFailureCloseStalls(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	conn := &stalledCloseConn{Conn: client, closing: make(chan struct{}), release: make(chan struct{})}
	p, err := NewPublisher(conn, "session", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	cycles := NewCyclePublisher(p)
	go io.Copy(io.Discard, server)
	token, err := cycles.Begin()
	if err != nil {
		t.Fatal(err)
	}
	failed := make(chan error, 1)
	go func() { failed <- cycles.Update(token, "abc", 3, false) }()
	defer func() { close(conn.release); <-failed }()
	select {
	case <-conn.closing:
	case <-time.After(time.Second):
		t.Fatal("invalid update did not start connection close")
	}
	queued := make(chan error, 1)
	go func() { _, err := cycles.Begin(); queued <- err }()
	select {
	case err := <-queued:
		if err == nil {
			t.Fatal("new cycle succeeded after publisher failure")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("new cycle blocked behind publisher's stalled close")
	}
}
