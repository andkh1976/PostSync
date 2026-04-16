package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	maxschemes "github.com/max-messenger/max-bot-api-client-go/schemes"
)

// runSyncWorker проверяет таблицу sync_tasks каждые 30 секунд
// и обрабатывает задачи со статусом pending.
func (b *Bridge) runSyncWorker(ctx context.Context) {
	slog.Info("Sync worker started (SaaS Mode)")
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Немедленная проверка при старте
	b.processPendingSyncTasks(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("Sync worker stopped")
			return
		case <-ticker.C:
			b.processPendingSyncTasks(ctx)
		}
	}
}

// processPendingSyncTasks получает все pending задачи и последовательно обрабатывает.
func (b *Bridge) processPendingSyncTasks(ctx context.Context) {
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
			"userID", task.UserID,
			"tgChatID", task.TgChatID,
			"maxChatID", task.MaxChatID,
			"from", task.StartDate.Format("2006-01-02"),
			"to", task.EndDate.Format("2006-01-02"),
		)

		err := b.runTaskWithSaaSClient(ctx, task)
		if err != nil {
			slog.Error("Sync worker: task failed", "taskID", task.ID, "err", err)
			_ = b.repo.SetSyncTaskStatus(task.ID, "failed", err.Error())
		}
	}
}

