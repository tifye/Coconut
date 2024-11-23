package coconut

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/tifye/Coconut/assert"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sync/errgroup"
)

var (
	ErrServerShutdown  = errors.New("server closed")
	ErrSessionNotFound = errors.New("session not found")
)

type serverOptions struct {
	SSHSigner               ssh.Signer
	clientListenAddr        string
	clientListenFunc        ListenFunc
	publicKeyAuthAlgorithms []string
	publicKeyCallback       PublicKeyCallback
	noClientAuth            bool
	proxyAddr               string
	proxyListenFunc         ListenFunc
	noDiscovery             bool
}

type ServerOption func(options *serverOptions) error

func WithHostKey(signer ssh.Signer) ServerOption {
	return func(options *serverOptions) error {
		if signer == nil {
			return errors.New("nil signer")
		}
		options.SSHSigner = signer
		return nil
	}
}

func WithNoClientAuth() ServerOption {
	return func(options *serverOptions) error {
		options.noClientAuth = true
		return nil
	}
}

func WithServerPublicKeyAlgorithms(algs []string) ServerOption {
	return func(options *serverOptions) error {
		if len(algs) <= 0 {
			return errors.New("invalid slice of aglorithms")
		}
		options.publicKeyAuthAlgorithms = algs
		return nil
	}
}

type PublicKeyCallback func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error)

func WithPublicKeyCallback(cb PublicKeyCallback) ServerOption {
	return func(options *serverOptions) error {
		if cb == nil {
			return errors.New("nil public key callback")
		}
		options.publicKeyCallback = cb
		return nil
	}
}

func WithProxyAddr(addr string) ServerOption {
	return func(options *serverOptions) error {
		if addr == "" {
			return errors.New("empty address")
		}
		options.proxyAddr = addr
		return nil
	}
}

func WithProxyListenFunc(f ListenFunc) ServerOption {
	return func(options *serverOptions) error {
		if f == nil {
			return errors.New("nil proxy listen func")
		}
		options.proxyListenFunc = f
		return nil
	}
}

func WithClientListenAddr(addr string) ServerOption {
	return func(options *serverOptions) error {
		if addr == "" {
			return errors.New("empty address")
		}
		options.clientListenAddr = addr
		return nil
	}
}

func WithClientListenFunc(f ListenFunc) ServerOption {
	return func(options *serverOptions) error {
		if f == nil {
			return errors.New("nil client listen func")
		}
		options.clientListenFunc = f
		return nil
	}
}

func WithNoDiscovery() ServerOption {
	return func(options *serverOptions) error {
		options.noDiscovery = true
		return nil
	}
}

type Server struct {
	logger *log.Logger

	mu         sync.Mutex
	inShutdown atomic.Bool
	donech     chan struct{}
	err        error

	clAddr       string
	clListener   net.Listener
	clListenFunc ListenFunc

	sshConfig *ssh.ServerConfig

	sessions map[string]*Session

	proxy           *http.Server
	proxyListener   net.Listener
	proxyListenFunc ListenFunc
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

	err := serverDefaults(logger, &opts)
	if err != nil {
		return nil, err
	}

	assert.Assert(opts.clientListenAddr != "", "zero value client listen address")
	assert.Assert(opts.clientListenFunc != nil, "nil client listen func")
	assert.Assert(opts.publicKeyCallback != nil, "nil public key callback")
	assert.Assert(len(opts.publicKeyAuthAlgorithms) > 0, "invalid public key auth algorithms")
	assert.Assert(opts.SSHSigner != nil, "nil signer")
	assert.Assert(opts.proxyAddr != "", "empty proxy address")
	assert.Assert(opts.proxyListenFunc != nil, "nil proxy listen func")

	sshConfig := ssh.ServerConfig{
		NoClientAuth:            opts.noClientAuth,
		PublicKeyAuthAlgorithms: opts.publicKeyAuthAlgorithms,
		PublicKeyCallback:       opts.publicKeyCallback,
		AuthLogCallback:         authLogCallback(logger),
	}
	sshConfig.AddHostKey(opts.SSHSigner)

	server := &Server{
		logger:          logger,
		mu:              sync.Mutex{},
		inShutdown:      atomic.Bool{},
		donech:          make(chan struct{}),
		clAddr:          opts.clientListenAddr,
		clListenFunc:    opts.clientListenFunc,
		sshConfig:       &sshConfig,
		sessions:        make(map[string]*Session),
		proxyListenFunc: opts.proxyListenFunc,
	}

	var discover discoverSession
	if opts.noDiscovery {
		discover = disoverFirst(server.Sessions)
	} else {
		discover = server.discoverSession
	}
	server.proxy = newServerProxy(logger.WithPrefix("proxy"), opts.proxyAddr, discover)

	return server, nil
}

