# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Vendel is a full-stack SMS gateway platform that allows sending SMS messages using registered devices (Android phones or modems) as gateways. Features include quota management, webhooks, multi-user support, and API key authentication.

## Commands

### Development (Docker Compose - Recommended)
```bash
docker compose up -d              # Start app
docker compose logs -f app        # View logs
docker compose down -v            # Clean up with volumes
```

### Modem Agent (Docker)
```bash
docker compose --profile modem up -d                    # Start with modem agent
docker compose --profile modem logs -f modem-agent      # View modem logs
```

### Modem Agent (Local)
```bash
cd modem-agent
cp .env.example .env    # Edit with your device API key + serial ports
go run .
```

### Backend
```bash
cd backend
go run . serve --http=0.0.0.0:8090   # Run dev server
go build -o vendel .                   # Build binary
go build ./...                        # Verify compilation
```

### Frontend
```bash
cd frontend
fnm use                       # Switch to Node 24 (or nvm use)
npm install
npm run dev                   # Dev server at localhost:5173
npm run build                 # TypeScript + Vite build
npm run lint                  # Biome check with auto-fix

# E2E tests (requires backend running)
npx playwright test
npx playwright test --ui      # Interactive UI mode
```

## Architecture

### Backend (`backend/`)
- **Framework**: PocketBase (Go) - provides auth, CRUD, admin dashboard, cron, migrations
- **Database**: SQLite (embedded)
- **Key directories**:
  - `hooks/` - Record lifecycle hooks, one file per domain (auth, users, devices, etc.)
  - `cronjobs/` - Periodic background tasks (quota reset, retries, purge)
  - `handlers/` - Custom API routes (sms, plans, webhooks)
  - `services/` - Business logic (SMS orchestration, FCM, quota, subscriptions, webhooks)
  - `services/payment/` - Payment provider abstraction (QvaPay)
  - `middleware/` - API key auth, maintenance mode
  - `migrations/` - PocketBase collection definitions + seed data
  - `ui/` - Embedded frontend via `go:embed` (build output in `ui/dist/`)
  - `main.go` - Thin wiring layer: env, app init, OnServe config, Register* calls

### Frontend (`frontend/`)
- **Framework**: React 19 + TypeScript + Vite
- **Routing/State**: TanStack Router + TanStack Query
- **Styling**: Tailwind CSS + shadcn/ui components
- **Key directories**:
  - `src/routes/` - Pages using TanStack Router file-based routing
  - `src/components/` - React components
  - `src/hooks/` - Custom React hooks using PocketBase JS SDK
  - `src/lib/pocketbase.ts` - PocketBase client instance

### Services Integration
- **FCM**: Firebase Cloud Messaging for push notifications to devices (via goroutines)
- **Payments**: QvaPay for subscription billing
- **Email**: PocketBase built-in SMTP, Mailcatcher for local dev at localhost:1080
- **AWS End User Messaging (AEUM)**: optional provider for SMS, short codes, and RCS text. Lives in `backend/services/smsprovider/`. Enabled via `AEUM_ENABLED=true` and an AWS phone pool ARN. See `docs/aws-end-user-messaging-setup.md`. The interface is intentionally tiny (`Provider` with `Name`, `IsConfigured`, `Send`); adding a new provider (Twilio, MessageBird, etc.) means adding a file in `services/smsprovider/` and a singleton constructor.

## Design System

The Vendel design system is defined in the **ender-homepage** repo (`../ender-homepage/src/pages/design-system.astro`) and documented at `/design-system` on the homepage site. It is the **single source of truth** for colors, typography, components, and patterns.

- **Reference**: `../ender-homepage/src/styles/global.css` — all CSS custom properties (colors, fonts, neutrals, code syntax)
- **Dashboard mapping**: `frontend/src/index.css` maps the same palette to shadcn/ui semantic variables
- **Fonts**: Inter (sans/body), Libre Baskerville (serif/headings), JetBrains Mono (mono/code) — loaded via Google Fonts in `frontend/index.html`
- **Accent**: `#2dd4a8` (mint/teal) — used consistently across both projects
- **Neutrals**: Mint-tinted gray scale (50–950), not standard Tailwind grays

When changing visual styles (colors, fonts, spacing, component patterns), update the design system page in ender-homepage **first**, then propagate changes to the dashboard's `frontend/src/index.css`.

## Code Quality Standards

### Backend
- Go standard formatting (`gofmt`)
- All code must compile: `go build ./...`

### Backend Structure Conventions

**main.go must stay thin** (~80 lines). It only contains: env loading, PocketBase init, migration config, `OnServe` bootstrap (FCM, seed, OAuth, SMTP, routes, middleware), `Register*` calls, and `app.Start()`. **Never add hooks or cron jobs directly to main.go.**

**Where to put new code:**

| Type of code | Where it goes | Pattern |
|---|---|---|
| Record lifecycle hook (`OnRecord*`) | `hooks/<domain>.go` | `RegisterXxxHooks(app *pocketbase.PocketBase)` |
| Cron job | `cronjobs/jobs.go` | Add a `register(...)` call in `RegisterCronJobs` |
| API route | `handlers/<domain>.go` | `RegisterXxxRoutes(se *core.ServeEvent)` |
| Business logic | `services/<domain>.go` | Pure functions taking `core.App` |
| Request middleware | `middleware/` | Func returning `func(*core.RequestEvent) error` |
| External SMS provider (AEUM, future Twilio, etc.) | `services/smsprovider/<provider>.go` | `New<X>Provider(cfg)` returns a `Provider`; singleton via `Default<X>()` if env-driven |

**Rules:**
- Each `hooks/` file owns **one domain** (e.g., devices, webhooks). If a new collection needs hooks, create a new file — don't append to an unrelated one.
- Hook files export a **single** `RegisterXxxHooks` function. Call it from `main.go` alongside the others.
- Cron jobs go in `cronjobs/jobs.go` using the `register()` helper to avoid boilerplate.
- Service functions accept `core.App` (the interface), not `*pocketbase.PocketBase`, so they stay testable.
- When create and update hooks share logic, extract a **private helper** in the same file (e.g., `computeNextRunAt`) instead of duplicating code.

### Frontend
- Biome for linting and formatting
- TypeScript strict mode
- PocketBase JS SDK for all API calls (no auto-generated client)
- All visual styles must follow the design system (see above)

## SDKs

Official client libraries live in separate repos, each with its own publish workflow:

| SDK | Repository | Registry | Publish trigger |
|-----|-----------|----------|----------------|
| JavaScript/TypeScript | [vendel-sdk-js](https://github.com/JimScope/vendel-sdk-js) | npm (`vendel-sdk`) | `git tag v* && git push origin v*` |
| Python | [vendel-sdk-python](https://github.com/JimScope/vendel-sdk-python) | PyPI (`vendel-sdk`) | `git tag v* && git push origin v*` |
| Go | [vendel-sdk-go](https://github.com/JimScope/vendel-sdk-go) | Go modules | `git tag v* && git push origin v*` |

## Development URLs
| Service | URL |
|---------|-----|
| App (API + Frontend) | http://localhost:8090 |
| PocketBase Admin | http://localhost:8090/_/ |
| Frontend (dev server) | http://localhost:5173 |
| Mailcatcher | http://localhost:1080 |
