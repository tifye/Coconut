package coconut

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/tifye/Coconut/assert"
	"golang.org/x/crypto/ssh"
)

type session struct {
	conn     net.Conn
	sshConn  io.Closer
	newChans <-chan ssh.NewChannel
	reqs     <-chan *ssh.Request
	closed   atomic.Bool
	closeWg  *sync.WaitGroup
}

func newSession(
	conn net.Conn,
	sshConn io.Closer,
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
	}, nil
}

func (s *session) Start() {
	s.closeWg.Add(1)
	go func() {
		defer s.closeWg.Done()
		ssh.DiscardRequests(s.reqs)
	}()

	for cha := range s.newChans {
		cha.Reject(ssh.UnknownChannelType, "not implemented")
	}
}

func (s *session) Close() error {
	if s.closed.Load() {
		return nil
	}
	s.closed.Store(true)
	err := s.sshConn.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	if err != nil {
		return err
	}

	s.closeWg.Wait()
	return nil
}
