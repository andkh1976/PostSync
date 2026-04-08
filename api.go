package main

import (
        "context"
        "crypto/hmac"
        "crypto/sha256"
        "encoding/hex"
        "encoding/json"
        "fmt"
        "log/slog"
        "net/http"
        "net/url"
        "os"
        "sort"
        "strings"
        "time"

        tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
        "github.com/gotd/td/telegram"
        "github.com/gotd/td/telegram/auth"
)

// apiOwner содержит данные авторизованного пользователя, извлечённые из initData.
type apiOwner struct {
        UserID   int64
        Platform string // "tg" or "max"
}

// apiChannelInfo — информация о связке канала для ответа API.
type apiChannelInfo struct {
        TgChatID   int64  `json:"tg_chat_id"`
        MaxChatID  int64  `json:"max_chat_id"`
        Direction  string `json:"direction"`
        LiveListen bool   `json:"live_listen"`
        TgTitle    string `json:"tg_title,omitempty"`
}

// apiSyncStartRequest — тело запроса для POST /api/sync/start.
type apiSyncStartRequest struct {
        TgChatID  int64  `json:"tg_chat_id"`
        MaxChatID int64  `json:"max_chat_id"`
        StartDate string `json:"start_date"` // YYYY-MM-DD
        EndDate   string `json:"end_date"`   // YYYY-MM-DD
}

// apiHistoryClearRequest — тело запроса для POST /api/history/clear.
type apiHistoryClearRequest struct {
        TgChatID  int64  `json:"tg_chat_id"`
        StartDate string `json:"start_date"` // YYYY-MM-DD
        EndDate   string `json:"end_date"`   // YYYY-MM-DD
}

// apiSettingsPatchRequest — тело запроса для PATCH /api/settings.
type apiSettingsPatchRequest struct {
        MaxChatID  int64   `json:"max_chat_id"`
        LiveListen *bool   `json:"live_listen,omitempty"`
        Direction  *string `json:"direction,omitempty"`
}

// apiPairRequest — тело запроса для POST /api/channels/pair.
// Sprint 4 Correction: заменяет ручной процесс /crosspost из бота.
type apiPairRequest struct {
        TgChatID  int64 `json:"tg_chat_id"`
        MaxChatID int64 `json:"max_chat_id"`
}

// apiUnpairRequest — тело запроса для DELETE /api/channels.
// Sprint 4 Correction: заменяет ручной /unbridge.
type apiUnpairRequest struct {
        MaxChatID int64 `json:"max_chat_id"`
}

// validateTgInitData проверяет подлинность Telegram Mini App initData.
// Возвращает userID из initData или ошибку.
func validateTgInitData(initData, botToken string) (int64, error) {
        vals, err := url.ParseQuery(initData)
        if err != nil {
                return 0, fmt.Errorf("parse initData: %w", err)
        }
        hash := vals.Get("hash")
        if hash == "" {
                return 0, fmt.Errorf("missing hash")
        }

        // Строим data-check-string: все поля кроме hash, отсортированные по ключу
        var pairs []string
        for k, v := range vals {
                if k == "hash" {
                        continue
                }
                pairs = append(pairs, k+"="+v[0])
        }
        sort.Strings(pairs)
        dataCheckString := strings.Join(pairs, "\n")

        // HMAC-SHA256("WebAppData", botToken) → secret key
        mac := hmac.New(sha256.New, []byte("WebAppData"))
        mac.Write([]byte(botToken))
        secretKey := mac.Sum(nil)

        // HMAC-SHA256(dataCheckString, secretKey)
        mac2 := hmac.New(sha256.New, secretKey)
        mac2.Write([]byte(dataCheckString))
        computedHash := hex.EncodeToString(mac2.Sum(nil))

        if !hmac.Equal([]byte(computedHash), []byte(hash)) {
                return 0, fmt.Errorf("invalid hash")
        }

        // Извлекаем user ID из поля "user" (JSON объект)
        userJSON := vals.Get("user")
        if userJSON == "" {
                return 0, fmt.Errorf("missing user field")
        }
        var userObj struct {
                ID int64 `json:"id"`
        }
        if err := json.Unmarshal([]byte(userJSON), &userObj); err != nil {
                return 0, fmt.Errorf("parse user: %w", err)
        }
        if userObj.ID == 0 {
                return 0, fmt.Errorf("user id is 0")
        }
        return userObj.ID, nil
}

