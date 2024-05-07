package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/chainguard-dev/clog"
	log2 "github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	slogmulti "github.com/samber/slog-multi"
)

var _ Inventory = &file{}

type file struct {
	id      string
	mu      sync.Mutex
	path    string
	logFile *os.File
}

func NewFile(path string, id string) Inventory {
	return &file{path: path, id: id}
}

// Create implements Inventory.
func (i *file) Create(ctx context.Context) error {
	data := InventoryModel{}

	// initialize map beforehand so the remainder of the operations remain the same
	data.HarnessFeatures = make(HarnessFeatureMapping)

	return i.write(ctx, &data)
}

// Open implements I.
func (*file) Open(_ context.Context) error {
	panic("open")
}

func (i *file) GetId() string {
	return i.id
}

// AddFeature implements Inventory. it returns true if the feature was added,
// false if it already exists.
func (i *file) AddFeature(ctx context.Context, f Feature) (bool, error) {
	data, err := i.read(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to read inventory: %w", err)
	}

	// add the feature only if it doesn't exist
	for _, feat := range data.HarnessFeatures[f.Harness] {
		if feat.Id == f.Id {
			return false, nil
		}
	}

	data.HarnessFeatures[f.Harness] = append(data.HarnessFeatures[f.Harness], f)
	if err := i.write(ctx, data); err != nil {
		return false, fmt.Errorf("inventory write error")
	}

	return true, nil
}

// AddHarness implements Inventory.
func (i *file) AddHarness(ctx context.Context, id Harness) (bool, error) {
	data, err := i.read(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to read inventory: %w", err)
	}

	if _, ok := data.HarnessFeatures[id]; ok {
		return false, nil
	}

	data.HarnessFeatures[id] = []Feature{}

	if err := i.write(ctx, data); err != nil {
		return false, fmt.Errorf("failed to write inventory: %w", err)
	}

	return true, nil
}

// GetFeatures implements I.
func (i *file) GetFeatures(ctx context.Context, id Harness) ([]Feature, error) {
	data, err := i.read(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read inventory: %w", err)
	}

	features, ok := data.HarnessFeatures[id]
	if !ok {
		return nil, fmt.Errorf("harness [%s] does not exist", id)
	}

	return features, nil
}

// RemoveFeature implements Inventory.
func (i *file) RemoveFeature(ctx context.Context, f Feature) ([]Feature, error) {
	data, err := i.read(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read inventory: %w", err)
	}

	features, ok := data.HarnessFeatures[f.Harness]
	if !ok {
		return nil, fmt.Errorf("harness [%s] does not exist", f.Harness)
	}

	// remove the feature only if it exists
	for idx, feature := range features {
		if f.Id == feature.Id {
			// match
			if idx == len(features)-1 {
				features = features[:idx]
			} else {
				features = append(features[:idx], features[idx+1:]...)
			}
			break
		}
	}

	data.HarnessFeatures[f.Harness] = features
	if err := i.write(ctx, data); err != nil {
		return nil, fmt.Errorf("failed to write inventory: %w", err)
	}

	return features, nil
}

// RemoveHarness implements Inventory.
func (i *file) RemoveHarness(ctx context.Context, h Harness) error {
	data, err := i.read(ctx)
	if err != nil {
		return fmt.Errorf("failed to read inventory: %w", err)
	}

	if _, ok := data.HarnessFeatures[h]; !ok {
		return fmt.Errorf("harness [%s] does not exist", h)
	}

	// error if the harness has features
	if len(data.HarnessFeatures[h]) > 0 {
		return fmt.Errorf("harness [%s] has features", h)
	}

	delete(data.HarnessFeatures, h)
	return i.write(ctx, data)
}

func (i *file) read(_ context.Context) (*InventoryModel, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	f, err := os.Open(i.path)
	if err != nil {
		return nil, fmt.Errorf("inventory open error")
	}
	defer func() {
		err := f.Close()
		if err != nil {
			panic(fmt.Errorf("failed to open inventory file: %w", err))
		}
	}()

	var inv *InventoryModel
	if err := json.NewDecoder(f).Decode(&inv); err != nil {
		return nil, fmt.Errorf("inventory decode error")
	}

	return inv, nil
}

func (i *file) write(_ context.Context, data *InventoryModel) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	f, err := os.OpenFile(i.path, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("inventory open error")
	}
	defer func() {
		err := f.Close()
		if err != nil {
			panic(fmt.Errorf("failed to close inventory file: %w", err))
		}
	}()

	if err := json.NewEncoder(f).Encode(data); err != nil {
		return fmt.Errorf("inventory encode error")
	}

	return nil
}

func (i *file) Logger(ctx context.Context) (context.Context, error) {
	var err error

	id := i.GetId()
	i.logFile, err = os.OpenFile(fmt.Sprintf("tf-imagetest-%s.log", id), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return ctx, fmt.Errorf("failed to create logfile: %w", err)
	}

	logger := clog.New(slogmulti.Fanout(
		&log2.TFHandler{},
		slog.NewTextHandler(i.logFile, &slog.HandlerOptions{}),
	))
	ctx = clog.WithLogger(ctx, logger)

	return ctx, nil
}
