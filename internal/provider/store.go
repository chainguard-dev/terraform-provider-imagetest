package provider

import (
	"crypto/sha256"
	"log/slog"
	"math/big"
	"os"
	"sync"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/inventory"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	"github.com/docker/docker/client"
	slogmulti "github.com/samber/slog-multi"
)

// ProviderStore manages the global runtime state of the provider. The provider
// uses this to lookup the defined relationships between resources, and manage
// shared external state.
type ProviderStore struct {
	// harnesses stores a map of the available harnesses, keyed by their ID.
	harnesses *smap[string, types.Harness]
	labels    map[string]string
	// providerResourceData stores the data for the provider resource.
	// TODO: there's probably a way to do this without passing around the whole
	// model
	providerResourceData ImageTestProviderModel

	// cli is the Docker client. it is initialized once during the providers
	// Configure() stage and reused for any resource that requires it.
	cli *client.Client
}

func NewProviderStore() *ProviderStore {
	return &ProviderStore{
		labels:    make(map[string]string),
		harnesses: newSmap[string, types.Harness](),
	}
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

// Inventory returns an instance of the inventory per inventory data source.
func (s *ProviderStore) Inventory(data InventoryDataSourceModel) inventory.Inventory {
	// TODO: More backends?
	return inventory.NewFile(data.Seed.ValueString())
}

// SkipTeardown returns true if the IMAGETEST_SKIP_TEARDOWN environment
// variable is declared.
func (s *ProviderStore) SkipTeardown() bool {
	v := os.Getenv("IMAGETEST_SKIP_TEARDOWN")
	return v != ""
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
