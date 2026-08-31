# Restaurant Management SaaS — Documentation

Status: living document, planning stage. Companion file: `schema.sql` (full DB schema referenced throughout).

---

## 1. Product overview

A multi-tenant SaaS for local restaurants covering tables, menu, order-taking, invoicing, and reports. Sold on a subscription basis, targeting eventual scale of 1,000+ restaurants, starting from a small pilot group. Cost-consciousness is a first-class constraint at every layer, not an afterthought — infra, stack, and even feature scope were chosen with this in mind.

Real-time kitchen display was explicitly ruled out as a core feature: most target restaurants relay orders verbally to the kitchen. This single decision simplified the whole architecture — no WebSocket/live-state layer needed for v1.

---

## 2. Roles & permissions

| Role | Scope | Sees billing? | Notes |
|---|---|---|---|
| **Platform admin** (you) | Entire platform | Yes, all accounts | Can lock any account or restaurant, edit plans/features, grant exceptions. Separate table (`platform_admins`) from any restaurant — not a restaurant role. |
| **Account owner** | One account, all its restaurants | Yes, own account only | The billing contact (`accounts.owner_user_id`). Can add restaurants up to the plan's limit, assign restaurant-level owner/admin roles. |
| **Restaurant owner** | One restaurant | No, unless also the account owner | Full operational control of a single restaurant (menu, tables, staff assignment). |
| **Restaurant admin** | One restaurant | No | Day-to-day operations, same operational surface as owner minus anything billing-adjacent. |

**Open item**: no staff/waiter role yet — only owner/admin, as specified. If floor staff need logins just to punch orders (not manage menu/settings), a narrower role is a one-line enum addition later (`ALTER TYPE restaurant_role ADD VALUE 'staff'`). Flagged, not built, since it wasn't asked for.

**Why account owner ≠ restaurant owner**: an account can hold multiple restaurants (see §4). The account owner is the billing relationship; each restaurant still has its own `owner`/`admin` assignments so day-to-day access can be delegated per location without exposing billing to everyone who runs a location.

---

## 3. Core features (v1)

- **Tables** — per-restaurant table list with status (available/occupied/reserved).
- **Menu** — categories and items, soft-deletable (so historical orders/invoices still reference the item that was actually sold, even after it's removed from today's menu).
- **Orders** — opened against a table, line items reference a menu item and snapshot its price at order time.
- **Order-level price overrides / discounts** (new, see §8) — staff can freely discount or comp a line item; every change is audited.
- **Invoices** — generated per closed order, sequential per-restaurant numbering (not a global counter — see reasoning in schema comments), PDF export.
- **Reports** — sales, top items, table turnover; tiered by plan (see §5).

---

## 4. Multi-tenancy & the account/restaurant split

**Single shared PostgreSQL database, Row-Level Security for isolation** — chosen over separate-database-per-tenant because at a 1,000-tenant target, per-tenant databases become an operational burden (migrations, connection pooling, backups all multiply), while RLS gives strong isolation with one schema to maintain. This was the same pattern already proven out on the ERP project.

**RLS is scoped by user, not by a single "current restaurant"** — the app sets `app.current_user_id` per request, and every restaurant-scoped policy resolves visibility through `restaurant_users` membership. This is why a query with no restaurant filter naturally returns rows across every restaurant a user belongs to: a consolidated multi-restaurant dashboard for an owner falls out of the RLS design itself, rather than needing to be built as a special case in the application layer.

