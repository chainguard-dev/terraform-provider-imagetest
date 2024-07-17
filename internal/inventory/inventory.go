package inventory

import "context"

type Inventory interface {
	AddHarness(context.Context, Harness) error
	RemoveHarness(context.Context, Harness) error
	AddFeature(context.Context, Feature) error
	RemoveFeature(context.Context, Feature) (map[string]Feature, error)
	ListFeatures(context.Context, Harness) (map[string]Feature, error)
}

type Harness struct {
	Id          string             `json:"id"`
	InventoryId string             `json:"inventory_id"`
	Features    map[string]Feature `json:"features"`
}

type Feature struct {
	Harness Harness           `json:"harness"`
	Id      string            `json:"id"`
	Labels  map[string]string `json:"labels"`
}
