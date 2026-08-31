-- Reference data: safe to run in any environment (dev, staging, prod).
-- Idempotent via ON CONFLICT — re-running this file does not duplicate rows.

INSERT INTO features (key, name, description, value_type) VALUES
  ('max_restaurants',           'Maximum restaurants',               'How many restaurants an account can add', 'number'),
  ('max_users_per_account',     'Maximum staff logins',               'Total staff logins across all the account''s restaurants', 'number'),
  ('max_tables_per_restaurant', 'Maximum tables per restaurant',      'Cap on tables per location', 'number'),
  ('reports_level',             'Reporting tier',                     'basic or advanced analytics', 'text'),
  ('consolidated_reports',      'Cross-restaurant consolidated view', 'Rollup reporting across an account''s restaurants', 'boolean'),
  ('invoice_branding',          'Custom logo on invoices',            NULL, 'boolean'),
  ('data_export',               'CSV/Excel export',                   NULL, 'boolean'),
  ('priority_support',          'Priority support channel',           NULL, 'boolean')
ON CONFLICT (key) DO NOTHING;

INSERT INTO subscription_plans (name, code, price_amount, billing_interval, sort_order) VALUES
  ('Basic',      'basic',      99900,  'monthly', 1),   -- 999.00 BDT/mo (amounts in poisha)
  ('Pro',        'pro',        249900, 'monthly', 2),   -- 2499.00 BDT/mo
  ('Enterprise', 'enterprise', 799900, 'monthly', 3)     -- 7999.00 BDT/mo — placeholder, expect custom pricing per deal
ON CONFLICT (code) DO NOTHING;

-- Plan -> feature values. Adjust freely from the dashboard once built; these are starting defaults.
INSERT INTO plan_features (plan_id, feature_id, value)
SELECT p.id, f.id, v.value FROM (VALUES
  ('basic',      'max_restaurants',           '1'),
  ('basic',      'max_users_per_account',     '3'),
  ('basic',      'max_tables_per_restaurant', '15'),
  ('basic',      'reports_level',             'basic'),
  ('basic',      'consolidated_reports',      'false'),
  ('basic',      'invoice_branding',          'false'),
  ('basic',      'data_export',               'false'),
  ('basic',      'priority_support',          'false'),

  ('pro',        'max_restaurants',           '5'),
  ('pro',        'max_users_per_account',     '20'),
  ('pro',        'max_tables_per_restaurant', '50'),
  ('pro',        'reports_level',             'advanced'),
  ('pro',        'consolidated_reports',      'true'),
  ('pro',        'invoice_branding',          'true'),
  ('pro',        'data_export',               'true'),
  ('pro',        'priority_support',          'false'),

  ('enterprise', 'max_restaurants',           '1000'),   -- effectively unlimited; a real number keeps the limit check uniform
  ('enterprise', 'max_users_per_account',     '1000'),
  ('enterprise', 'max_tables_per_restaurant', '1000'),
  ('enterprise', 'reports_level',             'advanced'),
  ('enterprise', 'consolidated_reports',      'true'),
  ('enterprise', 'invoice_branding',          'true'),
  ('enterprise', 'data_export',               'true'),
  ('enterprise', 'priority_support',          'true')
) AS v(plan_code, feature_key, value)
JOIN subscription_plans p ON p.code = v.plan_code
JOIN features f ON f.key = v.feature_key
ON CONFLICT (plan_id, feature_id) DO UPDATE SET value = EXCLUDED.value;

-- ============================================================
-- DEV-ONLY FIXTURES — do not run in production.
-- Gives you one platform admin, one paying account with two restaurants,
-- enough to exercise the multi-restaurant owner view and RLS immediately.
-- ============================================================

INSERT INTO users (id, email, password_hash, full_name) VALUES
  ('00000000-0000-0000-0000-000000000001', 'admin@example.com',  'REPLACE_WITH_REAL_HASH', 'Platform Admin'),
  ('00000000-0000-0000-0000-000000000002', 'owner@example.com',  'REPLACE_WITH_REAL_HASH', 'Demo Owner')
ON CONFLICT (id) DO NOTHING;

INSERT INTO platform_admins (user_id, role) VALUES
  ('00000000-0000-0000-0000-000000000001', 'superadmin')
ON CONFLICT DO NOTHING;

INSERT INTO accounts (id, name, owner_user_id) VALUES
  ('00000000-0000-0000-0000-0000000000a1', 'Demo Restaurants Group', '00000000-0000-0000-0000-000000000002')
ON CONFLICT (id) DO NOTHING;

INSERT INTO account_subscriptions (account_id, plan_id, status, current_period_start, current_period_end)
SELECT '00000000-0000-0000-0000-0000000000a1', id, 'active', now(), now() + interval '30 days'
FROM subscription_plans WHERE code = 'pro'
ON CONFLICT DO NOTHING;

INSERT INTO restaurants (id, account_id, name, slug) VALUES
  ('00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-0000000000a1', 'Demo Restaurant — Gulshan',  'demo-gulshan'),
  ('00000000-0000-0000-0000-0000000000b2', '00000000-0000-0000-0000-0000000000a1', 'Demo Restaurant — Banani',   'demo-banani')
ON CONFLICT (id) DO NOTHING;

INSERT INTO restaurant_users (restaurant_id, user_id, role) VALUES
  ('00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-000000000002', 'owner'),
  ('00000000-0000-0000-0000-0000000000b2', '00000000-0000-0000-0000-000000000002', 'owner')
ON CONFLICT DO NOTHING;

-- Default tax settings for the demo restaurants (dev fixtures)
INSERT INTO restaurant_tax_settings (restaurant_id, vat_rate_bps, vat_inclusive)
VALUES
  ('00000000-0000-0000-0000-0000000000b1', 1500, true),
  ('00000000-0000-0000-0000-0000000000b2', 1500, true)
ON CONFLICT (restaurant_id) DO NOTHING;

-- Example inventory items for the demo restaurant (dev fixtures)
INSERT INTO inventory_items (restaurant_id, name, unit, current_quantity, reorder_threshold)
VALUES
  ('00000000-0000-0000-0000-0000000000b1', 'Rice', 'kg', 40, 10),
  ('00000000-0000-0000-0000-0000000000b1', 'Cooking oil', 'l', 8, 5)
ON CONFLICT DO NOTHING;
