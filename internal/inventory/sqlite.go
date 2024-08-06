package inventory

import (
	"context"
	"database/sql"
	"database/sql/driver"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/inventory/sqlc"
)

//go:embed sqlc/schema.sql
var ddl string

type sqlite struct {
	db *sql.DB
	q  *sqlc.Queries
}

func NewSqlite(path string) (Inventory, error) {
	opts := url.Values{}
	opts.Set("_journal_mode", "WAL")
	opts.Set("_busy_timeout", "5000")

	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?%s", path, opts.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to open inventory database: %w", err)
	}

	if _, err := db.Exec(ddl); err != nil {
		return nil, fmt.Errorf("failed to open inventory database: %w", err)
	}

	db.SetMaxOpenConns(1)

	return &sqlite{
		db: db,
		q:  sqlc.New(db),
	}, nil
}

// AddHarness implements Inventory.
func (s *sqlite) AddHarness(ctx context.Context, h Harness) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	//nolint:errcheck
	defer tx.Rollback()

	qtx := s.q.WithTx(tx)

	// Create the inventory
	if err := qtx.AddInventory(ctx, h.InventoryId); err != nil {
		return fmt.Errorf("failed to add inventory: %w", err)
	}

	if err := qtx.AddHarness(ctx, sqlc.AddHarnessParams{
		ID:          h.Id,
		InventoryID: h.InventoryId,
	}); err != nil {
		return fmt.Errorf("failed to add harness: %w", err)
	}

	return tx.Commit()
}

// RemoveHarness implements Inventory.
func (s *sqlite) RemoveHarness(ctx context.Context, h Harness) error {
	return s.q.RemoveHarness(ctx, sqlc.RemoveHarnessParams{
		ID:          h.Id,
		InventoryID: h.InventoryId,
	})
}

// AddFeature implements Inventory.
func (s *sqlite) AddFeature(ctx context.Context, f Feature) error {
	return s.q.AddFeature(ctx, sqlc.AddFeatureParams{
		ID:          f.Id,
		InventoryID: f.Harness.InventoryId,
		HarnessID:   f.Harness.Id,
		Labels:      f.Labels,
	})
}

// ListFeatures implements Inventory.
func (s *sqlite) ListFeatures(ctx context.Context, h Harness) (map[string]Feature, error) {
	rf, err := s.q.ListFeatures(ctx, sqlc.ListFeaturesParams{
		HarnessID:   h.Id,
		InventoryID: h.InventoryId,
	})
	if err != nil {
		return nil, err
	}

	features := make(map[string]Feature, len(rf))
	for _, row := range rf {
		features[row.ID] = Feature{
			Id: row.ID,
			Harness: Harness{
				Id:          row.HarnessID,
				InventoryId: h.InventoryId,
			},
			// Labels: map[string]string(row.Labels),
		}
	}

	return features, nil
}

// RemoveFeature implements Inventory.
func (s *sqlite) RemoveFeature(ctx context.Context, f Feature) error {
	return s.q.RemoveFeature(ctx, sqlc.RemoveFeatureParams{
		ID:          f.Id,
		HarnessID:   f.Harness.Id,
		InventoryID: f.Harness.InventoryId,
	})
}

func (l Labels) Value() (driver.Value, error) {
	return json.Marshal(l)
}

func (l *Labels) Scan(src interface{}) error {
	source, ok := src.([]byte)
	if !ok {
		return fmt.Errorf("failed to unmarshal labels")
	}
	return json.Unmarshal(source, l)
}
