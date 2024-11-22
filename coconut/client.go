package coconut

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

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
	bannerCallback  ssh.BannerCallback
}

type ClientOption func(options *clientOptions) error

func WithBannerCallback(f ssh.BannerCallback) ClientOption {
	return func(options *clientOptions) error {
		if f == nil {
			return errors.New("nil banner callback func")
		}
		options.bannerCallback = f
		return nil
	}
}

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
	tunnels   []*clientTunnel

	ln          *connChannelListener
	proxy       *http.Server
	proxyToAddr string
}

func NewClient(
	logger *log.Logger,
	serverAddr string,
	proxyToAddr string,
	options ...ClientOption,
) (*Client, error) {
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
		BannerCallback:  func(message string) error { return nil },
	}

	proxy := newClientProxy(logger.WithPrefix("proxy"), proxyToAddr)

	return &Client{
		logger:   logger,
		srvAddr:  serverAddr,
		dialFunc: opts.dialFunc,

		mu:         sync.Mutex{},
		inShutdown: atomic.Bool{},
		closeWg:    &sync.WaitGroup{},

		sshConfig: sshConfig,
		tunnels:   make([]*clientTunnel, 0),

		ln:          newConnChannelListener(),
		proxy:       proxy,
		proxyToAddr: proxyToAddr,
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

	if opts.bannerCallback == nil {
		opts.bannerCallback = func(message string) error { return nil }
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

	go func() {
		ok, payload, err := sshConn.SendRequest("subdomain", true, nil)
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.logger.Error("conn closed before could receive subdomain")
			} else {
				c.logger.Error("failed to retrieve subdomain: %s", err)
			}
		}
		if !ok {
			c.logger.Error("request for subdomain was rejected")
			return
		}
		c.logger.Printf("subdomain: %s", string(payload))
	}()

	assert.Assert(c.closeWg != nil, "nil wait group")
	c.closeWg.Add(3)
	go func() {
		defer c.closeWg.Done()
		ssh.DiscardRequests(reqs)
	}()
	go func() {
		defer c.closeWg.Done()
		c.processNewChannels(chans)
	}()
	go func() {
		defer c.closeWg.Done()
		err := c.proxy.Serve(c.ln)
		if err != nil {
			c.logger.Error("")
		}
	}()

	assert.Assert(c.sshConn != nil, "nil ssh conn")
	return nil
}

func (c *Client) Close() (rerr error) {
	if c.inShutdown.Load() {
		return ErrClientShutdown
	}

	c.inShutdown.Store(true)

	c.mu.Lock()
	assert.Assert(c.closeWg != nil, "nil wait group")
	assert.Assert(c.tunnels != nil, "nil tunnels")
	assert.Assert(c.sshConn != nil, "nil network conn")
	assert.Assert(c.proxy != nil, "nil proxy")

	defer c.closeWg.Wait()

	eg := errgroup.Group{}
	for i, t := range c.tunnels {
		eg.Go(func() error {
			c.logger.Debugf("closing tunnel %d", i)
			return t.Close()
		})
	}
	c.mu.Unlock()

	eg.Go(func() error {
		c.logger.Debug("closing proxy")
		return c.proxy.Shutdown(context.Background())
	})

	eg.Go(func() error {
		c.logger.Debug("closing ssh connection")
		err := c.sshConn.Close()
		if errors.Is(err, net.ErrClosed) || err == nil {
			return nil
		}
		return err
	})

	return eg.Wait()
}

func newClientProxy(logger *log.Logger, addr string) *http.Server {
	s := &http.Server{
		Handler: &httputil.ReverseProxy{
			Rewrite: func(r *httputil.ProxyRequest) {
				r.SetXForwarded()
				uri, err := url.Parse("http://" + addr)
				assert.Assert(err == nil, "addr parse")
				logger.Debug("rewriting", "host", r.In.URL.Host, "method", r.In.Method, "path", r.In.URL.Path)
				r.SetURL(uri)
			},
			ModifyResponse: func(r *http.Response) error {
				logger.Debug("routing back response", "host", r.Request.URL.Host, "method", r.Request.Method, "path", r.Request.URL.Path)
				return nil
			},
		},
		ConnState: func(conn net.Conn, state http.ConnState) {
			logger.Debug("Conn state changed", "state", state.String())
		},
	}

	return s
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
		tunnel := &clientTunnel{
			tunnel: tunnel{
				sshChan: sshChan,
				reqs:    reqs,
			},
			conn: netSSHChannelConn{
				laddr:   c.conn.LocalAddr(),
				raddr:   c.conn.RemoteAddr(),
				Channel: sshChan,
			},
		}

		assert.Assert(c.tunnels != nil, "nil tunnels")
		c.mu.Lock()
		c.tunnels = append(c.tunnels, tunnel)
		c.mu.Unlock()

		c.ln.serveConn(tunnel.conn)
	}
}

type clientTunnel struct {
	tunnel
	conn net.Conn
}

type netSSHChannelConn struct {
	ssh.Channel
	laddr net.Addr
	raddr net.Addr
}

func (c netSSHChannelConn) LocalAddr() net.Addr {
	return c.laddr
}
func (c netSSHChannelConn) RemoteAddr() net.Addr {
	return c.raddr
}
func (c netSSHChannelConn) SetDeadline(t time.Time) error {
	return nil
}
func (c netSSHChannelConn) SetReadDeadline(t time.Time) error {
	return nil
}
func (c netSSHChannelConn) SetWriteDeadline(t time.Time) error {
	return nil
}

type connChannelListener struct {
	ch     chan net.Conn
	closed atomic.Bool
}

func newConnChannelListener() *connChannelListener {
	return &connChannelListener{
		ch:     make(chan net.Conn, 1),
		closed: atomic.Bool{},
	}
}

func (l *connChannelListener) serveConn(conn net.Conn) error {
	if l.closed.Load() {
		return net.ErrClosed
	}

	l.ch <- conn
	return nil
}

func (l *connChannelListener) Accept() (net.Conn, error) {
	conn, ok := <-l.ch
	if !ok {
		return nil, net.ErrClosed
	}
	return conn, nil
}

func (l *connChannelListener) Close() error {
	if l.closed.Load() {
		return net.ErrClosed
	}

	close(l.ch)
	l.closed.Store(true)
	return nil
}

func (l *connChannelListener) Addr() net.Addr {
	return emptyAddr{}
}

type emptyAddr struct{}

func (emptyAddr) Network() string {
	return ""
}
func (emptyAddr) String() string {
	return ""
}