func serverDefaults(logger *log.Logger, opts *serverOptions) error {
	if opts.clientListenAddr == "" {
		opts.clientListenAddr = ":9000"
	}

	if opts.clientListenFunc == nil {
		opts.clientListenFunc = DefaultListen
	}

	if opts.publicKeyCallback == nil {
		opts.publicKeyCallback = publicKeyCallback(logger)
	}

	if opts.publicKeyAuthAlgorithms == nil {
		opts.publicKeyAuthAlgorithms = []string{
			ssh.KeyAlgoED25519,
			ssh.KeyAlgoSKED25519, ssh.KeyAlgoSKECDSA256,
			ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
			ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSA,
			ssh.KeyAlgoDSA,
		}
	}

	if opts.SSHSigner == nil {
		key, err := rsa.GenerateKey(rand.Reader, 256)
		if err != nil {
			return err
		}

		signer, err := ssh.NewSignerFromKey(key)
		if err != nil {
			return err
		}

		opts.SSHSigner = signer
	}

	if opts.proxyAddr == "" {
		opts.proxyAddr = "127.0.0.1:0"
	}

	if opts.proxyListenFunc == nil {
		opts.proxyListenFunc = DefaultListen
	}

	assert.Assert(opts.clientListenAddr != "", "zero value client listen address")
	assert.Assert(opts.clientListenFunc != nil, "nil client listen func")
	assert.Assert(opts.publicKeyCallback != nil, "nil public key callback")
	assert.Assert(len(opts.publicKeyAuthAlgorithms) > 0, "invalid public key auth algorithms")
	assert.Assert(opts.SSHSigner != nil, "nil signer")
	assert.Assert(opts.proxyAddr != "", "empty proxy address")
	assert.Assert(opts.proxyListenFunc != nil, "nil proxy listen func")
	return nil
}

func publicKeyCallback(logger *log.Logger) PublicKeyCallback {
	assert.Assert(logger != nil, "nil logger")
	return func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		logger.Debug("public key callback", "seshId", conn.SessionID(), "user", conn.User(), "keyType", key.Type(), "raddr", conn.RemoteAddr())
		return nil, nil
	}
}

func authLogCallback(logger *log.Logger) func(conn ssh.ConnMetadata, method string, err error) {
	assert.Assert(logger != nil, "nil logger")
	return func(conn ssh.ConnMetadata, method string, err error) {
		if err != nil {
			if errors.Is(err, ssh.ErrNoAuth) {
				logger.Debug("auth log callback but auth has yet to be passed")
				return
			}

			logger.Error("failed auth attempt", "method", method, "raddr", conn.RemoteAddr(), "err", err)
			return
		}

		logger.Info("auth passed", "method", method, "raddr", conn.RemoteAddr())
	}
}

func (s *Server) ProxyAddr() string {
	s.mu.Lock()
	proxy := s.proxy
	s.mu.Unlock()
	if proxy == nil {
		return ""
	}
	return proxy.Addr
}

// ListenFunc is a function used to create a net.Listener
type ListenFunc func(network string, address string) (net.Listener, error)

// DefaultListen is the default ListenFunc used to create net.Listener
var DefaultListen ListenFunc = net.Listen

func (s *Server) Start(ctx context.Context) (rerr error) {
	defer func() {
		if rerr != nil {
			err := s.Close(ctx)
			rerr = errors.Join(rerr, err)
		}
	}()

	assert.Assert(s.clAddr != "", "zero value client listener address")
	assert.Assert(s.clListenFunc != nil, "nil client listen func")
	ln, err := s.clListenFunc("tcp", s.clAddr)
	if err != nil {
		return fmt.Errorf("client ListenFunc: %w", err)
	}
	s.clListener = ln
	assert.Assert(ln != nil, "nil client listener")
	s.logger.Debug("client listener started", "addr", s.clAddr)

	assert.Assert(s.proxyListenFunc != nil, "nil proxy listen func")
	proxyLn, err := s.proxyListenFunc("tcp", s.proxy.Addr)
	if err != nil {
		return fmt.Errorf("proxy listen func: %w", err)
	}
	s.proxyListener = proxyLn
	s.logger.Debug("serving proxy", "addr", s.proxy.Addr)
	go func() {
		err := s.proxy.Serve(proxyLn)
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return
		}

		s.mu.Lock()
		s.err = err
		s.mu.Unlock()

		cctx := context.WithoutCancel(ctx)
		s.logger.Error(s.Close(cctx))
	}()

	go s.processClients()

	return nil
}

