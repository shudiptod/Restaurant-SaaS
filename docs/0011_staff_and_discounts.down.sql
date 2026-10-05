DROP INDEX IF EXISTS idx_orders_open_table;
DROP INDEX IF EXISTS one_open_order_per_table;
DROP INDEX IF EXISTS idx_restaurant_staff_active;
DROP INDEX IF EXISTS idx_order_discount_adjustments_order;
DROP TABLE IF EXISTS order_discount_adjustments;

ALTER TABLE order_item_price_adjustments
  DROP COLUMN discount_value,
  DROP COLUMN discount_type;

ALTER TABLE order_items
  DROP COLUMN discount_amount,
  DROP COLUMN discount_value,
  DROP COLUMN discount_type;

ALTER TABLE orders
  DROP COLUMN discount_value,
  DROP COLUMN discount_type,
  DROP COLUMN staff_id;

DROP TABLE restaurant_staff;