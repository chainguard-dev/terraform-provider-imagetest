PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
PRAGMA temp_store = MEMORY;

CREATE TABLE IF NOT EXISTS inventories (
    id TEXT PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS harnesses (
    id TEXT NOT NULL,
    inventory_id TEXT NOT NULL CHECK(inventory_id != ''),
    PRIMARY KEY (id, inventory_id),
    -- Ensure we can't delete inventories that still reference harnesses
    FOREIGN KEY (inventory_id) REFERENCES inventories (id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS features (
    id TEXT NOT NULL,
    harness_id TEXT NOT NULL CHECK(harness_id != ''),
    inventory_id TEXT NOT NULL CHECK(inventory_id != ''),
    labels JSON,
    PRIMARY KEY (id, harness_id, inventory_id),
    -- Ensure we can't delete harnesses that still reference features
    FOREIGN KEY (harness_id, inventory_id) REFERENCES harnesses (id, inventory_id) ON DELETE RESTRICT
);

-- Create indexes that optimize for the read heavy queries we have
CREATE INDEX IF NOT EXISTS idx_harnesses_inventory_id ON harnesses(inventory_id);
CREATE INDEX IF NOT EXISTS idx_features_harness_id_inventory_id ON features(harness_id, inventory_id);
CREATE INDEX IF NOT EXISTS idx_features_id_harness_id_inventory_id ON features(id, harness_id, inventory_id);