// validateMaxInitData проверяет подлинность MAX Mini App initData.
// Формат аналогичен Telegram, но используется токен MAX-бота.
func validateMaxInitData(initData, maxToken string) (int64, error) {
        vals, err := url.ParseQuery(initData)
        if err != nil {
                return 0, fmt.Errorf("parse max initData: %w", err)
        }
        hash := vals.Get("hash")
        if hash == "" {
                return 0, fmt.Errorf("missing hash")
        }

        var pairs []string
        for k, v := range vals {
                if k == "hash" {
                        continue
                }
                pairs = append(pairs, k+"="+v[0])
        }
        sort.Strings(pairs)
        dataCheckString := strings.Join(pairs, "\n")

        mac := hmac.New(sha256.New, []byte("WebAppData"))
        mac.Write([]byte(maxToken))
        secretKey := mac.Sum(nil)

        mac2 := hmac.New(sha256.New, secretKey)
        mac2.Write([]byte(dataCheckString))
        computedHash := hex.EncodeToString(mac2.Sum(nil))

        if !hmac.Equal([]byte(computedHash), []byte(hash)) {
                return 0, fmt.Errorf("invalid max hash")
        }

        userJSON := vals.Get("user")
        if userJSON == "" {
                return 0, fmt.Errorf("missing user field")
        }
        var userObj struct {
                ID int64 `json:"id"`
        }
        if err := json.Unmarshal([]byte(userJSON), &userObj); err != nil {
                return 0, fmt.Errorf("parse max user: %w", err)
        }
        if userObj.ID == 0 {
                return 0, fmt.Errorf("max user id is 0")
        }
        return userObj.ID, nil
}

// authMiddleware проверяет заголовок Authorization и возвращает apiOwner.
// Заголовок формата: "Authorization: tg <initData>" или "Authorization: max <initData>"
func (b *Bridge) authMiddleware(r *http.Request) (*apiOwner, error) {
        authHeader := r.Header.Get("Authorization")
        if authHeader == "" {
                // Также проверяем X-Init-Data для совместимости с некоторыми клиентами
                authHeader = r.Header.Get("X-Init-Data")
        }
        if authHeader == "" {
                return nil, fmt.Errorf("missing Authorization header")
        }

        parts := strings.SplitN(authHeader, " ", 2)
        if len(parts) != 2 {
                return nil, fmt.Errorf("invalid Authorization format")
        }
        platform := strings.ToLower(parts[0])
        initData := parts[1]

        switch platform {
        case "tg", "telegram":
                userID, err := validateTgInitData(initData, b.tgBot.Token)
                if err != nil {
                        return nil, fmt.Errorf("tg auth failed: %w", err)
                }
                return &apiOwner{UserID: userID, Platform: "tg"}, nil
        case "max":
                userID, err := validateMaxInitData(initData, b.cfg.MaxToken)
                if err != nil {
                        return nil, fmt.Errorf("max auth failed: %w", err)
                }
                return &apiOwner{UserID: userID, Platform: "max"}, nil
        default:
                return nil, fmt.Errorf("unknown platform: %s", platform)
        }
}

// writeJSON отправляет JSON-ответ с указанным статусом.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
        w.Header().Set("Content-Type", "application/json; charset=utf-8")
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.WriteHeader(status)
        json.NewEncoder(w).Encode(v)
}

// writeError отправляет JSON-ошибку.
func writeError(w http.ResponseWriter, status int, msg string) {
        writeJSON(w, status, map[string]string{"error": msg})
}

// handleAPIChannels — GET /api/channels
// Возвращает список активных связок каналов для авторизованного пользователя.
func (b *Bridge) handleAPIChannels(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodOptions {
                w.Header().Set("Access-Control-Allow-Origin", "*")
                w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Init-Data")
                w.WriteHeader(http.StatusNoContent)
                return
        }
        if r.Method != http.MethodGet {
                writeError(w, http.StatusMethodNotAllowed, "method not allowed")
                return
        }

        owner, err := b.authMiddleware(r)
        if err != nil {
                slog.Warn("API /channels auth failed", "err", err, "ip", r.RemoteAddr)
                writeError(w, http.StatusUnauthorized, "unauthorized")
                return
        }

        links := b.repo.ListCrossposts(owner.UserID)
        result := make([]apiChannelInfo, 0, len(links))
        for _, l := range links {
                info := apiChannelInfo{
                        TgChatID:   l.TgChatID,
                        MaxChatID:  l.MaxChatID,
                        Direction:  l.Direction,
                        LiveListen: b.repo.GetCrosspostLiveListen(l.MaxChatID),
                }
                // Пробуем получить название TG-канала
                chat, err := b.tgBot.GetChat(tgbotapi.ChatInfoConfig{
                        ChatConfig: tgbotapi.ChatConfig{ChatID: l.TgChatID},
                })
                if err == nil {
                        info.TgTitle = chat.Title
                }
                result = append(result, info)
        }

        slog.Info("API /channels", "owner", owner.UserID, "platform", owner.Platform, "count", len(result))
        writeJSON(w, http.StatusOK, result)
}

