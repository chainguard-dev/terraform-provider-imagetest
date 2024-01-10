package provider

import (
	"log/slog"
	"os"
	"sync"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/environment"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	petname "github.com/dustinkirkland/golang-petname"
	slogmulti "github.com/samber/slog-multi"
)

const RuntimeLabelEnv = "IMAGETEST_LABELS"

// ProviderStore manages the global runtime state of the provider. The provider
// uses this to lookup the defined relationships between resources, and manage
// shared external state (such as open ports).
type ProviderStore struct {
	env           types.Environment
	portAllocator *environment.PortAllocator
	// harnesses stores a map of the available harnesses, keyed by their ID.
	harnesses *smap[string, types.Harness]

	// providerResourceData stores the data for the provider resource.
	// TODO: This shouldn't need to be like this
	providerResourceData ImageTestProviderModel
}

func NewProviderStore() *ProviderStore {
	return &ProviderStore{
		portAllocator: environment.NewPortAllocator(),
		env: environment.New(
			environment.WithLabelsFromEnv(os.Getenv(RuntimeLabelEnv)),
		),
		harnesses: newSmap[string, types.Harness](),
	}
}

func (s *ProviderStore) RandomID() string {
	// h/t dustin
	return petname.Generate(2, "-")
}

func (s *ProviderStore) Logger() *slog.Logger {
	handlers := []slog.Handler{
		log.TFOption{}.NewTFHandler(),
	}

	return slog.New(
		slogmulti.Fanout(
			handlers...,
		),
	)
}

func newSmap[K comparable, V any]() *smap[K, V] {
	return &smap[K, V]{
		store: make(map[K]V),
		mu:    sync.Mutex{},
	}
}

// smap is a generic thread-safe map implementation.
type smap[K comparable, V any] struct {
	store map[K]V
	mu    sync.Mutex
}

func (m *smap[K, V]) Set(key K, value V) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store[key] = value
}

func (m *smap[K, V]) Get(key K) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.store[key]
	return v, ok
}

func (m *smap[K, V]) Delete(key K) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.store, key)
}
