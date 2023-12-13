package provider

import (
	"os"
	"strings"
	"sync"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/envs"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	"github.com/google/uuid"
)

// ProviderStore manages the global runtime state of the provider. The provider
// uses this to lookup the defined relationships between resources, and manage
// shared external state (such as open ports).
type ProviderStore struct {
	// ports stores the available free ports usable by the provider
	ports *envs.Ports
	// harnesses stores a map of the available harnesses, keyed by their ID.
	harnesses *smap[string, types.Harness]
	// features stores a map of the available features, keyed by their ID.
	features *smap[string, FeatureResourceModel]
	// labels holds a map of labels set at runtime used to filter environments/features
	labels Labels
}

func NewProviderStore() *ProviderStore {
	return &ProviderStore{
		ports:     envs.NewFreePort(),
		harnesses: newSmap[string, types.Harness](),
		features:  newSmap[string, FeatureResourceModel](),
		labels:    newLabels(),
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

// Match takes a map of labels and returns true if all of the given labels are present in the map
func (ls Labels) Match(matches map[string]string) bool {
	for k, v := range ls {
		if matches[k] != v {
			return false
		}
	}
	return true
}
