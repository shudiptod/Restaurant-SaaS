# Restaurant Management SaaS — Project Planner & Full Context

**Purpose of this file**: this is the single source of truth for the project. Any AI agent (or human) picking up work with zero prior context should read this file fully before writing code or making a decision. It carries product intent, business decisions, architecture reasoning, current build status, and the backlog — not just "what exists" but "why," so decisions aren't silently reversed or duplicated. **Update this file whenever a new decision is made or a milestone is completed** — treat it as living documentation, not a one-time handoff note.

---

## 1. What this product is

A multi-tenant SaaS for local restaurants: tables, menu, order-taking, invoicing, reports. Sold on subscription. Target: 1,000+ restaurants eventually; realistic near-term expectation is slow growth (may take a while to reach even 20). Infra cost minimization is a hard constraint on every decision, not a nice-to-have.

The founder (owner of this business) has strong JS/TypeScript/Node/AWS-serverless expertise but deliberately chose Go for this product to keep it cheap to run — this trade-off (less personal fluency, lower cost/ops burden) was made consciously and should not be second-guessed by a future agent without new information.

Real-time kitchen order display was explicitly ruled OUT of scope: most target restaurants just relay orders verbally to the kitchen. This decision removed the need for WebSockets/live-state infrastructure and is why the stack can stay a simple server-rendered app. **Do not add real-time infra unless this requirement changes.**

---

## 2. Confirmed decisions (do not relitigate without new input)

