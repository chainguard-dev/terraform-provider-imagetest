package inventory

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/log"
	"go.etcd.io/bbolt"
)

type bolt struct {
	path string
}

func NewBolt(path string) (Inventory, error) {
	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open inventory database: %w", err)
	}
	defer db.Close()

	return &bolt{path: path}, nil
}

// AddFeature implements Inv.
func (b *bolt) AddFeature(ctx context.Context, f Feature) error {
	log.Info(ctx, "adding feature to inventory", "feature", f)

	if err := b.AddHarness(ctx, f.Harness); err != nil {
		return fmt.Errorf("failed to add harness to inventory: %w", err)
	}

	db, err := b.client()
	if err != nil {
		return fmt.Errorf("failed to open inventory database: %w", err)
	}
	defer db.Close()

	if err := db.Update(func(tx *bbolt.Tx) error {
		ib := tx.Bucket([]byte(f.Harness.InventoryId))
		if ib == nil {
			return fmt.Errorf("inventory bucket not found")
		}

		// Get the harness
		hraw := ib.Get([]byte(f.Harness.Id))
		if hraw == nil {
			return fmt.Errorf("harness not found in inventory")
		}

		var h Harness
		if err := json.Unmarshal(hraw, &h); err != nil {
			return fmt.Errorf("failed to unmarshal harness: %w", err)
		}

		if h.Features == nil {
			h.Features = make(map[string]Feature)
		}
		h.Features[f.Id] = f

		raw, err := json.Marshal(h)
		if err != nil {
			return fmt.Errorf("failed to marshal harness: %w", err)
		}

		return ib.Put([]byte(f.Harness.Id), raw)
	}); err != nil {
		return fmt.Errorf("failed to add feature to inventory: %w", err)
	}

	return nil
}

// AddHarness implements Inv.
func (b *bolt) AddHarness(ctx context.Context, h Harness) error {
	log.Info(ctx, "adding harness to inventory", "harness", h)

	db, err := b.client()
	if err != nil {
		return fmt.Errorf("failed to open inventory database: %w", err)
	}
	defer db.Close()

	if err := db.Update(func(tx *bbolt.Tx) error {
		ib, err := tx.CreateBucketIfNotExists([]byte(h.InventoryId))
		if err != nil {
			return fmt.Errorf("failed to create inventory bucket: %w", err)
		}

		// Only add the harness if it doesn't exist
		if ib.Get([]byte(h.Id)) == nil {
			return ib.Put([]byte(h.Id), []byte(`{}`))
		}

		return nil
	}); err != nil {
		return fmt.Errorf("failed to add harness to inventory: %w", err)
	}

	return nil
}

// ListFeatures implements Inv.
func (b *bolt) ListFeatures(ctx context.Context, h Harness) (map[string]Feature, error) {
	log.Info(ctx, "listing features for harness", "harness", h.Id)

	feats := make(map[string]Feature)

	db, err := b.client()
	if err != nil {
		return nil, fmt.Errorf("failed to open inventory database: %w", err)
	}
	defer db.Close()

	if err := db.Update(func(tx *bbolt.Tx) error {
		ib := tx.Bucket([]byte(h.InventoryId))
		if ib == nil {
			return fmt.Errorf("inventory bucket not found")
		}

		h, err := b.getHarness(ib, h.Id)
		if err != nil {
			return fmt.Errorf("failed to get harness: %w", err)
		}

		feats = h.Features

		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to list features for harness: %w", err)
	}

	return feats, nil
}

// RemoveFeature implements Inv.
func (b *bolt) RemoveFeature(ctx context.Context, f Feature) (map[string]Feature, error) {
	log.Info(ctx, "removing feature from inventory", "feature", f.Id)

	feats := make(map[string]Feature)

	db, err := b.client()
	if err != nil {
		return nil, fmt.Errorf("failed to open inventory database: %w", err)
	}
	defer db.Close()

	if err := db.Update(func(tx *bbolt.Tx) error {
		ib := tx.Bucket([]byte(f.Harness.InventoryId))
		if ib == nil {
			return fmt.Errorf("inventory bucket not found")
		}

		h, err := b.getHarness(ib, f.Harness.Id)
		if err != nil {
			return fmt.Errorf("failed to get harness: %w", err)
		}

		delete(h.Features, f.Id)

		raw, err := json.Marshal(h)
		if err != nil {
			return fmt.Errorf("failed to marshal harness: %w", err)
		}

		if err := ib.Put([]byte(f.Harness.Id), raw); err != nil {
			return fmt.Errorf("failed to update harness: %w", err)
		}

		feats = h.Features

		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to remove feature from inventory: %w", err)
	}

	return feats, nil
}

// RemoveHarness implements Inv.
func (b *bolt) RemoveHarness(ctx context.Context, h Harness) error {
	log.Info(ctx, "removing harness from inventory", "harness", h.Id)

	db, err := b.client()
	if err != nil {
		return fmt.Errorf("failed to open inventory database: %w", err)
	}
	defer db.Close()

	if err := db.Update(func(tx *bbolt.Tx) error {
		ib := tx.Bucket([]byte(h.InventoryId))
		if ib == nil {
			return fmt.Errorf("inventory bucket not found")
		}

		existing, err := b.getHarness(ib, h.Id)
		if err != nil {
			return fmt.Errorf("failed to get harness: %w", err)
		}

		// Check if the harness has features
		if len(existing.Features) > 0 {
			return fmt.Errorf("harness has features")
		}

		return ib.Delete([]byte(h.Id))
	}); err != nil {
		return fmt.Errorf("failed to remove harness from inventory: %w", err)
	}

	return nil
}

func (b *bolt) client() (*bbolt.DB, error) {
	return bbolt.Open(b.path, 0600, nil)
}

func (b *bolt) getHarness(bkt *bbolt.Bucket, id string) (Harness, error) {
	raw := bkt.Get([]byte(id))
	if raw == nil {
		return Harness{}, fmt.Errorf("harness not found")
	}

	var harness Harness
	if err := json.Unmarshal(raw, &harness); err != nil {
		return Harness{}, fmt.Errorf("failed to unmarshal harness: %w", err)
	}

	return harness, nil
}
