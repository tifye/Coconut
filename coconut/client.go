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
	ErrClientShutdown = errors.New("client closed")
)

type ClientConfig struct {
	ServerAddr string
	DialFunc   DialFunc
}

type Client struct {
	logger     *log.Logger
	srvAddr    string
	dialFunc   DialFunc
	conn       net.Conn
	mu         sync.Mutex
	inShutdown atomic.Bool
}

func NewClient(config ClientConfig, logger *log.Logger) *Client {
	if config.DialFunc == nil {
		config.DialFunc = DefaultDialFunc
	}

	return &Client{
		srvAddr:    config.ServerAddr,
		dialFunc:   config.DialFunc,
		logger:     logger,
		mu:         sync.Mutex{},
		inShutdown: atomic.Bool{},
	}
}

// DialFunc is a function use to open a network connection
type DialFunc func(network string, address string) (net.Conn, error)

var DefaultDialFunc DialFunc = net.Dial

func (c *Client) Start() error {
	assert.Assert(c.srvAddr != "", "zero value server address")
	assert.Assert(c.dialFunc != nil, "nil dial function")
	conn, err := c.dialFunc("tcp", c.srvAddr)
	if err != nil {
		return fmt.Errorf("dial func: %w", err)
	}
	c.conn = conn

	assert.Assert(conn != nil, "nil network conn")

	c.logger.Debug("connection to server established", "raddr", conn.RemoteAddr(), "laddr", conn.LocalAddr())

	return nil
}

func (c *Client) Close() error {
	if c.inShutdown.Load() {
		return ErrClientShutdown
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.inShutdown.Store(true)

	assert.Assert(c.conn != nil, "nil network conn")
	return c.conn.Close()
}
