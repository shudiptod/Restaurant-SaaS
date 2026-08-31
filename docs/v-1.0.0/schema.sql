-- Restaurant Management SaaS — PostgreSQL schema (v2)
-- Multi-tenant via shared schema + Row-Level Security (RLS)
-- See documentation.md for full reasoning behind every design decision below.

CREATE EXTENSION IF NOT EXISTS "pgcrypto"; -- for gen_random_uuid()

-- ============================================================
-- PLATFORM-LEVEL TABLES (not tenant-scoped, no RLS)
-- ============================================================

CREATE TABLE users (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email          TEXT UNIQUE NOT NULL,
  phone          TEXT UNIQUE,
  password_hash  TEXT NOT NULL,
  full_name      TEXT NOT NULL,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- You (and any future staff) — separate from account/restaurant-level roles.
-- This is what lets a platform admin lock ANY account or restaurant, and grant exceptions.
CREATE TYPE platform_role AS ENUM ('superadmin', 'support');

CREATE TABLE platform_admins (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id),
  role        platform_role NOT NULL DEFAULT 'superadmin',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Feature catalog: every togglable/limitable capability a plan can grant.
-- Numeric limits (max_restaurants, max_users_per_account) and boolean flags
-- (advanced_reports) both live here — same mechanism, dashboard-editable.
CREATE TABLE features (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  key         TEXT UNIQUE NOT NULL,
  name        TEXT NOT NULL,
  description TEXT,
  value_type  TEXT NOT NULL DEFAULT 'boolean' CHECK (value_type IN ('boolean','number','text')),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE subscription_plans (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name              TEXT NOT NULL,
  code              TEXT UNIQUE NOT NULL,       -- e.g. 'basic', 'pro', 'enterprise'
  price_amount      INTEGER NOT NULL,           -- in poisha (BDT minor unit)
  currency          TEXT NOT NULL DEFAULT 'BDT',
  billing_interval  TEXT NOT NULL DEFAULT 'monthly' CHECK (billing_interval IN ('monthly','yearly')),
  is_active         BOOLEAN NOT NULL DEFAULT true,
  sort_order        INTEGER NOT NULL DEFAULT 0,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Which features each plan includes and at what value/limit.
CREATE TABLE plan_features (
  plan_id     UUID NOT NULL REFERENCES subscription_plans(id) ON DELETE CASCADE,
  feature_id  UUID NOT NULL REFERENCES features(id) ON DELETE CASCADE,
  value       TEXT NOT NULL,
  PRIMARY KEY (plan_id, feature_id)
);

-- ============================================================
-- ACCOUNTS — the billing entity. An account can own multiple restaurants.
-- ============================================================

CREATE TYPE account_status AS ENUM ('active', 'locked', 'suspended', 'canceled');

CREATE TABLE accounts (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name           TEXT NOT NULL,               -- billing/business name, may differ from any single restaurant's name
  owner_user_id  UUID NOT NULL REFERENCES users(id),  -- the billing contact; only they see subscription/payment data
  status         account_status NOT NULL DEFAULT 'active',
  locked_reason  TEXT,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE account_status_log (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id  UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  changed_by  UUID REFERENCES users(id),   -- null if changed by an automated dunning job
  old_status  account_status,
  new_status  account_status NOT NULL,
  reason      TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Per-account exceptions: lets a platform admin grant a specific account extra
-- restaurants/users/features beyond what their plan normally allows, with a reason on record.
-- Resolution order when the app checks a limit: account_feature_overrides > plan_features.
CREATE TABLE account_feature_overrides (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id  UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  feature_id  UUID NOT NULL REFERENCES features(id) ON DELETE CASCADE,
  value       TEXT NOT NULL,
  reason      TEXT NOT NULL,               -- required — this is a manual exception, always justify it
  granted_by  UUID NOT NULL REFERENCES users(id),  -- must be a platform_admin
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (account_id, feature_id)
);

-- ============================================================
-- SUBSCRIPTIONS & BILLING (account-level)
-- ============================================================

CREATE TYPE subscription_status AS ENUM ('trialing','active','past_due','canceled');

CREATE TABLE account_subscriptions (
  id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id             UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  plan_id                UUID NOT NULL REFERENCES subscription_plans(id),
  status                 subscription_status NOT NULL DEFAULT 'trialing',
  current_period_start   TIMESTAMPTZ NOT NULL,
  current_period_end     TIMESTAMPTZ NOT NULL,
  canceled_at            TIMESTAMPTZ,
  created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX one_active_subscription_per_account
  ON account_subscriptions (account_id)
  WHERE status IN ('trialing','active','past_due');

CREATE TYPE payment_status AS ENUM ('pending','completed','failed','refunded');

CREATE TABLE payments (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id           UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  subscription_id      UUID REFERENCES account_subscriptions(id),
  amount               INTEGER NOT NULL,          -- poisha
  currency             TEXT NOT NULL DEFAULT 'BDT',
  provider             TEXT NOT NULL DEFAULT 'bkash',
  provider_payment_id  TEXT,                       -- bKash paymentID (create step)
  provider_trx_id      TEXT,                       -- bKash trxID (only set once completed)
  status               payment_status NOT NULL DEFAULT 'pending',
  raw_response         JSONB,                       -- full callback payload, for reconciliation/disputes
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  paid_at              TIMESTAMPTZ,
  UNIQUE (provider, provider_payment_id)             -- prevents double-processing the same bKash callback
);

-- ============================================================
-- RESTAURANTS (belong to an account)
-- ============================================================

CREATE TYPE restaurant_status AS ENUM ('active', 'locked');  -- manual, restaurant-specific override only;
                                                              -- billing-driven lockout is accounts.status

CREATE TABLE restaurants (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id     UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  name           TEXT NOT NULL,
  slug           TEXT UNIQUE NOT NULL,
  status         restaurant_status NOT NULL DEFAULT 'active',
  locked_reason  TEXT,
  timezone       TEXT NOT NULL DEFAULT 'Asia/Dhaka',  -- for correct daily report cutoffs
  address        TEXT,
  phone          TEXT,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE restaurant_status_log (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  restaurant_id  UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
  changed_by     UUID REFERENCES users(id),
  old_status     restaurant_status,
  new_status     restaurant_status NOT NULL,
  reason         TEXT,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TYPE restaurant_role AS ENUM ('owner', 'admin');

CREATE TABLE restaurant_users (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  restaurant_id  UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
  user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role           restaurant_role NOT NULL,
  status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','invited','disabled')),
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (restaurant_id, user_id)
);

-- ============================================================
-- CORE OPERATIONAL TABLES (restaurant-scoped)
-- ============================================================

CREATE TABLE tables (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  restaurant_id  UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
  name           TEXT NOT NULL,
  capacity       INTEGER,
  status         TEXT NOT NULL DEFAULT 'available' CHECK (status IN ('available','occupied','reserved')),
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE menu_categories (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  restaurant_id  UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
  name           TEXT NOT NULL,
  sort_order     INTEGER NOT NULL DEFAULT 0,
  deleted_at     TIMESTAMPTZ
);

CREATE TABLE menu_items (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  restaurant_id  UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
  category_id    UUID REFERENCES menu_categories(id),
  name           TEXT NOT NULL,
  description    TEXT,
  price          INTEGER NOT NULL,   -- poisha — the standard menu price
  is_available   BOOLEAN NOT NULL DEFAULT true,
  deleted_at     TIMESTAMPTZ,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TYPE order_status AS ENUM ('open','closed','cancelled');

CREATE TABLE orders (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  restaurant_id     UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
  table_id          UUID REFERENCES tables(id),
  status            order_status NOT NULL DEFAULT 'open',
  opened_by         UUID REFERENCES users(id),
  opened_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  closed_at         TIMESTAMPTZ,
  subtotal          INTEGER NOT NULL DEFAULT 0,
  tax_amount        INTEGER NOT NULL DEFAULT 0,
  discount_amount   INTEGER NOT NULL DEFAULT 0,
  total_amount      INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE order_items (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id       UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  menu_item_id   UUID NOT NULL REFERENCES menu_items(id),
  quantity       INTEGER NOT NULL DEFAULT 1,
  unit_price     INTEGER NOT NULL,   -- the price actually charged for this line (may be overridden — see below)
  notes          TEXT
);

-- Price-override audit trail. unit_price above always holds the CURRENT effective price;
-- every change to it — a manual discount, a comped item — is recorded here immutably,
-- separate from unit_price itself, so history survives even if overridden more than once.
CREATE TABLE order_item_price_adjustments (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_item_id   UUID NOT NULL REFERENCES order_items(id) ON DELETE CASCADE,
  restaurant_id   UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,  -- denormalized for RLS + reporting
  original_price  INTEGER NOT NULL,   -- unit_price immediately before this change
  adjusted_price  INTEGER NOT NULL,   -- unit_price immediately after this change
  reason          TEXT NOT NULL,       -- required — every override must be justified (e.g. "regular customer discount")
  adjusted_by     UUID NOT NULL REFERENCES users(id),
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Per-restaurant sequential invoice numbers (many tax regimes expect gapless, sequential numbering
-- per business — a single global auto-increment across all tenants won't satisfy that).
CREATE TABLE restaurant_invoice_counters (
  restaurant_id  UUID PRIMARY KEY REFERENCES restaurants(id) ON DELETE CASCADE,
  last_number    INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE invoices (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  restaurant_id    UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
  order_id         UUID NOT NULL REFERENCES orders(id),
  invoice_number   INTEGER NOT NULL,
  subtotal         INTEGER NOT NULL,
  tax_amount       INTEGER NOT NULL,
  discount_amount  INTEGER NOT NULL,
  total_amount     INTEGER NOT NULL,
  pdf_url          TEXT,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (restaurant_id, invoice_number)
);

-- ============================================================
-- ROW-LEVEL SECURITY
-- ============================================================
-- App sets:  SET app.current_user_id = '<uuid>';  once per request.
--
-- Restaurant-scoped tables (tables, menu, orders, invoices, ...) are visible when
-- the current user has an active restaurant_users membership for that restaurant —
-- this is what lets an owner's query span every restaurant they run with no filter.
--
-- Account-scoped tables (subscriptions, payments, overrides) are visible ONLY to the
-- account's owner_user_id — restaurant admins do not see billing data, by design.
--
-- Platform-admin dashboards use a separate DB role with BYPASSRLS.

ALTER TABLE restaurant_users              ENABLE ROW LEVEL SECURITY;
ALTER TABLE restaurant_status_log         ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_subscriptions         ENABLE ROW LEVEL SECURITY;
ALTER TABLE payments                      ENABLE ROW LEVEL SECURITY;
ALTER TABLE account_feature_overrides     ENABLE ROW LEVEL SECURITY;
ALTER TABLE tables                        ENABLE ROW LEVEL SECURITY;
ALTER TABLE menu_categories               ENABLE ROW LEVEL SECURITY;
ALTER TABLE menu_items                    ENABLE ROW LEVEL SECURITY;
ALTER TABLE orders                        ENABLE ROW LEVEL SECURITY;
ALTER TABLE order_items                   ENABLE ROW LEVEL SECURITY;
ALTER TABLE order_item_price_adjustments  ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoices                      ENABLE ROW LEVEL SECURITY;
ALTER TABLE restaurant_invoice_counters   ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON restaurant_users
  USING (restaurant_id IN (
    SELECT ru.restaurant_id FROM restaurant_users ru
    WHERE ru.user_id = current_setting('app.current_user_id')::uuid AND ru.status = 'active'
  ));
CREATE POLICY tenant_isolation ON restaurant_status_log
  USING (restaurant_id IN (
    SELECT restaurant_id FROM restaurant_users
    WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'
  ));
CREATE POLICY tenant_isolation ON tables
  USING (restaurant_id IN (
    SELECT restaurant_id FROM restaurant_users
    WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'
  ));
CREATE POLICY tenant_isolation ON menu_categories
  USING (restaurant_id IN (
    SELECT restaurant_id FROM restaurant_users
    WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'
  ));
CREATE POLICY tenant_isolation ON menu_items
  USING (restaurant_id IN (
    SELECT restaurant_id FROM restaurant_users
    WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'
  ));
CREATE POLICY tenant_isolation ON orders
  USING (restaurant_id IN (
    SELECT restaurant_id FROM restaurant_users
    WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'
  ));
CREATE POLICY tenant_isolation ON order_item_price_adjustments
  USING (restaurant_id IN (
    SELECT restaurant_id FROM restaurant_users
    WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'
  ));
CREATE POLICY tenant_isolation ON invoices
  USING (restaurant_id IN (
    SELECT restaurant_id FROM restaurant_users
    WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'
  ));
CREATE POLICY tenant_isolation ON restaurant_invoice_counters
  USING (restaurant_id IN (
    SELECT restaurant_id FROM restaurant_users
    WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'
  ));
CREATE POLICY tenant_isolation ON order_items
  USING (order_id IN (
    SELECT id FROM orders WHERE restaurant_id IN (
      SELECT restaurant_id FROM restaurant_users
      WHERE user_id = current_setting('app.current_user_id')::uuid AND status = 'active'
    )
  ));

-- Billing data: account owner only
CREATE POLICY owner_only ON account_subscriptions
  USING (account_id IN (SELECT id FROM accounts WHERE owner_user_id = current_setting('app.current_user_id')::uuid));
CREATE POLICY owner_only ON payments
  USING (account_id IN (SELECT id FROM accounts WHERE owner_user_id = current_setting('app.current_user_id')::uuid));
CREATE POLICY owner_only ON account_feature_overrides
  USING (account_id IN (SELECT id FROM accounts WHERE owner_user_id = current_setting('app.current_user_id')::uuid));

-- Helpful indexes
CREATE INDEX idx_restaurant_users_user_active ON restaurant_users (user_id) WHERE status = 'active';
CREATE INDEX idx_restaurants_account          ON restaurants (account_id);
CREATE INDEX idx_orders_restaurant_status     ON orders (restaurant_id, status);
CREATE INDEX idx_menu_items_restaurant        ON menu_items (restaurant_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_payments_account_status      ON payments (account_id, status);
CREATE INDEX idx_price_adj_order_item         ON order_item_price_adjustments (order_item_id);

-- ============================================================
-- SEED: suggested feature keys (see documentation.md for full reasoning)
-- ============================================================
INSERT INTO features (key, name, value_type) VALUES
  ('max_restaurants',        'Maximum restaurants',              'number'),
  ('max_users_per_account',  'Maximum staff logins',              'number'),
  ('max_tables_per_restaurant', 'Maximum tables per restaurant',  'number'),
  ('reports_level',          'Reporting tier (basic/advanced)',   'text'),
  ('consolidated_reports',   'Cross-restaurant consolidated view','boolean'),
  ('invoice_branding',       'Custom logo on invoices',           'boolean'),
  ('data_export',            'CSV/Excel export',                  'boolean'),
  ('priority_support',       'Priority support channel',          'boolean');

-- ============================================================
-- TAX SETTINGS & CUSTOMER PAYMENT RECORDING (added: reports/filters + Mushak/VAT review)
-- ============================================================

CREATE TABLE restaurant_tax_settings (
  restaurant_id            UUID PRIMARY KEY REFERENCES restaurants(id) ON DELETE CASCADE,
  vat_registration_number  TEXT,
  vat_rate_bps             INTEGER NOT NULL DEFAULT 1500,
  vat_inclusive            BOOLEAN NOT NULL DEFAULT true,
  service_charge_rate_bps  INTEGER NOT NULL DEFAULT 0,
  updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE order_payments (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id       UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  restaurant_id  UUID NOT NULL REFERENCES restaurants(id) ON DELETE CASCADE,
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

CREATE INDEX idx_order_payments_order        ON order_payments (order_id);
CREATE INDEX idx_order_payments_restaurant   ON order_payments (restaurant_id, method);
CREATE INDEX idx_orders_restaurant_closed_at ON orders (restaurant_id, closed_at);
CREATE INDEX idx_order_items_order           ON order_items (order_id);
CREATE INDEX idx_order_items_menu_item       ON order_items (menu_item_id);