| #   | Decision                                                                                                                                                                                                                                                                                       | Why                                                                                                                                                                                                                                                                                                                                                              |
| --- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| D1  | Backend: Go + Gin                                                                                                                                                                                                                                                                              | Cheap to run, low resource footprint, fits VPS hosting well                                                                                                                                                                                                                                                                                                      |
| D2  | Frontend: server-rendered Go templates + htmx                                                                                                                                                                                                                                                  | No real-time requirement (see above) means no SPA/JS-framework needed; htmx covers the modest interactivity (dynamic order lines, live totals)                                                                                                                                                                                                                   |
| D3  | Single shared PostgreSQL database, tenant isolation via Row-Level Security                                                                                                                                                                                                                     | Per-tenant databases don't scale operationally to 1,000 tenants (migrations/backups multiply); RLS gives strong isolation with one schema. Same pattern already proven on the founder's separate ERP project.                                                                                                                                                    |
| D4  | RLS scoped by **user**, not by a single "current restaurant" session variable                                                                                                                                                                                                                  | Lets a query with no restaurant filter automatically span every restaurant a user belongs to — this is what makes a consolidated multi-restaurant owner dashboard work with zero special-case code                                                                                                                                                               |
| D5  | Single domain, not subdomain-per-tenant                                                                                                                                                                                                                                                        | Simpler TLS/routing; tenant context resolved from session, not URL                                                                                                                                                                                                                                                                                               |
| D6  | Hosting: VPS (Hetzner/DigitalOcean), not managed AWS (App Runner+RDS)                                                                                                                                                                                                                          | ~2.5–3x cheaper at every growth stage (see §7); trade-off is ~2-4 hrs/month of self-managed ops once initial setup is done                                                                                                                                                                                                                                       |
| D7  | Money stored as **integers in poisha** (BDT minor unit), never floats                                                                                                                                                                                                                          | Avoids floating-point rounding bugs in financial data — non-negotiable for anything invoice/payment related                                                                                                                                                                                                                                                      |
| D8  | Billing attaches to an **account**, not a single restaurant                                                                                                                                                                                                                                    | A plan can grant multiple restaurants (max_restaurants feature) — subscriptions must live above the restaurant level. `restaurants.account_id` links them.                                                                                                                                                                                                       |
| D9  | Account owner ≠ restaurant owner/admin, and billing is visible ONLY to `accounts.owner_user_id`                                                                                                                                                                                                | Restaurant-level admins run day-to-day ops without seeing billing; delegation without financial exposure                                                                                                                                                                                                                                                         |
| D10 | Plan features are fully dashboard-configurable via `features` + `plan_features`, with `account_feature_overrides` for one-off exceptions                                                                                                                                                       | Founder explicitly wants to customize plan limits and grant exceptions to "special people" without code changes                                                                                                                                                                                                                                                  |
| D11 | Menu items/categories are soft-deleted (`deleted_at`), never hard-deleted                                                                                                                                                                                                                      | Historical orders/invoices must still reference what was actually sold even after menu changes                                                                                                                                                                                                                                                                   |
| D12 | `order_items.unit_price` snapshots the menu price; fixed-amount and percentage discounts are stored separately for line and order discounts. Line changes use `order_item_price_adjustments`; order discounts use `order_discount_adjustments`, both recording actor, reason, mode, and value. | Discount entry is intuitive (money or percent) without replacing the item’s base price, while retaining an audit trail. Percentages and money use integer basis points and poisha respectively.                                                                                                                                                                  |
| D13 | Invoice numbers are sequential **per restaurant**, via `restaurant_invoice_counters`                                                                                                                                                                                                           | Many tax regimes require gapless sequential numbering per business; a global counter across tenants wouldn't satisfy this                                                                                                                                                                                                                                        |
| D14 | Login-enabled restaurant roles remain `owner` and `admin`; named waiters/cashiers live in a separate non-login `restaurant_staff` roster.                                                                                                                                                      | Floor staff need to be selectable on orders without receiving accounts. A roster member may be waiter, cashier, both, or use a custom title.                                                                                                                                                                                                                     |
| D15 | Payment provider: bKash                                                                                                                                                                                                                                                                        | Local market fit (Bangladesh). bKash has **no native recurring billing** — unlike Stripe, there's no "charge automatically every month" primitive. Two real options, **not yet decided**: (a) bKash Tokenized Checkout (saved agreement, backend triggers charge), or (b) reminder-and-manual-pay flow. This blocks building the checkout UI — needs a decision. |
| D16 | Payment locking has a grace period (recommended 3–7 days) before auto-lock, not immediate lock on missed payment                                                                                                                                                                               | Avoids punishing a transient payment failure; reminders should go out before the lock, not just after                                                                                                                                                                                                                                                            |
| D17 | Two independent lock levels: `accounts.status` (billing-driven, cascades to all restaurants under the account) vs `restaurants.status` (manual, single-restaurant override by platform admin)                                                                                                  | Lets a platform admin lock one problem restaurant without touching the rest of a fine-paying account's locations                                                                                                                                                                                                                                                 |
| D18 | Migration tool convention: golang-migrate style, numbered `NNNN_description.up.sql` / `.down.sql` pairs                                                                                                                                                                                        | Standard, works cleanly with a Go backend, no ORM lock-in                                                                                                                                                                                                                                                                                                        |
| D19 | Tax/VAT is per-restaurant, via `restaurant_tax_settings` (`vat_rate_bps`, `vat_inclusive`, `service_charge_rate_bps`, `vat_registration_number`)                                                                                                                                               | Bangladesh VAT (Mushak) rate and registration (BIN) legitimately differ restaurant to restaurant; rate stored as basis-point integer, same no-float convention as money (D7)                                                                                                                                                                                     |
| D20 | Customer payment method is recorded separately from SaaS billing, via `order_payments` (cash/card/bkash_personal/nagad/rocket/bank_transfer/other), distinct from the `payments` table (which is subscription billing only)                                                                    | This was a real gap: nothing previously recorded _how a diner paid for their order_ — needed for cash-vs-digital reporting and to support split payments (part cash, part card)                                                                                                                                                                                  |

**Resolves gap #7 from the original gap list (tax/VAT handling)** — no longer open, see D19.

