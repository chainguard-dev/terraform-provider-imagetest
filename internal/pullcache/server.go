// Package pullcache implements a shared, content addressed pull-through cache
// for OCI registries.
//
// The provider runs one instance per process and points every harness at it,
// so parallel harnesses fetch each layer from upstream once instead of once per
// inner daemon. Two access modes are served from the same listener:
//
//   - Mirror mode: the registry v2 API on the plain HTTP listener. containerd
//     style clients (k3s) are configured with a wildcard mirror and identify the
//     upstream registry via the `ns` query parameter.
//   - Proxy mode: HTTP CONNECT. dockerd is pointed at the listener via its
//     https-proxy setting. Tunnels to registries the cache knows about are
//     terminated with a certificate from a per-process CA and served by the same
//     registry handler, everything else is relayed untouched.
package pullcache

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/chainguard-dev/clog"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"golang.org/x/sync/singleflight"
)

// DefaultRegistries are intercepted in proxy mode even when no test references
// them explicitly. They are the hosts test scripts commonly pull from ad hoc.
var DefaultRegistries = []string{
	"cgr.dev",
	"registry-1.docker.io",
	"mirror.gcr.io",
	"ghcr.io",
	"quay.io",
	"gcr.io",
	"registry.k8s.io",
	"public.ecr.aws",
	"mcr.microsoft.com",
}

const maxManifestSize = 16 << 20

type Option func(*Server) error

// WithKeychain sets the keychain used to authenticate against upstream
// registries. Defaults to the docker config keychain.
func WithKeychain(kc authn.Keychain) Option {
	return func(s *Server) error {
		s.keychain = kc
		return nil
	}
}

// WithRegistries adds hosts that are intercepted in proxy mode.
func WithRegistries(hosts ...string) Option {
	return func(s *Server) error {
		for _, h := range hosts {
			if h == "" {
				continue
			}
			s.intercept[strings.ToLower(h)] = struct{}{}
		}
		return nil
	}
}

// WithUpstreamAlias makes requests for host be served from registry instead.
// Docker Hub's registry-1.docker.io is aliased to index.docker.io by default.
func WithUpstreamAlias(host, registry string) Option {
	return func(s *Server) error {
		reg, err := name.NewRegistry(registry)
		if err != nil {
			return err
		}
		s.aliases[strings.ToLower(host)] = reg
		return nil
	}
}

// WithTransport sets the base transport used for upstream requests.
func WithTransport(rt http.RoundTripper) Option {
	return func(s *Server) error {
		s.base = rt
		return nil
	}
}

type Server struct {
	store     *store
	ca        *ca
	keychain  authn.Keychain
	base      http.RoundTripper
	intercept map[string]struct{}
	aliases   map[string]name.Registry

	mu      sync.Mutex
	clients map[string]*http.Client
	sf      singleflight.Group

	listener net.Listener
	srv      *http.Server
}

// New creates a cache backed by dir. Call Serve to start listening.
func New(dir string, opts ...Option) (*Server, error) {
	st, err := newStore(dir)
	if err != nil {
		return nil, err
	}

	authority, err := newCA()
	if err != nil {
		return nil, err
	}

	s := &Server{
		store:     st,
		ca:        authority,
		keychain:  authn.DefaultKeychain,
		base:      http.DefaultTransport,
		intercept: make(map[string]struct{}),
		aliases:   make(map[string]name.Registry),
		clients:   make(map[string]*http.Client),
	}
	WithRegistries(DefaultRegistries...)(s)                            //nolint:errcheck
	WithUpstreamAlias("registry-1.docker.io", name.DefaultRegistry)(s) //nolint:errcheck
	WithUpstreamAlias("docker.io", name.DefaultRegistry)(s)            //nolint:errcheck

	for _, opt := range opts {
		if err := opt(s); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Serve starts listening on addr (host:port, port may be 0) and serves in the
// background until Close is called.
func (s *Server) Serve(ctx context.Context, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}
	s.listener = ln
	s.srv = &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 30 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			clog.ErrorContext(ctx, "pull cache server stopped", "error", err)
		}
	}()
	clog.InfoContext(ctx, "pull cache listening", "addr", ln.Addr().String(), "dir", s.store.dir)
	return nil
}