**Why an `accounts` table exists at all**: originally, subscriptions attached directly to a restaurant. That breaks the moment a plan can include *multiple* restaurants — a subscription has to attach to something above a single restaurant. `accounts` is that billing entity: one account, one subscription, N restaurants underneath it (up to the plan's limit). `restaurants.account_id` is the link. Operational access (who can manage a given restaurant day to day) still runs through `restaurant_users`, deliberately kept separate from billing ownership — see the role table in §2 for why.

---

## 5. Subscription plans

### Feature dimensions (dashboard-configurable)

Every plan is defined by a set of feature values, editable from your dashboard without a code change (`features` + `plan_features` tables). Suggested starting set, seeded in `schema.sql`:

| Feature key | Type | What it controls |
|---|---|---|
| `max_restaurants` | number | How many restaurants an account can add |
| `max_users_per_account` | number | Total staff logins across all the account's restaurants |
| `max_tables_per_restaurant` | number | Caps table count per location — a natural usage-based upsell lever |
| `reports_level` | text (`basic`/`advanced`) | Whether advanced analytics (trends, comparisons) are unlocked |
| `consolidated_reports` | boolean | Cross-restaurant rollup view (relevant once `max_restaurants` > 1) |
| `invoice_branding` | boolean | Custom logo on generated invoices |
| `data_export` | boolean | CSV/Excel export of orders, menu, reports |
| `priority_support` | boolean | Faster support channel — cheap to grant, good upsell |

**Additional dimensions worth considering as the product matures**, not built yet:
- `max_orders_per_month` — a soft usage cap that also doubles as abuse/fraud protection, not just a monetization lever.
- API access — if you ever want restaurants to integrate with accounting software or a POS hardware partner.
- Multi-language menu support — relevant if you expand beyond Bangla/English.
- SMS notification credits — order confirmations, payment reminders; bundled SMS is a common local SaaS upsell.

### Exceptions for special people

`account_feature_overrides` lets a platform admin grant one specific account a value that beats their plan default — e.g. "give this account 10 restaurants while on the Basic plan, free of charge." Every override requires a `reason` (not optional) and records `granted_by`, so there's always an answer to "why does this account have extra restaurants" months later. **Resolution order**: override → plan default. No overrides table entry means the plan's own limit applies.

---

## 6. Locking & enforcement

Two independent lock levels, deliberately separate:

- **`accounts.status`** — billing-driven. Set to `locked` when payment fails past the grace period; cascades to every restaurant under that account (the app checks account status before restaurant status).
- **`restaurants.status`** — manual, restaurant-specific. Lets a platform admin lock *one* restaurant under an otherwise-fine account (e.g. a specific abuse complaint), without touching the rest of that account's restaurants.

**Recommended grace period before auto-lock**: 3–7 days after `current_period_end` passes with no successful payment, with reminders sent before the lock, not just after. Immediate locking on a single missed payment risks cutting off a paying customer over a transient wallet failure — a bad first impression for a product that's still building trust with small business owners.

Every status change writes to `account_status_log` or `restaurant_status_log` — who changed it, from what to what, and why. This is non-negotiable for a "lock the account if they don't pay" feature: if an owner disputes a lock, you need the record.

---

## 7. Payments — bKash

- `payments.account_id` — billing is account-level, matching §4.
- `provider_payment_id` / `provider_trx_id` capture bKash's own identifiers at each stage of their flow (create → execute).
- `raw_response JSONB` stores bKash's full callback payload — not strictly needed day-to-day, but invaluable the first time a payment is disputed and you need to see exactly what bKash sent.
- `UNIQUE (provider, provider_payment_id)` — bKash webhooks can and will retry; this constraint makes a duplicate callback fail safely at the database level instead of double-crediting an account.

**Known gap, flagged not solved**: bKash has no native "subscribe once, auto-charge monthly" mechanism like Stripe. Realistic options: (a) bKash Tokenized Checkout, which saves an agreement your backend can trigger a charge against, or (b) a simpler reminder-and-manual-pay flow — notify the owner before `current_period_end`, they tap to pay, webhook extends the period. Worth deciding before building the checkout UI, since it changes what you're asking the owner to do at signup.

---

## 8. Order price overrides (discounts) with audit

Restaurants can freely edit an order line's charged price (comps, regular-customer discounts, negotiated prices) — this was an explicit requirement. The design keeps that freedom while making every change traceable:

- `order_items.unit_price` always holds the **current effective price** — the app doesn't need special-case logic to know what to charge.
- `order_item_price_adjustments` is an **append-only audit log**: every override writes a row with the price before, the price after, a required `reason`, who did it, and when. Nothing is overwritten or deleted — if a line is discounted twice, both changes are on record.
- The original menu price is still recoverable via the first adjustment row (or `menu_items.price` if unadjusted), so reports can show "total discount given" as its own metric, not just final revenue — useful for spotting a staff member who discounts unusually often.
- No new role restriction was added — currently both `owner` and `admin` can override prices, matching that those are the only two roles that exist today. Worth revisiting once/if a narrower staff role exists.

---

## 9. DevOps & hosting decisions

| Decision | Reasoning |
|---|---|
| **Go + Gin backend, server-rendered templates + htmx** | Despite JS being your stronger stack, Go's low resource footprint keeps VPS costs minimal, and dropping the real-time kitchen-display requirement removed the one place a JS-heavy interactive frontend was actually justified. htmx covers the modest interactivity a POS-style flow needs (dynamic order lines, live totals) without a separate frontend build. |
| **Single shared PostgreSQL DB with RLS**, not per-tenant databases | Operational simplicity at 1,000-tenant scale; one schema, one migration path, one backup routine. |
| **Single domain, not subdomain-per-tenant** | Simpler routing and TLS (one certificate, no wildcard DNS needed); tenant context resolved from the logged-in user's session instead of the URL. |
| **VPS over managed AWS** (App Runner + RDS) | Roughly 2.5–3x cheaper at every stage from pilot to 1,000-tenant scale (see §10). Go's low overhead means a single mid-tier VPS can carry hundreds of tenants before any real scaling work is needed — the cost gap is largest precisely while the business is smallest and most cost-sensitive. |
| **Caddy for TLS**, not nginx + certbot | Automatic Let's Encrypt issuance/renewal with minimal config — converts a recurring chore into a one-time setup step. |
| **Coolify (optional) for deploys** | A free, self-hosted PaaS layer on top of the VPS — git-push deploys, automatic TLS, rollbacks — closes most of the ergonomic gap between "raw VPS" and "managed platform" at no extra cost. |

### Extra operational work VPS entails (vs. fully managed)

One-time (~2–3 days, mostly overlapping with app development): server hardening (SSH keys, firewall, fail2ban), Postgres install/tuning, TLS setup, deploy pipeline, automated backups (cron + object storage), uptime/resource monitoring.

Recurring: OS patches mostly automated via `unattended-upgrades` (~30 min/month); periodic backup-restore verification (an hour, quarterly); incident response is on you, not a support ticket; capacity watching before hitting resource limits. Net estimate: **2–4 hours/month** at pilot/growth scale — the trade for the cost savings below.

---

## 10. Cost analysis

Estimated monthly infra cost by stage (Aug 2026 pricing, VPS = Hetzner/DigitalOcean self-managed, AWS = App Runner + RDS):

| Stage | VPS (self-managed) | AWS managed |
|---|---|---|
| Pilot (<20 restaurants) | ~$12–15/mo | ~$35–55/mo |
| Growth (~200 restaurants) | ~$50–70/mo | ~$150–250/mo |
| Scale (~1,000 restaurants) | ~$200–350/mo | ~$500–900+/mo |

These are estimates based on published provider pricing, not committed quotes — actual cost depends on real traffic patterns, not just tenant count. The VPS route trades roughly 2–4 hours/month of ops time (§9) for a 2.5–3x cost reduction sustained across the whole growth trajectory.

---

## 11. Open items — not yet decided

- Staff/waiter role (narrower than admin) — schema supports adding it trivially, not built.
- General-purpose audit log beyond price overrides (who edited a menu price, who voided an order) — cheap to add now, expensive to reconstruct retroactively if skipped too long.
- Data retention policy for canceled accounts — delete after N days, keep read-only, or export-on-request?
- bKash recurring-charge trigger mechanism (Tokenized Checkout vs. manual reminder-and-pay) — affects checkout UX, needs a decision before that screen is built.
- Tax/VAT handling specifics — Bangladesh VAT rate application, whether it's configurable per restaurant.
- `max_orders_per_month` and other usage-based limits — mentioned in §5 as worth considering, not yet built.

---

## 12. Schema reference

Full DDL lives in `schema.sql` in this same output. Table groups:

- **Platform**: `users`, `platform_admins`, `features`, `subscription_plans`, `plan_features`
- **Billing**: `accounts`, `account_status_log`, `account_feature_overrides`, `account_subscriptions`, `payments`
- **Tenancy**: `restaurants`, `restaurant_status_log`, `restaurant_users`
- **Operations**: `tables`, `menu_categories`, `menu_items`, `orders`, `order_items`, `order_item_price_adjustments`, `restaurant_invoice_counters`, `invoices`

RLS policy shape and the reasoning behind user-scoped (not restaurant-scoped) access is documented in inline comments in `schema.sql` §"ROW-LEVEL SECURITY".
