package coconut

import (
	"io"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func Test_Session(t *testing.T) {
	signer, err := ssh.ParsePrivateKey(getBytes(t, "../testdata/mino"))
	require.Nil(t, err)

	setup := func(t *testing.T) (*Server, *Client, func()) {
		addr := "127.0.0.1:9000"
		server, err := NewServer(
			log.New(io.Discard),
			WithClientListenAddr(addr),
			WithNoClientAuth(true),
			WithHostKey(signer),
		)
		require.Nil(t, err)

		client, err := NewClient(
			log.New(io.Discard),
			addr,
		)
		require.Nil(t, err)

		err = server.Start()
		require.Nil(t, err)

		err = client.Start()
		require.Nil(t, err)
		return server, client, func() {
			cerr := client.Close()
			serr := server.Close()

			require.Nil(t, cerr)
			require.Nil(t, serr)
		}
	}

	t.Run("session create after client conneciton", func(t *testing.T) {
		server, _, teardown := setup(t)
		defer teardown()

		seshKey, ok := pickSession(t, server.sessions)
		require.True(t, ok, "should get session key")

		_, ok = server.sessions[seshKey]
		require.True(t, ok, "session should exist in map")
	})
}

func pickSession(t testing.TB, sessions map[string]*session) (string, bool) {
	t.Helper()
	for k := range sessions {
		return k, true
	}
	return "", false
}