// handleAPISyncStart — POST /api/sync/start
// Создаёт новую задачу ретроспективного скачивания истории TG-канала в MAX.
func (b *Bridge) handleAPISyncStart(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodOptions {
                w.Header().Set("Access-Control-Allow-Origin", "*")
                w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Init-Data")
                w.WriteHeader(http.StatusNoContent)
                return
        }
        if r.Method != http.MethodPost {
                writeError(w, http.StatusMethodNotAllowed, "method not allowed")
                return
        }

        owner, err := b.authMiddleware(r)
        if err != nil {
                slog.Warn("API /sync/start auth failed", "err", err, "ip", r.RemoteAddr)
                writeError(w, http.StatusUnauthorized, "unauthorized")
                return
        }

        if !b.checkAccess(owner.UserID) {
                writeError(w, http.StatusForbidden, "forbidden")
                return
        }

        var req apiSyncStartRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
                writeError(w, http.StatusBadRequest, "invalid JSON body")
                return
        }
        if req.TgChatID == 0 || req.MaxChatID == 0 {
                writeError(w, http.StatusBadRequest, "tg_chat_id and max_chat_id are required")
                return
        }
        if req.StartDate == "" || req.EndDate == "" {
                writeError(w, http.StatusBadRequest, "start_date and end_date are required")
                return
        }

        startDate, err := time.Parse("2006-01-02", req.StartDate)
        if err != nil {
                writeError(w, http.StatusBadRequest, "invalid start_date format, use YYYY-MM-DD")
                return
        }
        endDate, err := time.Parse("2006-01-02", req.EndDate)
        if err != nil {
                writeError(w, http.StatusBadRequest, "invalid end_date format, use YYYY-MM-DD")
                return
        }
        if endDate.Before(startDate) {
                writeError(w, http.StatusBadRequest, "end_date must be >= start_date")
                return
        }

        // Проверяем, что связка принадлежит пользователю
        _, _, ok := b.repo.GetCrosspostMaxChat(req.TgChatID, owner.UserID)
        if !ok {
                writeError(w, http.StatusForbidden, "crosspost pair not found or access denied")
                return
        }

        task := SyncTask{
                UserID:    owner.UserID,
                TgChatID:  req.TgChatID,
                MaxChatID: req.MaxChatID,
                StartDate: startDate,
                EndDate:   endDate.Add(24*time.Hour - time.Second), // включаем конец дня
        }
        id, err := b.repo.CreateSyncTask(task)
        if err != nil {
                slog.Error("API /sync/start create task failed", "err", err, "owner", owner.UserID)
                writeError(w, http.StatusInternalServerError, "failed to create sync task")
                return
        }

        slog.Info("API /sync/start task created", "id", id, "owner", owner.UserID,
                "tgChat", req.TgChatID, "maxChat", req.MaxChatID,
                "start", req.StartDate, "end", req.EndDate)
        writeJSON(w, http.StatusCreated, map[string]interface{}{
                "task_id": id,
                "status":  "pending",
        })
}

// handleAPIHistoryClear — POST /api/history/clear
// Удаляет маппинги сообщений за указанный период.
func (b *Bridge) handleAPIHistoryClear(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodOptions {
                w.Header().Set("Access-Control-Allow-Origin", "*")
                w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Init-Data")
                w.WriteHeader(http.StatusNoContent)
                return
        }
        if r.Method != http.MethodPost {
                writeError(w, http.StatusMethodNotAllowed, "method not allowed")
                return
        }

        owner, err := b.authMiddleware(r)
        if err != nil {
                slog.Warn("API /history/clear auth failed", "err", err, "ip", r.RemoteAddr)
                writeError(w, http.StatusUnauthorized, "unauthorized")
                return
        }

        if !b.checkAccess(owner.UserID) {
                writeError(w, http.StatusForbidden, "forbidden")
                return
        }

        var req apiHistoryClearRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
                writeError(w, http.StatusBadRequest, "invalid JSON body")
                return
        }
        if req.TgChatID == 0 {
                writeError(w, http.StatusBadRequest, "tg_chat_id is required")
                return
        }
        if req.StartDate == "" || req.EndDate == "" {
                writeError(w, http.StatusBadRequest, "start_date and end_date are required")
                return
        }

        // Parsing and validating the dates isn't necessary anymore since
        // ClearMessagesMapping affects the entire channel mappings.

        // Проверяем владение связкой
        _, _, ok := b.repo.GetCrosspostMaxChat(req.TgChatID, owner.UserID)
        if !ok {
                writeError(w, http.StatusForbidden, "crosspost pair not found or access denied")
                return
        }

        if err := b.repo.ClearMessagesMapping(req.TgChatID); err != nil {
                slog.Error("API /history/clear failed", "err", err, "owner", owner.UserID, "tgChat", req.TgChatID)
                writeError(w, http.StatusInternalServerError, "failed to clear history")
                return
        }

        slog.Info("API /history/clear done", "owner", owner.UserID, "platform", owner.Platform,
                "tgChat", req.TgChatID, "start", req.StartDate, "end", req.EndDate)
        writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleAPISettings — PATCH /api/settings