| D21 | Inventory is "open" — restaurants add any item with a free-text name/unit (`inventory_items`), no recipe/menu-item linkage or auto-deduction on order. Stock changes go through an audit trail (`inventory_adjustments`: purchase/usage/wastage/correction), and `current_quantity` is an app-maintained running total, not computed live from the trail on every read. | Matches the founder's explicit choice of "open inventory" over full ingredient-level recipe costing — right complexity level for this market/stage. Audit trail follows the same principle as price overrides (D12): every stock change is traceable to who/why. |
| D22 | Excel/CSV export uses the existing `data_export` plan feature (already in the seeded feature catalog) — generates from the same filtered query as the on-screen report, gated by plan | This was a real gap: `data_export` existed as a plan feature flag from the start but nothing had actually specified what it does or how it's implemented. Now specified. |
| D23 | An open order is resumable for its table until checkout/cancellation; the table row is locked during creation, legacy duplicate sessions are merged into the earliest open session, and a partial unique index enforces one open order per table. `opened_at` is the sitting start and drives the table-card/POS timer. | Staff can add items over multiple visits without losing the order or leaving unreachable duplicate sessions. |
| D24 | Restaurant creation belongs to the account owner and checks `max_restaurants`; an optional setup copy includes menu/categories, tables, inventory catalog/current quantity, and tax rates, but not order/payment/invoice or stock-adjustment history. | New locations can be launched quickly without inheriting another location’s operational history or tax registration number. |
| D25 | Login users may be assigned to multiple restaurants within one account; the account’s `max_users_per_account` counts distinct active users. Account owners can also assign an existing login, but a user identity cannot be attached across unrelated customer accounts. | Membership remains separate from the non-login staff roster and avoids sharing an identity across tenants. |
| D26 | Platform identities authenticate only at `/platform/login` using a path-scoped `rms_platform_session` cookie; customer `/login` rejects platform identities. Support has read-only platform access; only superadmins mutate platform data. | A distinct entry point and session boundary reduce accidental or cross-surface access. |

---

## 3. Roles & permissions model

- **Platform admin** (the founder + any future staff) — `platform_admins` table, separate from any restaurant. Can lock any account/restaurant, edit plans/features/overrides. Should use a DB role with `BYPASSRLS` for dashboard queries rather than threading admin checks into every RLS policy.
- **Account owner** — `accounts.owner_user_id`. Sees billing/subscription/payments for their account. Adds restaurants up to plan limit (or override). Assigns restaurant owner/admin roles.
- **Restaurant owner / admin** — `restaurant_users`, scoped per restaurant. Full operational access (menu, tables, orders, price overrides) to that restaurant only. No billing visibility unless they're also the account owner.

---

## 4. Subscription plan feature catalog (current defaults — see `seed.sql`)

| Feature key               | Type    | Basic | Pro      | Enterprise                   |
| ------------------------- | ------- | ----- | -------- | ---------------------------- |
| max_restaurants           | number  | 1     | 5        | 1000 (effectively unlimited) |
| max_users_per_account     | number  | 3     | 20       | 1000                         |
| max_tables_per_restaurant | number  | 15    | 50       | 1000                         |
| reports_level             | text    | basic | advanced | advanced                     |
| consolidated_reports      | boolean | false | true     | true                         |
| invoice_branding          | boolean | false | true     | true                         |
| data_export               | boolean | false | true     | true                         |
| priority_support          | boolean | false | false    | true                         |

Prices (placeholder, easy to change in dashboard once built): Basic ৳999/mo, Pro ৳2,499/mo, Enterprise ৳7,999/mo — **these numbers were never validated against the market and should be treated as placeholders, not a pricing decision.**

**Exception mechanism**: `account_feature_overrides` — a platform admin can grant one account a value beating their plan default (e.g. extra restaurants for free). Requires a `reason` and records `granted_by`. Resolution order when checking any limit: override → plan default.

**Feature ideas raised but not yet built** (add to `features` table when ready, no schema change needed): `max_orders_per_month` (usage cap / abuse guard), API access, multi-language menus, bundled SMS notification credits.

---

## 4b. Reports & filtering (previously only mentioned in passing — specified here)

