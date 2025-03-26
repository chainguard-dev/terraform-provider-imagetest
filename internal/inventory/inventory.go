package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Inventory struct {
	base string
	mu   sync.RWMutex
}

type Harness struct {
	Id       string             `json:"id"`
	Features map[string]Feature `json:"features"`
}

type Feature struct {
	Id      string `json:"id"`
	Skipped string `json:"skipped"` // Either the reason for skipping or an empty string
}

func NewInventory(base string) (*Inventory, error) {
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create inventory base directory: %w", err)
	}

	return &Inventory{
		base: base,
		mu:   sync.RWMutex{},
	}, nil
}

// AddHarness creates a new harness with the given id. If the harness already exists this is a no-op.
func (i *Inventory) AddHarness(ctx context.Context, id string) error {
	hpath := i.harnessPath(id)

	if _, err := os.Stat(hpath); err == nil {
		return nil
	}

	f, err := os.OpenFile(hpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("failed to create harness: %w", err)
	}
	defer f.Close()

	fs := make(map[string]Feature)

	return json.NewEncoder(f).Encode(fs)
}

// AddFeature adds a feature to an existing harness. It returns an error if the harness does not exist.
func (i *Inventory) AddFeature(ctx context.Context, harness string, feature Feature) error {
	hpath := i.harnessPath(harness)
	if _, err := os.Stat(hpath); err != nil {
		return fmt.Errorf("harness [%s] does not exist at [%s]: base %s: %v", harness, hpath, i.base, err)
	}

	fs := make(map[string]Feature)

	i.mu.Lock()
	defer i.mu.Unlock()

	data, err := os.ReadFile(hpath)
	if err != nil {
		return fmt.Errorf("failed to read harness: %w", err)
	}

	if err := json.Unmarshal(data, &fs); err != nil {
		return fmt.Errorf("failed to unmarshal harness: %w", err)
	}

	fs[feature.Id] = feature

	fw, err := os.OpenFile(hpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open harness: %w", err)
	}
	defer fw.Close()

	if err := json.NewEncoder(fw).Encode(fs); err != nil {
		return fmt.Errorf("failed to encode harness: %w", err)
	}

	return nil
}

func (i *Inventory) GetFeatures(ctx context.Context, id string) (map[string]Feature, error) {
	hpath := i.harnessPath(id)

	if _, err := os.Stat(hpath); err != nil {
		return nil, fmt.Errorf("harness [%s] does not exist at [%s]: %v", id, hpath, err)
	}

	i.mu.RLock()
	defer i.mu.RUnlock()

	f, err := os.Open(hpath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fs := make(map[string]Feature)

	if err := json.NewDecoder(f).Decode(&fs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal harness: %v", err)
	}

	return fs, nil
}

func (i *Inventory) RemoveHarness(ctx context.Context, id string) error {
	hpath := i.harnessPath(id)

	i.mu.Lock()
	defer i.mu.Unlock()

	data, err := os.ReadFile(hpath)
	if err != nil {
		return fmt.Errorf("failed to read harness [%s]: %w", id, err)
	}

	fs := make(map[string]Feature)
	if err := json.Unmarshal(data, &fs); err != nil {
		return fmt.Errorf("failed to unmarshal harness [%s]: %w", id, err)
	}

	if len(fs) > 0 {
		return fmt.Errorf("cannot remove harness [%s]: harness contains features", id)
	}

	err = os.Remove(hpath)
	if err != nil {
		return fmt.Errorf("failed to remove harness [%s]: %w", id, err)
	}

	return nil
}

func (i *Inventory) RemoveFeature(ctx context.Context, harness string, id string) error {
	hpath := i.harnessPath(harness)

	i.mu.Lock()
	defer i.mu.Unlock()

	fs := make(map[string]Feature)

	data, err := os.ReadFile(hpath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("harness [%s] does not exist", harness)
		}
		return fmt.Errorf("failed to read harness [%s]: %w", harness, err)
	}

	if err := json.Unmarshal(data, &fs); err != nil {
		return fmt.Errorf("failed to unmarshal harness [%s]: %w", harness, err)
	}

	if _, exists := fs[id]; !exists {
		return fmt.Errorf("feature [%s] does not exist in harness [%s]", id, harness)
	}

	delete(fs, id)

	fw, err := os.OpenFile(hpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open harness [%s] for writing: %w", harness, err)
	}
	defer fw.Close()

	if err := json.NewEncoder(fw).Encode(fs); err != nil {
		return fmt.Errorf("failed to encode harness [%s]: %w", harness, err)
	}

	return nil
}

func (i *Inventory) harnessPath(id string) string {
	return filepath.Join(i.base, id+".json")
}