// Изменяет настройки связки: направление и режим живого прослушивания.
func (b *Bridge) handleAPISettings(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodOptions {
                w.Header().Set("Access-Control-Allow-Origin", "*")
                w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Init-Data")
                w.WriteHeader(http.StatusNoContent)
                return
        }
        if r.Method != http.MethodPatch {
                writeError(w, http.StatusMethodNotAllowed, "method not allowed")
                return
        }

        owner, err := b.authMiddleware(r)
        if err != nil {
                slog.Warn("API /settings auth failed", "err", err, "ip", r.RemoteAddr)
                writeError(w, http.StatusUnauthorized, "unauthorized")
                return
        }

        if !b.checkAccess(owner.UserID) {
                writeError(w, http.StatusForbidden, "forbidden")
                return
        }

        var req apiSettingsPatchRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
                writeError(w, http.StatusBadRequest, "invalid JSON body")
                return
        }
        if req.MaxChatID == 0 {
                writeError(w, http.StatusBadRequest, "max_chat_id is required")
                return
        }

        // Проверяем владение связкой
        _, _, ok := b.repo.GetCrosspostTgChat(req.MaxChatID, owner.UserID)
        if !ok {
                writeError(w, http.StatusForbidden, "crosspost pair not found or access denied")
                return
        }

        changed := false

        if req.Direction != nil {
                validDirs := map[string]bool{"tg>max": true, "max>tg": true, "both": true}
                if !validDirs[*req.Direction] {
                        writeError(w, http.StatusBadRequest, "direction must be one of: tg>max, max>tg, both")
                        return
                }
                if b.repo.SetCrosspostDirection(req.MaxChatID, *req.Direction) {
                        slog.Info("API /settings direction changed", "owner", owner.UserID, "platform", owner.Platform,
                                "maxChat", req.MaxChatID, "direction", *req.Direction)
                        changed = true
                }
        }

        if req.LiveListen != nil {
                if b.repo.SetCrosspostLiveListen(req.MaxChatID, *req.LiveListen) {
                        slog.Info("API /settings live_listen changed", "owner", owner.UserID, "platform", owner.Platform,
                                "maxChat", req.MaxChatID, "live_listen", *req.LiveListen)
                        changed = true
                }
        }

        if !changed {
                writeError(w, http.StatusBadRequest, "nothing to update")
                return
        }

        // Возвращаем актуальное состояние
        tgChatID, direction, _ := b.repo.GetCrosspostTgChat(req.MaxChatID, 0)
        liveListen := b.repo.GetCrosspostLiveListen(req.MaxChatID)

        writeJSON(w, http.StatusOK, map[string]interface{}{
                "max_chat_id": req.MaxChatID,
                "tg_chat_id":  tgChatID,
                "direction":   direction,
                "live_listen": liveListen,
        })
}

// handleAPIProfile — GET /api/profile (Sprint 4)
// Возвращает ID пользователя и статус подписки для раздела «Профиль» Mini App.
func (b *Bridge) handleAPIProfile(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodOptions {
                w.Header().Set("Access-Control-Allow-Origin", "*")
                w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Init-Data")
                w.WriteHeader(http.StatusNoContent)
                return
        }
        if r.Method != http.MethodGet {
                writeError(w, http.StatusMethodNotAllowed, "method not allowed")
                return
        }

        owner, err := b.authMiddleware(r)
        if err != nil {
                slog.Warn("API /profile auth failed", "err", err, "ip", r.RemoteAddr)
                writeError(w, http.StatusUnauthorized, "unauthorized")
                return
        }

        adminContact := os.Getenv("ADMIN_USERNAME")
        if adminContact == "" {
                adminContact = "PstSncBot" // fallback bot
        }
        adminContact = strings.TrimPrefix(adminContact, "@")

        isAdmin := b.cfg.AdminChatID != 0 && owner.UserID == b.cfg.AdminChatID

        profile, err := b.repo.GetUserProfile(owner.UserID)
        if err != nil {
                // Пользователь может не иметь записи в users (новый), возвращаем минимальный профиль
                slog.Debug("API /profile: user not found in DB, returning minimal profile", "userID", owner.UserID)
                writeJSON(w, http.StatusOK, UserProfile{
                        UserID:          owner.UserID,
                        Platform:        owner.Platform,
                        HasSubscription: isAdmin,
                        AdminContact:    adminContact,
                })
                return
        }

        if isAdmin {
                profile.HasSubscription = true
        }
        profile.AdminContact = adminContact

        slog.Info("API /profile", "owner", owner.UserID, "platform", owner.Platform, "hasSub", profile.HasSubscription)
        writeJSON(w, http.StatusOK, profile)
}

// handleAPITasks — GET /api/tasks (Sprint 4)
// Возвращает список задач ретроспективной синхронизации для отображения прогресса в Mini App.
func (b *Bridge) handleAPITasks(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodOptions {
                w.Header().Set("Access-Control-Allow-Origin", "*")
                w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Init-Data")
                w.WriteHeader(http.StatusNoContent)
                return
        }
        if r.Method != http.MethodGet {
                writeError(w, http.StatusMethodNotAllowed, "method not allowed")
                return
        }

        owner, err := b.authMiddleware(r)
        if err != nil {
                slog.Warn("API /tasks auth failed", "err", err, "ip", r.RemoteAddr)
                writeError(w, http.StatusUnauthorized, "unauthorized")
                return
        }

        tasks, err := b.repo.ListUserSyncTasks(owner.UserID)
        if err != nil {
                slog.Error("API /tasks failed", "err", err, "owner", owner.UserID)
                writeError(w, http.StatusInternalServerError, "failed to load tasks")
                return
        }

        type apiTask struct {
                ID        int64  `json:"id"`
                TgChatID  int64  `json:"tg_chat_id"`
                MaxChatID int64  `json:"max_chat_id"`
                Status    string `json:"status"`
                StartDate string `json:"start_date"`
                EndDate   string `json:"end_date"`
                Error     string `json:"error,omitempty"`
        }

        result := make([]apiTask, 0, len(tasks))
        for _, t := range tasks {
                result = append(result, apiTask{
                        ID:        t.ID,
                        TgChatID:  t.TgChatID,
                        MaxChatID: t.MaxChatID,
                        Status:    t.Status,
                        StartDate: t.StartDate.Format("2006-01-02"),
                        EndDate:   t.EndDate.Format("2006-01-02"),
                        Error:     t.Error,
                })
        }

        slog.Info("API /tasks", "owner", owner.UserID, "count", len(result))
        writeJSON(w, http.StatusOK, result)
}

