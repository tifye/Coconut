package coconut

import (
	"io"
	"net"
	"testing"

	"github.com/charmbracelet/log"
	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func Test_Session(t *testing.T) {
	signer, err := ssh.ParsePrivateKey(getBytes(t, "../testdata/mino"))
	require.Nil(t, err)
	setup := func(t *testing.T) (*Client, *session, func()) {
		sshConfig := ssh.ServerConfig{NoClientAuth: true}
		sshConfig.AddHostKey(signer)

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.Nil(t, err)

		client, err := NewClient(log.New(io.Discard), ln.Addr().String())
		require.Nil(t, err)

		clientReady := make(chan struct{})
		go func() {
			err = client.Start()
			require.Nil(t, err)
			clientReady <- struct{}{}
		}()

		conn, err := ln.Accept()
		require.Nil(t, err)

		sshConn, chans, reqs, err := ssh.NewServerConn(conn, &sshConfig)
		require.Nil(t, err)

		session, err := newSession(conn, sshConn, chans, reqs)
		require.Nil(t, err)

		<-clientReady

		return client, session, func() {
			serr := session.Close()
			require.Nil(t, serr)

			cerr := client.Close()
			require.Nil(t, cerr)
		}
	}

	t.Run("nothing should happen", func(t *testing.T) {
		_, _, teardown := setup(t)
		defer teardown()
	})

	t.Run("session started and tunnel created", func(t *testing.T) {
		_, session, teardown := setup(t)
		defer teardown()

		err := session.Start()
		require.Nil(t, err)

		tassert.True(t, len(session.tunnels) > 0, "at least one tunnel created")
	})

	t.Run("session closes underlying connection", func(t *testing.T) {
		_, session, teardown := setup(t)
		defer teardown()

		err := session.Start()
		require.Nil(t, err)

		err = session.Close()
		require.Nil(t, err, "should close without error")

		err = session.conn.Close()
		tassert.ErrorIs(t, err, net.ErrClosed, "should return conn already closed")
	})

	t.Run("close before start should not error", func(t *testing.T) {
		_, session, teardown := setup(t)
		defer teardown()

		err = session.Close()
		require.Nil(t, err, "should close without error")

		err = session.conn.Close()
		tassert.ErrorIs(t, err, net.ErrClosed, "should return conn already closed")
	})

	t.Run("client close before session", func(t *testing.T) {
		client, session, _ := setup(t)

		err := session.Start()
		require.Nil(t, err)

		err = client.Close()
		require.Nil(t, err)

		err = session.Close()
		require.Nil(t, err)
	})
}
