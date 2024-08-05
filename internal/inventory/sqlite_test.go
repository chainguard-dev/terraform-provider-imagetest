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

func TestSqliteBehavior(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	inv := testSqliteDb(t, dbPath)

	iId := rand.String(6)
	hId := rand.String(6)
	f1Id := rand.String(6)
	f2Id := rand.String(6)

	ctx := context.Background()

	// Add a harness (without first creating the inventory)
	err := inv.AddHarness(ctx, inventory.Harness{
		Id:          hId,
		InventoryId: iId,
	})
	assert.NoError(t, err, "failed to add harness")

	// Can add a duplicate harness (and inventory)
	err = inv.AddHarness(ctx, inventory.Harness{
		Id:          hId,
		InventoryId: iId,
	})
	assert.NoError(t, err, "failed to add duplicate harness")

	// Adding a harness without an inventory id should fail
	err = inv.AddHarness(ctx, inventory.Harness{
		Id: hId,
	})
	assert.ErrorContains(t, err, "constraint failed")

	// Add a feature
	err = inv.AddFeature(ctx, inventory.Feature{
		Id: f1Id,
		Harness: inventory.Harness{
			Id:          hId,
			InventoryId: iId,
		},
	})
	assert.NoError(t, err, "failed to add feature")

	// Can add a feature with a duplicate id
	err = inv.AddFeature(ctx, inventory.Feature{
		Id: f1Id,
		Harness: inventory.Harness{
			Id:          hId,
			InventoryId: iId,
		},
	})
	assert.NoError(t, err, "failed to add duplicate feature")

	// Add a feature without a harness id should fail
	err = inv.AddFeature(ctx, inventory.Feature{
		Id: f1Id,
	})
	assert.ErrorContains(t, err, "constraint failed")

	// Add a feature without an inventory id should fail
	err = inv.AddFeature(ctx, inventory.Feature{
		Id: f1Id,
		Harness: inventory.Harness{
			Id: hId,
		},
	})
	assert.ErrorContains(t, err, "constraint failed")

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

	// Remove the first feature
	err = inv.RemoveFeature(ctx, inventory.Feature{
		Id: f1Id,
		Harness: inventory.Harness{
			Id:          hId,
			InventoryId: iId,
		},
	})
	assert.NoError(t, err, "failed to remove feature")

	left, err := inv.ListFeatures(ctx, inventory.Harness{
		Id:          hId,
		InventoryId: iId,
	})
	assert.NoError(t, err, "failed to list features")
	assert.Len(t, left, 1, "expected 1 remaining feature")
	assert.Equal(t, f2Id, left[f2Id].Id, "expected feature id %s, got %s", f2Id, left[f2Id].Id)

	// Cannot remove a harness that has features
	err = inv.RemoveHarness(ctx, inventory.Harness{
		Id:          hId,
		InventoryId: iId,
	})
	assert.Error(t, err, "expected error removing harness with features")

	// Remove the second feature
	err = inv.RemoveFeature(ctx, inventory.Feature{
		Id: f2Id,
		Harness: inventory.Harness{
			Id:          hId,
			InventoryId: iId,
		},
	})
	assert.NoError(t, err, "failed to remove feature")

	left, err = inv.ListFeatures(ctx, inventory.Harness{
		Id:          hId,
		InventoryId: iId,
	})
	assert.NoError(t, err, "failed to list features")
	assert.Len(t, left, 0, "expected 0 remaining features")

	// Remove the harness
	err = inv.RemoveHarness(ctx, inventory.Harness{
		Id:          hId,
		InventoryId: iId,
	})
	assert.NoError(t, err, "failed to remove harness")
}

