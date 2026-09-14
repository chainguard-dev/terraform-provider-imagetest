package pullcache

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/validate"
)

// countingHandler counts upstream requests per path prefix so tests can assert
// that the cache actually absorbed traffic.
type countingHandler struct {
	inner http.Handler
	blobs atomic.Int64
	mfsts atomic.Int64
}

func (c *countingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		switch {
		case strings.Contains(r.URL.Path, "/blobs/"):
			c.blobs.Add(1)
		case strings.Contains(r.URL.Path, "/manifests/"):
			c.mfsts.Add(1)
		}
	}
	c.inner.ServeHTTP(w, r)
}

type fixture struct {
	upstream *httptest.Server
	counts   *countingHandler
	cache    *Server
	img      v1.Image
	ref      name.Reference
}

func newFixture(t *testing.T, opts ...Option) *fixture {
	t.Helper()

	counts := &countingHandler{inner: registry.New()}
	up := httptest.NewServer(counts)
	t.Cleanup(up.Close)

	upHost := strings.TrimPrefix(up.URL, "http://")
	ref, err := name.ParseReference(upHost + "/test/img:latest")
	if err != nil {
		t.Fatal(err)
	}

	img, err := random.Image(1024, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatal(err)
	}
	counts.blobs.Store(0)
	counts.mfsts.Store(0)

	cache, err := New(t.TempDir(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Serve(context.Background(), "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cache.Close() })

	return &fixture{upstream: up, counts: counts, cache: cache, img: img, ref: ref}
}

func (f *fixture) upstreamHost() string {
	return strings.TrimPrefix(f.upstream.URL, "http://")
}

// TestMirrorMode drives the cache the way containerd does: plain http to the
// mirror with the upstream registry in the ns query parameter.
func TestMirrorMode(t *testing.T) {
	f := newFixture(t)

	// A transport that rewrites requests for the upstream host to the cache and
	// tags them with ns, mimicking a containerd mirror.
	mirror := &http.Transport{Proxy: nil}
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		q.Set("ns", f.upstreamHost())
		r.URL.RawQuery = q.Encode()
		r.URL.Host = f.cache.Addr().String()
		r.Host = r.URL.Host
		return mirror.RoundTrip(r)
	})

	pull := func() {
		img, err := remote.Image(f.ref, remote.WithTransport(rt))
		if err != nil {
			t.Fatal(err)
		}
		if err := validate.Image(img); err != nil {
			t.Fatal(err)
		}
	}

	pull()
	blobs, mfsts := f.counts.blobs.Load(), f.counts.mfsts.Load()
	if blobs == 0 || mfsts == 0 {
		t.Fatalf("expected upstream traffic on first pull, got blobs=%d manifests=%d", blobs, mfsts)
	}

	pull()
	if got := f.counts.blobs.Load(); got != blobs {
		t.Fatalf("second pull hit upstream for blobs: %d -> %d", blobs, got)
	}
	// The tag lookup always goes upstream, but the digest lookups the client
	// does afterwards must be served from cache.
	if got := f.counts.mfsts.Load(); got > mfsts+1 {
		t.Fatalf("second pull hit upstream for manifests more than the tag resolve: %d -> %d", mfsts, got)
	}
}

// TestProxyMode drives the cache the way dockerd does: HTTPS to the real
// registry hostname through an http proxy, trusting the cache CA.
func TestProxyMode(t *testing.T) {
	const host = "registry.example.test"
	f := newFixture(t, WithRegistries(host))
	// The upstream address is only known once the fixture is up, so alias the
	// intercepted hostname to it after the fact.
	if err := WithUpstreamAlias(host, f.upstreamHost())(f.cache); err != nil {
		t.Fatal(err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(f.cache.CACert()) {
		t.Fatal("failed to load cache CA")
	}
	proxyURL, _ := url.Parse("http://" + f.cache.Addr().String())
	tr := &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}

	ref, err := name.ParseReference(host + "/test/img:latest")
	if err != nil {
		t.Fatal(err)
	}

	pull := func() {
		img, err := remote.Image(ref, remote.WithTransport(tr))
		if err != nil {
			t.Fatal(err)
		}
		if err := validate.Image(img); err != nil {
			t.Fatal(err)
		}
	}

	pull()
	blobs := f.counts.blobs.Load()
	if blobs == 0 {
		t.Fatal("expected upstream blob traffic on first pull")
	}
	pull()
	if got := f.counts.blobs.Load(); got != blobs {
		t.Fatalf("second pull hit upstream for blobs: %d -> %d", blobs, got)
	}
}

// TestProxyModeRelay checks that tunnels to hosts the cache does not know about
// are relayed untouched.
func TestProxyModeRelay(t *testing.T) {
	f := newFixture(t)

	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "hello")
	}))
	t.Cleanup(target.Close)

	proxyURL, _ := url.Parse("http://" + f.cache.Addr().String())
	client := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(proxyURL),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, //nolint:gosec
	}}

	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %d", resp.StatusCode)
	}
}

// TestConcurrentBlobFetch checks that parallel requests for one blob result in
// exactly one upstream fetch.
func TestConcurrentBlobFetch(t *testing.T) {
	f := newFixture(t)

	layers, err := f.img.Layers()
	if err != nil {
		t.Fatal(err)
	}
	dgst, err := layers[0].Digest()
	if err != nil {
		t.Fatal(err)
	}

	u := fmt.Sprintf("http://%s/v2/test/img/blobs/%s?ns=%s", f.cache.Addr(), dgst, f.upstreamHost())

	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			resp, err := http.Get(u)
			if err != nil {
				t.Error(err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("unexpected status %d", resp.StatusCode)
			}
			if got := resp.Header.Get("Docker-Content-Digest"); got != dgst.String() {
				t.Errorf("digest header: got %q want %q", got, dgst)
			}
		})
	}
	wg.Wait()

	if got := f.counts.blobs.Load(); got != 1 {
		t.Fatalf("expected exactly one upstream blob fetch, got %d", got)
	}
}

func TestNotFoundPassesThrough(t *testing.T) {
	f := newFixture(t)

	u := fmt.Sprintf("http://%s/v2/test/missing/manifests/latest?ns=%s", f.cache.Addr(), f.upstreamHost())
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 from upstream, got %d", resp.StatusCode)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
