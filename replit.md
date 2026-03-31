# MaxTelegramBridgeBot

A bridge bot between Telegram and the MAX messenger. Forwards messages, media, files, and edits between linked chats in both directions. Includes a web Mini App for management (Sprint 3).

## Architecture

- **Language**: Go (1.25)
- **Type**: Backend service / bot + REST API + Mini App frontend
- **Database**: SQLite (default) or PostgreSQL (via `DATABASE_URL`)
- **Build**: `CGO_ENABLED=1 go build .` (required for go-sqlite3)
- **Frontend**: `frontend/index.html` — vanilla HTML/JS Mini App served at `/app/`

## Key Files

| File | Purpose |
|------|---------|
| `main.go` | Entry point, config from env |
| `bridge.go` | Core struct, Config, HTTP server startup |
| `telegram.go` | Telegram Bot updates / commands |
| `max.go` | MAX Bot updates / commands |
| `api.go` | REST API server (Sprint 3) |
| `repository.go` | Repository interface |
| `sqlite.go` / `postgres.go` | DB implementations |
| `sync_worker.go` | MTProto retrospective sync |
| `frontend/index.html` | Mini App UI (Sprint 3) |

## Required Environment Variables

| Variable | Description |
|----------|-------------|
| `TG_TOKEN` | Telegram Bot API token (required) |
| `MAX_TOKEN` | MAX messenger Bot API token (required) |

## Optional Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL DSN (if set, SQLite is ignored) | — |
| `DB_PATH` | SQLite database path | `bridge.db` |
| `WEBHOOK_URL` | Base URL for webhook mode | — (uses long polling) |
| `WEBHOOK_PORT` | HTTP server port (webhooks + API) | `8443` |
| `LOG_LEVEL` | Log level: debug, info, warn, error | `info` |
| `ALLOWED_USERS` | Comma-separated Telegram user IDs whitelist | — |
| `TG_MAX_FILE_SIZE_MB` | Max file size Telegram→MAX in MB | unlimited |
| `MAX_MAX_FILE_SIZE_MB` | Max file size MAX→Telegram in MB | unlimited |
| `MAX_ALLOWED_EXTENSIONS` | Allowed file extensions whitelist | all |
| `MESSAGE_FORMAT` | `inline` or `newline` | `inline` |
| `TG_APP_ID` | MTProto App ID (enables retrospective sync) | — |
| `TG_APP_HASH` | MTProto App Hash | — |
| `TG_PHONE` | Phone for first-time MTProto auth | — |
| `TG_SESSION_FILE` | MTProto session file path | `tg_session.json` |
| `MINI_APP_URL` | Mini App public URL (shows button in bots) | — |
| `MINI_APP_DIR` | Path to frontend static files | `frontend` |

## Sprint 4 Correction: Legacy Commands Disabled, Mini App API Completed

### Changes (Sprint 4 Correction)

- **bridge.go** `registerCommands`: `/bridge`, `/unbridge`, `/crosspost` removed from Telegram bot command menu (only `/help` remains). DEPRECATED comments in place.
- **telegram.go**: `/bridge`, `/unbridge`, `/crosspost`, manual channel-ID forwarding blocks all commented out (DEPRECATED). Admin-check block commented out (no longer needed).
- **max.go**: `/bridge`, `/unbridge`, `/crosspost <TG_ID>`, manual forward-pairing blocks all commented out (DEPRECATED). `forwardMaxToTg` function and all its calls fully commented out — MAX→TG forwarding disabled. Admin-check block commented out.
- **api.go** — 3 new endpoints added:
  - `POST /api/channels/pair` — create a crosspost pair (replaces manual `/crosspost` bot flow)
  - `DELETE /api/channels` (via `/api/channels/delete`) — remove a crosspost pair
  - `GET/POST /api/replacements` — read and add auto-replacement rules for a pair
- **README.md** "Быстрый старт" updated: manual `/crosspost` + forward flow replaced with one-click Mini App pairing instructions.
- **bridge.go** `checkAccess`: already correct — returns `true` for all users, real billing logic remains commented.

## Sprint 4: Billing Stubs, Mini App Polish, Documentation

### checkAccess (bridge.go)
- `checkAccess(userID int64) bool` — access control stub. Currently always returns `true` (full access).
- Real subscription-check logic is **commented out** inside the function — uncomment one line to activate.
- Commented constant `freeUserRetroSyncMsgLimit = 500` added as a placeholder for free-tier message limits.

### New REST API Endpoints (api.go)
| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/profile` | Return user profile: ID, platform, subscription status |
| `GET` | `/api/tasks` | List the user's sync tasks with status (for progress bar) |

### Mini App UI updates (frontend/index.html)
- **Profile section**: shows user ID, platform, first name/username, subscription status.
- **Progress Bar**: `sync_tasks` are displayed with per-status animated progress bars (pending/processing/done/failed).
- **Auto-refresh**: tasks list auto-refreshes every 5 seconds when there are active tasks.
- **Theme support**: improved Telegram/MAX theme param application — uses all `themeParams` fields for correct light/dark rendering.

### New Repository methods
- `GetUserProfile(userID int64) (*UserProfile, error)` — reads user + subscription_end from `users` table.
- `ListUserSyncTasks(userID int64) ([]SyncTask, error)` — lists last 20 tasks for a user.

## Sprint 3: API + Mini App

### REST API Endpoints

All endpoints require an `Authorization` header: `tg <initData>` or `max <initData>`.
The initData is validated via HMAC-SHA256 using the bot tokens (Telegram WebApp auth spec).

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/channels` | List active crosspost pairs for the authenticated user |
| `POST` | `/api/sync/start` | Create a retrospective sync task (body: `tg_chat_id`, `max_chat_id`, `start_date`, `end_date`) |
| `POST` | `/api/history/clear` | Delete message mappings for a period (body: `tg_chat_id`, `start_date`, `end_date`) |
| `PATCH` | `/api/settings` | Update direction/live_listen for a pair (body: `max_chat_id`, `direction?`, `live_listen?`) |

### Mini App (frontend)

Served at `/app/` from the `frontend/` directory. Features:
- List of active channel pairs with direction selector and live-listen toggle
- Date picker for retrospective sync period
- "Start History Download" and "Reset History for Period" buttons
- Settings auto-saved via API

### Multitenancy

All API endpoints filter data by `owner_id` extracted from the initData auth header.

## Retrospective Sync Worker

When `TG_APP_ID` and `TG_APP_HASH` are set, a background sync worker:
1. Polls `sync_tasks` every 30s for `pending` tasks
2. Fetches TG channel history via MTProto for the specified date range
3. Forwards new messages to MAX with a 2s pause between sends
4. Updates `last_synced_id` for resumable progress

## HTTP Server

The HTTP server **always starts** (not only in webhook mode) on `WEBHOOK_PORT` (default 8443).
- Webhook paths for TG/MAX registered dynamically when `WEBHOOK_URL` is set
- API routes always available at `/api/*`
- Frontend served at `/app/*`

## Database Migrations

Applied automatically on startup. Current version: 14 (adds `live_listen` column to crossposts).

## Running

Workflow `Start application` runs: `CGO_ENABLED=1 go run . 2>&1`

Requires `TG_TOKEN` and `MAX_TOKEN` to start.
