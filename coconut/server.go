package coconut

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/log"
	"github.com/tifye/Coconut/assert"
	"golang.org/x/crypto/ssh"
)

var (
	ErrServerShutdown = errors.New("server closed")
)

type serverOptions struct {
	sshSigner        ssh.Signer
	clientListenAddr string
	clientListenFunc ListenFunc
}

type ServerOption func(options *serverOptions) error

func WithSigner(signer ssh.Signer) ServerOption {
	assert.Assert(signer != nil, "nil signer")
	return func(options *serverOptions) error {
		options.sshSigner = signer
		return nil
	}
}

func WithClientListenAddr(addr string) ServerOption {
	assert.Assert(addr != "", "zero value address")
	return func(options *serverOptions) error {
		options.clientListenAddr = addr
		return nil
	}
}

func WithClientListenFunc(f ListenFunc) ServerOption {
	assert.Assert(f != nil, "nil client listen func")
	return func(options *serverOptions) error {
		options.clientListenFunc = f
		return nil
	}
}

type Server struct {
	clAddr       string
	clListenFunc ListenFunc
	clListener   net.Listener
	mu           sync.Mutex
	inShutdown   atomic.Bool
	logger       *log.Logger
}

func NewServer(logger *log.Logger, options ...ServerOption) (*Server, error) {
	assert.Assert(logger != nil, "nil logger")

	var opts serverOptions
	for _, f := range options {
		err := f(&opts)
		if err != nil {
			return nil, err
		}
	}

	if opts.clientListenAddr == "" {
		opts.clientListenAddr = ":9000"
	}

	if opts.clientListenFunc == nil {
		opts.clientListenFunc = DefaultListen
	}

	return &Server{
		mu:           sync.Mutex{},
		inShutdown:   atomic.Bool{},
		logger:       logger,
		clAddr:       opts.clientListenAddr,
		clListenFunc: opts.clientListenFunc,
	}, nil
}

// ListenFunc is a function used to create a net.Listener
type ListenFunc func(network string, address string) (net.Listener, error)

// DefaultListen is the default ListenFunc used to create net.Listener
var DefaultListen ListenFunc = net.Listen

func (s *Server) Start() error {
	assert.Assert(s.clAddr != "", "zero value client listener address")
	assert.Assert(s.clListenFunc != nil, "nil client listen func")
	ln, err := s.clListenFunc("tcp", s.clAddr)
	if err != nil {
		return fmt.Errorf("client ListenFunc: %w", err)
	}
	s.clListener = ln
	s.logger.Debug("client listener started", "addr", s.clAddr)

	assert.Assert(ln != nil, "nil client listener")

	return nil
}

func (s *Server) Close() error {
	if s.inShutdown.Load() {
		return ErrServerShutdown
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.inShutdown.Store(true)

	assert.Assert(s.clListener != nil, "nil client listener")
	return s.clListener.Close()
}
