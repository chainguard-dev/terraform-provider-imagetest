-- Add a new inventory
-- name: AddInventory :exec
INSERT INTO inventories (id) VALUES (?)
ON CONFLICT(id) DO NOTHING;

-- Remove an inventory
-- name: RemoveInventory :exec
DELETE FROM inventories WHERE id = ?;

-- Add a new harness
-- name: AddHarness :exec
INSERT INTO harnesses (id, inventory_id) VALUES (?, ?)
ON CONFLICT(id, inventory_id) DO NOTHING;

-- Remove a harness
-- name: RemoveHarness :exec
DELETE FROM harnesses
WHERE id = ? AND inventory_id = ?;

-- Add a new feature
-- name: AddFeature :exec
INSERT INTO features (id, harness_id, inventory_id, labels) VALUES (?, ?, ?, ?)
ON CONFLICT(id, harness_id, inventory_id) DO NOTHING;

-- Remove a feature
-- name: RemoveFeature :exec
DELETE FROM features
WHERE id = ? AND harness_id = ? AND inventory_id = ?;

-- List all features for a given harness
-- name: ListFeatures :many
SELECT id, harness_id, inventory_id, labels
FROM features
WHERE harness_id = ? AND inventory_id = ?;
