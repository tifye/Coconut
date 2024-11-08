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
	ErrClientShutdown = errors.New("client closed")
)

type clientOptions struct {
	dialFunc        DialFunc
	hostKeyCallback ssh.HostKeyCallback
	user            string
	authMethod      ssh.AuthMethod
}

type ClientOption func(options *clientOptions) error

func WithDialFunc(f DialFunc) ClientOption {
	return func(options *clientOptions) error {
		if f == nil {
			return errors.New("nil dial func")
		}
		options.dialFunc = f
		return nil
	}
}

func WithUser(user string) ClientOption {
	return func(options *clientOptions) error {
		if user == "" {
			return errors.New("empty user")
		}
		options.user = user
		return nil
	}
}

func WithHostKeyCallback(cb ssh.HostKeyCallback) ClientOption {
	return func(options *clientOptions) error {
		if cb == nil {
			return errors.New("nil host hey callback")
		}
		options.hostKeyCallback = cb
		return nil
	}
}

func WithAuthMethod(method ssh.AuthMethod) ClientOption {
	return func(options *clientOptions) error {
		if method == nil {
			return errors.New("nil auth method")
		}
		options.authMethod = method
		return nil
	}
}

type Client struct {
	logger *log.Logger

	srvAddr  string
	dialFunc DialFunc
	conn     net.Conn

	mu         sync.Mutex
	inShutdown atomic.Bool

	sshConfig *ssh.ClientConfig
	sshConn   ssh.Conn
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

	clientDefaults(&opts)

	assert.Assert(opts.authMethod != nil, "nil auth method")
	assert.Assert(opts.dialFunc != nil, "nil dial func")
	assert.Assert(opts.hostKeyCallback != nil, "nil host key callback")
	assert.Assert(opts.user != "", "zero value user")

	sshConfig := &ssh.ClientConfig{
		User:            opts.user,
		Auth:            []ssh.AuthMethod{opts.authMethod},
		HostKeyCallback: opts.hostKeyCallback,
	}

	return &Client{
		srvAddr:    serverAddr,
		dialFunc:   opts.dialFunc,
		logger:     logger,
		mu:         sync.Mutex{},
		inShutdown: atomic.Bool{},
		sshConfig:  sshConfig,
	}, nil
}

func clientDefaults(opts *clientOptions) {
	if opts.authMethod == nil {
		opts.authMethod = ssh.Password("fruit-pie")
	}

	if opts.dialFunc == nil {
		opts.dialFunc = DefaultDialFunc
	}

	if opts.hostKeyCallback == nil {
		opts.hostKeyCallback = ssh.InsecureIgnoreHostKey()
	}

	if opts.user == "" {
		opts.user = "unkown"
	}

	assert.Assert(opts.authMethod != nil, "nil auth method")
	assert.Assert(opts.dialFunc != nil, "nil dial func")
	assert.Assert(opts.hostKeyCallback != nil, "nil host key callback")
	assert.Assert(opts.user != "", "zero value user")
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

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, c.srvAddr, c.sshConfig)
	if err != nil {
		return fmt.Errorf("new ssh client conn: %s", err)
	}
	c.sshConn = sshConn

	go ssh.DiscardRequests(reqs)
	go func() {
		for nc := range chans {
			nc.Reject(ssh.UnknownChannelType, "not implemented")
		}
	}()

	assert.Assert(c.sshConn != nil, "nil ssh conn")
	return nil
}

func (c *Client) Close() error {
	if c.inShutdown.Load() {
		return ErrClientShutdown
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.inShutdown.Store(true)

	assert.Assert(c.sshConn != nil, "nil network conn")
	err := c.sshConn.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
