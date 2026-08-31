# Restaurant Management SaaS — Project Planner & Full Context

**Purpose of this file**: this is the single source of truth for the project. Any AI agent (or human) picking up work with zero prior context should read this file fully before writing code or making a decision. It carries product intent, business decisions, architecture reasoning, current build status, and the backlog — not just "what exists" but "why," so decisions aren't silently reversed or duplicated. **Update this file whenever a new decision is made or a milestone is completed** — treat it as living documentation, not a one-time handoff note.

---

## 1. What this product is

A multi-tenant SaaS for local restaurants: tables, menu, order-taking, invoicing, reports. Sold on subscription. Target: 1,000+ restaurants eventually; realistic near-term expectation is slow growth (may take a while to reach even 20). Infra cost minimization is a hard constraint on every decision, not a nice-to-have.

The founder (owner of this business) has strong JS/TypeScript/Node/AWS-serverless expertise but deliberately chose Go for this product to keep it cheap to run — this trade-off (less personal fluency, lower cost/ops burden) was made consciously and should not be second-guessed by a future agent without new information.

Real-time kitchen order display was explicitly ruled OUT of scope: most target restaurants just relay orders verbally to the kitchen. This decision removed the need for WebSockets/live-state infrastructure and is why the stack can stay a simple server-rendered app. **Do not add real-time infra unless this requirement changes.**

---

## 2. Confirmed decisions (do not relitigate without new input)

| # | Decision | Why |
|---|---|---|
| D1 | Backend: Go + Gin | Cheap to run, low resource footprint, fits VPS hosting well |
| D2 | Frontend: server-rendered Go templates + htmx | No real-time requirement (see above) means no SPA/JS-framework needed; htmx covers the modest interactivity (dynamic order lines, live totals) |
| D3 | Single shared PostgreSQL database, tenant isolation via Row-Level Security | Per-tenant databases don't scale operationally to 1,000 tenants (migrations/backups multiply); RLS gives strong isolation with one schema. Same pattern already proven on the founder's separate ERP project. |
| D4 | RLS scoped by **user**, not by a single "current restaurant" session variable | Lets a query with no restaurant filter automatically span every restaurant a user belongs to — this is what makes a consolidated multi-restaurant owner dashboard work with zero special-case code |
| D5 | Single domain, not subdomain-per-tenant | Simpler TLS/routing; tenant context resolved from session, not URL |
| D6 | Hosting: VPS (Hetzner/DigitalOcean), not managed AWS (App Runner+RDS) | ~2.5–3x cheaper at every growth stage (see §7); trade-off is ~2-4 hrs/month of self-managed ops once initial setup is done |
| D7 | Money stored as **integers in poisha** (BDT minor unit), never floats | Avoids floating-point rounding bugs in financial data — non-negotiable for anything invoice/payment related |
| D8 | Billing attaches to an **account**, not a single restaurant | A plan can grant multiple restaurants (max_restaurants feature) — subscriptions must live above the restaurant level. `restaurants.account_id` links them. |
| D9 | Account owner ≠ restaurant owner/admin, and billing is visible ONLY to `accounts.owner_user_id` | Restaurant-level admins run day-to-day ops without seeing billing; delegation without financial exposure |
| D10 | Plan features are fully dashboard-configurable via `features` + `plan_features`, with `account_feature_overrides` for one-off exceptions | Founder explicitly wants to customize plan limits and grant exceptions to "special people" without code changes |
| D11 | Menu items/categories are soft-deleted (`deleted_at`), never hard-deleted | Historical orders/invoices must still reference what was actually sold even after menu changes |
| D12 | `order_items.unit_price` snapshots price at order time; price overrides are tracked in an append-only `order_item_price_adjustments` audit table | Founder wants staff to freely discount/comp items, but every change must be traceable (who, when, why, before/after) |
| D13 | Invoice numbers are sequential **per restaurant**, via `restaurant_invoice_counters` | Many tax regimes require gapless sequential numbering per business; a global counter across tenants wouldn't satisfy this |
| D14 | Two restaurant-level roles only: `owner`, `admin` | As specified. Schema leaves room to add `staff`/`waiter` later (single enum value addition) if floor-staff-only logins are ever needed — **not built, flagged only** |
| D15 | Payment provider: bKash | Local market fit (Bangladesh). bKash has **no native recurring billing** — unlike Stripe, there's no "charge automatically every month" primitive. Two real options, **not yet decided**: (a) bKash Tokenized Checkout (saved agreement, backend triggers charge), or (b) reminder-and-manual-pay flow. This blocks building the checkout UI — needs a decision. |
| D16 | Payment locking has a grace period (recommended 3–7 days) before auto-lock, not immediate lock on missed payment | Avoids punishing a transient payment failure; reminders should go out before the lock, not just after |
| D17 | Two independent lock levels: `accounts.status` (billing-driven, cascades to all restaurants under the account) vs `restaurants.status` (manual, single-restaurant override by platform admin) | Lets a platform admin lock one problem restaurant without touching the rest of a fine-paying account's locations |
| D18 | Migration tool convention: golang-migrate style, numbered `NNNN_description.up.sql` / `.down.sql` pairs | Standard, works cleanly with a Go backend, no ORM lock-in |
| D19 | Tax/VAT is per-restaurant, via `restaurant_tax_settings` (`vat_rate_bps`, `vat_inclusive`, `service_charge_rate_bps`, `vat_registration_number`) | Bangladesh VAT (Mushak) rate and registration (BIN) legitimately differ restaurant to restaurant; rate stored as basis-point integer, same no-float convention as money (D7) |
| D20 | Customer payment method is recorded separately from SaaS billing, via `order_payments` (cash/card/bkash_personal/nagad/rocket/bank_transfer/other), distinct from the `payments` table (which is subscription billing only) | This was a real gap: nothing previously recorded *how a diner paid for their order* — needed for cash-vs-digital reporting and to support split payments (part cash, part card) |

