package coconut

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/log"
	"github.com/tifye/Coconut/assert"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"
)

type sessionSSHConn interface {
	io.Closer
	OpenChannel(name string, data []byte) (ssh.Channel, <-chan *ssh.Request, error)
}

// Session represents a clients entire connection with the server
type Session struct {
	logger *log.Logger

	conn    net.Conn
	sshConn sessionSSHConn

	newChans <-chan ssh.NewChannel
	reqs     <-chan *ssh.Request

	closed  atomic.Bool
	closeWg *sync.WaitGroup

	mu      sync.Mutex
	tunnels []*sessionTunnel
	trch    chan *tunnelRequest
}

func newSession(
	conn net.Conn,
	sshConn sessionSSHConn,
	newChans <-chan ssh.NewChannel,
	reqs <-chan *ssh.Request,
	logger *log.Logger,
) (*Session, error) {
	assert.Assert(conn != nil, "nil net conn")
	assert.Assert(sshConn != nil, "nil ssh conn")
	assert.Assert(newChans != nil, "nil new channels channel")
	assert.Assert(reqs != nil, "nil reqs channel")
	assert.Assert(logger != nil, "nil logger")

	return &Session{
		logger:   logger,
		conn:     conn,
		sshConn:  sshConn,
		newChans: newChans,
		reqs:     reqs,
		closed:   atomic.Bool{},
		closeWg:  &sync.WaitGroup{},
		mu:       sync.Mutex{},
		tunnels:  make([]*sessionTunnel, 0),
		trch:     make(chan *tunnelRequest),
	}, nil
}

func (s *Session) Start() error {
	assert.Assert(s.newChans != nil, "nil channel")
	assert.Assert(s.tunnels != nil, "nil tunnels")

	s.closeWg.Add(1)
	go func() {
		defer s.closeWg.Done()
		ssh.DiscardRequests(s.reqs)
	}()

	sshChan, reqs, err := s.sshConn.OpenChannel("tunnel", nil)
	if err != nil {
		return fmt.Errorf("tunnel open: %w", err)
	}

	assert.Assert(sshChan != nil, "nil ssh channel")
	assert.Assert(reqs != nil, "nil channel")
	assert.Assert(s.trch != nil, "nil tunnel request channel")
	tunnel := &sessionTunnel{
		tunnel: tunnel{
			sshChan: sshChan,
			reqs:    reqs,
		},
		logger: s.logger.WithPrefix("tunnel-1"),
	}
	go tunnel.listen(s.trch)

	s.mu.Lock()
	s.tunnels = append(s.tunnels, tunnel)
	s.mu.Unlock()

	s.closeWg.Add(1)
	go func() {
		defer s.closeWg.Done()
		for cha := range s.newChans {
			cha.Reject(ssh.UnknownChannelType, "not implemented")
		}
	}()

	return nil
}

func (s *Session) Close() (rerr error) {
	if s.closed.Load() {
		return nil
	}
	s.closed.Store(true)

	s.mu.Lock()
	defer s.mu.Unlock()

	assert.Assert(s.closeWg != nil, "nil wait group")
	assert.Assert(s.sshConn != nil, "nil ssh conn")
	assert.Assert(s.tunnels != nil, "nil tunnels")
	assert.Assert(s.trch != nil, "nil tunnel request channel")

	close(s.trch)

	defer s.closeWg.Wait()
	defer func() {
		err := s.sshConn.Close()
		if errors.Is(err, net.ErrClosed) || err == nil {
			return
		}
		if rerr == nil {
			rerr = err
		} else {
			rerr = errors.Join(rerr, err)
		}
	}()

	teg := errgroup.Group{}
	for _, t := range s.tunnels {
		teg.Go(t.Close)
	}
	return teg.Wait()
}

type tunnelRequest struct {
	req    *http.Request
	respch chan *http.Response
	errch  chan error
	done   chan struct{}
}

func (s *Session) RoundTrip(r *http.Request) (*http.Response, error) {
	tr := &tunnelRequest{
		req:    r,
		respch: make(chan *http.Response),
		errch:  make(chan error),
		done:   make(chan struct{}, 1),
	}

	ctx := r.Context()
	select {
	case s.trch <- tr:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	select {
	case resp := <-tr.respch:
		return resp, nil
	case err := <-tr.errch:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type sessionTunnel struct {
	tunnel
	logger *log.Logger
}

func (st *sessionTunnel) listen(trch <-chan *tunnelRequest) {
	for tr := range trch {
		st.logger.Debug("performing round trip", "host", tr.req.URL.Host, "path", tr.req.URL.Path)
		resp, err := st.RoundTrip(tr.req)
		if err != nil {
			tr.errch <- err
			continue
		}

		resp.Body = &signalClose{done: tr.done, ReadCloser: resp.Body}

		ctx := tr.req.Context()
		select {
		case tr.respch <- resp:
		case <-ctx.Done():
			tr.errch <- ctx.Err()
		}

		select {
		case <-tr.done:
		case <-ctx.Done():
			tr.errch <- ctx.Err()
		}
	}
}

func (st *sessionTunnel) RoundTrip(r *http.Request) (*http.Response, error) {
	err := r.Write(st.sshChan)
	if err != nil {
		return nil, fmt.Errorf("req write: %w", err)
	}

	rr := bufio.NewReader(st.sshChan)
	resp, err := http.ReadResponse(rr, r)
	if err != nil {
		return nil, fmt.Errorf("resp read: %w", err)
	}

	return resp, nil
}

type signalClose struct {
	io.ReadCloser
	done chan<- struct{}
}

func (sc *signalClose) Close() error {
	defer func() {
		sc.done <- struct{}{}
	}()
	return sc.ReadCloser.Close()
}
