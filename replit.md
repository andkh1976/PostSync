# MaxTelegramBridgeBot

A bridge bot between Telegram and the MAX messenger. Forwards messages, media, files, and edits between linked chats in both directions.

## Architecture

- **Language**: Go (1.24)
- **Type**: Backend service / bot (no frontend)
- **Database**: SQLite (default) or PostgreSQL (via `DATABASE_URL`)
- **Build**: `CGO_ENABLED=1 go build .` (required for go-sqlite3)

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
| `WEBHOOK_PORT` | Webhook server port | `8443` |
| `LOG_LEVEL` | Log level: debug, info, warn, error | `info` |
| `ALLOWED_USERS` | Comma-separated Telegram user IDs whitelist | — |
| `TG_MAX_FILE_SIZE_MB` | Max file size Telegram→MAX in MB | unlimited |
| `MAX_MAX_FILE_SIZE_MB` | Max file size MAX→Telegram in MB | unlimited |
| `MAX_ALLOWED_EXTENSIONS` | Allowed file extensions whitelist | all |
| `MESSAGE_FORMAT` | `inline` or `newline` | `inline` |
| `TG_APP_ID` | MTProto App ID from my.telegram.org (enables retrospective sync) | — |
| `TG_APP_HASH` | MTProto App Hash from my.telegram.org | — |
| `TG_PHONE` | Phone number for first-time MTProto auth (international format) | — |
| `TG_SESSION_FILE` | Path to MTProto session file | `tg_session.json` |

## Retrospective Sync Worker

When `TG_APP_ID` and `TG_APP_HASH` are set, a background sync worker starts that:

1. Polls the `sync_tasks` table every 30 seconds for tasks with status `pending`
2. Sets each task to `processing`, then fetches TG channel history via MTProto for the `start_date`–`end_date` period
3. Skips already-forwarded messages (checks the `messages` table by `tg_message_id`)
4. Forwards new messages to MAX with a 2-second pause between sends to avoid rate limits
5. Updates `last_synced_id` after each batch (allows resuming on failure)
6. Sets the task to `done` (or `failed` with error text) when finished

**First-time auth**: On first run with no session file, set `TG_PHONE` and the bot will prompt for the SMS code on stdin. The session is then saved to `TG_SESSION_FILE` and reused on subsequent restarts.

**Live mode is unaffected**: The sync worker runs in a separate goroutine and does not interfere with real-time message bridging.

## Running

The workflow `Start application` runs: `CGO_ENABLED=1 go run . 2>&1`

The app requires `TG_TOKEN` and `MAX_TOKEN` to start. It will exit immediately if either is missing.

## Database

When `DATABASE_URL` is set (PostgreSQL), it uses that instead of SQLite. A PostgreSQL database is provisioned in this Replit environment.

Migrations are applied automatically on startup via `github.com/golang-migrate/migrate/v4`.
