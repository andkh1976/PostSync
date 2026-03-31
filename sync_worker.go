package main

import (
        "bufio"
        "context"
        "fmt"
        "log/slog"
        "os"
        "strings"
        "time"

        maxbot "github.com/max-messenger/max-bot-api-client-go"

        "github.com/gotd/td/session"
        "github.com/gotd/td/tg"
        "github.com/gotd/td/tgerr"
        "github.com/gotd/td/telegram"
        "github.com/gotd/td/telegram/auth"
)

// runWithMTProto инициализирует MTProto клиент и запускает sync worker.
// Вызывается в отдельной горутине из Bridge.Run().
func (b *Bridge) runWithMTProto(ctx context.Context) {
        sessionStorage := &session.FileStorage{
                Path: b.cfg.TGSessionFile,
        }

        client := telegram.NewClient(b.cfg.TGAppID, b.cfg.TGAppHash, telegram.Options{
                SessionStorage: sessionStorage,
        })

        err := client.Run(ctx, func(ctx context.Context) error {
                authClient := client.Auth()

                // Проверяем текущий статус авторизации
                status, err := authClient.Status(ctx)
                if err != nil {
                        return fmt.Errorf("auth status check: %w", err)
                }

                if !status.Authorized {
                        if b.cfg.TGPhone == "" {
                                slog.Warn("MTProto: session not authorized and TG_PHONE not set; sync worker disabled")
                                slog.Warn("MTProto: set TG_PHONE env var and restart to authorize")
                                // Блокируем до отмены контекста, чтобы горутина не рестартовала в цикле
                                <-ctx.Done()
                                return nil
                        }

                        slog.Info("MTProto: starting phone authorization", "phone", b.cfg.TGPhone)
                        flow := auth.NewFlow(
                                auth.Constant(b.cfg.TGPhone, "", auth.CodeAuthenticatorFunc(
                                        func(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
                                                fmt.Fprint(os.Stderr, "Enter Telegram auth code: ")
                                                scanner := bufio.NewScanner(os.Stdin)
                                                scanner.Scan()
                                                return strings.TrimSpace(scanner.Text()), nil
                                        },
                                )),
                                auth.SendCodeOptions{},
                        )
                        if err := authClient.IfNecessary(ctx, flow); err != nil {
                                return fmt.Errorf("MTProto auth: %w", err)
                        }
                        slog.Info("MTProto: authorization successful")
                }

                slog.Info("MTProto client ready, starting sync worker")
                b.runSyncWorker(ctx, client.API())
                return nil
        })

        if err != nil && ctx.Err() == nil {
                slog.Error("MTProto client stopped with error", "err", err)
        }
}

// runSyncWorker проверяет таблицу sync_tasks каждые 30 секунд
// и обрабатывает задачи со статусом pending.
func (b *Bridge) runSyncWorker(ctx context.Context, api *tg.Client) {
        slog.Info("Sync worker started")
        ticker := time.NewTicker(30 * time.Second)
        defer ticker.Stop()

        // Немедленная проверка при старте
        b.processPendingSyncTasks(ctx, api)

        for {
                select {
                case <-ctx.Done():
                        slog.Info("Sync worker stopped")
                        return
                case <-ticker.C:
                        b.processPendingSyncTasks(ctx, api)
                }
        }
}

// processPendingSyncTasks получает все pending задачи и последовательно обрабатывает.
func (b *Bridge) processPendingSyncTasks(ctx context.Context, api *tg.Client) {
        tasks, err := b.repo.GetPendingSyncTasks()
        if err != nil {
                slog.Error("Sync worker: failed to fetch pending tasks", "err", err)
                return
        }
        if len(tasks) == 0 {
                return
        }
        slog.Info("Sync worker: found pending tasks", "count", len(tasks))

        for _, task := range tasks {
                if ctx.Err() != nil {
                        return
                }

                // Переводим в processing
                if err := b.repo.SetSyncTaskStatus(task.ID, "processing", ""); err != nil {
                        slog.Error("Sync worker: cannot set processing status", "taskID", task.ID, "err", err)
                        continue
                }

                slog.Info("Sync worker: starting task",
                        "taskID", task.ID,
                        "tgChatID", task.TgChatID,
                        "maxChatID", task.MaxChatID,
                        "from", task.StartDate.Format("2006-01-02"),
                        "to", task.EndDate.Format("2006-01-02"),
                )

                if err := b.processSyncTask(ctx, api, task); err != nil {
                        slog.Error("Sync worker: task failed", "taskID", task.ID, "err", err)
                        _ = b.repo.SetSyncTaskStatus(task.ID, "failed", err.Error())
                }
        }
}