// handleAPITaskCancel — POST /api/tasks/cancel
// Отмечает задачу для отмены; sync_worker остановится при следующей проверке.
func (b *Bridge) handleAPITaskCancel(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Init-Data")
        if r.Method == http.MethodOptions {
                w.WriteHeader(http.StatusNoContent)
                return
        }
        if r.Method != http.MethodPost {
                writeError(w, http.StatusMethodNotAllowed, "method not allowed")
                return
        }
        owner, err := b.authMiddleware(r)
        if err != nil {
                writeError(w, http.StatusUnauthorized, "unauthorized")
                return
        }

        if !b.checkAccess(owner.UserID) {
                writeError(w, http.StatusForbidden, "forbidden")
                return
        }
        var req struct {
                TaskID int64 `json:"task_id"`
        }
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TaskID == 0 {
                writeError(w, http.StatusBadRequest, "task_id required")
                return
        }
        // Ставим флаг — sync_worker заметит его при следующей итерации
        b.cancelledTasks.Store(req.TaskID, struct{}{})
        // Также сразу обновляем статус pending → cancelled, если задача ещё не взята в работу
        _ = b.repo.SetSyncTaskStatus(req.TaskID, "cancelled", "")
        slog.Info("API /tasks/cancel", "owner", owner.UserID, "taskID", req.TaskID)
        writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// handleAPITasksClearHistory — DELETE /api/tasks/history
// Удаляет завершённые, упавшие и отменённые задачи пользователя.
func (b *Bridge) handleAPITasksClearHistory(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Init-Data")
        if r.Method == http.MethodOptions {
                w.WriteHeader(http.StatusNoContent)
                return
        }
        if r.Method != http.MethodDelete {
                writeError(w, http.StatusMethodNotAllowed, "method not allowed")
                return
        }
        owner, err := b.authMiddleware(r)
        if err != nil {
                writeError(w, http.StatusUnauthorized, "unauthorized")
                return
        }
        if err := b.repo.DeleteCompletedSyncTasks(owner.UserID); err != nil {
                slog.Error("API /tasks/history delete failed", "owner", owner.UserID, "err", err)
                writeError(w, http.StatusInternalServerError, "failed to clear history")
                return
        }
        slog.Info("API /tasks/history cleared", "owner", owner.UserID)
        writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleAPIChannelsPair — POST /api/channels/pair (Sprint 4 Correction)
// Создаёт новую связку каналов TG ↔ MAX. Заменяет ручной процесс /crosspost из бота.
// Тело запроса: {"tg_chat_id": <int64>, "max_chat_id": <int64>}
func (b *Bridge) handleAPIChannelsPair(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodOptions {
                w.Header().Set("Access-Control-Allow-Origin", "*")
                w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Init-Data")
                w.WriteHeader(http.StatusNoContent)
                return
        }
        if r.Method != http.MethodPost {
                writeError(w, http.StatusMethodNotAllowed, "method not allowed")
                return
        }

        owner, err := b.authMiddleware(r)
        if err != nil {
                slog.Warn("API /channels/pair auth failed", "err", err, "ip", r.RemoteAddr)
                writeError(w, http.StatusUnauthorized, "unauthorized")
                return
        }

        if !b.checkAccess(owner.UserID) {
                writeError(w, http.StatusForbidden, "forbidden")
                return
        }

        var req apiPairRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
                writeError(w, http.StatusBadRequest, "invalid JSON body")
                return
        }
        if req.TgChatID == 0 || req.MaxChatID == 0 {
                writeError(w, http.StatusBadRequest, "tg_chat_id and max_chat_id are required")
                return
        }

        // Проверяем, не связан ли MAX-канал уже
        if _, _, ok := b.repo.GetCrosspostTgChat(req.MaxChatID, 0); ok {
                writeError(w, http.StatusConflict, "max channel already paired")
                return
        }

        // Проверяем, не связан ли TG-канал уже с другим MAX-каналом
        if _, _, ok := b.repo.GetCrosspostMaxChat(req.TgChatID, 0); ok {
                writeError(w, http.StatusConflict, "tg channel already paired")
                return
        }

        // Владелец со стороны TG не известен при API-паринге — передаём 0
        if err := b.repo.PairCrosspost(req.TgChatID, req.MaxChatID, owner.UserID, 0); err != nil {
                slog.Error("API /channels/pair failed", "err", err, "owner", owner.UserID)
                writeError(w, http.StatusInternalServerError, "failed to create pair")
                return
        }

        slog.Info("API /channels/pair created", "owner", owner.UserID, "platform", owner.Platform,
                "tgChat", req.TgChatID, "maxChat", req.MaxChatID)
        writeJSON(w, http.StatusCreated, map[string]interface{}{
                "tg_chat_id":  req.TgChatID,
                "max_chat_id": req.MaxChatID,
                "direction":   "tg>max",
        })
}

// handleAPIChannelsDelete — DELETE /api/channels (Sprint 4 Correction)
// Удаляет связку каналов. Заменяет ручную команду /crosspost delete из бота.
// Тело запроса: {"max_chat_id": <int64>}
func (b *Bridge) handleAPIChannelsDelete(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodOptions {
                w.Header().Set("Access-Control-Allow-Origin", "*")
                w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Init-Data")
                w.WriteHeader(http.StatusNoContent)
                return
        }
        if r.Method != http.MethodDelete {
                writeError(w, http.StatusMethodNotAllowed, "method not allowed")
                return
        }

        owner, err := b.authMiddleware(r)
        if err != nil {
                slog.Warn("API DELETE /channels auth failed", "err", err, "ip", r.RemoteAddr)
                writeError(w, http.StatusUnauthorized, "unauthorized")
                return
        }

        if !b.checkAccess(owner.UserID) {
                writeError(w, http.StatusForbidden, "forbidden")
                return
        }

        var req apiUnpairRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
                writeError(w, http.StatusBadRequest, "invalid JSON body")
                return
        }
        if req.MaxChatID == 0 {
                writeError(w, http.StatusBadRequest, "max_chat_id is required")
                return
        }

        // Проверяем владение связкой
        if _, _, ok := b.repo.GetCrosspostTgChat(req.MaxChatID, owner.UserID); !ok {
                writeError(w, http.StatusForbidden, "crosspost pair not found or access denied")
                return
        }

        if ok := b.repo.UnpairCrosspost(req.MaxChatID, owner.UserID); !ok {
                writeError(w, http.StatusNotFound, "pair not found")
                return
        }

        slog.Info("API DELETE /channels done", "owner", owner.UserID, "platform", owner.Platform, "maxChat", req.MaxChatID)
        writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleAPIReplacements — GET /api/replacements и POST /api/replacements (Sprint 4 Correction)
// GET: возвращает список автозамен для связки. POST: добавляет новую автозамену.
// Параметр: ?max_chat_id=<int64>
func (b *Bridge) handleAPIReplacements(w http.ResponseWriter, r *http.Request) {
        if r.Method == http.MethodOptions {
                w.Header().Set("Access-Control-Allow-Origin", "*")
                w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Init-Data")
                w.WriteHeader(http.StatusNoContent)
                return
        }

        owner, err := b.authMiddleware(r)
        if err != nil {
                slog.Warn("API /replacements auth failed", "err", err, "ip", r.RemoteAddr)
                writeError(w, http.StatusUnauthorized, "unauthorized")
                return
        }

        maxChatIDStr := r.URL.Query().Get("max_chat_id")
        if maxChatIDStr == "" {
                writeError(w, http.StatusBadRequest, "max_chat_id query param required")
                return
        }
        var maxChatID int64
        if _, err := fmt.Sscanf(maxChatIDStr, "%d", &maxChatID); err != nil || maxChatID == 0 {
                writeError(w, http.StatusBadRequest, "invalid max_chat_id")
                return
        }

        // Проверяем владение связкой
        if _, _, ok := b.repo.GetCrosspostTgChat(maxChatID, owner.UserID); !ok {
                writeError(w, http.StatusForbidden, "crosspost pair not found or access denied")
                return
        }

        switch r.Method {
        case http.MethodGet:
                repl := b.repo.GetCrosspostReplacements(maxChatID)
                writeJSON(w, http.StatusOK, repl)

        case http.MethodPost:
                var body struct {
                        Direction string `json:"direction"`
                        From      string `json:"from"`
                        To        string `json:"to"`
                        Regex     bool   `json:"regex"`
                        Target    string `json:"target"`
                }
                if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
                        writeError(w, http.StatusBadRequest, "invalid JSON body")
                        return
                }
                if body.Direction != "tg>max" && body.Direction != "max>tg" {
                        writeError(w, http.StatusBadRequest, "direction must be tg>max or max>tg")
                        return
                }
                if body.From == "" {
                        writeError(w, http.StatusBadRequest, "from is required")
                        return
                }
                rule := Replacement{From: body.From, To: body.To, Regex: body.Regex, Target: body.Target}
                repl := b.repo.GetCrosspostReplacements(maxChatID)
                if body.Direction == "tg>max" {
                        repl.TgToMax = append(repl.TgToMax, rule)
                } else {
                        repl.MaxToTg = append(repl.MaxToTg, rule)
                }
                if err := b.repo.SetCrosspostReplacements(maxChatID, repl); err != nil {
                        slog.Error("API /replacements save failed", "err", err, "owner", owner.UserID)
                        writeError(w, http.StatusInternalServerError, "failed to save replacement")
                        return
                }
                slog.Info("API /replacements added", "owner", owner.UserID, "maxChat", maxChatID, "dir", body.Direction)
                writeJSON(w, http.StatusCreated, repl)

        default:
                writeError(w, http.StatusMethodNotAllowed, "method not allowed")
        }
}

