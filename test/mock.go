package test

import (
	"net"
	"sync/atomic"
	"time"
)

type MockNetAddr struct {
	network, address string
}

func (m MockNetAddr) Network() string {
	return m.network
}
func (m MockNetAddr) String() string {
	return m.address
}

type MockNetListener struct {
	Closed  atomic.Bool
	NetAddr net.Addr
}

func NewMockNetListener(network string, address string) *MockNetListener {
	return &MockNetListener{
		Closed:  atomic.Bool{},
		NetAddr: MockNetAddr{network: network, address: address},
	}
}

func (m *MockNetListener) Accept() (net.Conn, error) {
	return nil, nil
}

func (m *MockNetListener) Close() error {
	if m.Closed.Load() {
		return net.ErrClosed
	}

	m.Closed.Store(true)
	return nil
}

func (m *MockNetListener) Addr() net.Addr {
	return m.NetAddr
}

type MockNetConn struct {
	Closed  atomic.Bool
	NetAddr net.Addr
}

func NewMockNetConn(network string, address string) *MockNetConn {
	return &MockNetConn{
		Closed:  atomic.Bool{},
		NetAddr: MockNetAddr{network: network, address: address},
	}
}

func (m *MockNetConn) Read(b []byte) (n int, err error) {
	return len(b), nil
}

func (m *MockNetConn) Write(b []byte) (n int, err error) {
	return len(b), nil
}

func (m *MockNetConn) Close() error {
	if m.Closed.Load() {
		return net.ErrClosed
	}

	m.Closed.Store(true)
	return nil
}

func (m *MockNetConn) LocalAddr() net.Addr {
	return m.NetAddr
}

func (m *MockNetConn) RemoteAddr() net.Addr {
	return m.NetAddr
}

func (m *MockNetConn) SetDeadline(t time.Time) error {
	return nil
}

func (m *MockNetConn) SetReadDeadline(t time.Time) error {
	return nil
}

func (m *MockNetConn) SetWriteDeadline(t time.Time) error {
	return nil
}
