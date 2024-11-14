package coconut

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tifye/Coconut/testutil"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"
)

func Test_ServerClientSessions(t *testing.T) {
	signer, err := ssh.ParsePrivateKey(testutil.GetBytes(t, "../testdata/mino"))
	require.Nil(t, err)

	type suite struct {
		server  *Server
		clients []*Client
	}
	setup := func(t *testing.T, numClients int) (*suite, func()) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.Nil(t, err)
		server, err := NewServer(
			log.New(io.Discard),
			WithClientListenAddr("127.0.0.1:0"),
			WithClientListenFunc(func(network, address string) (net.Listener, error) {
				return ln, nil
			}),
			WithHostKey(signer),
			WithNoClientAuth(),
		)
		require.Nil(t, err)

		err = server.Start(context.Background())
		require.Nil(t, err)

		clients := make([]*Client, 0, numClients)
		wg := sync.WaitGroup{}
		wg.Add(numClients)
		for i := range numClients {
			client, err := NewClient(log.New(io.Discard), ln.Addr().String(), WithHostKeyCallback(ssh.InsecureIgnoreHostKey()))
			require.Nil(t, err)

			clients = append(clients, client)
			go func(idx int) {
				defer wg.Done()
				err := client.Start()
				require.Nil(t, err)
			}(i)
		}
		wg.Wait()

		return &suite{
				server:  server,
				clients: clients,
			}, func() {
				err := server.Close(context.Background())
				require.Nil(t, err)

				eg := errgroup.Group{}
				for _, c := range clients {
					eg.Go(c.Close)
				}
				err = eg.Wait()
				require.Nil(t, err)
			}
	}

	t.Run("nothing should happen", func(t *testing.T) {
		_, teardown := setup(t, 10)
		defer teardown()
	})

	t.Run("session created for each client", func(t *testing.T) {
		suite, teardown := setup(t, 10)
		defer teardown()

		tassert.True(t, len(suite.server.Sessions()) == len(suite.clients), "number of sessions should match number of clients")
	})
}

func Test_ServerClosesUnderlyNetworkIO(t *testing.T) {
	signer, err := ssh.ParsePrivateKey(testutil.GetBytes(t, "../testdata/mino"))
	require.Nil(t, err)

	addr := "127.0.0.1:9000"
	server, err := NewServer(
		log.New(io.Discard),
		WithClientListenAddr(addr),
		WithNoClientAuth(),
		WithHostKey(signer),
	)
	require.Nil(t, err, "server create err")

	client, err := NewClient(
		log.New(io.Discard),
		addr,
	)
	require.Nil(t, err, "client create err")

	err = server.Start(context.Background())
	require.Nil(t, err, "server start err")

	err = client.Start()
	require.Nil(t, err, "client start err")

	defer func() {
		err = client.Close()
		require.Nil(t, err, "client close err")
	}()

	var proxyShutdownCalled bool
	server.proxy.RegisterOnShutdown(func() {
		proxyShutdownCalled = true
	})

	err = server.Close(context.Background())
	require.Nil(t, err, "server close err")

	tassert.True(t, server.inShutdown.Load(), "server should be marked as closed")
	clListenerCloseErr := server.clListener.Close()
	tassert.ErrorIs(t, clListenerCloseErr, net.ErrClosed, "client listener should be closed")

	serverCloseErr := server.Close(context.Background())
	tassert.ErrorIs(t, serverCloseErr, ErrServerShutdown, "should return ErrServerShutdown after calling close again")

	tassert.True(t, proxyShutdownCalled, "proxy shutdown should be called")

	for _, sesh := range server.sessions {
		tassert.True(t, sesh.closed.Load(), "session should be closed")
	}
}

func Test_ServerAcceptsConns(t *testing.T) {
	addr := "127.0.0.1:9000"
	server, err := NewServer(
		log.New(io.Discard),
		WithClientListenAddr(addr),
	)
	require.Nil(t, err, "server create err")

	err = server.Start(context.Background())
	require.Nil(t, err, "server start err")

	defer func() {
		err := server.Close(context.Background())
		require.Nil(t, err, "server close err")
	}()

	conn, err := net.Dial("tcp", addr)
	require.Nil(t, err, "connection dial err")

	err = conn.Close()
	tassert.Nil(t, err, "conn close err")
}

func Test_ServerStartErr(t *testing.T) {
	addr := "127.0.0.1:9000"

	// block server from using addr
	testLn, err := net.Listen("tcp", addr)
	require.Nil(t, err)
	defer testLn.Close()

	server, err := NewServer(
		log.New(io.Discard),
		WithClientListenAddr(addr),
	)
	require.Nil(t, err, "server create err")

	err = server.Start(context.Background())
	tassert.NotNil(t, err, "server start should err")
}

func Test_ServerDoneWithProxyErr(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:9000")
	require.Nil(t, err)

	server, err := NewServer(
		log.New(io.Discard),
		WithClientListenAddr("127.0.0.1:0"),
		WithProxyListenFunc(func(network, address string) (net.Listener, error) {
			return ln, nil
		}),
	)
	require.Nil(t, err)

	err = server.Start(context.Background())
	tassert.Nil(t, err)

	_ = ln.Close()

	<-server.Done()
	tassert.ErrorIs(t, server.Err(), net.ErrClosed)
}

func Test_ServerDoneWithNoErr(t *testing.T) {
	setup := func(t *testing.T) *Server {
		server, err := NewServer(
			log.New(io.Discard),
			WithClientListenAddr("127.0.0.1:0"),
			WithProxyAddr("127.0.0.1:0"),
		)
		require.NoError(t, err)

		err = server.Start(context.Background())
		require.NoError(t, err)
		return server
	}

	t.Run("call Done before Close", func(t *testing.T) {
		server := setup(t)
		go func() {
			time.Sleep(10 * time.Millisecond)
			err := server.Close(context.Background())
			tassert.NoError(t, err)
		}()
		<-server.Done()
		tassert.NoError(t, server.Err())
	})

	t.Run("call Close before Done", func(t *testing.T) {
		server := setup(t)

		err := server.Close(context.Background())
		require.NoError(t, err)

		<-server.Done()
		tassert.NoError(t, server.Err())
	})
}