**Report types**: sales summary (with date-range filter — today/week/month/custom), top-selling items, sales by menu category, discounts/comps given (rolls up `order_item_price_adjustments` — flags staff who discount unusually often, per the audit design in D12), table turnover / average order value, payment-method breakdown (cash vs. card vs. mobile banking, enabled by `order_payments` per D20), current inventory levels with a low-stock flag (`current_quantity <= reorder_threshold`), inventory movement log (from `inventory_adjustments`, filterable by item/reason/date — see D21), and — for multi-restaurant accounts — a consolidated cross-restaurant rollup (falls out of the RLS design in D4 with no special-case query needed).

**Export**: every report above can export to Excel (.xlsx), gated behind the `data_export` plan feature (D22) — implement with a Go xlsx library (e.g. `excelize`), generating from the exact same filtered query/result set as the on-screen HTML report, not a separate export-specific query path.

**Filters, common across reports**: date range, restaurant (when the account has more than one), table, staff member (`orders.opened_by` / `order_item_price_adjustments.adjusted_by`), menu category.

**Interactivity**: consistent with D2 (server-rendered + htmx, no SPA) — a filter form submits via htmx and re-renders only the results fragment, not a full page reload. No client-side charting framework needed; keep charts server-rendered or a single lightweight JS include only where a trend genuinely needs showing (see `UI_UX_THEME.md` §4 — numbers-forward, sparing charts).

**Indexes added to support this** (in `migrations/0006`): `orders (restaurant_id, closed_at)` for date-range queries, `order_items (order_id)` and `order_items (menu_item_id)` for per-item aggregation, `order_payments (restaurant_id, method)` for payment-method breakdown.

---

## 5. Cost analysis (Aug 2026 estimates — re-verify pricing before committing budget)

| Stage                     | VPS (self-managed) | AWS managed (App Runner + RDS) |
| ------------------------- | ------------------ | ------------------------------ |
| Pilot (<20 restaurants)   | ~$12–15/mo         | ~$35–55/mo                     |
| Growth (~200 restaurants) | ~$50–70/mo         | ~$150–250/mo                   |
| Scale (~1000 restaurants) | ~$200–350/mo       | ~$500–900+/mo                  |

VPS ops overhead: ~2-3 days one-time setup (server hardening, Postgres tuning, TLS via Caddy, deploy pipeline — Coolify recommended for git-push deploys/rollbacks at no cost, systemd+script also fine), then ~2-4 hrs/month recurring (patches mostly automated via `unattended-upgrades`, periodic backup-restore tests, incident response, capacity watching).

---

## 6. Current build status

**Implemented in the application**:

- Table orders can be resumed; occupied tables are selectable and the table-card action opens the existing order or starts one. Order lines remain persisted until changed or checkout.
- Table cards show status by background and an elapsed open-order timer; the POS order drawer shows the same timer.
- POS supports fixed and percentage discounts per line and per order, with reason/audit records and integer-poisha recalculation.
- Restaurant staff roster supports waiter/cashier flags and custom titles without logins; an optional server can be assigned per order.
- Account owners can add a restaurant from the selector, enforce plan limits, and optionally copy menu/categories, tables, inventory balances, and tax rates.
- Account owners can create login users or assign existing users to restaurants, bounded by the account user limit.
- Platform login is separate from customer login and uses its own path-scoped session cookie. Superadmins manage support identities, plans/features, account overrides, and subscriptions; support is read-only.
- Runtime forward migrations `0011`–`0013` cover staff/discount data, account-owner membership RLS, and platform administration policies. The v-1.0.1 schema reference is synchronized.
- Focused tests cover table-order resume and discount arithmetic. Full-suite verification remains blocked by stale repository test setup (migration runner call signature and absent demo login fixture in the local DB).

**Still outstanding**:

- Apply migrations `0011`–`0013` to a clean database and exercise the workflows end-to-end, including RLS under the production database role.
- Repair the repository-wide test setup and add integration coverage for cloning, account user limits, platform login separation, support read-only behavior, plan/override management, and checkout discount totals.
- No CI/CD pipeline, production VPS, or bKash integration exists yet.

