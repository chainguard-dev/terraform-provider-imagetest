package inventory_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/inventory"
	"github.com/stretchr/testify/assert"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/util/rand"
)

func TestBolt(t *testing.T) {
	inv := testDb(t, filepath.Join(t.TempDir(), "inventory.db"))

	iId := rand.String(6)
	hId := rand.String(6)
	f1Id := rand.String(6)
	f2Id := rand.String(6)

	ctx := context.Background()

	// Add a harness
	err := inv.AddHarness(ctx, inventory.Harness{
		Id:          hId,
		InventoryId: iId,
	})
	assert.NoError(t, err, "failed to add harness")

	// Adding a harness without an inventory id should fail
	err = inv.AddHarness(ctx, inventory.Harness{
		Id: hId,
	})
	assert.Error(t, err, "expected error adding harness without inventory id")

	// Add a feature
	err = inv.AddFeature(ctx, inventory.Feature{
		Id: f1Id,
		Harness: inventory.Harness{
			Id:          hId,
			InventoryId: iId,
		},
	})
	assert.NoError(t, err, "failed to add feature")

	// Adding a feature without an inventory id should fail
	err = inv.AddFeature(ctx, inventory.Feature{
		Id: f1Id,
		Harness: inventory.Harness{
			Id: hId,
		},
	})
	assert.Error(t, err, "expected error adding feature without inventory id")

	// Adding a feature without a harness id should fail
	err = inv.AddFeature(ctx, inventory.Feature{
		Id: f1Id,
	})
	assert.Error(t, err, "expected error adding feature without harness id")

	// Add another feature
	err = inv.AddFeature(ctx, inventory.Feature{
		Id: f2Id,
		Harness: inventory.Harness{
			Id:          hId,
			InventoryId: iId,
		},
	})
	assert.NoError(t, err, "failed to add feature")

	// Validate what we've got
	features, err := inv.ListFeatures(ctx, inventory.Harness{
		Id:          hId,
		InventoryId: iId,
	})
	assert.NoError(t, err, "failed to list features")

	assert.Len(t, features, 2, "expected 2 features")

	assert.Equal(t, f1Id, features[f1Id].Id, "expected feature id %s, got %s", f1Id, features[f1Id].Id)
	assert.Equal(t, f2Id, features[f2Id].Id, "expected feature id %s, got %s", f2Id, features[f2Id].Id)

	// Remove the first feature
	remaining, err := inv.RemoveFeature(ctx, inventory.Feature{
		Id: f1Id,
		Harness: inventory.Harness{
			Id:          hId,
			InventoryId: iId,
		},
	})
	assert.NoError(t, err, "failed to remove feature")

	assert.Len(t, remaining, 1, "expected 1 remaining feature")
	assert.Equal(t, f2Id, remaining[f2Id].Id, "expected feature id %s, got %s", f2Id, remaining[f2Id].Id)

	// Cannot remove a harness that has features
	err = inv.RemoveHarness(ctx, inventory.Harness{
		Id:          hId,
		InventoryId: iId,
	})
	assert.Error(t, err, "expected error removing harness with features")

	// Remove the second feature
	remaining, err = inv.RemoveFeature(ctx, inventory.Feature{
		Id: f2Id,
		Harness: inventory.Harness{
			Id:          hId,
			InventoryId: iId,
		},
	})
	assert.NoError(t, err, "failed to remove feature")

	assert.Len(t, remaining, 0, "expected 0 remaining features")

	// Remove the harness
	err = inv.RemoveHarness(ctx, inventory.Harness{
		Id:          hId,
		InventoryId: iId,
	})
	assert.NoError(t, err, "failed to remove harness")
}

// TestBoltConcurrent tests that multiple inventories can be created
// concurrently against the same inventory backend.
func TestBoltConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	inv := testDb(t, path)

	var eg errgroup.Group
	for i := 0; i < 100; i++ {
		eg.Go(func() error {
			return inv.AddHarness(context.Background(), inventory.Harness{
				Id:          rand.String(6),
				InventoryId: rand.String(6),
			})
		})
	}

	if err := eg.Wait(); err != nil {
		t.Fatal(err)
	}
}

func testDb(t *testing.T, path string) inventory.Inventory {
	inv, err := inventory.NewBolt(path)
	assert.NoError(t, err, "failed to create test database")

	return inv
}
