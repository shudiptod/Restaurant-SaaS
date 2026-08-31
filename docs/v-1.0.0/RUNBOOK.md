# RUNBOOK — Local Setup, Deployment, Service Flow, Migrations

Companion to `PROJECT_PLANNER.md` (read that first for *why*; this file is *how*). Assumes the Go project layout implied by the stack decisions in the planner (D1–D18) — adjust paths if your actual repo differs, but keep the workflow shape.

---

## 1. How the pieces talk to each other

**Request path**: `Browser → Caddy (TLS) → Go app (Gin) → PostgreSQL`

- **Caddy** terminates TLS (automatic Let's Encrypt cert) and reverse-proxies everything to the Go binary on a local port (e.g. `127.0.0.1:8080`). Caddy is the only thing exposed on 443; the Go app never faces the internet directly.
- **Go app** (Gin router) handles the request. Auth middleware runs first: it reads the session, resolves the logged-in `user_id`, and — critically — runs `SET app.current_user_id = '<uuid>'` on the Postgres connection for that request before any query touches a tenant-scoped table. This single line is what makes every RLS policy in the schema work; skipping it on any code path is a tenant-isolation bug.
- Most pages are full server-rendered HTML. htmx-driven interactions (adding an order line, live totals, menu search) hit small Gin routes that return an HTML *fragment*, not a full page — htmx swaps it into the DOM. There is no JSON API layer for the browser; the browser only ever receives HTML.
- **PostgreSQL** — every tenant-scoped table enforces RLS as described in the planner (D4). The Go app should use a connection-pooled non-superuser DB role for normal requests (superuser bypasses RLS, which would silently defeat the whole isolation model) and a separate, explicitly privileged role only for platform-admin dashboard queries.

**Payments (bKash)** — two separate flows, don't conflate them:
- *Outbound*: Go app calls bKash's Create/Execute Payment API when an owner initiates a subscription payment.
- *Inbound*: bKash calls back to a webhook endpoint on the Go app to confirm payment status. This request does **not** go through a logged-in user session — verify it via bKash's signature/callback validation, then look up the payment by `provider_payment_id`, insert/update the `payments` row, and rely on the `UNIQUE (provider, provider_payment_id)` constraint to make retried callbacks harmless.

**Invoices** — on order close, the Go app renders the invoice (HTML→PDF or a PDF library), uploads it to object storage (S3-compatible — Backblaze B2 or your VPS provider's equivalent), and stores the resulting URL in `invoices.pdf_url`. The PDF itself is never stored in Postgres.

---

## 2. Running locally

**Prerequisites**: Go (1.22+), PostgreSQL (15+) or Docker, `golang-migrate` CLI.

```bash
# 1. Postgres via Docker (fastest path — skip if you already run Postgres locally)
docker run --name rms-postgres -e POSTGRES_PASSWORD=devpass -e POSTGRES_DB=rms_dev -p 5432:5432 -d postgres:16

# 2. Install the migrate CLI (one-time)
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# 3. Point at your local DB
export DATABASE_URL="postgres://postgres:devpass@localhost:5432/rms_dev?sslmode=disable"

# 4. Apply all migrations in order
migrate -path ./migrations -database "$DATABASE_URL" up

# 5. Load reference data + dev fixtures
psql "$DATABASE_URL" -f seed.sql

# 6. Run the app (adjust to your actual entrypoint)
go run ./cmd/server
```

The app should read `DATABASE_URL`, session secret, and bKash sandbox credentials from environment variables (`.env` locally via a loader like `godotenv`, real secrets manager in prod — this is gap #2 in the planner, decide the concrete mechanism before shipping). Never commit real bKash credentials or the session secret to the repo.

Visit `http://localhost:8080`. Use the dev fixture login from `seed.sql` (`owner@example.com`) once auth is wired up — remember its `password_hash` is a placeholder, not a real hash; set a real one locally before testing login.

---

## 3. Deploying to the VPS

One-time server setup (see planner §5/§7 for the reasoning behind each of these):

```bash
# On a fresh Ubuntu VPS
sudo apt update && sudo apt upgrade -y
sudo apt install -y postgresql caddy unattended-upgrades fail2ban ufw

# Firewall: only SSH, HTTP, HTTPS
sudo ufw allow OpenSSH
sudo ufw allow 80,443/tcp
sudo ufw enable

# Postgres: create the app's database and a non-superuser role
sudo -u postgres createdb rms_prod
sudo -u postgres psql -c "CREATE ROLE rms_app WITH LOGIN PASSWORD '<strong-password>';"
sudo -u postgres psql -d rms_prod -c "GRANT ALL ON SCHEMA public TO rms_app;"
```

**Caddy** (`/etc/caddy/Caddyfile`) — automatic HTTPS with zero manual cert handling:

```
yourapp.com {
    reverse_proxy 127.0.0.1:8080
}
```

**Deploying the binary** (simplest version — a systemd service; swap for Coolify if you want git-push deploys and rollbacks instead):

```bash
# Build for Linux from your dev machine, or build on the server directly
GOOS=linux GOARCH=amd64 go build -o rms-server ./cmd/server
scp rms-server youruser@your-vps-ip:/opt/rms/rms-server

# /etc/systemd/system/rms.service
[Unit]
Description=Restaurant Management SaaS
After=network.target postgresql.service

[Service]
ExecStart=/opt/rms/rms-server
EnvironmentFile=/opt/rms/.env
Restart=on-failure
User=rms

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now rms
```

Redeploying after a code change is: rebuild → `scp` the new binary over → `sudo systemctl restart rms`. A short shell script wrapping those three lines is enough until deploy frequency justifies Coolify or CI/CD (planner gap #4).

**Backups** — set up before real tenant data exists, not after:

```bash
# Daily cron: dump + upload to object storage, keep 14 days
0 2 * * * pg_dump rms_prod | gzip | aws s3 cp - s3://your-bucket/backups/rms-$(date +\%F).sql.gz --endpoint-url=<your-provider-s3-endpoint>
```

---

## 4. Running migrations after a schema change

**Creating a new migration** (never hand-edit an already-applied migration file — always add a new one):

```bash
migrate create -ext sql -dir ./migrations -seq add_staff_role_to_restaurant_role
# creates: migrations/000N_add_staff_role_to_restaurant_role.up.sql
#          migrations/000N_add_staff_role_to_restaurant_role.down.sql
```

Write the schema change in the `.up.sql` file and its exact reverse in `.down.sql`. Test both directions locally before touching prod:

```bash
migrate -path ./migrations -database "$DATABASE_URL" up      # apply
migrate -path ./migrations -database "$DATABASE_URL" down 1  # roll back the one you just applied, confirm it's clean
migrate -path ./migrations -database "$DATABASE_URL" up      # re-apply
```

**Applying to production** — always in this order, never skip the backup:

```bash
# 1. Backup first, unconditionally
pg_dump rms_prod | gzip > pre-migration-backup-$(date +%F).sql.gz

# 2. Check current version
migrate -path ./migrations -database "$PROD_DATABASE_URL" version

# 3. Apply
migrate -path ./migrations -database "$PROD_DATABASE_URL" up

# 4. Verify the app still boots and a basic request works before walking away
```

If a migration fails partway on Postgres, `golang-migrate` marks the version **dirty** — do not blindly re-run `up`. Inspect what actually got applied by hand, fix the underlying migration file if it had a bug, then use `migrate force <version>` to tell the tool the true current state before proceeding. This is the one place where guessing is expensive — read the actual DB state first.

**A note on RLS-related migrations specifically**: adding a new tenant-scoped table without also adding its `ENABLE ROW LEVEL SECURITY` + `CREATE POLICY` pair in the same migration is the single easiest way to silently reintroduce a cross-tenant data leak. Treat "new table" and "its RLS policy" as one atomic unit when writing a migration, not two separate steps you might forget the second half of.

---

## 5. Quick reference: file → purpose

| File | Purpose |
|---|---|
| `migrations/*.sql` | Applied in order via `migrate up`; the authoritative schema history |
| `schema.sql` | Current-state snapshot for reference/ERD generation — not applied directly, migrations are the source of truth |
| `seed.sql` | Reference data (safe anywhere) + dev-only fixtures (local/staging only) |
| `PROJECT_PLANNER.md` | Why every decision was made; read before changing architecture |
| `RUNBOOK.md` (this file) | How to actually run, deploy, and migrate |
