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

// Session represents a clients entire connection with the server
type Session struct {
	logger *log.Logger

	conn      net.Conn
	sshConn   *ssh.ServerConn
	subdomain string

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
	sshConn *ssh.ServerConn,
	subdomain string,
	newChans <-chan ssh.NewChannel,
	reqs <-chan *ssh.Request,
	logger *log.Logger,
) (*Session, error) {
	assert.Assert(conn != nil, "nil net conn")
	assert.Assert(sshConn != nil, "nil ssh conn")
	assert.Assert(newChans != nil, "nil new channels channel")
	assert.Assert(reqs != nil, "nil reqs channel")
	assert.Assert(logger != nil, "nil logger")
	assert.Assert(subdomain != "", "zero value subdomain")

	return &Session{
		logger:    logger,
		conn:      conn,
		sshConn:   sshConn,
		subdomain: subdomain,
		newChans:  newChans,
		reqs:      reqs,
		closed:    atomic.Bool{},
		closeWg:   &sync.WaitGroup{},
		mu:        sync.Mutex{},
		tunnels:   make([]*sessionTunnel, 0),
		trch:      make(chan *tunnelRequest),
	}, nil
}

func (s *Session) Start() error {
	assert.Assert(s.newChans != nil, "nil channel")
	assert.Assert(s.tunnels != nil, "nil tunnels")

	s.closeWg.Add(1)
	go func() {
		defer s.closeWg.Done()
		for r := range s.reqs {
			if r.Type == "subdomain" {
				assert.Assert(r.WantReply, "should want reply")
				r.Reply(true, []byte(s.subdomain))
				continue
			}
			if r.WantReply {
				r.Reply(false, nil)
			}
		}
	}()

	for i := range 1 {
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
			logger: s.logger.WithPrefix(fmt.Sprintf("tunnel-%d", i)),
		}
		go tunnel.listen(s.trch)

		s.mu.Lock()
		s.tunnels = append(s.tunnels, tunnel)
		s.mu.Unlock()
	}

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
	}
}

type sessionTunnel struct {
	tunnel
	logger *log.Logger
}

func (st *sessionTunnel) listen(trch <-chan *tunnelRequest) {
	for tr := range trch {
		ctx := tr.req.Context()
		rlogger := ctx.Value(reqLoggerCtxKey).(*log.Logger)
		rlogger.Debug("performing round trip", "tunnel", st.logger.GetPrefix())

		resp, err := st.RoundTrip(tr.req)
		if err != nil {
			tr.errch <- err
			continue
		}

		resp.Body = &signalClose{done: tr.done, ReadCloser: resp.Body}

		select {
		case tr.respch <- resp:
			<-tr.done
		case <-ctx.Done():
			if resp != nil {
				resp.Body.Close()
			}
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
