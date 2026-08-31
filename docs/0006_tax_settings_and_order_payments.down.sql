DROP INDEX IF EXISTS idx_order_items_menu_item;
DROP INDEX IF EXISTS idx_order_items_order;
DROP INDEX IF EXISTS idx_orders_restaurant_closed_at;
DROP INDEX IF EXISTS idx_order_payments_restaurant;
DROP INDEX IF EXISTS idx_order_payments_order;

DROP POLICY IF EXISTS tenant_isolation ON order_payments;
DROP POLICY IF EXISTS tenant_isolation ON restaurant_tax_settings;

DROP TABLE IF EXISTS order_payments;
DROP TABLE IF EXISTS restaurant_tax_settings;
