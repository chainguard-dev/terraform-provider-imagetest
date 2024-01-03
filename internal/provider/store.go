package provider

import (
	"os"
	"strings"
	"sync"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/environment"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	"github.com/google/uuid"
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
	return uuid.NewString()
}

func newSmap[K comparable, V any]() *smap[K, V] {
	return &smap[K, V]{
		store: make(map[K]V),
		mu:    sync.Mutex{},
	}
}

// smap is a generic thread-safe map implementation
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

type Labels map[string]string

func newLabels() Labels {
	ls := make(Labels)
	for _, label := range strings.Split(os.Getenv("IMAGETEST_LABELS"), ",") {
		kv := strings.SplitN(label, "=", 2)
		if len(kv) != 2 {
			continue
		}
		ls[kv[0]] = kv[1]
	}
	return ls
}

// Match takes a map of labels and returns true if all of the given labels are
// present in the map
func (ls Labels) Match(matches map[string]string) bool {
	for k, v := range ls {
		if matches[k] != v {
			return false
		}
	}
	return true
}
