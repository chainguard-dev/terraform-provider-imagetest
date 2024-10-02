package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
)

var _ Inventory = &file{}

type file struct {
	mu   sync.Mutex
	path string
}

func NewFile(path string) Inventory {
	return &file{path: path}
}

// Create implements Inventory.
func (i *file) Create(ctx context.Context) error {
	data := InventoryModel{}
	return i.write(ctx, data)
}

// Open implements I.
func (*file) Open(_ context.Context) error {
	panic("open")
}

// AddFeature implements [Inventory]. It returns true if the feature was added,
// false if it already exists.
func (i *file) AddFeature(ctx context.Context, f Feature) (bool, error) {
	data, err := i.read(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to read inventory: %w", err)
	}

	// add the feature only if it doesn't exist
	for _, feat := range data[f.Harness] {
		if feat.Id == f.Id {
			return false, nil
		}
	}

	data[f.Harness] = append(data[f.Harness], f)
	if err := i.write(ctx, data); err != nil {
		return false, fmt.Errorf("inventory write error")
	}

	return true, nil
}

// AddHarness implements [Inventory].
func (i *file) AddHarness(ctx context.Context, id Harness) (bool, error) {
	data, err := i.read(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to read inventory: %w", err)
	}

	if _, ok := data[id]; ok {
		return false, nil
	}

	data[id] = []Feature{}

	if err := i.write(ctx, data); err != nil {
		return false, fmt.Errorf("failed to write inventory: %w", err)
	}

	return true, nil
}

// GetFeatures implements [Inventory].
func (i *file) GetFeatures(ctx context.Context, id Harness) ([]Feature, error) {
	data, err := i.read(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read inventory: %w", err)
	}

	features, ok := data[id]
	if !ok {
		return nil, fmt.Errorf("harness [%s] does not exist", id)
	}

	return features, nil
}

// RemoveFeature implements [Inventory].
func (i *file) RemoveFeature(ctx context.Context, f Feature) ([]Feature, error) {
	data, err := i.read(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read inventory: %w", err)
	}

	features, ok := data[f.Harness]
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

	data[f.Harness] = features
	if err := i.write(ctx, data); err != nil {
		return nil, fmt.Errorf("failed to write inventory: %w", err)
	}

	return features, nil
}

// RemoveHarness implements [Inventory].
func (i *file) RemoveHarness(ctx context.Context, h Harness) error {
	data, err := i.read(ctx)
	if err != nil {
		return fmt.Errorf("failed to read inventory: %w", err)
	}

	if _, ok := data[h]; !ok {
		return fmt.Errorf("harness [%s] does not exist", h)
	}

	// error if the harness has features
	if len(data[h]) > 0 {
		return fmt.Errorf("harness [%s] has features", h)
	}

	delete(data, h)
	return i.write(ctx, data)
}

func (i *file) read(ctx context.Context) (InventoryModel, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	f, err := os.Open(i.path)
	if err != nil {
		return nil, fmt.Errorf("inventory open error: %w", err)
	}
	defer func(ctx context.Context) {
		err := f.Close()
		if err != nil {
			log.Warn(ctx, "failed to open inventory file", "error", err)
		}
	}(ctx)

	var inv InventoryModel
	if err := json.NewDecoder(f).Decode(&inv); err != nil {
		return nil, fmt.Errorf("inventory decode error")
	}

	return inv, nil
}

func (i *file) write(ctx context.Context, data InventoryModel) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	f, err := os.OpenFile(i.path, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("inventory open error: %w", err)
	}
	defer func(ctx context.Context) {
		err := f.Close()
		if err != nil {
			log.Warn(ctx, "failed to close inventory file", "error", err)
		}
	}(ctx)

	if err := json.NewEncoder(f).Encode(data); err != nil {
		return fmt.Errorf("inventory encode error")
	}

	return nil
}