func (s *Server) Close(ctx context.Context) (rerr error) {
	if s.inShutdown.Load() {
		return ErrServerShutdown
	}
	s.inShutdown.Store(true)
	defer close(s.donech)

	s.mu.Lock()
	defer s.mu.Unlock()

	eg := errgroup.Group{}
	if s.clListener != nil {
		eg.Go(s.clListener.Close)
	}
	for _, sesh := range s.sessions {
		eg.Go(sesh.Close)
	}
	defer func() {
		err := eg.Wait()
		rerr = errors.Join(rerr, err)
	}()

	assert.Assert(s.proxy != nil, "nil server proxy")
	err := s.proxy.Shutdown(ctx)
	if err != nil || !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

func (s *Server) Done() <-chan struct{} {
	return s.donech
}

func (s *Server) Err() error {
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	return err
}

func (s *Server) processClients() {
	assert.Assert(s.clListener != nil, "nil client listener")
	assert.Assert(s.sshConfig != nil, "nil ssh client config")
	for {
		conn, err := s.clListener.Accept()
		if err != nil {
			if s.inShutdown.Load() {
				s.logger.Debug("server in shutdown, no longer accepting clients")
				return
			}
			s.logger.Error("failed to accept network conn", "err", err)
			continue
		}
		s.logger.Debug("accepted net conn", "raddr", conn.RemoteAddr(), "laddr", conn.LocalAddr())

		sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.sshConfig)
		if err != nil {
			s.logger.Error("ssh handsake failed", "err", err)
			continue
		}

		subdomain := generateSubdomain()
		sesh, err := newSession(conn, sshConn, subdomain, chans, reqs, s.logger.WithPrefix(subdomain))
		if err != nil {
			s.logger.Error("failed to create new sesson", "err", err)
			continue
		}
		assert.Assert(sesh != nil, "nil session")

		// todo: send to client through banner
		s.logger.Info("client session created", "subdomain", subdomain)

		s.mu.Lock()
		s.sessions[subdomain] = sesh
		s.mu.Unlock()

		err = sesh.Start()
		if err != nil {
			s.logger.Error("failed to start session", "err", err)
		}
	}
}

func (s *Server) Sessions() map[string]*Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions
}

type discoverSession func(string) (*Session, error)

func (s *Server) discoverSession(k string) (*Session, error) {
	sessions := s.Sessions()
	sesh, ok := sessions[k]
	if !ok {
		return nil, ErrSessionNotFound
	}
	return sesh, nil
}

func disoverFirst(sessions func() map[string]*Session) discoverSession {
	return func(s string) (*Session, error) {
		for _, v := range sessions() {
			return v, nil
		}
		return nil, nil
	}
}

type ctxKey string

const sessionCtxKey ctxKey = "session"
const reqLoggerCtxKey ctxKey = "logger"

func newServerProxy(logger *log.Logger, addr string, discover discoverSession) *http.Server {
	var reqIdCounter atomic.Uint64

	proxyHandler := &httputil.ReverseProxy{
		Transport: &serverTransport{
			logger: logger.WithPrefix("server-transport"),
		},
		Rewrite: func(r *httputil.ProxyRequest) {
			sesh := r.In.Context().Value(sessionCtxKey).(*Session)
			rlogger := r.In.Context().Value(reqLoggerCtxKey).(*log.Logger)

			rlogger.Debug("rewriting")

			r.SetXForwarded()
			url, err := url.Parse(fmt.Sprintf("http://%s", sesh.conn.RemoteAddr().String()))
			assert.Assert(err == nil, "url parse err")
			r.SetURL(url)
		},
		ErrorLog: logger.StandardLog(),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			rlogger := r.Context().Value(reqLoggerCtxKey).(*log.Logger)
			rlogger.Error("proxy err", "err", err)
			w.WriteHeader(http.StatusBadGateway)
		},
		ModifyResponse: func(r *http.Response) error {
			rlogger := r.Request.Context().Value(reqLoggerCtxKey).(*log.Logger)
			rlogger.Debug("routing back response")
			return nil
		},
	}

	mux := http.ServeMux{}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		rlogger := logger.With("requestId", reqIdCounter.Add(1), "raddr", r.RemoteAddr, "host", r.Host, "method", r.Method, "path", r.URL.Path)

		upgrade := r.Header.Get("Upgrade")
		if upgrade == "websocket" {
			w.WriteHeader(http.StatusNotAcceptable)
			rlogger.Warn("blocking websocket request")
			return
		}

		subpart, _, _ := strings.Cut(r.Host, ".")
		sesh, err := discover(subpart)
		if err != nil {
			if errors.Is(err, ErrSessionNotFound) {
				rlogger.Warnf("session '%s' not found", subpart)
			} else {
				rlogger.Error("session discover err")
			}
			w.WriteHeader(http.StatusBadGateway)
			w.Write(nil)
			return
		}

		ctx := context.WithValue(r.Context(), sessionCtxKey, sesh)
		ctx = context.WithValue(ctx, reqLoggerCtxKey, rlogger.WithPrefix(sesh.subdomain))
		r = r.WithContext(ctx)
		proxyHandler.ServeHTTP(w, r)
	})

	return &http.Server{
		Addr:         addr,
		Handler:      &mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
}

type serverTransport struct {
	logger *log.Logger
}

func (st *serverTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	sesh := r.Context().Value(sessionCtxKey).(*Session)
	return sesh.RoundTrip(r)
}
