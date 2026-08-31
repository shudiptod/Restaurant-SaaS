DROP INDEX IF EXISTS idx_inventory_adj_restaurant_dt;
DROP INDEX IF EXISTS idx_inventory_adj_item;
DROP INDEX IF EXISTS idx_inventory_items_low_stock;
DROP INDEX IF EXISTS idx_inventory_items_restaurant;

DROP POLICY IF EXISTS tenant_isolation ON inventory_adjustments;
DROP POLICY IF EXISTS tenant_isolation ON inventory_items;

DROP TABLE IF EXISTS inventory_adjustments;
DROP TABLE IF EXISTS inventory_items;
