package coconut

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/tifye/Coconut/assert"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"
)

type sessionSSHConn interface {
	io.Closer
	OpenChannel(name string, data []byte) (ssh.Channel, <-chan *ssh.Request, error)
}

// session represents a clients entire connection with the server
type session struct {
	conn    net.Conn
	sshConn sessionSSHConn

	newChans <-chan ssh.NewChannel
	reqs     <-chan *ssh.Request

	closed  atomic.Bool
	closeWg *sync.WaitGroup

	mu      sync.Mutex
	tunnels []*tunnel
}

func newSession(
	conn net.Conn,
	sshConn sessionSSHConn,
	newChans <-chan ssh.NewChannel,
	reqs <-chan *ssh.Request,
) (*session, error) {
	assert.Assert(conn != nil, "nil net conn")
	assert.Assert(sshConn != nil, "nil ssh conn")
	assert.Assert(newChans != nil, "nil new channels channel")
	assert.Assert(reqs != nil, "nil reqs channel")

	return &session{
		conn:     conn,
		sshConn:  sshConn,
		newChans: newChans,
		reqs:     reqs,
		closed:   atomic.Bool{},
		closeWg:  &sync.WaitGroup{},
		mu:       sync.Mutex{},
		tunnels:  make([]*tunnel, 0),
	}, nil
}

func (s *session) Start() error {
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
	tunnel := &tunnel{
		sshChan: sshChan,
		reqs:    reqs,
	}

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

func (s *session) Close() (rerr error) {
	if s.closed.Load() {
		return nil
	}
	s.closed.Store(true)

	s.mu.Lock()
	defer s.mu.Unlock()

	assert.Assert(s.closeWg != nil, "nil wait group")
	assert.Assert(s.sshConn != nil, "nil ssh conn")
	assert.Assert(s.tunnels != nil, "nil tunnels")

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
