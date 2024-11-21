package coconut

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/charmbracelet/log"
	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tifye/Coconut/testutil"
	"golang.org/x/crypto/ssh"
)

func Test_Session(t *testing.T) {
	signer, err := ssh.ParsePrivateKey(testutil.GetBytes(t, "../testdata/mino"))
	require.Nil(t, err)
	setup := func(t *testing.T) (*Client, *Session, func()) {
		sshConfig := ssh.ServerConfig{NoClientAuth: true}
		sshConfig.AddHostKey(signer)

		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.Nil(t, err)

		client, err := NewClient(log.New(io.Discard), ln.Addr().String(), "")
		require.Nil(t, err)

		clientReady := make(chan struct{})
		go func() {
			err := client.Start()
			require.Nil(t, err)
			clientReady <- struct{}{}
		}()

		conn, err := ln.Accept()
		require.Nil(t, err)

		sshConn, chans, reqs, err := ssh.NewServerConn(conn, &sshConfig)
		require.Nil(t, err)

		session, err := newSession(conn, sshConn, chans, reqs, log.New(io.Discard))
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

type mockReadWriteCloser struct {
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
}

func (m *mockReadWriteCloser) Read(p []byte) (int, error) {
	return m.readBuf.Read(p)
}

func (m *mockReadWriteCloser) Write(p []byte) (int, error) {
	return m.writeBuf.Write(p)
}

func (m *mockReadWriteCloser) Close() error { return nil }

func Test_SessionTunnelRoundTrip(t *testing.T) {
	tests := []struct {
		name           string
		request        *http.Request
		mockResponse   string
		expectedError  string
		expectedStatus int
	}{
		{
			name: "successful GET request",
			request: func() *http.Request {
				req, _ := http.NewRequest("GET", "http://example.com/test", nil)
				return req
			}(),
			mockResponse: "HTTP/1.1 200 OK\r\n" +
				"Content-Length: 2\r\n" +
				"\r\n" +
				"ok",
			expectedStatus: 200,
		},
		{
			name: "successful POST request",
			request: func() *http.Request {
				body := bytes.NewBufferString("test-body")
				req, _ := http.NewRequest("POST", "http://example.com/test", body)
				req.Header.Set("Content-Type", "text/plain")
				return req
			}(),
			mockResponse: "HTTP/1.1 201 Created\r\n" +
				"Content-Length: 7\r\n" +
				"\r\n" +
				"created",
			expectedStatus: 201,
		},
		{
			name: "malformed response",
			request: func() *http.Request {
				req, _ := http.NewRequest("GET", "http://example.com/test", nil)
				return req
			}(),
			mockResponse:  "invalid-response",
			expectedError: "resp read:",
		},
	}

	for _, tt := range tests {
		mockRWC := &mockReadWriteCloser{
			readBuf:  bytes.NewBuffer(nil),
			writeBuf: bytes.NewBuffer(nil),
		}
		st := &sessionTunnel{
			tunnel: tunnel{
				sshChan: mockRWC,
			},
		}

		mockRWC.readBuf.Write([]byte(tt.mockResponse))

		resp, err := st.RoundTrip(tt.request)
		if tt.expectedError != "" {
			tassert.Contains(t, err.Error(), "malformed HTTP response")
			continue
		}
		require.NoError(t, err)
		require.NotNil(t, resp)
		tassert.Equal(t, tt.expectedStatus, resp.StatusCode)

		writtenReq := mockRWC.writeBuf.String()
		tassert.Contains(t, writtenReq, tt.request.Method)
		tassert.Contains(t, writtenReq, tt.request.URL.Path)

		if tt.request.Method == "POST" {
			body, _ := io.ReadAll(resp.Body)
			tassert.Equal(t, "created", string(body))
		}
	}
}
