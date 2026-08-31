ALTER TABLE restaurant_users ENABLE ROW LEVEL SECURITY;
ALTER TABLE restaurant_status_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE payments ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_feature_overrides ENABLE ROW LEVEL SECURITY;
ALTER TABLE tables ENABLE ROW LEVEL SECURITY;
ALTER TABLE menu_categories ENABLE ROW LEVEL SECURITY;
ALTER TABLE menu_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE order_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE order_item_price_adjustments ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoices ENABLE ROW LEVEL SECURITY;
ALTER TABLE restaurant_invoice_counters ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON restaurant_users
  USING (restaurant_id IN (SELECT ru.restaurant_id FROM restaurant_users ru WHERE ru.user_id = current_setting('app.current_user_id')::uuid AND ru.status = 'active'));
CREATE POLICY tenant_isolation ON restaurant_status_log
  USING (restaurant_id IN (SELECT restaurant_id FROM restaurant_users WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'));
CREATE POLICY tenant_isolation ON tables
  USING (restaurant_id IN (SELECT restaurant_id FROM restaurant_users WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'));
CREATE POLICY tenant_isolation ON menu_categories
  USING (restaurant_id IN (SELECT restaurant_id FROM restaurant_users WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'));
CREATE POLICY tenant_isolation ON menu_items
  USING (restaurant_id IN (SELECT restaurant_id FROM restaurant_users WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'));
CREATE POLICY tenant_isolation ON orders
  USING (restaurant_id IN (SELECT restaurant_id FROM restaurant_users WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'));
CREATE POLICY tenant_isolation ON order_item_price_adjustments
  USING (restaurant_id IN (SELECT restaurant_id FROM restaurant_users WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'));
CREATE POLICY tenant_isolation ON invoices
  USING (restaurant_id IN (SELECT restaurant_id FROM restaurant_users WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'));
CREATE POLICY tenant_isolation ON restaurant_invoice_counters
  USING (restaurant_id IN (SELECT restaurant_id FROM restaurant_users WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'));
CREATE POLICY tenant_isolation ON order_items
  USING (order_id IN (SELECT id FROM orders WHERE restaurant_id IN (SELECT restaurant_id FROM restaurant_users WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active')));

CREATE POLICY owner_only ON account_subscriptions
  USING (account_id IN (SELECT id FROM accounts WHERE owner_user_id = current_setting('app.current_user_id')::uuid));
CREATE POLICY owner_only ON payments
  USING (account_id IN (SELECT id FROM accounts WHERE owner_user_id = current_setting('app.current_user_id')::uuid));
CREATE POLICY owner_only ON account_feature_overrides
  USING (account_id IN (SELECT id FROM accounts WHERE owner_user_id = current_setting('app.current_user_id')::uuid));

CREATE INDEX idx_restaurant_users_user_active ON restaurant_users (user_id) WHERE status = 'active';
CREATE INDEX idx_restaurants_account ON restaurants (account_id);
CREATE INDEX idx_orders_restaurant_status ON orders (restaurant_id, status);
CREATE INDEX idx_menu_items_restaurant ON menu_items (restaurant_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_payments_account_status ON payments (account_id, status);
CREATE INDEX idx_price_adj_order_item ON order_item_price_adjustments (order_item_id);
