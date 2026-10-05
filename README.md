# Restaurant SaaS

A multi-tenant restaurant management application built with Go, Gin, PostgreSQL, server-rendered HTML templates, and htmx.

## Requirements

- Go 1.25 or newer
- PostgreSQL 15 or newer, or Docker
- Git

## Run locally

1. Start a local PostgreSQL database. For a disposable development database with Docker:

   ```sh
   docker run --name restaurant-saas-db \
     -e POSTGRES_USER=postgres \
     -e POSTGRES_PASSWORD=devpass \
     -e POSTGRES_DB=rms_dev \
     -p 5432:5432 \
     -d postgres:16
   ```

   If the container already exists, start it with `docker start restaurant-saas-db`.

2. Create your local environment file:

   ```sh
   cp .env.example .env
   ```

   Set `DATABASE_URL` to your local database. Add a long random `SESSION_SECRET`, and choose a platform owner username and password. The first platform owner password must be at least 12 characters. For example, generate a session secret with:

   ```sh
   openssl rand -hex 32
   ```

   Put the generated value and your chosen bootstrap username/password in `.env` as `SESSION_SECRET`, `PLATFORM_OWNER_USERNAME`, and `PLATFORM_OWNER_PASSWORD`. You can also set `PLATFORM_OWNER_EMAIL` and `PLATFORM_OWNER_NAME`. Do not commit `.env` or real credentials.

3. Start the application from the repository root:

   ```sh
   go run ./cmd/server
   ```

   On startup, the app connects to `DATABASE_URL`, applies pending SQL migrations from `docs/`, loads reference data from `docs/seed.sql`, and creates the initial platform owner if no platform admin exists yet. Database migrations and seed data are managed by the application; you do not need to run a separate migration command for local startup.

4. Open [http://localhost:8080](http://localhost:8080). Use `/platform/login` with the bootstrap platform owner credentials. Create a customer account in the platform dashboard; no ready-to-use customer login is seeded by `docs/seed.sql`.

The server uses `PORT` if set (default `8080`) and `GIN_MODE` if set. For localhost, leave `COOKIE_SECURE` unset or set it to `false`; HTTPS deployments should set it to `true`.

To stop the database container:

```sh
docker stop restaurant-saas-db
```

## Build and tests

Build all Go packages:

```sh
go build ./...
```

Run the auth tests:

```sh
go test ./internal/auth
```

The handler integration tests use a local PostgreSQL database at `postgres://postgres:devpass@localhost:5432/rms_dev?sslmode=disable`. The focused order and discount tests can be run with:

```sh
go test ./internal/handlers -run '^(TestCalculateDiscount|TestParseDiscount|TestCreateOrderResumesOpenTableOrder)$' -count=1
```

The full `go test ./...` suite currently includes older tests with stale migration-runner and demo-login assumptions; see the test output if running the entire suite.

## Alwaysdata deployment with GitHub Actions

Deployment is defined in [`.github/workflows/deploy.yml`](.github/workflows/deploy.yml). A push to `main` starts the workflow. It checks out the repository, installs the Go version declared in `go.mod`, builds a statically linked Linux `amd64` executable for `./cmd/server`, uploads it to the Alwaysdata account, swaps it into place, and terminates the old process:

1. Build `bangla-saas-backend.new` with `GOOS=linux`, `GOARCH=amd64`, and `CGO_ENABLED=0`.
2. Upload it to `/home/shudipta/bangla-saas/` over SCP.
3. Make it executable, rename it to `bangla-saas-backend`, then run `pkill -x bangla-saas-backend`.

The workflow requires these repository-level GitHub Actions secrets:

| Secret            | Value                                                  |
| ----------------- | ------------------------------------------------------ |
| `SSH_HOST`        | Alwaysdata SSH host name                               |
| `SSH_USER`        | Alwaysdata SSH account/user                            |
| `SSH_PRIVATE_KEY` | Private key authorized for that Alwaysdata SSH account |

Add them in the GitHub repository under **Settings → Secrets and variables → Actions**. Do not put the private key in the repository. The deployment target path is currently hard-coded in the workflow; change that workflow if the Alwaysdata account uses a different home directory or deployment folder.

### Alwaysdata application configuration

The deployed executable is named `bangla-saas-backend` and lives in `/home/shudipta/bangla-saas/`. Configure the Alwaysdata app/process to run that executable from this directory, and configure its environment there, including at least:

- `DATABASE_URL`
- `SESSION_SECRET`
- `PLATFORM_OWNER_USERNAME`
- `PLATFORM_OWNER_PASSWORD`
- `PORT` if required by the Alwaysdata application setup
- `GIN_MODE=release`
- `COOKIE_SECURE=true` when served behind HTTPS

The workflow does **not** upload `.env` or configure environment variables; keep production configuration in Alwaysdata. The binary embeds the templates, static assets, and `docs/` files at build time, so those assets and SQL migrations ship inside the executable.

**Process restart behavior:** the workflow renames the binary and kills the process, but it does not run an explicit Alwaysdata start/restart command. The Alwaysdata process configuration must supervise/relaunch the configured executable when it exits. Confirm that restart behavior in the Alwaysdata panel before relying on automated deployments; otherwise the workflow can leave the application stopped after `pkill`.

The app applies database migrations automatically at startup. Back up the production database before deploying a release that adds or changes migrations, and verify the Alwaysdata process logs and application URL after deployment.

## Repository layout

- `cmd/server/`: application entry point and route registration
- `internal/`: auth, database, models, and request handlers
- `templates/`: server-rendered HTML pages and shared components
- `static/`: CSS and other static assets
- `docs/`: runtime SQL migrations and reference seed data
- `.github/workflows/deploy.yml`: push-to-main deployment workflow
