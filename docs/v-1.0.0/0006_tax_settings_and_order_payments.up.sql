-- Per-restaurant tax/VAT (Mushak) configuration
CREATE TABLE restaurant_tax_settings (
  restaurant_id            UUID PRIMARY KEY REFERENCES restaurants(id) ON DELETE CASCADE,
  vat_registration_number  TEXT,                          -- NBR-issued BIN, shown on invoices once set
  vat_rate_bps             INTEGER NOT NULL DEFAULT 1500,  -- basis points; 1500 = 15.00% (standard BD VAT). Integer, not float — same convention as money.
  vat_inclusive            BOOLEAN NOT NULL DEFAULT true,  -- true: menu price already includes VAT. false: VAT added on top at checkout.
  service_charge_rate_bps  INTEGER NOT NULL DEFAULT 0,     -- optional service charge, tracked separately from VAT
  updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- How the CUSTOMER actually paid for an order (cash/card/mobile banking) —
-- distinct from `payments`, which is the restaurant's SaaS subscription billing.
-- One row per tender; multiple rows support split payments (part cash, part card).
CREATE TABLE order_payments (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id       UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  restaurant_id  UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,  -- denormalized for RLS + reporting
  method         TEXT NOT NULL CHECK (method IN ('cash','card','bkash_personal','nagad','rocket','bank_transfer','other')),
  amount         INTEGER NOT NULL,
  received_by    UUID REFERENCES users(id),
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE restaurant_tax_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE order_payments          ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON restaurant_tax_settings
  USING (restaurant_id IN (SELECT restaurant_id FROM restaurant_users WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'));
CREATE POLICY tenant_isolation ON order_payments
  USING (restaurant_id IN (SELECT restaurant_id FROM restaurant_users WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'));

-- Indexes for reporting filters (date range, per-item, per-payment-method)
CREATE INDEX idx_order_payments_order        ON order_payments (order_id);
CREATE INDEX idx_order_payments_restaurant   ON order_payments (restaurant_id, method);
CREATE INDEX idx_orders_restaurant_closed_at ON orders (restaurant_id, closed_at);
CREATE INDEX idx_order_items_order           ON order_items (order_id);
CREATE INDEX idx_order_items_menu_item       ON order_items (menu_item_id);
