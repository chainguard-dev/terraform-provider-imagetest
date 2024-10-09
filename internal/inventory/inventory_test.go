package inventory_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/chainguard-dev/terraform-provider-imagetest/internal/inventory"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/sync/errgroup"
)

func TestNewInventory(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		wantErr bool
	}{
		{
			name: "valid base",
			base: t.TempDir(),
		},
		{
			name:    "invalid base",
			base:    "/invali d/path",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := inventory.NewInventory(tt.base)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewInventory() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestInventory_AddHarness(t *testing.T) {
	ctx := context.Background()
	inv := tinv(t)

	tests := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{
			name: "add valid harness",
			id:   "foo",
		},
		{
			name: "can add harness that already exists",
			id:   "foo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := inv.AddHarness(ctx, tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("AddHarness() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestInventory_AddFeature(t *testing.T) {
	ctx := context.Background()
	inv := tinv(t)

	h1 := "foo"
	if err := inv.AddHarness(ctx, h1); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		harness string
		feature inventory.Feature
		wantErr bool
	}{
		{
			name:    "add valid feature",
			harness: h1,
			feature: inventory.Feature{
				Id: "bar",
			},
		},
		{
			name:    "add feature to non-existent harness",
			harness: "foobear",
			feature: inventory.Feature{
				Id: "bar",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := inv.AddFeature(ctx, tt.harness, tt.feature)
			if (err != nil) != tt.wantErr {
				t.Errorf("AddFeature() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestInventory_GetFeatures(t *testing.T) {
	ctx := context.Background()
	inv := tinv(t)

	// Create harness and feature for the first test case
	h1 := "foo"
	err := inv.AddHarness(ctx, h1)
	if err != nil {
		t.Fatalf("Failed to add harness: %v", err)
	}
	f1 := inventory.Feature{Id: "bar"}
	err = inv.AddFeature(ctx, h1, f1)
	if err != nil {
		t.Fatalf("Failed to add feature: %v", err)
	}

	tests := []struct {
		name    string
		harness string
		want    map[string]inventory.Feature
		wantErr bool
	}{
		{
			name:    "get features",
			harness: h1,
			want: map[string]inventory.Feature{
				"bar": f1,
			},
		},
		{
			name:    "get features for non-existent harness",
			harness: "foobear",
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := inv.GetFeatures(ctx, tt.harness)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetFeatures() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if diff := cmp.Diff(tt.want, got); diff != "" {
					t.Errorf("GetFeatures() mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestInventory_RemoveHarness(t *testing.T) {
	ctx := context.Background()
	inv := tinv(t)

	h1 := "foo"
	err := inv.AddHarness(ctx, h1)
	if err != nil {
		t.Fatalf("Failed to add harness: %v", err)
	}

	tests := []struct {
		name    string
		harness string
		wantErr bool
	}{
		{
			name:    "remove harness",
			harness: h1,
		},
		{
			name:    "remove non-existent harness",
			harness: "foobear",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err = inv.RemoveHarness(context.Background(), tc.harness)
			if (err != nil) != tc.wantErr {
				t.Errorf("RemoveHarness() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestInventory_RemoveFeature(t *testing.T) {
	ctx := context.Background()
	inv := tinv(t)

	// Create harness and feature for the first test case
	h1 := "foo"
	err := inv.AddHarness(ctx, h1)
	if err != nil {
		t.Fatalf("Failed to add harness: %v", err)
	}
	f1 := inventory.Feature{Id: "bar"}
	err = inv.AddFeature(ctx, h1, f1)
	if err != nil {
		t.Fatalf("Failed to add feature: %v", err)
	}

	tests := []struct {
		name    string
		harness string
		feature string
		wantErr bool
	}{
		{
			name:    "remove feature",
			harness: h1,
			feature: "bar",
		},
		{
			name:    "remove non-existent feature",
			harness: h1,
			feature: "baz",
			wantErr: true,
		},
		{
			name:    "remove feature from non-existent harness",
			harness: "foobear",
			feature: "bar",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err = inv.RemoveFeature(context.Background(), tc.harness, tc.feature)
			if (err != nil) != tc.wantErr {
				t.Errorf("RemoveFeature() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestInventory_Concurrency(t *testing.T) {
	inv := tinv(t)
	ctx := context.Background()

	// Pick something on the order of images
	numHarnesses := 1000
	numFeaturesPerHarness := 10

	g, gctx := errgroup.WithContext(ctx)

	for i := 0; i < numHarnesses; i++ {
		i := i
		g.Go(func() error {
			return inv.AddHarness(gctx, fmt.Sprintf("harness-%d", i))
		})
	}

	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}

	// Create features for each harness
	g, gctx = errgroup.WithContext(ctx)
	for i := 0; i < numHarnesses; i++ {
		i := i
		g.Go(func() error {
			innerG, innerCtx := errgroup.WithContext(gctx)
			for j := 0; j < numFeaturesPerHarness; j++ {
				j := j
				innerG.Go(func() error {
					return inv.AddFeature(innerCtx, fmt.Sprintf("harness-%d", i), inventory.Feature{
						Id:      fmt.Sprintf("feature-%d-%d", i, j),
						Skipped: "",
					})
				})
			}
			return innerG.Wait()
		})
	}

	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}

	// Check if all features were added properly
	for i := 0; i < numHarnesses; i++ {
		harnessID := fmt.Sprintf("harness-%d", i)
		features, err := inv.GetFeatures(ctx, harnessID)
		if err != nil {
			t.Fatalf("Failed to get features for harness %s: %v", harnessID, err)
		}
		if len(features) != numFeaturesPerHarness {
			t.Errorf("Expected %d features for harness %s, got %d", numFeaturesPerHarness, harnessID, len(features))
		}
		for j := 0; j < numFeaturesPerHarness; j++ {
			featureID := fmt.Sprintf("feature-%d-%d", i, j)
			if _, exists := features[featureID]; !exists {
				t.Errorf("Feature %s not found in harness %s", featureID, harnessID)
			}
		}
	}
}

func tinv(t *testing.T) *inventory.Inventory {
	inv, err := inventory.NewInventory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return inv
}
