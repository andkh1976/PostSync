package main

import (
        "context"
        "crypto/rand"
        "encoding/hex"
        "fmt"
        "log/slog"
        "os"
        "os/signal"
        "strconv"
        "strings"
        "syscall"

        maxbot "github.com/max-messenger/max-bot-api-client-go"

        tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func mustEnv(key string) string {
        v := os.Getenv(key)
        if v == "" {
                fmt.Fprintf(os.Stderr, "Environment variable %s is not set\n", key)
                os.Exit(1)
        }
        return v
}

func envOr(key, fallback string) string {
        if v := os.Getenv(key); v != "" {
                return v
        }
        return fallback
}

func genKey() string {
        b := make([]byte, 8)
        rand.Read(b)
        return hex.EncodeToString(b)
}

func logLevel() slog.Level {
        switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
        case "debug":
                return slog.LevelDebug
        case "warn":
                return slog.LevelWarn
        case "error":
                return slog.LevelError
        default:
                return slog.LevelInfo
        }
}

func main() {
        slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel()})))

        cfg := Config{
                MaxToken:    mustEnv("MAX_TOKEN"),
                TgBotURL:    envOr("TG_BOT_URL", ""),
                MaxBotURL:   envOr("MAX_BOT_URL", "https://max.ru/id710708943262_bot"),
                WebhookURL:  os.Getenv("WEBHOOK_URL"),
                WebhookPort: envOr("WEBHOOK_PORT", "8443"),
                TgAPIURL:    strings.TrimRight(os.Getenv("TG_API_URL"), "/"),
        }

        // Parse ALLOWED_USERS whitelist
        if v := os.Getenv("ALLOWED_USERS"); v != "" {
                for _, s := range strings.Split(v, ",") {
                        s = strings.TrimSpace(s)
                        if s == "" {
                                continue
                        }
                        id, err := strconv.ParseInt(s, 10, 64)
                        if err != nil {
                                slog.Error("Invalid ALLOWED_USERS value", "value", s, "err", err)
                                os.Exit(1)
                        }
                        cfg.AllowedUsers = append(cfg.AllowedUsers, id)
                }
                slog.Info("User whitelist enabled", "count", len(cfg.AllowedUsers))
        }

        // Parse file size limits
        if v := os.Getenv("TG_MAX_FILE_SIZE_MB"); v != "" {
                if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
                        cfg.TgMaxFileSizeMB = n
                } else {
                        slog.Error("Invalid TG_MAX_FILE_SIZE_MB value", "value", v)
                        os.Exit(1)
                }
        }
        if v := os.Getenv("MAX_MAX_FILE_SIZE_MB"); v != "" {
                if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
                        cfg.MaxMaxFileSizeMB = n
                } else {
                        slog.Error("Invalid MAX_MAX_FILE_SIZE_MB value", "value", v)
                        os.Exit(1)
                }
        }

        // Parse MAX_ALLOWED_EXTENSIONS whitelist (e.g. "pdf,docx,zip")
        // Если не задан — расширения не проверяются локально (ошибка придёт от CDN).
        if v := os.Getenv("MAX_ALLOWED_EXTENSIONS"); v != "" {
                cfg.MaxAllowedExts = make(map[string]struct{})
                for _, ext := range strings.Split(v, ",") {
                        ext = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(ext, ".")))
                        if ext != "" {
                                cfg.MaxAllowedExts[ext] = struct{}{}
                        }
                }
                slog.Info("MAX file extension whitelist enabled", "count", len(cfg.MaxAllowedExts))
        }

        // MESSAGE_FORMAT=newline — текст с новой строки после имени: "Имя:\nтекст"
        // По умолчанию (или MESSAGE_FORMAT=inline) — "Имя: текст"
        if strings.ToLower(os.Getenv("MESSAGE_FORMAT")) == "newline" {
                cfg.MessageNewline = true
                slog.Info("Message format: newline")
        }

        // MTProto (ретроспективный sync): TG_APP_ID и TG_APP_HASH — необязательные.
        // Если не заданы, sync worker не запускается.
        if v := os.Getenv("TG_APP_ID"); v != "" {
                id, err := strconv.Atoi(strings.TrimSpace(v))
                if err != nil || id <= 0 {
                        slog.Error("Invalid TG_APP_ID value", "value", v)
                        os.Exit(1)
                }
                cfg.TGAppID = id
        }
        cfg.TGAppHash = os.Getenv("TG_APP_HASH")
        cfg.TGPhone = os.Getenv("TG_PHONE")
        cfg.TG2FAPassword = os.Getenv("TG_2FA_PASSWORD")
        cfg.TGSessionFile = envOr("TG_SESSION_FILE", "tg_session.json")

        if cfg.TGAppID != 0 && cfg.TGAppHash != "" {
                slog.Info("MTProto sync worker enabled", "appID", cfg.TGAppID, "session", cfg.TGSessionFile)
        }

        // Mini App (Sprint 3): URL для кнопки WebApp и путь к папке фронтенда
        cfg.MiniAppURL = os.Getenv("MINI_APP_URL")
        cfg.MiniAppDir = envOr("MINI_APP_DIR", "frontend")
        if cfg.MiniAppURL != "" {
                slog.Info("Mini App configured", "url", cfg.MiniAppURL)
        }

        tgToken := mustEnv("TG_TOKEN")

        var repo Repository
        var err error
        dsn := os.Getenv("DATABASE_URL")
        if dsn == "" {
                slog.Error("DATABASE_URL is required (SQLite has been removed for better performance)")
                os.Exit(1)
        }
        
        repo, err = NewPostgresRepo(dsn)
        if err != nil {
                slog.Error("PostgreSQL error", "err", err)
                os.Exit(1)
        }
        slog.Info("DB: PostgreSQL")
        defer repo.Close()

        var tgBot *tgbotapi.BotAPI
        if tgAPI := os.Getenv("TG_API_URL"); tgAPI != "" {
                tgBot, err = tgbotapi.NewBotAPIWithAPIEndpoint(tgToken, tgAPI+"/bot%s/%s")
                if err != nil {
                        slog.Error("TG bot error", "err", err)
                        os.Exit(1)
                }
                slog.Info("Telegram bot started (local API — large file support enabled, up to 4 GB with Premium)", "username", tgBot.Self.UserName, "api", tgAPI)
        } else {
                tgBot, err = tgbotapi.NewBotAPI(tgToken)
                if err != nil {
                        slog.Error("TG bot error", "err", err)
                        os.Exit(1)
                }
                slog.Info("Telegram bot started (standard API — file limit 50 MB upload / 20 MB download)", "username", tgBot.Self.UserName)
        }

        maxApi, err := maxbot.New(cfg.MaxToken)
        if err != nil {
                slog.Error("MAX bot error", "err", err)
                os.Exit(1)
        }
        maxInfo, err := maxApi.Bots.GetBot(context.Background())
        if err != nil {
                slog.Error("MAX bot info error", "err", err)
                os.Exit(1)
        }
        slog.Info("MAX bot started", "name", maxInfo.Name)

        ctx, cancel := context.WithCancel(context.Background())
        defer cancel()

        sigCh := make(chan os.Signal, 1)
        signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
        go func() {
                <-sigCh
                slog.Info("Shutting down...")
                cancel()
        }()

        bridge := NewBridge(cfg, repo, tgBot, maxApi)
        bridge.Run(ctx)
        slog.Info("Bridge stopped")
}
