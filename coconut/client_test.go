package coconut

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/charmbracelet/log"
	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tifye/Coconut/testutil"
	"golang.org/x/crypto/ssh"
)

func Test_ClientClosesUnderlyNetworkIO(t *testing.T) {
	signer, err := ssh.ParsePrivateKey(testutil.GetBytes(t, "../testdata/mino"))
	require.Nil(t, err)

	addr := "127.0.0.1:0"
	server, err := NewServer(
		log.New(io.Discard),
		WithClientListenAddr(addr),
		WithNoClientAuth(),
		WithHostKey(signer),
	)
	require.Nil(t, err, "server create err")

	err = server.Start(context.Background())
	require.Nil(t, err, "server start err")

	client, err := NewClient(
		log.New(io.Discard),
		server.clListener.Addr().String(),
		"",
	)
	require.Nil(t, err, "client create err")

	defer func() {
		err := server.Close(context.Background())
		require.Nil(t, err, "server close err")
	}()

	err = client.Start()
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
	signer, err := ssh.ParsePrivateKey(testutil.GetBytes(t, "../testdata/mino"))
	require.Nil(t, err)

	addr := "127.0.0.1:0"
	server, err := NewServer(
		log.New(io.Discard),
		WithClientListenAddr(addr),
		WithNoClientAuth(),
		WithHostKey(signer),
	)
	require.Nil(t, err, "server create err")

	err = server.Start(context.Background())
	require.Nil(t, err, "server start err")

	client, err := NewClient(
		log.New(io.Discard),
		server.clListener.Addr().String(),
		"",
	)
	require.Nil(t, err, "client create err")

	defer func() {
		err := server.Close(context.Background())
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
	addr := "127.0.0.1:0"
	client, err := NewClient(log.New(io.Discard), addr, "")
	require.Nil(t, err, "client create err")

	err = client.Start()
	require.NotNil(t, err, "client should err")
}
