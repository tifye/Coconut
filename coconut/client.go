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
	"golang.org/x/sync/errgroup"
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
	closeWg    *sync.WaitGroup

	sshConfig *ssh.ClientConfig
	sshConn   ssh.Conn
	tunnels   []*tunnel
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
		logger:     logger,
		srvAddr:    serverAddr,
		dialFunc:   opts.dialFunc,
		sshConfig:  sshConfig,
		mu:         sync.Mutex{},
		inShutdown: atomic.Bool{},
		closeWg:    &sync.WaitGroup{},
		tunnels:    make([]*tunnel, 0),
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

	assert.Assert(c.closeWg != nil, "nil wait group")
	c.closeWg.Add(2)
	go func() {
		defer c.closeWg.Done()
		ssh.DiscardRequests(reqs)
	}()
	go func() {
		defer c.closeWg.Done()
		c.processNewChannels(chans)
	}()

	assert.Assert(c.sshConn != nil, "nil ssh conn")
	return nil
}

func (c *Client) Close() (rerr error) {
	if c.inShutdown.Load() {
		return ErrClientShutdown
	}

	c.inShutdown.Store(true)

	assert.Assert(c.closeWg != nil, "nil wait group")
	assert.Assert(c.tunnels != nil, "nil tunnels")
	assert.Assert(c.sshConn != nil, "nil network conn")

	defer c.closeWg.Wait()
	defer func() {
		c.logger.Debug("closing ssh connection")
		err := c.sshConn.Close()
		if errors.Is(err, net.ErrClosed) || err == nil {
			return
		}
		if rerr == nil {
			rerr = err
		} else {
			rerr = errors.Join(rerr, err)
		}
	}()

	c.mu.Lock()
	teg := errgroup.Group{}
	for _, t := range c.tunnels {
		teg.Go(t.Close)
	}
	c.mu.Unlock()

	return teg.Wait()
}

func (c *Client) processNewChannels(chans <-chan ssh.NewChannel) {
	assert.Assert(chans != nil, "nil channel")
	for nc := range chans {
		if nc.ChannelType() != "tunnel" {
			c.logger.Debug("non-tunnel type new channel request")

			err := nc.Reject(ssh.UnknownChannelType, "only accepts tunnel type channels")
			if err != nil {
				c.logger.Error("err rejecting new channel request", "err", err)
			}

			return
		}

		sshChan, reqs, err := nc.Accept()
		if err != nil {
			c.logger.Error("err accepting new channel request", "err", err)
			return
		}
		assert.Assert(sshChan != nil, "nil ssh channel")
		assert.Assert(reqs != nil, "nil channel")
		tunnel := &tunnel{
			sshChan: sshChan,
			reqs:    reqs,
		}

		assert.Assert(c.tunnels != nil, "nil tunnels")
		c.mu.Lock()
		c.tunnels = append(c.tunnels, tunnel)
		c.mu.Unlock()
	}
}
