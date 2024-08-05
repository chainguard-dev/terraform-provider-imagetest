package inventory

import (
	"context"
	"sync"
)

type Inventory interface {
	AddHarness(context.Context, Harness) error
	RemoveHarness(context.Context, Harness) error
	AddFeature(context.Context, Feature) error
	RemoveFeature(context.Context, Feature) error
	ListFeatures(context.Context, Harness) (map[string]Feature, error)
}

type Harness struct {
	Id          string             `json:"id"`
	InventoryId string             `json:"inventory_id"`
	Features    map[string]Feature `json:"features"`
}

type Feature struct {
	Harness Harness `json:"harness"`
	Id      string  `json:"id"`
	Labels  Labels  `json:"labels"`
}

type Labels map[string]string

type Inventories struct {
	items  map[string]Inventory
	mu     sync.RWMutex
	create InventoryCreate
}

type InventoryCreate func(id string) (Inventory, error)

func NewInventories(create InventoryCreate) *Inventories {
	return &Inventories{
		items:  make(map[string]Inventory),
		mu:     sync.RWMutex{},
		create: create,
	}
}

func (i *Inventories) Get(id string) (Inventory, error) {
	i.mu.RLock()
	item, exists := i.items[id]
	i.mu.RUnlock()

	if exists {
		return item, nil
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	if inv, exists := i.items[id]; exists {
		return inv, nil
	}

	inv, err := i.create(id)
	if err != nil {
		return nil, err
	}

	i.items[id] = inv
	return inv, nil
}
