package provider

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/chainguard-dev/clog"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/containers/provider"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/inventory"
	ilog "github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"github.com/chainguard-dev/terraform-provider-imagetest/internal/types"
	slogmulti "github.com/samber/slog-multi"
)

// ProviderStore manages the global runtime state of the provider. The provider
// uses this to look up the defined relationships between resources, and manage
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
	cli *provider.DockerClient
}

func NewProviderStore() *ProviderStore {
	return &ProviderStore{
		labels:    make(map[string]string),
		harnesses: newSmap[string, types.Harness](),
	}
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

// PortForwards returns the IMAGETEST_PORT_FORWARDS environment variable.
func (s *ProviderStore) PortForwards() []string {
	v, ok := os.LookupEnv("IMAGETEST_PORT_FORWARDS")
	if !ok || "" == v {
		return nil
	}
	return strings.Split(v, ",")
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
