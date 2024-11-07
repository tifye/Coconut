package coconut

import (
	"io"
	"net"
	"testing"

	"github.com/charmbracelet/log"
	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tifye/Coconut/test"
)

func Test_ServerClosesUnderlyNetworkIO(t *testing.T) {
	serverConfig := ServerConfig{
		ClientListenAddr: ":9000",
		ClientListenFunc: func(network, address string) (net.Listener, error) {
			return test.NewMockNetListener("tcp", ":9000"), nil
		},
	}
	server := NewServer(&serverConfig, log.New(io.Discard))

	err := server.Start()
	require.Nil(t, err, "server start err")

	err = server.Close()
	require.Nil(t, err, "server close err")

	tassert.True(t, server.inShutdown.Load(), "server should be marked as closed")
	clListenerCloseErr := server.clListener.Close()
	tassert.ErrorIs(t, clListenerCloseErr, net.ErrClosed, "client listener should be closed")

	serverCloseErr := server.Close()
	tassert.ErrorIs(t, serverCloseErr, ErrServerShutdown, "should return ErrServerShutdown after calling close again")
}

func Test_ServerAcceptsConns(t *testing.T) {
	addr := "127.0.0.1:9000"
	serverConfig := ServerConfig{
		ClientListenAddr: addr,
	}
	server := NewServer(&serverConfig, log.New(io.Discard))

	err := server.Start()
	require.Nil(t, err, "server start err")

	defer func() {
		err := server.Close()
		require.Nil(t, err, "server close err")
	}()

	conn, err := net.Dial("tcp", addr)
	require.Nil(t, err, "connection dial err")

	err = conn.Close()
	tassert.Nil(t, err, "conn close err")
}
