package main

import (
        "context"
        "crypto/sha256"
        "encoding/hex"
        "log/slog"
        "net/http"
        "sync"
        "sync/atomic"
        "time"

        maxbot "github.com/max-messenger/max-bot-api-client-go"

        tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Config — настройки bridge, читаемые из env.
type Config struct {
        MaxToken     string  // токен MAX API (нужен для direct-send/upload)
        TgBotURL     string  // ссылка на TG-бота для /help
        MaxBotURL    string  // ссылка на MAX-бота для /help
        WebhookURL   string  // базовый URL для webhook (если пусто — long polling)
        WebhookPort  string  // порт для webhook сервера
        TgAPIURL         string  // custom TG Bot API URL (если пусто — api.telegram.org)
        AllowedUsers     []int64 // whitelist TG user IDs (empty = allow all)
        AdminChatID      int64   // TG chat ID куда слать системные уведомления и ошибки
        TgMaxFileSizeMB  int     // max file size TG->MAX in MB (0 = unlimited)
        MaxMaxFileSizeMB int     // max file size MAX->TG in MB (0 = unlimited)
        // MaxAllowedExts — whitelist расширений для TG→MAX (nil = не проверять локально).
        // Если задан, файлы с не-вхождением блокируются до отправки на CDN.
        MaxAllowedExts map[string]struct{}
        // MessageNewline — если true, текст идёт с новой строки после имени отправителя:
        // "Имя:\nтекст" вместо "Имя: текст". Задаётся через env MESSAGE_FORMAT=newline.
        MessageNewline bool

        // MTProto (Telegram user client) — для ретроспективного скачивания истории каналов.
        TGAppID       int    // TG_APP_ID — ID приложения из my.telegram.org
        TGAppHash     string // TG_APP_HASH — хэш приложения из my.telegram.org
        TGPhone       string // TG_PHONE — номер телефона для первичной авторизации
        TG2FAPassword string // TG_2FA_PASSWORD — облачный пароль 2FA (если включена)
        TGSessionFile string // TG_SESSION_FILE — путь к файлу сессии (default: tg_session.json)

        // Mini App (Sprint 3)
        MiniAppURL string // MINI_APP_URL — URL Mini App для кнопки WebApp в ботах
        MiniAppDir string // MINI_APP_DIR — путь к папке с фронтендом (default: frontend)
}

// chatBreaker хранит состояние circuit breaker для одного чата.
type chatBreaker struct {
        fails    int
        blockedAt time.Time
}

const (
        cbMaxFails = 3              // после N фейлов — блокируем
        cbCooldown = 5 * time.Minute // на сколько блокируем
)

// Bridge — основная структура, объединяющая зависимости.
type Bridge struct {
        cfg        Config
        repo       Repository
        tgBot      *tgbotapi.BotAPI
        maxApi     *maxbot.Api
        httpClient *http.Client // для скачивания/загрузки файлов (большой таймаут)
        apiClient  *http.Client // для коротких API-запросов (малый таймаут)
        whSecret   string // random path segment for webhook URLs

        // mtprotoReady — true когда глобальный (дежурный) MTProto успешно авторизован (legacy)
        mtprotoReady atomic.Bool

        // MTProto SaaS Auth Flows
        authFlowsMu sync.Mutex
        authFlows   map[int64]*AuthFlow // TG User ID -> flow

        cpWaitMu sync.Mutex
        cpWait   map[int64]int64 // MAX userId → TG channel ID (ожидание пересылки)

        cpTgOwnerMu sync.Mutex
        cpTgOwner   map[int64]int64 // TG channel ID → TG user ID (кто переслал пост)

        cbMu       sync.Mutex
        breakers   map[int64]*chatBreaker // destination chatID → breaker

        // Буферизация TG media groups (альбомы)
        mgMu      sync.Mutex
        mgBuffers map[string]*mediaGroupBuffer // MediaGroupID → buffer

        // cancelledTasks — задачи, которые пользователь попросил отменить во время выполнения
        cancelledTasks sync.Map // key: int64 taskID, value: struct{}

        // tgSeenMsgs — кольцевой буфер для дедупликации Telegram канальных постов.
        // Локальный Telegram Bot API сервер может генерировать два ChannelPost
        // с разными UpdateID для одного и того же поста — дедуплицируем по (chatID, msgID).
        tgSeenMu   sync.Mutex
        tgSeenMsgs [256]tgMsgKey // хранит последние 256 (chatID, msgID)
        tgSeenHead int
        tgSeenLen  int
}

// tgMsgKey — ключ для дедупликации TG-сообщений.
type tgMsgKey struct {
        chatID int64
        msgID  int
}


// NewBridge создаёт экземпляр Bridge.
func NewBridge(cfg Config, repo Repository, tgBot *tgbotapi.BotAPI, maxApi *maxbot.Api) *Bridge {
        // Derive webhook secret from tokens (stable across restarts)
        h := sha256.Sum256([]byte(cfg.MaxToken + tgBot.Token))
        secret := hex.EncodeToString(h[:8])

        // Transport для transfer файлов: большой таймаут, отдельный пул соединений
        transferTransport := &http.Transport{
                MaxIdleConns:        20,
                MaxIdleConnsPerHost: 5,
                IdleConnTimeout:     90 * time.Second,
        }
        // Transport для коротких API-запросов: отдельный пул, не засоряется при upload
        apiTransport := &http.Transport{
                MaxIdleConns:        20,
                MaxIdleConnsPerHost: 10,
                IdleConnTimeout:     90 * time.Second,
        }

        return &Bridge{
                cfg:    cfg,
                repo:   repo,
                tgBot:  tgBot,
                maxApi: maxApi,
                httpClient: &http.Client{
                        Timeout:   15 * time.Minute, // для download/upload больших файлов (до 4 ГБ)
                        Transport: transferTransport,
                },
                apiClient: &http.Client{
                        Timeout:   15 * time.Second, // для коротких API-запросов
                        Transport: apiTransport,
                },
                whSecret:  secret,
                cpWait:    make(map[int64]int64),
                cpTgOwner: make(map[int64]int64),
                breakers:  make(map[int64]*chatBreaker),
                mgBuffers: make(map[string]*mediaGroupBuffer),
        }
}

// tgMsgSeen проверяет, было ли сообщение (chatID, msgID) уже обработано.
// Если нет — запоминает и возвращает false ("не видели").
// Если да — возвращает true ("дубль").
func (b *Bridge) tgMsgSeen(chatID int64, msgID int) bool {
        key := tgMsgKey{chatID: chatID, msgID: msgID}
        b.tgSeenMu.Lock()
        defer b.tgSeenMu.Unlock()

        for i := 0; i < b.tgSeenLen; i++ {
                if b.tgSeenMsgs[i] == key {
                        return true // дубль
                }
        }

        b.tgSeenMsgs[b.tgSeenHead] = key
        b.tgSeenHead = (b.tgSeenHead + 1) % len(b.tgSeenMsgs)
        if b.tgSeenLen < len(b.tgSeenMsgs) {
                b.tgSeenLen++
        }
        return false
}

// cbBlocked проверяет, заблокирован ли чат.
func (b *Bridge) cbBlocked(chatID int64) bool {
        b.cbMu.Lock()
        defer b.cbMu.Unlock()
        cb, ok := b.breakers[chatID]
        if !ok {
                return false
        }
        if cb.fails >= cbMaxFails && time.Since(cb.blockedAt) < cbCooldown {
                return true
        }
        if cb.fails >= cbMaxFails {
                // Кулдаун прошёл — сбрасываем, пробуем снова
                delete(b.breakers, chatID)
        }
        return false
}

// cbFail регистрирует ошибку. Возвращает true если чат только что заблокировался.
func (b *Bridge) cbFail(chatID int64) bool {
        b.cbMu.Lock()
        defer b.cbMu.Unlock()
        cb, ok := b.breakers[chatID]
        if !ok {
                cb = &chatBreaker{}
                b.breakers[chatID] = cb
        }
        cb.fails++
        if cb.fails == cbMaxFails {
                cb.blockedAt = time.Now()
                slog.Warn("circuit breaker: chat blocked", "chatID", chatID, "cooldown", cbCooldown)
                return true
        }
        return false
}

// cbSuccess сбрасывает счётчик ошибок для чата.
func (b *Bridge) cbSuccess(chatID int64) {
        b.cbMu.Lock()
        defer b.cbMu.Unlock()
        delete(b.breakers, chatID)
}

// maxMaxFileBytes returns the MAX-to-TG file size limit in bytes (0 = unlimited).
func (c *Config) maxMaxFileBytes() int64 {
        if c.MaxMaxFileSizeMB <= 0 {
                return 0
        }
        return int64(c.MaxMaxFileSizeMB) * 1024 * 1024
}

// notifyAdmin отправляет сообщение об ошибке администратору бота.
// Если AdminChatID не задан (0), ошибка только логируется.
func (b *Bridge) notifyAdmin(ctx context.Context, text string) {
        if b.cfg.AdminChatID == 0 {
                slog.Warn("Admin notification", "text", text)
                return
        }
        m := tgbotapi.NewMessage(b.cfg.AdminChatID, text)
        if _, err := b.tgBot.Send(m); err != nil {
                slog.Error("Failed to notify admin", "err", err, "text", text)
        }
}

// isUserAllowed проверяет, есть ли tgUserID в белом списке.
// Если AllowedUsers пуст — доступ разрешён всем.
func (b *Bridge) isUserAllowed(tgUserID int64) bool {
        if len(b.cfg.AllowedUsers) == 0 {
                return true
        }
        for _, id := range b.cfg.AllowedUsers {
                if id == tgUserID {
                        return true
                }
        }
        return false
}

// checkUserAllowed проверяет доступ пользователя и отправляет сообщение об отказе если нужно.
// Возвращает true если доступ разрешён, false — если запрещён (и уже отправил ответ).
// userID == 0 трактуется как «нет отправителя» — доступ запрещается.
func (b *Bridge) checkUserAllowed(chatID, userID int64) bool {
        if userID != 0 && b.isUserAllowed(userID) {
                return true
        }
        slog.Debug("TG user not allowed", "uid", userID)
        b.tgBot.Send(tgbotapi.NewMessage(chatID, "У вас нет прав доступа к боту."))
        return false
}

// isCrosspostOwner проверяет, является ли userID владельцем связки.
// owner_id=0 и tg_owner_id=0 — старая связка, доступна всем.
func (b *Bridge) isCrosspostOwner(maxChatID, userID int64) bool {
        maxOwner, tgOwner := b.repo.GetCrosspostOwner(maxChatID)
        if maxOwner == 0 && tgOwner == 0 {
                return true // legacy, no owner
        }
        return userID == maxOwner || userID == tgOwner
}

// ─── Sprint 4: Billing / Access Control ───────────────────────────────────────

// freeUserRetroSyncMsgLimit — лимит сообщений ретро-синхронизации для бесплатных пользователей.
// DEPRECATED (Sprint 4): закомментировано — раскомментировать когда будет готова биллинговая логика.
// const freeUserRetroSyncMsgLimit = 500

// checkAccess проверяет доступ пользователя по подписке.
// Sprint 4: всегда возвращает true (полный доступ).
// Реальная логика проверки даты закомментирована — включить одной строкой когда нужно.
func (b *Bridge) checkAccess(userID int64) bool {
        // DEPRECATED (Sprint 4): реальная проверка подписки — раскомментировать для включения:
        // profile, err := b.repo.GetUserProfile(userID)
        // if err != nil {
        //         slog.Warn("checkAccess: failed to get user profile", "userID", userID, "err", err)
        //         return false
        // }
        // return profile.HasSubscription

        return true // временно: полный доступ для всех
}

// ──────────────────────────────────────────────────────────────────────────────

// tgFileURL возвращает прямой URL файла из TG — через custom API если настроен.
// В локальном режиме (TELEGRAM_LOCAL=1) возвращает file:// путь к файлу на диске.
// Для этого нужен shared volume между контейнерами telegram-bot-api и bridge.
func (b *Bridge) tgFileURL(fileID string) (string, error) {
        file, err := b.tgBot.GetFile(tgbotapi.FileConfig{FileID: fileID})
        if err != nil {
                return "", err
        }
        if b.cfg.TgAPIURL != "" {
                // Локальный Telegram Bot API (--local) возвращает абсолютный путь на диске.
                // HTTP endpoint /file/ не работает — читаем файл прямо с диска (shared volume).
                return file.FilePath, nil
        }
        return file.Link(b.tgBot.Token), nil
}

// tgChatTitle возвращает title TG-чата/канала по ID. Пустая строка если не удалось.
func (b *Bridge) tgChatTitle(chatID int64) string {
        chat, err := b.tgBot.GetChat(tgbotapi.ChatInfoConfig{ChatConfig: tgbotapi.ChatConfig{ChatID: chatID}})
        if err != nil {
                return ""
        }
        return chat.Title
}

func (b *Bridge) tgWebhookPath() string {
        return "/tg-webhook-" + b.whSecret
}

func (b *Bridge) maxWebhookPath() string {
        return "/max-webhook-" + b.whSecret
}

// registerCommands регистрирует команды бота в Telegram.
func (b *Bridge) registerCommands() {
        // Команды для групп и личных чатов
        groupCmds := tgbotapi.NewSetMyCommands(
                tgbotapi.BotCommand{Command: "start", Description: "Запустить бота / открыть панель управления"},
                tgbotapi.BotCommand{Command: "help", Description: "Инструкция"},
                // DEPRECATED (Sprint 4 Final): legacy команды отключены, управление через Mini App
                // tgbotapi.BotCommand{Command: "bridge", Description: "Связать чат с MAX-чатом"},
                // tgbotapi.BotCommand{Command: "unbridge", Description: "Удалить связку чатов"},
                // tgbotapi.BotCommand{Command: "crosspost", Description: "Список связок кросспостинга"},
        )
        if _, err := b.tgBot.Request(groupCmds); err != nil {
                slog.Error("TG setMyCommands (default) failed", "err", err)
        }

        // Команды для админов (группы + каналы)
        channelCmds := tgbotapi.NewSetMyCommandsWithScope(
                tgbotapi.NewBotCommandScopeAllChatAdministrators(),
                tgbotapi.BotCommand{Command: "start", Description: "Запустить бота / открыть панель управления"},
                tgbotapi.BotCommand{Command: "help", Description: "Инструкция"},
                // DEPRECATED (Sprint 4 Final): legacy команды отключены, управление через Mini App
                // tgbotapi.BotCommand{Command: "bridge", Description: "Связать чат с MAX-чатом"},
                // tgbotapi.BotCommand{Command: "unbridge", Description: "Удалить связку чатов"},
                // tgbotapi.BotCommand{Command: "crosspost", Description: "Список связок кросспостинга"},
        )
        if _, err := b.tgBot.Request(channelCmds); err != nil {
                slog.Error("TG setMyCommands (admins) failed", "err", err)
        }
}

// Run запускает TG и MAX listener'ы + периодическую очистку.
func (b *Bridge) Run(ctx context.Context) {
        b.registerCommands()
        go func() {
                t := time.NewTicker(10 * time.Minute)
                defer t.Stop()
                for {
                        select {
                        case <-ctx.Done():
                                return
                        case <-t.C:
                                b.repo.CleanOldMessages()
                        }
                }
        }()

        // Воркер очереди — проверяет каждые 10 секунд
        go func() {
                t := time.NewTicker(10 * time.Second)
                defer t.Stop()
                for {
                        select {
                        case <-ctx.Done():
                                return
                        case <-t.C:
                                b.processQueue(ctx)
                        }
                }
        }()

        // HTTP-сервер всегда запускается: для вебхуков (если настроены) и API (Sprint 3).
        // Используем DefaultServeMux — вебхуки TG/MAX регистрируются в listenTelegram/listenMax.
        b.registerAPIRoutes(http.DefaultServeMux)
        if b.cfg.WebhookURL != "" {
                slog.Info("Webhook mode enabled", "url", b.cfg.WebhookURL)
        }
        go b.startHTTPServer(ctx, http.DefaultServeMux)

        // Запускаем воркер ретроспективной синхронизации в SaaS режиме.
        // Он будет сам поднимать изолированные клиенты MTProto для каждой задачи.
        if b.cfg.TGAppID != 0 && b.cfg.TGAppHash != "" {
                go b.runSyncWorker(ctx)
        }

        // При старте получаем все MAX-чаты, в которых уже состоит бот
        go b.preloadMaxChats(ctx)

        var wg sync.WaitGroup
        wg.Add(2)
        go func() { defer wg.Done(); b.listenTelegram(ctx) }()
        go func() { defer wg.Done(); b.listenMax(ctx) }()
        wg.Wait()
}

// preloadMaxChats при старте получает список MAX-чатов через API и сохраняет их в БД.
func (b *Bridge) preloadMaxChats(ctx context.Context) {
        chats, err := b.maxApi.Chats.GetChats(ctx, 100, 0)
        if err != nil {
                slog.Warn("preloadMaxChats: failed to get chats", "err", err)
                return
        }
        if chats == nil {
                return
        }
        for _, c := range chats.Chats {
                b.repo.UpsertMaxKnownChat(MaxKnownChat{
                        ChatID:   c.ChatId,
                        Title:    c.Title,
                        ChatType: string(c.Type),
                })
        }
        slog.Info("preloadMaxChats: loaded", "count", len(chats.Chats))
}
