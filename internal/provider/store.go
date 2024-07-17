package provider

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path"
	"sync"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/harness"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/inventory"
	ilog "github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	slogmulti "github.com/samber/slog-multi"
)

// ProviderStore manages the global runtime state of the provider. The provider
// uses this to look up the defined relationships between resources, and manage
// shared external state.
type ProviderStore struct {
	// harnesses stores a map of the available harnesses, keyed by their ID.
	harnesses *mmap[string, harness.Harness]
	labels    map[string]string
	// providerResourceData stores the data for the provider resource.
	// TODO: there's probably a way to do this without passing around the whole
	// model
	providerResourceData ImageTestProviderModel

	// cli is the Docker client. it is initialized once during the providers
	// Configure() stage and reused for any resource that requires it.
	cli *provider.DockerClient

	inv inventory.Inventory
}

func NewProviderStore() *ProviderStore {
	return &ProviderStore{
		labels: make(map[string]string),
		harnesses: &mmap[string, harness.Harness]{
			store: make(map[string]harness.Harness),
			mu:    sync.Mutex{},
		},
	}
}

func (s *ProviderStore) AddHarness(id string, harness harness.Harness) {
	s.harnesses.Set(id, harness)
}

func (s *ProviderStore) GetHarness(id string) (harness.Harness, bool) {
	h, ok := s.harnesses.Get(id)
	return h, ok
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
	if s.providerResourceData.Log == nil {
		return ctx, nil
	}

	if lf := s.providerResourceData.Log.File; lf != nil {
		ihash, err := s.Encode(inv.Seed.ValueString())
		if err != nil {
			return ctx, fmt.Errorf("failed to encode inventory hash: %w", err)
		}
		logpath := fmt.Sprintf("%s.log", ihash)

		if dir := lf.Directory.ValueString(); dir != "" {
			if err := os.MkdirAll(dir, os.ModePerm); err != nil {
				return ctx, fmt.Errorf("failed to create log directory: %w", err)
			}
			logpath = path.Join(dir, logpath)
		}

		f, err := os.OpenFile(logpath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return ctx, fmt.Errorf("failed to create logfile: %w", err)
		}

		var fhandler slog.Handler
		switch lf.Format.ValueString() {
		case "text":
			fhandler = slog.NewTextHandler(f, &slog.HandlerOptions{})
		default:
			fhandler = slog.NewJSONHandler(f, &slog.HandlerOptions{})
		}

		logger := clog.New(slogmulti.Fanout(
			&ilog.TFHandler{},
			fhandler,
		)).With("inventory", ihash)

		ctx = clog.WithLogger(ctx, logger)
	}

	logger := clog.FromContext(ctx).With(withs...)
	ctx = clog.WithLogger(ctx, logger)

	return ctx, nil
}

// SkipTeardown returns true if the IMAGETEST_SKIP_TEARDOWN environment
// variable is declared.
func (s *ProviderStore) SkipTeardown() bool {
	v := os.Getenv("IMAGETEST_SKIP_TEARDOWN")
	return v != ""
}

func (s *ProviderStore) EnableDebugLogging() bool {
	const EnvTrue = "true"

	ghaRunnerDebug, found := os.LookupEnv("ACTIONS_RUNNER_DEBUG")
	if found {
		return EnvTrue == ghaRunnerDebug
	}

	ghaStepDebug, found := os.LookupEnv("ACTIONS_STEP_DEBUG")
	if found {
		return EnvTrue == ghaStepDebug
	}

	localDebug, found := os.LookupEnv("IMAGETEST_DEBUG_OUTPUT")
	if found {
		return EnvTrue == localDebug
	}

	return false
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
