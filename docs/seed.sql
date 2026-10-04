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