// registerAPIRoutes регистрирует маршруты API на указанном ServeMux.
// Вызывается из Bridge.Run() перед запуском HTTP-сервера.
func (b *Bridge) registerAPIRoutes(mux *http.ServeMux) {
        // Serve frontend Mini App static files
        if b.cfg.MiniAppDir != "" {
                fs := http.FileServer(http.Dir(b.cfg.MiniAppDir))
                mux.Handle("/app/", http.StripPrefix("/app/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
                        w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
                        w.Header().Set("Pragma", "no-cache")
                        w.Header().Set("Expires", "0")
                        fs.ServeHTTP(w, r)
                })))
                slog.Info("Mini App static served", "path", b.cfg.MiniAppDir, "url", "/app/")
        }

        mux.HandleFunc("/api/channels", b.handleAPIChannels)
        mux.HandleFunc("/api/sync/start", b.handleAPISyncStart)
        mux.HandleFunc("/api/history/clear", b.handleAPIHistoryClear)
        mux.HandleFunc("/api/settings", b.handleAPISettings)
        // Sprint 4: новые эндпоинты для профиля и списка задач
        mux.HandleFunc("/api/profile", b.handleAPIProfile)
        mux.HandleFunc("/api/tasks", b.handleAPITasks)
        mux.HandleFunc("/api/tasks/cancel", b.handleAPITaskCancel)
        mux.HandleFunc("/api/tasks/history", b.handleAPITasksClearHistory)
        mux.HandleFunc("/api/config", b.handleAPIConfig)
        // Sprint 4 Correction: Mini App управляет связками напрямую через API
        mux.HandleFunc("/api/channels/pair", b.handleAPIChannelsPair)
        mux.HandleFunc("/api/channels/delete", b.handleAPIChannelsDelete)
        mux.HandleFunc("/api/replacements", b.handleAPIReplacements)
        // MTProto SaaS Auth
        mux.HandleFunc("/api/mtproto/auth/status", b.handleAPIMTProtoAuthStatus)
        mux.HandleFunc("/api/mtproto/auth/start", b.handleAPIMTProtoAuthStart)
        mux.HandleFunc("/api/mtproto/auth/phone", b.handleAPIMTProtoAuthPhone)
        mux.HandleFunc("/api/mtproto/auth/code", b.handleAPIMTProtoAuthCode)
        mux.HandleFunc("/api/mtproto/auth/password", b.handleAPIMTProtoAuthPassword)
}



