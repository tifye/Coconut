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

type clientOptions struct {
	serverAddr string
	dialFunc   DialFunc
}

type ClientOption func(options *clientOptions) error

func WithServerAddr(addr string) ClientOption {
	assert.Assert(addr != "", "zero value address")
	return func(options *clientOptions) error {
		options.serverAddr = addr
		return nil
	}
}

func WithDialFunc(f DialFunc) ClientOption {
	assert.Assert(f != nil, "nil dial func")
	return func(options *clientOptions) error {
		options.dialFunc = f
		return nil
	}
}

type Client struct {
	logger     *log.Logger
	srvAddr    string
	dialFunc   DialFunc
	conn       net.Conn
	mu         sync.Mutex
	inShutdown atomic.Bool
}

func NewClient(logger *log.Logger, serverAddr string, options ...ClientOption) (*Client, error) {
	assert.Assert(serverAddr != "", "zero value server address")
	assert.Assert(logger != nil, "nil logger")

	var opts clientOptions
	for _, f := range options {
		err := f(&opts)
		if err != nil {
			return nil, err
		}
	}

	if opts.dialFunc == nil {
		opts.dialFunc = DefaultDialFunc
	}
	assert.Assert(opts.dialFunc != nil, "nil dial func")

	return &Client{
		srvAddr:    serverAddr,
		dialFunc:   opts.dialFunc,
		logger:     logger,
		mu:         sync.Mutex{},
		inShutdown: atomic.Bool{},
	}, nil
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