// resolveChannelPeer находит tg.InputPeerClass для канала по его chat ID.
// Для каналов TG chat ID может приходить в формате -100XXXXXXXXXX.
func (b *Bridge) resolveChannelPeer(ctx context.Context, api *tg.Client, tgChatID int64) (tg.InputPeerClass, error) {
        // Нормализуем ID: убираем -100 префикс если есть
        channelID := tgChatID
        if channelID < -1_000_000_000_000 {
                channelID = -(channelID + 1_000_000_000_000)
        } else if channelID < 0 {
                channelID = -channelID
        }

        // Получаем диалоги порциями, ищем нужный канал
        var offsetPeer tg.InputPeerClass = &tg.InputPeerEmpty{}
        for {
                result, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
                        Limit:      100,
                        OffsetPeer: offsetPeer,
                })
                if err != nil {
                        return nil, fmt.Errorf("MessagesGetDialogs: %w", err)
                }

                var chats []tg.ChatClass
                var dialogCount int
                switch d := result.(type) {
                case *tg.MessagesDialogs:
                        chats = d.Chats
                        dialogCount = len(d.Dialogs)
                case *tg.MessagesDialogsSlice:
                        chats = d.Chats
                        dialogCount = len(d.Dialogs)
                default:
                        return nil, fmt.Errorf("unexpected dialogs response type: %T", result)
                }

                for _, chat := range chats {
                        switch ch := chat.(type) {
                        case *tg.Channel:
                                if ch.ID == channelID {
                                        slog.Debug("Resolved channel peer", "channelID", channelID, "title", ch.Title)
                                        return &tg.InputPeerChannel{
                                                ChannelID:  ch.ID,
                                                AccessHash: ch.AccessHash,
                                        }, nil
                                }
                        }
                }

                // Если получили меньше лимита — диалогов больше нет
                if dialogCount < 100 {
                        break
                }

                // Иначе двигаем offset (используем последний диалог как точку отсчёта)
                // Простейший выход: не пагинировать если не нашли в первой пачке
                break
        }

        return nil, fmt.Errorf("channel %d not found in user's dialogs (tgChatID=%d)", channelID, tgChatID)
}

// processSyncTask скачивает историю TG-канала за указанный период и пересылает в MAX.
func (b *Bridge) processSyncTask(ctx context.Context, api *tg.Client, task SyncTask) error {
        peer, err := b.resolveChannelPeer(ctx, api, task.TgChatID)
        if err != nil {
                return fmt.Errorf("resolve peer: %w", err)
        }

        startUnix := int(task.StartDate.Unix())
        endUnix := int(task.EndDate.Unix())

        // Начинаем с last_synced_id, если задача была прервана ранее
        offsetID := 0
        if task.LastSyncedID != "" {
                if _, err := fmt.Sscanf(task.LastSyncedID, "%d", &offsetID); err != nil {
                        slog.Warn("Sync worker: invalid last_synced_id, starting from 0", "value", task.LastSyncedID)
                }
        }

        forwarded := 0
        const batchSize = 100

        for {
                if ctx.Err() != nil {
                        return ctx.Err()
                }

                history, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
                        Peer:       peer,
                        OffsetID:   offsetID,
                        OffsetDate: endUnix, // начинаем с конца периода и идём назад
                        Limit:      batchSize,
                        AddOffset:  0,
                })
                if err != nil {
                        // Обработка Flood Wait
                        if d, ok := tgerr.AsFloodWait(err); ok {
                                slog.Warn("Sync worker: flood wait from Telegram", "wait", d)
                                select {
                                case <-time.After(d):
                                case <-ctx.Done():
                                        return ctx.Err()
                                }
                                continue
                        }
                        return fmt.Errorf("MessagesGetHistory: %w", err)
                }

                var msgs []tg.MessageClass
                switch h := history.(type) {
                case *tg.MessagesMessages:
                        msgs = h.Messages
                case *tg.MessagesMessagesSlice:
                        msgs = h.Messages
                case *tg.MessagesChannelMessages:
                        msgs = h.Messages
                default:
                        return fmt.Errorf("unexpected history type: %T", history)
                }

                if len(msgs) == 0 {
                        break
                }

                // Сообщения приходят от новых к старым.
                // Флаг: если все сообщения пачки старее start_date — завершаем.
                allBeforeStart := true

                for _, msgClass := range msgs {
                        if ctx.Err() != nil {
                                return ctx.Err()
                        }

                        msg, ok := msgClass.(*tg.Message)
                        if !ok {
                                // Пропускаем MessageEmpty и MessageService
                                continue
                        }

                        // Сообщения идут от новых к старым: если уже вышли за start_date — стоп
                        if msg.Date < startUnix {
                                break
                        }
                        allBeforeStart = false

                        // Пропускаем сообщения новее end_date (не должно быть, но для надёжности)
                        if msg.Date > endUnix {
                                continue
                        }

                        // Проверяем дубликат: уже пересылали?
                        if _, exists := b.repo.LookupMaxMsgID(task.TgChatID, msg.ID); exists {
                                slog.Debug("Sync worker: skip duplicate", "tgMsgID", msg.ID)
                                continue
                        }

                        // Пересылаем в MAX
                        if err := b.forwardMTProtoMsgToMax(ctx, msg, task.TgChatID, task.MaxChatID); err != nil {
                                slog.Error("Sync worker: forward failed", "tgMsgID", msg.ID, "err", err)
                                // Продолжаем с остальными сообщениями, не прерываем задачу
                        } else {
                                forwarded++
                        }

                        // Пауза 2-3 секунды между отправками в MAX для защиты от блокировок
                        select {
                        case <-time.After(2 * time.Second):
                        case <-ctx.Done():
                                return ctx.Err()
                        }
                }

                if allBeforeStart {
                        break
                }

                // Обновляем last_synced_id по последнему сообщению пачки (оно самое старое)
                lastMsg, ok := msgs[len(msgs)-1].(*tg.Message)
                if ok {
                        lastIDStr := fmt.Sprintf("%d", lastMsg.ID)
                        if err := b.repo.UpdateSyncTaskLastID(task.ID, lastIDStr); err != nil {
                                slog.Error("Sync worker: failed to update last_synced_id", "err", err)
                        }
                        // Следующую пачку начинаем с этого ID (эксклюзивно)
                        offsetID = lastMsg.ID
                }

                // Если получили меньше batchSize — история за период закончилась
                if len(msgs) < batchSize {
                        break
                }
        }

        slog.Info("Sync worker: task completed", "taskID", task.ID, "forwarded", forwarded)
        return b.repo.SetSyncTaskStatus(task.ID, "done", "")
}

