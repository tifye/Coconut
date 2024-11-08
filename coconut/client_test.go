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

func Test_ClientClosesUnderlyNetworkIO(t *testing.T) {
	addr := ":9000"
	clientConfig := ClientConfig{
		ServerAddr: addr,
		DialFunc: func(network, address string) (net.Conn, error) {
			return test.NewMockNetConn(network, address), nil
		},
	}
	client := NewClient(clientConfig, log.New(io.Discard))

	err := client.Start()
	require.Nil(t, err, "client start err")

	err = client.Close()
	require.Nil(t, err, "client close err")

	tassert.True(t, client.inShutdown.Load(), "client should be marked as closed")
	connCloseErr := client.conn.Close()
	tassert.ErrorIs(t, connCloseErr, net.ErrClosed, "network conn should be closed")

	clientCloseErr := client.Close()
	tassert.ErrorIs(t, clientCloseErr, ErrClientShutdown, "should return ErrClientShutdown after calling close again")
}

func Test_ClientOpensConnToServer(t *testing.T) {
	addr := "127.0.0.1:9000"
	serverConfig := ServerConfig{
		ClientListenAddr: addr,
	}
	server := NewServer(&serverConfig, log.New(io.Discard))

	clientConfig := ClientConfig{
		ServerAddr: addr,
	}
	client := NewClient(clientConfig, log.New(io.Discard))

	err := server.Start()
	require.Nil(t, err, "server start err")

	defer func() {
		err := server.Close()
		require.Nil(t, err, "server close err")
	}()

	err = client.Start()
	require.Nil(t, err, "client start err")

	defer func() {
		err := client.Close()
		require.Nil(t, err, "client close err")
	}()
}

func Test_ClientErrsOnStartWithNoServer(t *testing.T) {
	addr := "127.0.0.1:9000"
	clientConfig := ClientConfig{
		ServerAddr: addr,
	}
	client := NewClient(clientConfig, log.New(io.Discard))

	err := client.Start()
	require.NotNil(t, err, "client should err")
}