**Resolves gap #7 from the original gap list (tax/VAT handling)** — no longer open, see D19.

---

## 3. Roles & permissions model

- **Platform admin** (the founder + any future staff) — `platform_admins` table, separate from any restaurant. Can lock any account/restaurant, edit plans/features/overrides. Should use a DB role with `BYPASSRLS` for dashboard queries rather than threading admin checks into every RLS policy.
- **Account owner** — `accounts.owner_user_id`. Sees billing/subscription/payments for their account. Adds restaurants up to plan limit (or override). Assigns restaurant owner/admin roles.
- **Restaurant owner / admin** — `restaurant_users`, scoped per restaurant. Full operational access (menu, tables, orders, price overrides) to that restaurant only. No billing visibility unless they're also the account owner.

---

## 4. Subscription plan feature catalog (current defaults — see `seed.sql`)

| Feature key | Type | Basic | Pro | Enterprise |
|---|---|---|---|---|
| max_restaurants | number | 1 | 5 | 1000 (effectively unlimited) |
| max_users_per_account | number | 3 | 20 | 1000 |
| max_tables_per_restaurant | number | 15 | 50 | 1000 |
| reports_level | text | basic | advanced | advanced |
| consolidated_reports | boolean | false | true | true |
| invoice_branding | boolean | false | true | true |
| data_export | boolean | false | true | true |
| priority_support | boolean | false | false | true |

Prices (placeholder, easy to change in dashboard once built): Basic ৳999/mo, Pro ৳2,499/mo, Enterprise ৳7,999/mo — **these numbers were never validated against the market and should be treated as placeholders, not a pricing decision.**

**Exception mechanism**: `account_feature_overrides` — a platform admin can grant one account a value beating their plan default (e.g. extra restaurants for free). Requires a `reason` and records `granted_by`. Resolution order when checking any limit: override → plan default.

**Feature ideas raised but not yet built** (add to `features` table when ready, no schema change needed): `max_orders_per_month` (usage cap / abuse guard), API access, multi-language menus, bundled SMS notification credits.

---

## 4b. Reports & filtering (previously only mentioned in passing — specified here)

**Report types**: sales summary (with date-range filter — today/week/month/custom), top-selling items, sales by menu category, discounts/comps given (rolls up `order_item_price_adjustments` — flags staff who discount unusually often, per the audit design in D12), table turnover / average order value, payment-method breakdown (cash vs. card vs. mobile banking, enabled by `order_payments` per D20), and — for multi-restaurant accounts — a consolidated cross-restaurant rollup (falls out of the RLS design in D4 with no special-case query needed).

**Filters, common across reports**: date range, restaurant (when the account has more than one), table, staff member (`orders.opened_by` / `order_item_price_adjustments.adjusted_by`), menu category.

**Interactivity**: consistent with D2 (server-rendered + htmx, no SPA) — a filter form submits via htmx and re-renders only the results fragment, not a full page reload. No client-side charting framework needed; keep charts server-rendered or a single lightweight JS include only where a trend genuinely needs showing (see `UI_UX_THEME.md` §4 — numbers-forward, sparing charts).

