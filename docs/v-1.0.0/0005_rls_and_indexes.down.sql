DROP INDEX IF EXISTS idx_price_adj_order_item;
DROP INDEX IF EXISTS idx_payments_account_status;
DROP INDEX IF EXISTS idx_menu_items_restaurant;
DROP INDEX IF EXISTS idx_orders_restaurant_status;
DROP INDEX IF EXISTS idx_restaurants_account;
DROP INDEX IF EXISTS idx_restaurant_users_user_active;

DROP POLICY IF EXISTS owner_only ON account_feature_overrides;
DROP POLICY IF EXISTS owner_only ON payments;
DROP POLICY IF EXISTS owner_only ON account_subscriptions;

DROP POLICY IF EXISTS tenant_isolation ON order_items;
DROP POLICY IF EXISTS tenant_isolation ON restaurant_invoice_counters;
DROP POLICY IF EXISTS tenant_isolation ON invoices;
DROP POLICY IF EXISTS tenant_isolation ON order_item_price_adjustments;
DROP POLICY IF EXISTS tenant_isolation ON orders;
DROP POLICY IF EXISTS tenant_isolation ON menu_items;
DROP POLICY IF EXISTS tenant_isolation ON menu_categories;
DROP POLICY IF EXISTS tenant_isolation ON tables;
DROP POLICY IF EXISTS tenant_isolation ON restaurant_status_log;
DROP POLICY IF EXISTS tenant_isolation ON restaurant_users;

ALTER TABLE restaurant_invoice_counters DISABLE ROW LEVEL SECURITY;
ALTER TABLE invoices DISABLE ROW LEVEL SECURITY;
ALTER TABLE order_item_price_adjustments DISABLE ROW LEVEL SECURITY;
ALTER TABLE order_items DISABLE ROW LEVEL SECURITY;
ALTER TABLE orders DISABLE ROW LEVEL SECURITY;
ALTER TABLE menu_items DISABLE ROW LEVEL SECURITY;
ALTER TABLE menu_categories DISABLE ROW LEVEL SECURITY;
ALTER TABLE tables DISABLE ROW LEVEL SECURITY;
ALTER TABLE account_feature_overrides DISABLE ROW LEVEL SECURITY;
ALTER TABLE payments DISABLE ROW LEVEL SECURITY;
ALTER TABLE account_subscriptions DISABLE ROW LEVEL SECURITY;
ALTER TABLE restaurant_status_log DISABLE ROW LEVEL SECURITY;
ALTER TABLE restaurant_users DISABLE ROW LEVEL SECURITY;
