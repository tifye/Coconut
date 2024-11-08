package coconut

import (
	"errors"
	"net"
	"sync/atomic"

	"github.com/tifye/Coconut/assert"
	"golang.org/x/crypto/ssh"
)

type session struct {
	conn     net.Conn
	sshConn  *ssh.ServerConn
	newChans <-chan ssh.NewChannel
	reqs     <-chan *ssh.Request
	closed   atomic.Bool
}

func newSession(
	conn net.Conn,
	sshConn *ssh.ServerConn,
	newChans <-chan ssh.NewChannel,
	reqs <-chan *ssh.Request,
) (*session, error) {
	assert.Assert(conn != nil, "nil net conn")
	assert.Assert(sshConn.Conn != nil, "nil ssh conn")
	assert.Assert(newChans != nil, "nil new channels channel")
	assert.Assert(reqs != nil, "nil reqs channel")

	return &session{
		conn:     conn,
		sshConn:  sshConn,
		newChans: newChans,
		reqs:     reqs,
		closed:   atomic.Bool{},
	}, nil
}

func (s *session) Start() {
	for {
		select {
		case req, ok := <-s.reqs:
			if !ok {
				s.reqs = nil
				continue
			}
			if req.WantReply {
				req.Reply(false, nil)
			}
		case cha, ok := <-s.newChans:
			if !ok {
				s.newChans = nil
				continue
			}
			cha.Reject(ssh.ConnectionFailed, "not implemented")
		}
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
	return err
}
