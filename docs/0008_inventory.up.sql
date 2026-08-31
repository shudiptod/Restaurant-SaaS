CREATE TABLE IF NOT EXISTS inventory_items (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  restaurant_id       UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
  name                TEXT NOT NULL,
  unit                TEXT NOT NULL,
  current_quantity    NUMERIC(12,3) NOT NULL DEFAULT 0,
  reorder_threshold   NUMERIC(12,3),
  unit_cost           INTEGER,
  deleted_at          TIMESTAMPTZ,
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS inventory_adjustments (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  inventory_item_id   UUID NOT NULL REFERENCES inventory_items(id) ON DELETE CASCADE,
  restaurant_id       UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
  change_quantity     NUMERIC(12,3) NOT NULL,
  reason              TEXT NOT NULL CHECK (reason IN ('purchase','usage','wastage','correction','other')),
  note                TEXT,
  adjusted_by         UUID REFERENCES users(id),
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE inventory_items       ENABLE ROW LEVEL SECURITY;
ALTER TABLE inventory_adjustments ENABLE ROW LEVEL SECURITY;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies WHERE tablename = 'inventory_items' AND policyname = 'tenant_isolation'
  ) THEN
    CREATE POLICY tenant_isolation ON inventory_items
      USING (restaurant_id IN (SELECT restaurant_id FROM restaurant_users WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'));
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_policies WHERE tablename = 'inventory_adjustments' AND policyname = 'tenant_isolation'
  ) THEN
    CREATE POLICY tenant_isolation ON inventory_adjustments
      USING (restaurant_id IN (SELECT restaurant_id FROM restaurant_users WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'));
  END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_inventory_items_restaurant   ON inventory_items (restaurant_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_inventory_items_low_stock    ON inventory_items (restaurant_id) WHERE reorder_threshold IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_inventory_adj_item           ON inventory_adjustments (inventory_item_id);
CREATE INDEX IF NOT EXISTS idx_inventory_adj_restaurant_dt  ON inventory_adjustments (restaurant_id, created_at);
