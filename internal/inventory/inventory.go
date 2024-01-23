package inventory

import (
	"context"
)

type Inventory interface {
	Create(ctx context.Context) error
	Open(ctx context.Context) error
	AddHarness(ctx context.Context, id Harness) (bool, error)
	AddFeature(ctx context.Context, f Feature) (bool, error)
	GetFeatures(ctx context.Context, id Harness) ([]Feature, error)
	RemoveHarness(ctx context.Context, h Harness) error
	RemoveFeature(ctx context.Context, f Feature) ([]Feature, error)
}

type Harness string

type Feature struct {
	Id      string            `json:"id"`
	Labels  map[string]string `json:"labels"`
	Harness Harness           `json:"harness"`
}

type InventoryModel map[Harness][]Feature
