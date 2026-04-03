# PostSynk

Сервис односторонней синхронизации постов из Telegram-каналов в MAX (TG → MAX). Управление через Mini App внутри Telegram-бота.

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
| `api.go` | REST API server |
| `repository.go` | Repository interface |
| `sqlite.go` / `postgres.go` | DB implementations |
| `sync_worker.go` | MTProto retrospective sync |
| `frontend/index.html` | Mini App UI |

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

## REST API Endpoints

All endpoints require `Authorization: tg <initData>` or `max <initData>` header.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/channels` | List active crosspost pairs |
| `POST` | `/api/channels/pair` | Create a crosspost pair |
| `DELETE` | `/api/channels/delete` | Remove a crosspost pair |
| `GET` | `/api/tasks` | List sync tasks |
| `POST` | `/api/tasks/cancel` | Cancel a running sync task |
| `DELETE` | `/api/tasks/history` | Clear completed/failed/cancelled tasks |
| `POST` | `/api/sync/start` | Start a retrospective sync task |
| `POST` | `/api/history/clear` | Clear message mappings for a period |
| `GET` | `/api/profile` | User profile |
| `GET` | `/api/max/chats` | List known MAX chats (where bot is present) |
| `GET/POST` | `/api/replacements` | Auto-replacement rules |
| `PATCH` | `/api/settings` | Update pair direction/live_listen |
| `GET` | `/api/config` | MTProto availability status |
| `POST` | `/api/tg-auth-code` | Submit MTProto auth code |
| `POST` | `/api/tg-2fa-password` | Submit MTProto 2FA password |

## HTTP Server

Always starts on `WEBHOOK_PORT` (default 8443):
- Webhook paths registered when `WEBHOOK_URL` is set
- API always at `/api/*`
- Frontend at `/app/*`

## Database Migrations

Applied automatically on startup. Current: migration 000015 (`max_known_chats` table).

## Running

Workflow `Start application`: `CGO_ENABLED=1 go run . 2>&1`