// Close stops the server.
func (s *Server) Close() error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Close()
}

// Addr returns the bound address. Only valid after Serve.
func (s *Server) Addr() net.Addr {
	if s.listener == nil {
		return nil
	}
	return s.listener.Addr()
}

// Port returns the bound port. Only valid after Serve.
func (s *Server) Port() int {
	if s.listener == nil {
		return 0
	}
	addr, ok := s.listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0
	}
	return addr.Port
}

// CACert returns the PEM encoded CA certificate clients must trust for
// intercepted hosts.
func (s *Server) CACert() []byte {
	return s.ca.pem
}

// Registries returns the sorted list of hosts intercepted in proxy mode.
func (s *Server) Registries() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.intercept))
	for h := range s.intercept {
		out = append(out, h)
	}
	slices.Sort(out)
	return out
}

// AddRegistries adds hosts to intercept. Safe to call while serving.
func (s *Server) AddRegistries(hosts ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	WithRegistries(hosts...)(s) //nolint:errcheck
}

func (s *Server) intercepts(host string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.intercept[strings.ToLower(host)]
	return ok
}

type ctxKey struct{}

// ServeHTTP dispatches between CONNECT tunnels and registry requests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}
	s.handleRegistry(w, r)
}

// handleConnect terminates or relays a proxied TLS connection.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		host, port = r.Host, "443"
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}

	if !s.intercepts(host) || port != "443" {
		upstream, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 30*time.Second)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		relay(conn, upstream)
		return
	}

	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		_ = conn.Close()
		return
	}

	tlsConn := &notifyConn{Conn: tls.Server(conn, s.ca.tlsConfig(host)), done: make(chan struct{})}
	inner := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.handleRegistry(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, host)))
		}),
		ReadHeaderTimeout: 30 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	// Serve returns once the single connection has been closed.
	_ = inner.Serve(&singleConnListener{conn: tlsConn, done: tlsConn.done})
}

// upstreamFor decides which registry a request is meant for. containerd style
// mirrors say so explicitly via `ns`, intercepted tunnels carry the host in the
// context, and anything else is assumed to be a plain Docker Hub mirror.
func (s *Server) upstreamFor(r *http.Request) (name.Registry, error) {
	host := r.URL.Query().Get("ns")
	if host == "" {
		if h, ok := r.Context().Value(ctxKey{}).(string); ok {
			host = h
		}
	}
	if host == "" {
		host = name.DefaultRegistry
	}

	if reg, ok := s.aliases[strings.ToLower(host)]; ok {
		return reg, nil
	}

	// Registries the provider reaches over http (e.g. localhost:5005) are
	// fetched that way, everything else is https.
	return name.NewRegistry(host)
}

