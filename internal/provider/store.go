package provider

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/big"
	"os"
	"sync"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/entrypoint"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/inventory"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/google"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// ProviderStore manages the global runtime state of the provider. The provider
// uses this to look up the defined relationships between resources, and manage
// shared external state.
type ProviderStore struct {
	// harnesses stores a map of the available harnesses, keyed by their ID. This
	// is used for passing harness implementations between the providers various
	// resources.
	harnesses *mmap[string, harness.Harness]
	inv       *mmap[string, *inventory.Inventory]
	// test execution configuration
	skipTeardown bool
	skipAll      bool
	includeTests map[string]string
	excludeTests map[string]string
	// providerResourceData stores the data for the provider resource.
	// TODO: there's probably a way to do this without passing around the whole
	// model
	providerResourceData ImageTestProviderModel
	repo                 name.Repository
	extraRepos           []name.Repository
	ropts                []remote.Option
	entrypointLayers     map[string][]v1.Layer
	logsDirectory        string // Base directory for test logs
}

func NewProviderStore(repo name.Repository) (*ProviderStore, error) {
	kc := authn.NewMultiKeychain(google.Keychain, authn.DefaultKeychain)
	ropts := []remote.Option{
		remote.WithAuthFromKeychain(kc),
		remote.WithUserAgent("terraform-provider-imagetest"),
	}

	pusher, err := remote.NewPusher(ropts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create pusher: %w", err)
	}
	ropts = append(ropts, remote.Reuse(pusher))

	el, err := getEntrypointLayers(ropts...)
	if err != nil {
		return nil, fmt.Errorf("failed to get entrypoint layers: %w", err)
	}

	return &ProviderStore{
		inv: &mmap[string, *inventory.Inventory]{
			store: make(map[string]*inventory.Inventory),
			mu:    sync.Mutex{},
		},
		excludeTests: make(map[string]string),
		includeTests: make(map[string]string),
		harnesses: &mmap[string, harness.Harness]{
			store: make(map[string]harness.Harness),
			mu:    sync.Mutex{},
		},
		repo:             repo,
		ropts:            ropts,
		entrypointLayers: el,
	}, nil
}

func (s *ProviderStore) Encode(components ...string) (string, error) {
	hasher := sha256.New()
	for _, component := range components {
		_, err := hasher.Write([]byte(component))
		if err != nil {
			return "", err
		}
	}
	hashed := hasher.Sum(nil)

	hashint := new(big.Int).SetBytes(hashed)
	// truncate it to some reasonable length, knowing these will mostly be used
	// as suffixes and prefixes and conflict is unlikely
	return hashint.Text(36)[:5], nil
}

// Logger initializes the context logger for the given inventory.
func (s *ProviderStore) Logger(ctx context.Context, inv InventoryDataSourceModel, withs ...any) (context.Context, error) {
	logger := clog.FromContext(ctx).With(withs...)
	ctx = clog.WithLogger(ctx, logger)
	return ctx, nil
}

// SkipTeardown returns true if harnesses should skip teardown steps.
func (s *ProviderStore) SkipTeardown() bool {
	return s.skipTeardown
}

func getEntrypointLayers(opts ...remote.Option) (map[string][]v1.Layer, error) {
	rawRef := entrypoint.ImageRef
	// Allow for runtimes to override
	if o := os.Getenv("IMAGETEST_ENTRYPOINT_REF"); o != "" {
		rawRef = o
	}

	eref, err := name.ParseReference(rawRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse entrypoint reference: %w", err)
	}

	eidx, err := remote.Index(eref, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to get entrypoint index: %w", err)
	}

	emfst, err := eidx.IndexManifest()
	if err != nil {
		return nil, fmt.Errorf("failed to get entrypoint index manifest: %w", err)
	}

	players := make(map[string][]v1.Layer)
	for _, m := range emfst.Manifests {
		img, err := eidx.Image(m.Digest)
		if err != nil {
			return nil, fmt.Errorf("failed to load entrypoint image: %w", err)
		}

		l, err := img.Layers()
		if err != nil {
			return nil, fmt.Errorf("failed to get entrypoint layers: %w", err)
		}

		players[m.Platform.Architecture] = l
	}

	if len(players) == 0 {
		return nil, fmt.Errorf("no entrypoint layers found")
	}

	return players, nil
}

// mmap is a generic thread-safe map implementation.
type mmap[K comparable, V any] struct {
	mu    sync.Mutex
	store map[K]V
}

func (m *mmap[K, V]) Set(key K, value V) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store[key] = value
}

func (m *mmap[K, V]) Get(key K) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.store[key]
	return v, ok
}

func (m *mmap[K, V]) Delete(key K) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.store, key)
}
