package xgorm

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

type errorCloseConn struct {
	net.Conn
	err   error
	calls int
}

func (c *errorCloseConn) Close() error { c.calls++; _ = c.Conn.Close(); return c.err }
func TestSQLServerDialerFinish(t *testing.T) {
	t.Parallel()
	type finishCase struct {
		name             string
		closeConnections bool
	}
	for _, tc := range []finishCase{{"failure closes", true}, {"success detaches", false}} {
		t.Run(tc.name, func(t *testing.T) {
			local, remote := net.Pipe()
			defer remote.Close()
			defer local.Close()
			sentinel := errors.New("socket close failed")
			connection := &errorCloseConn{Conn: local, err: sentinel}
			dialer := &sqlServerDialer{connections: []net.Conn{connection}}
			err := dialer.finish(tc.closeConnections)
			if tc.closeConnections {
				if !errors.Is(err, sentinel) || connection.calls != 1 {
					t.Fatalf("finish = %v closes=%d", err, connection.calls)
				}
			} else if err != nil || connection.calls != 0 {
				t.Fatalf("finish = %v closes=%d", err, connection.calls)
			}
			if got := dialer.finish(true); !errors.Is(got, err) || connection.calls != btoi(tc.closeConnections) {
				t.Fatalf("second finish = %v closes=%d", got, connection.calls)
			}
			if dialer.connections != nil {
				t.Fatal("sockets retained after finish")
			}
		})
	}
}
func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
}
func TestSQLServerDialerLateConnection(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	dialer := &sqlServerDialer{}
	if err = dialer.finish(false); err != nil {
		t.Fatal(err)
	}
	conn, err := dialer.DialContext(ctx, "tcp", listener.Addr().String())
	if conn != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("late dial = %v, %v", conn, err)
	}
}
func TestSQLServerConnectorCanceledLogin(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan struct{})
	peerDone := make(chan struct{})
	go func() {
		defer close(peerDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		close(accepted)
		buffer := make([]byte, 4096)
		for {
			if _, err = conn.Read(buffer); err != nil {
				return
			}
		}
	}()
	connection, err := newSQLServerConnection("sqlserver://user:password@" + listener.Addr().String() + "?database=example&encrypt=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.pool.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- connection.pool.PingContext(ctx) }()
	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("login socket not opened")
	}
	cancel()
	select {
	case err = <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Ping = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled login did not finish")
	}
	select {
	case <-peerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled login socket remained open")
	}
}
