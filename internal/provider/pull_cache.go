package provider

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/docker"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/pullcache"
	"github.com/google/go-containerregistry/pkg/name"
)

// pullCacheHost is the name harness containers use to reach the provider host.
const pullCacheHost = "host.docker.internal"

// configurePullCache starts the shared pull cache when enabled via config or
// the IMAGETEST_PULL_CACHE environment variable.
func (s *ProviderStore) configurePullCache(ctx context.Context, cfg *ProviderPullCacheModel) error {
	if cfg == nil {
		cfg = &ProviderPullCacheModel{}
	}

	enabled := cfg.Enabled.ValueBool()
	if v := os.Getenv("IMAGETEST_PULL_CACHE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("parsing IMAGETEST_PULL_CACHE=%q: %w", v, err)
		}
		enabled = b
	}
	if !enabled {
		return nil
	}

	dir := cfg.Dir.ValueString()
	if v := os.Getenv("IMAGETEST_PULL_CACHE_DIR"); v != "" {
		dir = v
	}
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return fmt.Errorf("resolving user cache dir: %w", err)
		}
		dir = filepath.Join(base, "imagetest", "pull-cache")
	}

	ip := cfg.ListenAddress.ValueString()
	if v := os.Getenv("IMAGETEST_PULL_CACHE_ADDR"); v != "" {
		ip = v
	}
	if ip == "" {
		if h := os.Getenv("IMAGETEST_DOCKER_HOST"); strings.HasPrefix(h, "ssh://") || strings.HasPrefix(h, "tcp://") {
			return fmt.Errorf("the pull cache runs inside the provider process and requires a local docker daemon, but IMAGETEST_DOCKER_HOST=%s is remote", h)
		}
		cli, err := docker.New()
		if err != nil {
			return fmt.Errorf("connecting to docker to determine listen address: %w", err)
		}
		ip, err = cli.HostListenIP(ctx)
		if err != nil {
			return err
		}
	}

	hosts := append([]string{}, cfg.Registries...)
	hosts = append(hosts, s.repo.RegistryStr())
	for _, r := range s.extraRepos {
		hosts = append(hosts, r.RegistryStr())
	}

	srv, err := pullcache.New(dir,
		pullcache.WithKeychain(s.keychain),
		pullcache.WithRegistries(hosts...),
	)
	if err != nil {
		return err
	}
	if err := srv.Serve(context.WithoutCancel(ctx), net.JoinHostPort(ip, "0")); err != nil {
		return err
	}
	clog.InfoContext(ctx, "pull cache enabled", "addr", srv.Addr().String(), "dir", dir, "registries", srv.Registries())

	s.pullCache = srv
	return nil
}

// pullCacheURL is the address harness containers use to reach the cache, both
// as a containerd mirror endpoint and as dockerd's https proxy.
func pullCacheURL(c *pullcache.Server) string {
	return fmt.Sprintf("http://%s:%d", pullCacheHost, c.Port())
}

// registriesOf returns the registry hosts referenced by the given image refs.
func registriesOf(images TestsImageResource) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(images))
	for _, raw := range images {
		ref, err := name.ParseReference(raw)
		if err != nil {
			continue
		}
		host := ref.Context().RegistryStr()
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}
	return out
}