// handleAPIConfig — GET /api/config
// Возвращает флаги доступности функций (без авторизации).
func (b *Bridge) handleAPIConfig(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        if r.Method == http.MethodOptions {
                w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Init-Data")
                w.WriteHeader(http.StatusNoContent)
                return
        }
        mtprotoEnabled := b.cfg.TGAppID != 0 && b.cfg.TGAppHash != ""
        // mtprotoReady устанавливается в sync_worker только после успешной авторизации
        mtprotoAuthorized := b.mtprotoReady.Load()
        writeJSON(w, http.StatusOK, map[string]interface{}{
                "mtproto_enabled":    mtprotoEnabled,
                "mtproto_authorized": mtprotoAuthorized,
        })
}

// handleAPIMTProtoAuthStatus — GET /api/mtproto/auth/status
func (b *Bridge) handleAPIMTProtoAuthStatus(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        if r.Method == http.MethodOptions {
                w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Init-Data")
                w.WriteHeader(http.StatusNoContent)
                return
        }
        owner, err := b.authMiddleware(r)
        if err != nil {
                writeError(w, http.StatusUnauthorized, "unauthorized")
                return
        }

        b.authFlowsMu.Lock()
        flow, exists := b.authFlows[owner.UserID]
        b.authFlowsMu.Unlock()

        if exists {
                writeJSON(w, http.StatusOK, map[string]string{
                        "status": flow.Status,
                        "error":  flow.Error,
                })
                return
        }

        // Если флоу нет, проверим есть ли уже сессия в БД
        sessionBytes, err := b.repo.GetMTProtoSession(owner.UserID)
        if err == nil && len(sessionBytes) > 0 {
                writeJSON(w, http.StatusOK, map[string]interface{}{
                        "status": "authorized",
                })
                return
        }

        writeJSON(w, http.StatusOK, map[string]string{
                "status": "none",
        })
}

// handleAPIMTProtoAuthStart — POST /api/mtproto/auth/start
func (b *Bridge) handleAPIMTProtoAuthStart(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        if r.Method == http.MethodOptions {
                w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Init-Data")
                w.WriteHeader(http.StatusNoContent)
                return
        }
        owner, err := b.authMiddleware(r)
        if err != nil {
                writeError(w, http.StatusUnauthorized, "unauthorized")
                return
        }

        b.authFlowsMu.Lock()
        if existing, ok := b.authFlows[owner.UserID]; ok {
                existing.cancel()
        }
        flow := NewAuthFlow()
        b.authFlows[owner.UserID] = flow
        b.authFlowsMu.Unlock()

        go b.runSaaSAuthClient(owner.UserID, flow)

        writeJSON(w, http.StatusOK, map[string]string{"status": flow.Status})
}