func TestSqliteConcurrent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	inv := testSqliteDb(t, dbPath)
	ctx := context.Background()

	var eg errgroup.Group
	for i := 0; i < 100; i++ {
		eg.Go(func() error {
			return inv.AddHarness(ctx, inventory.Harness{
				Id:          rand.String(6),
				InventoryId: rand.String(6),
			})
		})
	}

	if err := eg.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestSqliteExisting(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	inv1 := testSqliteDb(t, dbPath)

	ctx := context.Background()

	iId := rand.String(6)
	hId := rand.String(6)
	f1Id := rand.String(6)

	// Add a harness
	err := inv1.AddHarness(ctx, inventory.Harness{
		Id:          hId,
		InventoryId: iId,
	})
	assert.NoError(t, err, "failed to add harness")

	// Add a feature
	err = inv1.AddFeature(ctx, inventory.Feature{
		Id: f1Id,
		Harness: inventory.Harness{
			Id:          hId,
			InventoryId: iId,
		},
		Labels: map[string]string{
			"foo": "bar",
		},
	})
	assert.NoError(t, err, "failed to add feature")

	inv2 := testSqliteDb(t, dbPath)

	// List features
	features, err := inv2.ListFeatures(ctx, inventory.Harness{
		Id:          hId,
		InventoryId: iId,
	})
	assert.NoError(t, err, "failed to list features")
	assert.Len(t, features, 1, "expected 1 feature")
	assert.Equal(t, f1Id, features[f1Id].Id, "expected feature id %s, got %s", f1Id, features[f1Id].Id)

	// Remove the feature
	err = inv2.RemoveFeature(ctx, inventory.Feature{
		Id: f1Id,
		Harness: inventory.Harness{
			Id:          hId,
			InventoryId: iId,
		},
	})
	assert.NoError(t, err, "failed to remove feature")
}

func BenchmarkSqlite(b *testing.B) {
	dbPath := filepath.Join(b.TempDir(), "test.db")
	inv, err := inventory.NewSqlite(dbPath)
	assert.NoError(b, err, "failed to create test database")

	ctx := context.Background()

	// Add some data
	nh := 1000
	nf := 10
	harnesses := make([]inventory.Harness, nh)
	features := make([]inventory.Feature, nh*nf)
	for i := 0; i < nh; i++ {
		h := inventory.Harness{
			Id:          rand.String(8),
			InventoryId: rand.String(6),
		}
		err := inv.AddHarness(ctx, h)
		assert.NoError(b, err, "failed to add harness")
		harnesses[i] = h

		// Add some features
		for j := 0; j < nf; j++ {
			f := inventory.Feature{
				Id:      rand.String(10),
				Harness: h,
				Labels: map[string]string{
					"foo": "bar",
				},
			}

			err := inv.AddFeature(ctx, f)
			assert.NoError(b, err, "failed to add feature")
			features[i*nf+j] = f
		}
	}

	b.ResetTimer()

	ops := []struct {
		name string
		fn   func(pb *testing.PB)
	}{
		{
			name: "Add Harness",
			fn: func(pb *testing.PB) {
				for pb.Next() {
					h := inventory.Harness{
						// Need a slightly higher entropy here to ensure we don't get
						// collisions, this benchmark is more than we'll ever see in
						// reality
						Id:          rand.String(8),
						InventoryId: rand.String(6),
					}
					err := inv.AddHarness(ctx, h)
					assert.NoError(b, err, "failed to add harness")
				}
			},
		},
		{
			name: "Add Feature",
			fn: func(pb *testing.PB) {
				for pb.Next() {
					h := harnesses[rand.Intn(len(harnesses))]
					f := inventory.Feature{
						Id:      rand.String(10),
						Harness: h,
						Labels: map[string]string{
							"foo": "bar",
						},
					}
					err := inv.AddFeature(ctx, f)
					assert.NoError(b, err, "failed to add feature")
				}
			},
		},
		{
			name: "List Features",
			fn: func(pb *testing.PB) {
				for pb.Next() {
					h := harnesses[rand.Intn(len(harnesses))]
					_, err := inv.ListFeatures(ctx, h)
					assert.NoError(b, err, "failed to list features")
				}
			},
		},
		{
			name: "Remove Feature",
			fn: func(pb *testing.PB) {
				for pb.Next() {
					f := features[rand.Intn(len(features))]
					err := inv.RemoveFeature(ctx, f)
					assert.NoError(b, err, "failed to remove feature")
				}
			},
		},
	}

	for _, op := range ops {
		b.Run(op.name, func(b *testing.B) {
			b.ResetTimer()
			b.RunParallel(op.fn)
		})
	}
}

func testSqliteDb(t *testing.T, dsn string) inventory.Inventory {
	inv, err := inventory.NewSqlite(dsn)
	assert.NoError(t, err, "failed to create test database")

	return inv
}