**Indexes added to support this** (in `migrations/0006`): `orders (restaurant_id, closed_at)` for date-range queries, `order_items (order_id)` and `order_items (menu_item_id)` for per-item aggregation, `order_payments (restaurant_id, method)` for payment-method breakdown.

---

## 5. Cost analysis (Aug 2026 estimates — re-verify pricing before committing budget)

| Stage | VPS (self-managed) | AWS managed (App Runner + RDS) |
|---|---|---|
| Pilot (<20 restaurants) | ~$12–15/mo | ~$35–55/mo |
| Growth (~200 restaurants) | ~$50–70/mo | ~$150–250/mo |
| Scale (~1000 restaurants) | ~$200–350/mo | ~$500–900+/mo |

VPS ops overhead: ~2-3 days one-time setup (server hardening, Postgres tuning, TLS via Caddy, deploy pipeline — Coolify recommended for git-push deploys/rollbacks at no cost, systemd+script also fine), then ~2-4 hrs/month recurring (patches mostly automated via `unattended-upgrades`, periodic backup-restore tests, incident response, capacity watching).

---

## 6. Current build status

**Done** (this session's output):
- `schema.sql` — full current-state schema, single file, for reference/diagrams
- `migrations/0001`–`0005` (`.up.sql`/`.down.sql`) — same schema split into golang-migrate-style ordered migrations
- `seed.sql` — idempotent reference data (features, plans, plan_features) + clearly-marked dev-only demo fixtures (one admin, one account, two restaurants, one owner)
- `documentation.md` — earlier full narrative writeup (this planner file supersedes it as the primary context source; documentation.md can still be read for the original prose walkthrough)

**NOT started** (no code written yet, planning stage only):
- No Go application code exists yet — no Gin routes, no htmx templates, no auth
- No CI/CD pipeline
- No actual VPS provisioned
- No bKash integration code
- No tests of any kind, including RLS isolation tests

---

## 7. Gaps flagged — not yet decided, raised during planning but never resolved

These are real gaps, not nice-to-haves — a future agent should raise them before building the adjacent feature:

1. **Auth strategy** — session cookies vs JWT, password reset flow, email/phone verification. `users.password_hash` exists but nothing about the auth flow itself has been designed.
2. **Secrets/config management** — bKash API keys, DB credentials, session secret. No `.env` structure or secrets-handling approach chosen yet.
3. **Testing strategy** — especially RLS isolation tests. Multi-tenant correctness lives or dies on RLS working as designed; this needs explicit automated tests (e.g. attempt cross-tenant reads as different `app.current_user_id` values and assert zero rows), not just manual spot-checks.
4. **CI/CD** — deploy pipeline automation beyond "Coolify or a shell script" was never designed in detail.
5. **bKash recurring-charge mechanism** (D15 above) — blocks the checkout UI.
6. **Data retention policy** for canceled accounts — delete after N days? Keep read-only? Export on request?
7. ~~Tax/VAT handling~~ — **resolved, see D19.**
8. **Domain/DNS** — no domain has been chosen or registered yet.
9. **Terms of Service / refund policy** — the locking-for-nonpayment feature (D16/D17) has a legal dimension (what happens to a locked account's data, refund rules) that hasn't been addressed at all.
10. **Staff/waiter role** (D14) — flagged repeatedly, never built. Ask before building floor-staff-facing screens.
11. **General-purpose audit log** beyond price overrides (who edited a menu price, who voided an order) — cheap to add now, expensive to backfill later.

---

## 8. Suggested immediate next steps (in order)

1. Resolve the auth strategy (gap #1) — everything else depends on having a session/user-identity model in the Go app.
2. Stand up the Go project skeleton: Gin router, `app.current_user_id` session-setting middleware wired to the RLS pattern in D4.
3. Provision the VPS, apply `migrations/` in order via golang-migrate, run `seed.sql`.
4. Write the RLS isolation test (gap #3) before building any feature on top of the schema — cheapest moment to catch a policy mistake is now, not after data exists.
5. Build restaurant CRUD + menu + tables (lowest-risk, no billing/payment complexity) before touching bKash.
6. Decide the bKash recurring mechanism (gap #5) before building the subscription checkout screen.

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
- `seed.sql` — reference data + dev fixtures (now includes default 15% VAT settings for the two demo restaurants)
- `RUNBOOK.md` — local setup, VPS deployment, service/request flow, migration workflow
- `UI_UX_THEME.md` — fixed design system (colors, type, layout) so any agent stays visually consistent

If your agent/tool expects a specific auto-loaded context filename (e.g. an `AGENTS.md`-style convention), copy or symlink this file to that name — the content is what matters, not the filename.
