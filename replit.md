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

## Running

The workflow `Start application` runs: `CGO_ENABLED=1 go run . 2>&1`

The app requires `TG_TOKEN` and `MAX_TOKEN` to start. It will exit immediately if either is missing.

## Database

When `DATABASE_URL` is set (PostgreSQL), it uses that instead of SQLite. A PostgreSQL database is provisioned in this Replit environment.

Migrations are applied automatically on startup via `github.com/golang-migrate/migrate/v4`.
