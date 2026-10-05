CREATE TABLE restaurant_staff (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  is_waiter BOOLEAN NOT NULL DEFAULT false,
  is_cashier BOOLEAN NOT NULL DEFAULT false,
  custom_title TEXT,
  is_active BOOLEAN NOT NULL DEFAULT true,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (id, restaurant_id)
);

ALTER TABLE restaurant_staff ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON restaurant_staff
  USING (restaurant_id IN (
    SELECT restaurant_id FROM restaurant_users
    WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'
  ))
  WITH CHECK (restaurant_id IN (
    SELECT restaurant_id FROM restaurant_users
    WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'
  ));

ALTER TABLE orders
  ADD COLUMN staff_id UUID REFERENCES restaurant_staff(id) ON DELETE SET NULL,
  ADD COLUMN discount_type TEXT CHECK (discount_type IN ('amount', 'percent')),
  ADD COLUMN discount_value INTEGER CHECK (discount_value >= 0),
  ADD CONSTRAINT orders_discount_value_valid CHECK (
    (discount_type IS NULL AND discount_value IS NULL) OR
    (discount_type = 'amount' AND discount_value IS NOT NULL) OR
    (discount_type = 'percent' AND discount_value BETWEEN 0 AND 10000)
  );

ALTER TABLE order_items
  ADD COLUMN discount_type TEXT CHECK (discount_type IN ('amount', 'percent')),
  ADD COLUMN discount_value INTEGER CHECK (discount_value >= 0),
  ADD COLUMN discount_amount INTEGER NOT NULL DEFAULT 0 CHECK (discount_amount >= 0),
  ADD CONSTRAINT order_items_discount_value_valid CHECK (
    (discount_type IS NULL AND discount_value IS NULL) OR
    (discount_type = 'amount' AND discount_value IS NOT NULL) OR
    (discount_type = 'percent' AND discount_value BETWEEN 0 AND 10000)
  );

ALTER TABLE order_item_price_adjustments
  ADD COLUMN discount_type TEXT CHECK (discount_type IN ('amount', 'percent')),
  ADD COLUMN discount_value INTEGER CHECK (discount_value >= 0);
CREATE TEMP TABLE duplicate_open_table_orders ON COMMIT DROP AS
SELECT id,
       first_value(id) OVER (PARTITION BY restaurant_id, table_id ORDER BY opened_at, id) AS canonical_id,
       count(*) OVER (PARTITION BY restaurant_id, table_id) AS group_count
FROM orders
WHERE status = 'open' AND table_id IS NOT NULL;

UPDATE order_items item
SET order_id = duplicates.canonical_id
FROM duplicate_open_table_orders duplicates
WHERE item.order_id = duplicates.id
  AND duplicates.id <> duplicates.canonical_id
  AND duplicates.group_count > 1;

UPDATE order_payments payment
SET order_id = duplicates.canonical_id
FROM duplicate_open_table_orders duplicates
WHERE payment.order_id = duplicates.id
  AND duplicates.id <> duplicates.canonical_id
  AND duplicates.group_count > 1;

UPDATE orders canonical
SET opened_at = merged.opened_at,
    subtotal = merged.subtotal,
    tax_amount = merged.tax_amount,
    discount_amount = merged.discount_amount,
    total_amount = merged.total_amount
FROM (
  SELECT duplicates.canonical_id,
         min(source.opened_at) AS opened_at,
         sum(source.subtotal) AS subtotal,
         sum(source.tax_amount) AS tax_amount,
         sum(source.discount_amount) AS discount_amount,
         sum(source.total_amount) AS total_amount
  FROM duplicate_open_table_orders duplicates
  JOIN orders source ON source.id = duplicates.id
  WHERE duplicates.group_count > 1
  GROUP BY duplicates.canonical_id
) merged
WHERE canonical.id = merged.canonical_id;

UPDATE orders duplicate
SET status = 'cancelled', closed_at = now(), subtotal = 0, tax_amount = 0,
    discount_amount = 0, total_amount = 0
FROM duplicate_open_table_orders mapping
WHERE duplicate.id = mapping.id
  AND mapping.id <> mapping.canonical_id
  AND mapping.group_count > 1;

CREATE TABLE order_discount_adjustments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  restaurant_id UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
  discount_type TEXT NOT NULL CHECK (discount_type IN ('amount', 'percent', 'cleared')),
  discount_value INTEGER NOT NULL CHECK (discount_value >= 0),
  discount_amount INTEGER NOT NULL CHECK (discount_amount >= 0),
  reason TEXT NOT NULL,
  adjusted_by UUID NOT NULL REFERENCES users(id),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE order_discount_adjustments ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON order_discount_adjustments
  USING (restaurant_id IN (
    SELECT restaurant_id FROM restaurant_users
    WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'
  ))
  WITH CHECK (restaurant_id IN (
    SELECT restaurant_id FROM restaurant_users
    WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'
  ));

CREATE INDEX idx_restaurant_staff_active ON restaurant_staff (restaurant_id, name) WHERE is_active = true;
CREATE INDEX idx_orders_open_table ON orders (restaurant_id, table_id, opened_at DESC) WHERE status = 'open';
CREATE UNIQUE INDEX one_open_order_per_table ON orders (restaurant_id, table_id)
  WHERE status = 'open' AND table_id IS NOT NULL;
CREATE INDEX idx_order_discount_adjustments_order ON order_discount_adjustments (order_id, created_at DESC);