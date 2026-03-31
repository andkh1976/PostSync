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
	"sort"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
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
	endDate = endDate.Add(24*time.Hour - time.Second)

	// Проверяем владение связкой
	_, _, ok := b.repo.GetCrosspostMaxChat(req.TgChatID, owner.UserID)
	if !ok {
		writeError(w, http.StatusForbidden, "crosspost pair not found or access denied")
		return
	}

	if err := b.repo.DeleteMessagesByPeriod(req.TgChatID, startDate, endDate); err != nil {
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

// registerAPIRoutes регистрирует маршруты API на указанном ServeMux.
// Вызывается из Bridge.Run() перед запуском HTTP-сервера.
func (b *Bridge) registerAPIRoutes(mux *http.ServeMux) {
	// Serve frontend Mini App static files
	if b.cfg.MiniAppDir != "" {
		mux.Handle("/app/", http.StripPrefix("/app/", http.FileServer(http.Dir(b.cfg.MiniAppDir))))
		slog.Info("Mini App static served", "path", b.cfg.MiniAppDir, "url", "/app/")
	}

	mux.HandleFunc("/api/channels", b.handleAPIChannels)
	mux.HandleFunc("/api/sync/start", b.handleAPISyncStart)
	mux.HandleFunc("/api/history/clear", b.handleAPIHistoryClear)
	mux.HandleFunc("/api/settings", b.handleAPISettings)
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
