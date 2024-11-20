package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tifye/Coconut/coconut"
	"github.com/tifye/Coconut/testutil"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"
)

type suite struct {
	server   *coconut.Server
	clients  []*coconut.Client
	backends []*backend
}

func newSuite(t *testing.T, numClients int) *suite {
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
	)
	require.NoError(t, err)

	backends := make([]*backend, 0)
	backend := newBackend(t, "backend-1", "127.0.0.1:0")
	backends = append(backends, backend)

	clients := make([]*coconut.Client, 0, numClients)
	for range numClients {
		client, err := coconut.NewClient(
			log.New(io.Discard),
			cln.Addr().String(),
			backend.Addr,
			coconut.WithHostKeyCallback(ssh.InsecureIgnoreHostKey()),
		)
		require.Nil(t, err)

		clients = append(clients, client)
	}

	return &suite{
		server:   server,
		clients:  clients,
		backends: backends,
	}
}

func (s *suite) setup(t *testing.T) {
	err := s.server.Start(context.Background())
	require.NoError(t, err)

	for _, b := range s.backends {
		go func() {
			err := b.Serve(b.ln)
			if !errors.Is(err, http.ErrServerClosed) {
				tassert.NoError(t, err)
			}
		}()
	}

	numClients := len(s.clients)
	wg := sync.WaitGroup{}
	wg.Add(numClients)
	for _, c := range s.clients {
		go func(c *coconut.Client) {
			defer wg.Done()
			err := c.Start()
			require.NoError(t, err)
		}(c)
	}
	wg.Wait()
}

func (s *suite) teardown(t *testing.T) {
	err := s.server.Close(context.Background())
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eg := errgroup.Group{}
	for _, c := range s.clients {
		eg.Go(c.Close)
	}
	for _, b := range s.backends {
		eg.Go(func() error {
			return b.Shutdown(ctx)
		})
	}
	err = eg.Wait()
	require.NoError(t, err)
}

func Test_NothingShouldHappen(t *testing.T) {
	suite := newSuite(t, 4)
	suite.setup(t)
	defer suite.teardown(t)
}

func Test_BasicRequest(t *testing.T) {
	suite := newSuite(t, 1)
	suite.setup(t)
	defer suite.teardown(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	requestBody := `{"key":"value"}`
	requestHeaders := map[string]string{
		"Content-Type":  "application/json",
		"Custom-Header": "CustomValue",
	}
	url := fmt.Sprintf("http://%s/echo", suite.server.ProxyAddr())
	req, err := http.NewRequest(http.MethodGet, url, bytes.NewBufferString(requestBody))
	require.NoError(t, err)
	for key, value := range requestHeaders {
		req.Header.Set(key, value)
	}
	req = req.WithContext(ctx)

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	tassert.Equal(t, http.StatusOK, resp.StatusCode, "status code should be equal")

	var body map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err, "json body decode")

	tassert.Equal(t, requestBody, body["body"])

	responseHeaders, ok := body["headers"].(map[string]interface{})
	require.True(t, ok, "expected headers in response")
	for k, v := range requestHeaders {
		tassert.Equalf(t, v, responseHeaders[k], "expected header %q to be %q, got %q", k, v, responseHeaders[k])
	}
}