---

## 7. Gaps flagged — not yet decided, raised during planning but never resolved

These are real gaps, not nice-to-haves — a future agent should raise them before building the adjacent feature:

1. **Customer account security** — signed session cookies now exist, and platform/customer sessions are separated. Password reset, email verification, MFA, and session revocation remain unimplemented.
2. **Secrets/config management** — bKash API keys, DB credentials, session secret. No `.env` structure or secrets-handling approach chosen yet.
3. **Testing strategy** — especially RLS isolation tests. Multi-tenant correctness lives or dies on RLS working as designed; this needs explicit automated tests (e.g. attempt cross-tenant reads as different `app.current_user_id` values and assert zero rows), not just manual spot-checks.
4. **CI/CD** — deploy pipeline automation beyond "Coolify or a shell script" was never designed in detail.
5. **bKash recurring-charge mechanism** (D15 above) — blocks the checkout UI.
6. **Data retention policy** for canceled accounts — delete after N days? Keep read-only? Export on request?
7. ~~Tax/VAT handling~~ — **resolved, see D19.**
8. **Domain/DNS** — no domain has been chosen or registered yet.
9. **Terms of Service / refund policy** — the locking-for-nonpayment feature (D16/D17) has a legal dimension (what happens to a locked account's data, refund rules) that hasn't been addressed at all.
10. **Login-user permissions** — the current `owner`/`admin` roles still need an explicit operation-by-operation permission matrix; membership management itself is account-owner-only.
11. **General-purpose audit log** beyond price overrides (who edited a menu price, who voided an order) — cheap to add now, expensive to backfill later.

---

## 8. Suggested immediate next steps (in order)

1. Apply migrations `0011`–`0013` to a clean database, verify down migrations, and run the RLS isolation suite with a non-owner database role.
2. Repair stale test harness assumptions and add integration tests for restaurant cloning, user limits, discount totals/invoices, and platform role isolation.
3. Verify the browser workflows on desktop and mobile, including several add-item/leave/return cycles and support-vs-superadmin access.
4. Decide the bKash recurring mechanism (D15 above) before replacing the current mocked subscription checkout.

---

## 9. File manifest (all in this output)

- `PROJECT_PLANNER.md` — this file, primary context source
- `documentation.md` — earlier prose walkthrough of the same decisions (secondary/historical)
- `schema.sql` — full schema as one file (reference/ERD generation)
- `migrations/0001_extensions_and_enums.{up,down}.sql`
- `migrations/0002_platform_and_billing.{up,down}.sql`
- `migrations/0003_restaurants_and_users.{up,down}.sql`
- `migrations/0004_operations.{up,down}.sql`
- `migrations/0005_rls_and_indexes.{up,down}.sql`
- `migrations/0006_tax_settings_and_order_payments.{up,down}.sql` — `restaurant_tax_settings` (VAT/Mushak) + `order_payments` (how the diner paid)
- `migrations/0007_inventory.{up,down}.sql` — `inventory_items` + `inventory_adjustments` (open inventory tracking)
- `migrations/0011_staff_and_discounts.{up,down}.sql` — named staff roster, audited discounts, and order linkage
- `migrations/0012_account_owner_memberships.{up,down}.sql` — owner membership management across account restaurants
- `migrations/0013_platform_admin_controls.{up,down}.sql` — platform admin uniqueness and support read-only policies
- `seed.sql` — reference data + dev fixtures (now includes default 15% VAT settings for the two demo restaurants)
- `RUNBOOK.md` — local setup, VPS deployment, service/request flow, migration workflow
- `UI_UX_THEME.md` — fixed design system (colors, type, layout) so any agent stays visually consistent

If your agent/tool expects a specific auto-loaded context filename (e.g. an `AGENTS.md`-style convention), copy or symlink this file to that name — the content is what matters, not the filename.