// runSaaSAuthClient запускает клиент gotd для авторизации пользователя.
func (b *Bridge) runSaaSAuthClient(userID int64, flow *AuthFlow) {
        defer func() {
                b.authFlowsMu.Lock()
                if b.authFlows[userID] == flow {
                        delete(b.authFlows, userID)
                }
                b.authFlowsMu.Unlock()
                flow.cancel()
        }()

        memSession := &MemorySessionStorage{}
        client := telegram.NewClient(b.cfg.TGAppID, b.cfg.TGAppHash, telegram.Options{
                SessionStorage: memSession,
                NoUpdates:      true,
        })

        err := client.Run(flow.ctx, func(ctx context.Context) error {
                status, err := client.Auth().Status(ctx)
                if err != nil {
                        return err
                }
                if status.Authorized {
                        flow.Status = "authorized"
                        return nil
                }

                flowDef := auth.NewFlow(flow, auth.SendCodeOptions{})
                if err := client.Auth().IfNecessary(ctx, flowDef); err != nil {
                        return err
                }
                flow.Status = "authorized"
                return nil
        })

        if err != nil && flow.ctx.Err() == nil {
                slog.Error("SaaS Auth client failed", "user", userID, "err", err)
                flow.Status = "error"
                flow.Error = err.Error()
        } else if flow.Status == "authorized" {
                if memSession.Data != nil {
                        _ = b.repo.SaveMTProtoSession(userID, memSession.Data)
                        slog.Info("SaaS Auth success, session saved", "user", userID)
                }
        }
}

// handleAPIMTProtoAuthPhone — POST /api/mtproto/auth/phone
func (b *Bridge) handleAPIMTProtoAuthPhone(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        if r.Method == http.MethodOptions {
                w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Init-Data")
                w.WriteHeader(http.StatusNoContent)
                return
        }
        owner, err := b.authMiddleware(r)
        if err != nil {
                writeError(w, http.StatusUnauthorized, "unauthorized")
                return
        }
        var req struct {
                Phone string `json:"phone"`
        }
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
                writeError(w, http.StatusBadRequest, "invalid request")
                return
        }
        b.authFlowsMu.Lock()
        flow, ok := b.authFlows[owner.UserID]
        b.authFlowsMu.Unlock()
        if !ok {
                writeError(w, http.StatusBadRequest, "auth flow not started")
                return
        }
        flow.phoneChan <- req.Phone
        writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleAPIMTProtoAuthCode — POST /api/mtproto/auth/code
func (b *Bridge) handleAPIMTProtoAuthCode(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        if r.Method == http.MethodOptions {
                w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Init-Data")
                w.WriteHeader(http.StatusNoContent)
                return
        }
        owner, err := b.authMiddleware(r)
        if err != nil {
                writeError(w, http.StatusUnauthorized, "unauthorized")
                return
        }
        var req struct {
                Code string `json:"code"`
        }
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
                writeError(w, http.StatusBadRequest, "invalid request")
                return
        }
        b.authFlowsMu.Lock()
        flow, ok := b.authFlows[owner.UserID]
        b.authFlowsMu.Unlock()
        if !ok {
                writeError(w, http.StatusBadRequest, "auth flow not started")
                return
        }
        flow.codeChan <- req.Code
        writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleAPIMTProtoAuthPassword — POST /api/mtproto/auth/password
func (b *Bridge) handleAPIMTProtoAuthPassword(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        if r.Method == http.MethodOptions {
                w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Init-Data")
                w.WriteHeader(http.StatusNoContent)
                return
        }
        owner, err := b.authMiddleware(r)
        if err != nil {
                writeError(w, http.StatusUnauthorized, "unauthorized")
                return
        }
        var req struct {
                Password string `json:"password"`
        }
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
                writeError(w, http.StatusBadRequest, "invalid request")
                return
        }
        b.authFlowsMu.Lock()
        flow, ok := b.authFlows[owner.UserID]
        b.authFlowsMu.Unlock()
        if !ok {
                writeError(w, http.StatusBadRequest, "auth flow not started")
                return
        }
        flow.passwordChan <- req.Password
        writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// startHTTPServer запускает HTTP-сервер с регистрацией вебхуков и API.
// Всегда запускается (не только в режиме вебхука) для работы API.
func (b *Bridge) startHTTPServer(ctx context.Context, mux *http.ServeMux) {
        addr := ":" + b.cfg.WebhookPort
        srv := &http.Server{
                Addr:         addr,
                Handler:      mux,
                ReadTimeout:  10 * time.Second,
                WriteTimeout: 10 * time.Second,
                IdleTimeout:  60 * time.Second,
        }

        go func() {
                <-ctx.Done()
                shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
                defer cancel()
                if err := srv.Shutdown(shutCtx); err != nil {
                        slog.Error("HTTP server shutdown error", "err", err)
                }
        }()

        slog.Info("HTTP server starting (webhooks + API)", "addr", addr)
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
                slog.Error("HTTP server failed", "err", err)
        }
}