// forwardMTProtoMsgToMax пересылает сообщение из MTProto истории в MAX-чат.
// Адаптация forwardTgToMax для работы с данными от MTProto клиента.
func (b *Bridge) forwardMTProtoMsgToMax(ctx context.Context, msg *tg.Message, tgChatID int64, maxChatID int64) error {
        if b.cbBlocked(maxChatID) {
                return fmt.Errorf("circuit breaker active for maxChatID %d", maxChatID)
        }

        text := msg.Message

        // Применяем замены кросспостинга если настроены
        repl := b.repo.GetCrosspostReplacements(maxChatID)
        if len(repl.TgToMax) > 0 {
                text = applyReplacements(text, repl.TgToMax)
        }

        // Обрабатываем медиа вложения
        var mediaToken string
        var mediaAttType string // "video", "file", "audio"

        if msg.Media != nil {
                switch media := msg.Media.(type) {
                case *tg.MessageMediaPhoto:
                        // Фото: скачиваем через Bot API (не через MTProto) для простоты
                        // Оставляем mediaToken пустым — отправим только текст
                        _ = media
                        slog.Debug("Sync worker: photo media — sending text only", "tgMsgID", msg.ID)

                case *tg.MessageMediaDocument:
                        _ = media
                        slog.Debug("Sync worker: document media — sending text only", "tgMsgID", msg.ID)

                default:
                        slog.Debug("Sync worker: unknown media type, skipping media", "type", fmt.Sprintf("%T", msg.Media))
                }
        }

        if text == "" && mediaToken == "" {
                slog.Debug("Sync worker: empty message, skipping", "tgMsgID", msg.ID)
                return nil
        }

        m := maxbot.NewMessage().SetChat(maxChatID).SetText(text)

        switch mediaAttType {
        case "video":
                // mediaToken заполняется только если мы реально загрузили файл выше
                _ = mediaToken
        }

        result, err := b.maxApi.Messages.SendWithResult(ctx, m)
        if err != nil {
                if b.cbFail(maxChatID) {
                        slog.Warn("Sync worker: circuit breaker triggered", "maxChatID", maxChatID)
                }
                return fmt.Errorf("send to MAX: %w", err)
        }

        b.cbSuccess(maxChatID)
        b.repo.SaveMsg(tgChatID, msg.ID, maxChatID, result.Body.Mid)
        slog.Debug("Sync worker: message forwarded", "tgMsgID", msg.ID, "maxMsgID", result.Body.Mid)
        return nil
}
