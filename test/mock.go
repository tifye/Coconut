package test

import (
	"net"
	"sync/atomic"
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
