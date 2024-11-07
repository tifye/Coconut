package coconut

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/log"
	"github.com/tifye/Coconut/assert"
)

var (
	ErrServerShutdown = errors.New("server closed")
)

type ServerConfig struct {
	ClientListenAddr string
	ClientListenFunc ListenFunc
}

type Server struct {
	clAddr       string
	clListenFunc ListenFunc
	clListener   net.Listener
	mu           sync.Mutex
	inShutdown   atomic.Bool
	logger       *log.Logger
}

func NewServer(config *ServerConfig, logger *log.Logger) *Server {
	assert.Assert(logger != nil, "nil logger")

	s := &Server{
		mu:         sync.Mutex{},
		inShutdown: atomic.Bool{},
		logger:     logger,
	}

	if config.ClientListenFunc != nil {
		s.clListenFunc = config.ClientListenFunc
	} else {
		s.clListenFunc = DefaultListen
	}

	if config.ClientListenAddr != "" {
		s.clAddr = config.ClientListenAddr
	} else {
		s.clAddr = ":9000"
	}

	return s
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