// handleRegistry serves the registry v2 API.
func (s *Server) handleRegistry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.URL.Path == "/v2/" || r.URL.Path == "/v2" {
		w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	reg, err := s.upstreamFor(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rest, ok := strings.CutPrefix(r.URL.Path, "/v2/")
	if !ok {
		http.NotFound(w, r)
		return
	}

	if i := strings.LastIndex(rest, "/blobs/"); i > 0 {
		s.serveBlob(ctx, w, r, reg, rest[:i], rest[i+len("/blobs/"):])
		return
	}
	if i := strings.LastIndex(rest, "/manifests/"); i > 0 {
		s.serveManifest(ctx, w, r, reg, rest[:i], rest[i+len("/manifests/"):])
		return
	}

	// Tags, referrers and anything else are passed straight through.
	s.passthrough(ctx, w, r, reg, rest)
}

func (s *Server) serveBlob(ctx context.Context, w http.ResponseWriter, r *http.Request, reg name.Registry, repoPath, rawDigest string) {
	h, err := v1.NewHash(rawDigest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if f, ok := s.store.openBlob(h); ok {
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Docker-Content-Digest", h.String())
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", fmt.Sprint(st.Size()))
			w.WriteHeader(http.StatusOK)
			return
		}
		http.ServeContent(w, r, "", time.Time{}, f)
		return
	}

	if r.Method == http.MethodHead {
		s.passthrough(ctx, w, r, reg, repoPath+"/blobs/"+rawDigest)
		return
	}

	// Coalesce concurrent downloads of the same blob into one upstream fetch.
	// The download outlives the requester that triggered it so that waiters
	// are not failed by the first client going away.
	dlctx := context.WithoutCancel(ctx)
	_, err, _ = s.sf.Do(h.String(), func() (any, error) {
		if _, ok := s.store.openBlob(h); ok {
			return nil, nil
		}
		resp, err := s.upstreamGet(dlctx, reg, repoPath, "/blobs/"+rawDigest, nil)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, newStatusError(resp)
		}
		start := time.Now()
		n, err := s.store.putBlob(h, resp.Body)
		if err != nil {
			return nil, err
		}
		clog.InfoContext(ctx, "pull cache: fetched blob", "registry", reg.RegistryStr(), "repo", repoPath, "digest", h.String(), "bytes", n, "took", time.Since(start).Round(time.Millisecond))
		return nil, nil
	})
	if err != nil {
		writeError(w, err)
		return
	}

	f, ok := s.store.openBlob(h)
	if !ok {
		http.Error(w, "blob vanished from cache", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Docker-Content-Digest", h.String())
	http.ServeContent(w, r, "", time.Time{}, f)
}

func (s *Server) serveManifest(ctx context.Context, w http.ResponseWriter, r *http.Request, reg name.Registry, repoPath, ref string) {
	byDigest, err := v1.NewHash(ref)
	if err == nil {
		if body, mt, ok := s.store.getManifest(byDigest); ok {
			w.Header().Set("Content-Type", mt)
			w.Header().Set("Docker-Content-Digest", byDigest.String())
			w.Header().Set("Content-Length", fmt.Sprint(len(body)))
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_, _ = w.Write(body)
			}
			return
		}
	}

	if r.Method == http.MethodHead {
		s.passthrough(ctx, w, r, reg, repoPath+"/manifests/"+ref)
		return
	}

	// Clients are picky about which manifest media types they accept, so the
	// Accept header is forwarded verbatim instead of being normalised.
	hdr := http.Header{}
	if accept := r.Header.Values("Accept"); len(accept) > 0 {
		hdr["Accept"] = accept
	}
	resp, err := s.upstreamGet(ctx, reg, repoPath, "/manifests/"+ref, hdr)
	if err != nil {
		writeError(w, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		copyResponse(w, resp)
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestSize+1))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if len(body) > maxManifestSize {
		http.Error(w, "manifest too large", http.StatusBadGateway)
		return
	}

	h, _, err := v1.SHA256(strings.NewReader(string(body)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if byDigest.Hex != "" && byDigest.Algorithm == "sha256" && byDigest != h {
		http.Error(w, fmt.Sprintf("upstream manifest digest mismatch: want %s got %s", byDigest, h), http.StatusBadGateway)
		return
	}

	mt := resp.Header.Get("Content-Type")
	if err := s.store.putManifest(h, mt, body); err != nil {
		clog.WarnContext(ctx, "pull cache: failed to store manifest", "digest", h.String(), "error", err)
	}

	w.Header().Set("Content-Type", mt)
	w.Header().Set("Docker-Content-Digest", h.String())
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// passthrough forwards a request to upstream and relays the response.
func (s *Server) passthrough(ctx context.Context, w http.ResponseWriter, r *http.Request, reg name.Registry, rest string) {
	repoPath, suffix := rest, ""
	for _, sep := range []string{"/manifests/", "/blobs/", "/tags/", "/referrers/"} {
		if i := strings.LastIndex(rest, sep); i > 0 {
			repoPath, suffix = rest[:i], rest[i:]
			break
		}
	}

	q := r.URL.Query()
	q.Del("ns")
	if enc := q.Encode(); enc != "" {
		suffix += "?" + enc
	}

	hdr := http.Header{}
	for _, k := range []string{"Accept", "Range"} {
		if v := r.Header.Values(k); len(v) > 0 {
			hdr[k] = v
		}
	}

	resp, err := s.upstreamDo(ctx, r.Method, reg, repoPath, suffix, hdr)
	if err != nil {
		writeError(w, err)
		return
	}
	defer resp.Body.Close()
	copyResponse(w, resp)
}

func (s *Server) upstreamGet(ctx context.Context, reg name.Registry, repoPath, suffix string, hdr http.Header) (*http.Response, error) {
	return s.upstreamDo(ctx, http.MethodGet, reg, repoPath, suffix, hdr)
}

func (s *Server) upstreamDo(ctx context.Context, method string, reg name.Registry, repoPath, suffix string, hdr http.Header) (*http.Response, error) {
	repo, err := name.NewRepository(reg.RegistryStr() + "/" + repoPath)
	if err != nil {
		return nil, err
	}
	// Keep the registry (and with it the scheme) the caller resolved.
	repo.Registry = reg

	client, err := s.client(ctx, repo)
	if err != nil {
		return nil, err
	}

	path, query, _ := strings.Cut(suffix, "?")
	u := &url.URL{
		Scheme:   reg.Scheme(),
		Host:     reg.RegistryStr(),
		Path:     "/v2/" + repo.RepositoryStr() + path,
		RawQuery: query,
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	maps.Copy(req.Header, hdr)
	return client.Do(req)
}

// client returns an authenticated client scoped to pulling from repo.
func (s *Server) client(ctx context.Context, repo name.Repository) (*http.Client, error) {
	key := repo.String()

	s.mu.Lock()
	c, ok := s.clients[key]
	s.mu.Unlock()
	if ok {
		return c, nil
	}

	auth, err := s.keychain.Resolve(repo.Registry)
	if err != nil {
		return nil, fmt.Errorf("resolving credentials for %s: %w", repo.Registry, err)
	}

	tr, err := transport.NewWithContext(ctx, repo.Registry, auth, s.base, []string{repo.Scope(transport.PullScope)})
	if err != nil {
		return nil, fmt.Errorf("authenticating to %s: %w", repo.Registry, err)
	}

	c = &http.Client{Transport: tr}
	s.mu.Lock()
	s.clients[key] = c
	s.mu.Unlock()
	return c, nil
}

type statusError struct {
	code int
	body []byte
	hdr  http.Header
}

func newStatusError(resp *http.Response) *statusError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return &statusError{code: resp.StatusCode, body: body, hdr: resp.Header}
}

func (e *statusError) Error() string {
	return fmt.Sprintf("upstream returned %d: %s", e.code, strings.TrimSpace(string(e.body)))
}

func writeError(w http.ResponseWriter, err error) {
	if se, ok := errors.AsType[*statusError](err); ok {
		if ct := se.hdr.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		w.WriteHeader(se.code)
		_, _ = w.Write(se.body)
		return
	}
	if te, ok := errors.AsType[*transport.Error](err); ok {
		http.Error(w, err.Error(), te.StatusCode)
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}

func copyResponse(w http.ResponseWriter, resp *http.Response) {
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Docker-Content-Digest", "Link", "Location", "Www-Authenticate"} {
		if v := resp.Header.Values(k); len(v) > 0 {
			w.Header()[k] = v
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func relay(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
	}
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
	_ = a.Close()
	_ = b.Close()
}

// notifyConn closes done when the connection is closed, which lets
// singleConnListener end the inner http.Server.Serve loop.
type notifyConn struct {
	net.Conn
	once sync.Once
	done chan struct{}
}

func (c *notifyConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.done) })
	return err
}

// singleConnListener hands out exactly one connection and then blocks until
// that connection is closed.
type singleConnListener struct {
	mu   sync.Mutex
	conn net.Conn
	done chan struct{}
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	c := l.conn
	l.conn = nil
	l.mu.Unlock()
	if c != nil {
		return c, nil
	}
	<-l.done
	return nil, net.ErrClosed
}

func (l *singleConnListener) Close() error   { return nil }
func (l *singleConnListener) Addr() net.Addr { return &net.TCPAddr{} }
