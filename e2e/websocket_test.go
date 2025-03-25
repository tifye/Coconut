package e2e

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gorilla/websocket"
	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tifye/Coconut/coconut"
	"github.com/tifye/Coconut/testutil"
	"golang.org/x/crypto/ssh"
)

func setupWebsocketServer(t *testing.T) *http.Server {
	t.Helper()

	upgrader := websocket.Upgrader{
		// Allow connections from any origin for testing
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatal("failed to upgrade")
		}
		defer conn.Close()

		<-r.Context().Done()
	})
	return &http.Server{
		Handler: mux,
	}
}

func Test_WebsocketConnection(t *testing.T) {
	type wsSuite struct {
		server  *coconut.Server
		client  *coconut.Client
		backend *http.Server
	}
	setup := func(t *testing.T) (*wsSuite, func(*testing.T)) {
		backend := setupWebsocketServer(t)
		bln, err := net.Listen("tcp", "127.0.0.1:5123")
		require.NoError(t, err)

		go func() {
			err := backend.Serve(bln)
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				fmt.Println(err)
			}
		}()

		signer, err := ssh.ParsePrivateKey(testutil.GetBytes(t, "../testdata/mino"))
		require.NoError(t, err)

		cln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		pln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		server, err := coconut.NewServer(
			log.New(io.Discard),
			coconut.WithProxyAddr(pln.Addr().String()),
			coconut.WithProxyListenFunc(func(network, address string) (net.Listener, error) {
				return pln, err
			}),
			coconut.WithClientListenAddr(cln.Addr().String()),
			coconut.WithClientListenFunc(func(network, address string) (net.Listener, error) {
				return cln, err
			}),
			coconut.WithHostKey(signer),
			coconut.WithNoClientAuth(),
			coconut.WithNoDiscovery(),
		)
		require.NoError(t, err)

		client, err := coconut.NewClient(
			log.New(io.Discard),
			cln.Addr().String(),
			"127.0.0.1:5123",
		)
		require.NoError(t, err)

		err = server.Start(context.Background())
		require.NoError(t, err)

		err = client.Start()
		require.NoError(t, err)

		return &wsSuite{
				server:  server,
				client:  client,
				backend: backend,
			}, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				err := backend.Shutdown(ctx)
				tassert.NoError(t, err)

				_ = client.Close()

				err = server.Close(ctx)
				tassert.NoError(t, err)
			}
	}

	suite, teardown := setup(t)
	defer teardown(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	url := fmt.Sprintf("ws://%s/ws", suite.server.ProxyAddr())
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	tassert.NoError(t, err)
	defer conn.Close()
}
