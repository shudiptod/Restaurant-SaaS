-- Open inventory: restaurant defines any item it wants to track, no fixed taxonomy,
-- no link to menu_items/recipes required. Stock is tracked via an audit trail of
-- adjustments (stock in, usage, wastage, correction) rather than free-editing a count directly,
-- so "why is the count what it is" is always answerable — same audit principle as D12 (price overrides).

CREATE TABLE inventory_items (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  restaurant_id       UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
  name                TEXT NOT NULL,               -- free text — restaurant can add any item
  unit                TEXT NOT NULL,                -- free text unit: 'kg', 'l', 'pcs', 'pack', etc.
  current_quantity    NUMERIC(12,3) NOT NULL DEFAULT 0,  -- maintained by the app as adjustments are recorded (see inventory_adjustments)
  reorder_threshold   NUMERIC(12,3),                -- nullable; current_quantity <= this = low stock
  unit_cost           INTEGER,                       -- poisha, nullable, optional — enables inventory valuation in reports
  deleted_at          TIMESTAMPTZ,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE inventory_adjustments (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  inventory_item_id   UUID NOT NULL REFERENCES inventory_items(id) ON DELETE CASCADE,
  restaurant_id       UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,  -- denormalized for RLS + reporting
  change_quantity     NUMERIC(12,3) NOT NULL,   -- positive = stock in (purchase/correction up), negative = stock out (usage/wastage/correction down)
  reason              TEXT NOT NULL CHECK (reason IN ('purchase','usage','wastage','correction','other')),
  note                TEXT,
  adjusted_by         UUID REFERENCES users(id),
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE inventory_items       ENABLE ROW LEVEL SECURITY;
ALTER TABLE inventory_adjustments ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON inventory_items
  USING (restaurant_id IN (SELECT restaurant_id FROM restaurant_users WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'));
CREATE POLICY tenant_isolation ON inventory_adjustments
  USING (restaurant_id IN (SELECT restaurant_id FROM restaurant_users WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'));

CREATE INDEX idx_inventory_items_restaurant   ON inventory_items (restaurant_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_inventory_items_low_stock    ON inventory_items (restaurant_id) WHERE reorder_threshold IS NOT NULL;
CREATE INDEX idx_inventory_adj_item           ON inventory_adjustments (inventory_item_id);
CREATE INDEX idx_inventory_adj_restaurant_dt  ON inventory_adjustments (restaurant_id, created_at);