// runTaskWithSaaSClient инициализирует изолированный MTProto-клиент для пользователя и выполняет задачу.
func (b *Bridge) runTaskWithSaaSClient(ctx context.Context, task SyncTask) error {
	sessionBytes, err := b.repo.GetMTProtoSession(task.UserID)
	if err != nil || len(sessionBytes) == 0 {
		return fmt.Errorf("no MTProto session found for user %d", task.UserID)
	}

	memSession := &MemorySessionStorage{Data: sessionBytes}
	client := telegram.NewClient(b.cfg.TGAppID, b.cfg.TGAppHash, telegram.Options{
		SessionStorage: memSession,
		NoUpdates:      true,
	})

	return client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("auth status check: %w", err)
		}

		if !status.Authorized {
			return fmt.Errorf("user %d session is not authorized", task.UserID)
		}

		return b.processSyncTask(ctx, client.API(), task)
	})
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

	result, err := api.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		Limit:      100,
		OffsetPeer: offsetPeer,
	})
	if err != nil {
		return nil, fmt.Errorf("MessagesGetDialogs: %w", err)
	}

	var chats []tg.ChatClass
	switch d := result.(type) {
	case *tg.MessagesDialogs:
		chats = d.Chats
	case *tg.MessagesDialogsSlice:
		chats = d.Chats
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

	// Простейший выход: не пагинировать если не нашли в первой пачке
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
		// Собираем валидные сообщения пачки, определяем allBeforeStart,
		// затем разворачиваем и пересылаем в хронологическом порядке (от старых к новым).
		allBeforeStart := true
		var validMsgs []*tg.Message

		for _, msgClass := range msgs {
			msg, ok := msgClass.(*tg.Message)
			if !ok {
				// Пропускаем MessageEmpty и MessageService
				continue
			}

			// Сообщения идут от новых к старым: если вышли за start_date — стоп
			if msg.Date < startUnix {
				break
			}
			allBeforeStart = false

			// Пропускаем сообщения новее end_date (не должно быть, но для надёжности)
			if msg.Date > endUnix {
				continue
			}

			validMsgs = append(validMsgs, msg)
		}

		// Разворачиваем: пересылаем от старых к новым (хронологический порядок)
		for i, j := 0, len(validMsgs)-1; i < j; i, j = i+1, j-1 {
			validMsgs[i], validMsgs[j] = validMsgs[j], validMsgs[i]
		}

		for _, msg := range validMsgs {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Проверяем, не запросил ли пользователь отмену задачи
			if _, cancelled := b.cancelledTasks.Load(task.ID); cancelled {
				b.cancelledTasks.Delete(task.ID)
				slog.Info("Sync worker: task cancelled by user", "taskID", task.ID, "forwarded", forwarded)
				return b.repo.SetSyncTaskStatus(task.ID, "cancelled", "")
			}

			// Проверяем дубликат: уже пересылали?
			if _, exists := b.repo.LookupMaxMsgID(task.TgChatID, msg.ID); exists {
				slog.Debug("Sync worker: skip duplicate", "tgMsgID", msg.ID)
				continue
			}

			// Пересылаем в MAX
			if err := b.forwardMTProtoMsgToMax(ctx, api, msg, task.TgChatID, task.MaxChatID); err != nil {
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
func (b *Bridge) forwardMTProtoMsgToMax(ctx context.Context, api *tg.Client, msg *tg.Message, tgChatID int64, maxChatID int64) error {
	if b.cbBlocked(maxChatID) {
		return fmt.Errorf("circuit breaker active for maxChatID %d", maxChatID)
	}

	prepared := tgMaxText{
		Text: mtprotoEntitiesToMarkdown(msg.Message, msg.Entities),
	}
	if prepared.Text != msg.Message {
		prepared.Format = "markdown"
	}

	// Применяем замены кросспостинга если настроены
	repl := b.repo.GetCrosspostReplacements(maxChatID)
	if len(repl.TgToMax) > 0 {
		prepared.Text = applyReplacements(prepared.Text, repl.TgToMax)
	}

	// Добавляем метку источника с датой оригинального поста
	if msg.Date > 0 {
		label := formatTgDateLabel(msg.Date)
		if prepared.Text != "" {
			prepared.Text += "\n\n" + label
		} else if msg.Media != nil {
			// Медиа без подписи: метка становится единственным текстом
			prepared.Text = label
		}
	}

	// Обрабатываем медиа вложения
	var mediaToken string
	var mediaAttType string // "video", "file", "audio"

	if msg.Media != nil {
		var loc tg.InputFileLocationClass
		var size int64
		var fileName string

		switch media := msg.Media.(type) {
		case *tg.MessageMediaPhoto:
			if p, ok := media.Photo.AsNotEmpty(); ok {
				// Выбираем самый большой размер
				var maxSize *tg.PhotoSize
				for _, ps := range p.Sizes {
					if sizeClass, ok := ps.(*tg.PhotoSize); ok {
						if maxSize == nil || sizeClass.Size > maxSize.Size {
							maxSize = sizeClass
						}
					}
				}
				if maxSize != nil {
					mediaAttType = "image"
					fileName = "photo.jpg"
					size = int64(maxSize.Size)
					loc = &tg.InputPhotoFileLocation{
						ID:            p.ID,
						AccessHash:    p.AccessHash,
						FileReference: p.FileReference,
						ThumbSize:     maxSize.Type,
					}
				}
			}

		case *tg.MessageMediaDocument:
			if doc, ok := media.Document.AsNotEmpty(); ok {
				size = doc.Size
				mediaAttType = "file"
				if strings.HasPrefix(doc.MimeType, "video/") {
					mediaAttType = "video"
				} else if strings.HasPrefix(doc.MimeType, "image/") {
					mediaAttType = "image"
				}

				name := "file"
				for _, attr := range doc.Attributes {
					if filenameAttr, ok := attr.(*tg.DocumentAttributeFilename); ok {
						name = filenameAttr.FileName
					}
				}
				fileName = name
				loc = doc.AsInputDocumentFileLocation()
			}

		default:
			slog.Debug("Sync worker: unknown media type, skipping media", "type", fmt.Sprintf("%T", msg.Media))
		}

		if loc != nil && size > 0 {
			slog.Info("MTProto: starting media download", "type", mediaAttType, "filename", fileName, "size", size)

			pr, pw := io.Pipe()

			// Горутина для скачивания чанками через MTProto downloader
			go func() {
				dl := downloader.NewDownloader()
				_, err := dl.Download(api, loc).Stream(ctx, pw)
				pw.CloseWithError(err)
			}()

			// Передаем pipeReader и точный размер прямо в MAX SDK
			uploadType := maxschemes.UploadType(mediaAttType)
			uploaded, err := b.customUploadToMax(ctx, uploadType, pr, fileName, size)
			if err != nil {
				slog.Error("Sync worker: MAX media upload failed", "filename", fileName, "err", err)
				// Fallback: отправляем без медиа
				mediaAttType = ""
			} else if uploaded != nil {
				mediaToken = uploaded.Token
				slog.Info("Sync worker: media uploaded successfully", "token", mediaToken)
			}
		}
	}

	if prepared.Text == "" && mediaToken == "" {
		slog.Debug("Sync worker: empty message, skipping", "tgMsgID", msg.ID)
		return nil
	}

	mid, err := b.sendMaxDirectFormatted(ctx, maxChatID, prepared.Text, mediaAttType, mediaToken, "", prepared.Format)
	if err != nil {
		if b.cbFail(maxChatID) {
			slog.Warn("Sync worker: circuit breaker triggered", "maxChatID", maxChatID)
		}
		return fmt.Errorf("send to MAX: %w", err)
	}

	b.cbSuccess(maxChatID)
	b.repo.SaveMsg(tgChatID, msg.ID, maxChatID, mid)
	slog.Debug("Sync worker: message forwarded", "tgMsgID", msg.ID, "maxMsgID", mid)
	return nil
}

// CheckHistoryDataExists выполняет предварительную проверку наличия сообщений за указанный период.
func (b *Bridge) CheckHistoryDataExists(ctx context.Context, userID int64, tgChatID int64, startUnix, endUnix int) (bool, error) {
	sessionBytes, err := b.repo.GetMTProtoSession(userID)
	if err != nil || len(sessionBytes) == 0 {
		return false, fmt.Errorf("no MTProto session found for user %d", userID)
	}

	memSession := &MemorySessionStorage{Data: sessionBytes}
	client := telegram.NewClient(b.cfg.TGAppID, b.cfg.TGAppHash, telegram.Options{
		SessionStorage: memSession,
		NoUpdates:      true,
	})

	var hasData bool
	err = client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("auth status check: %w", err)
		}
		if !status.Authorized {
			return fmt.Errorf("user %d session is not authorized", userID)
		}

		api := client.API()
		peer, err := b.resolveChannelPeer(ctx, api, tgChatID)
		if err != nil {
			return fmt.Errorf("resolve peer: %w", err)
		}

		history, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:       peer,
			OffsetID:   0,
			OffsetDate: endUnix, // Ищем начиная с конца периода назад
			Limit:      10,      // Берем с запасом на сервис-сообщения
			AddOffset:  0,
		})
		if err != nil {
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

		for _, msgClass := range msgs {
			if msg, ok := msgClass.(*tg.Message); ok {
				// Важно: сообщение возвращается самое свежее до endUnix.
				// Если его дата >= startUnix, значит в этом периоде есть данные.
				if msg.Date >= startUnix && msg.Date <= endUnix {
					hasData = true
				}
				break
			}
		}
		return nil
	})

	return hasData, err
}
