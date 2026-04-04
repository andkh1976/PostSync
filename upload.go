package main

import (
        "bytes"
        "context"
        "encoding/json"
        "fmt"
        "io"
        "log/slog"
        "mime/multipart"
        "net/http"
        "os"
        "strings"
        "time"

        maxschemes "github.com/max-messenger/max-bot-api-client-go/schemes"
        tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// downloadURL скачивает файл по URL и возвращает bytes.
func (b *Bridge) downloadURL(url string) ([]byte, error) {
        resp, err := b.httpClient.Get(url)
        if err != nil {
                return nil, err
        }
        defer resp.Body.Close()
        if resp.StatusCode != 200 {
                return nil, fmt.Errorf("download status %d", resp.StatusCode)
        }
        return io.ReadAll(resp.Body)
}

// sendTgMediaFromURL скачивает файл с URL и отправляет в TG как upload.
// maxBytes=0 means no size limit. fileName overrides name extracted from URL.
// Если настроен локальный Bot API (b.cfg.TgAPIURL != ""), для video и file
// используется FileURL — локальный сервер сам скачивает файл без буферизации в RAM.
func (b *Bridge) sendTgMediaFromURL(tgChatID int64, mediaURL, mediaType, caption, parseMode string, replyToID int, maxBytes int64, fileName ...string) (tgbotapi.Message, error) {
        // При локальном Bot API сервере для крупных типов используем FileURL:
        // Telegram-сервер сам получает файл напрямую, без загрузки в память приложения.
        if b.cfg.TgAPIURL != "" && (mediaType == "video" || mediaType == "file" || mediaType == "audio") {
                fu := tgbotapi.FileURL(mediaURL)
                switch mediaType {
                case "video":
                        msg := tgbotapi.NewVideo(tgChatID, fu)
                        msg.Caption = caption
                        if parseMode != "" {
                                msg.ParseMode = parseMode
                        }
                        msg.ReplyToMessageID = replyToID
                        return b.tgBot.Send(msg)
                case "audio":
                        msg := tgbotapi.NewAudio(tgChatID, fu)
                        msg.Caption = caption
                        if parseMode != "" {
                                msg.ParseMode = parseMode
                        }
                        msg.ReplyToMessageID = replyToID
                        return b.tgBot.Send(msg)
                case "file":
                        msg := tgbotapi.NewDocument(tgChatID, fu)
                        msg.Caption = caption
                        if parseMode != "" {
                                msg.ParseMode = parseMode
                        }
                        msg.ReplyToMessageID = replyToID
                        return b.tgBot.Send(msg)
                }
        }

        data, nameFromURL, err := b.downloadURLWithLimit(mediaURL, maxBytes)
        if err != nil {
                return tgbotapi.Message{}, fmt.Errorf("download media: %w", err)
        }

        name := nameFromURL
        if len(fileName) > 0 && fileName[0] != "" {
                name = fileName[0]
        }
        fb := tgbotapi.FileBytes{Name: name, Bytes: data}

        switch mediaType {
        case "photo":
                msg := tgbotapi.NewPhoto(tgChatID, fb)
                msg.Caption = caption
                if parseMode != "" {
                        msg.ParseMode = parseMode
                }
                msg.ReplyToMessageID = replyToID
                return b.tgBot.Send(msg)
        case "video":
                msg := tgbotapi.NewVideo(tgChatID, fb)
                msg.Caption = caption
                if parseMode != "" {
                        msg.ParseMode = parseMode
                }
                msg.ReplyToMessageID = replyToID
                return b.tgBot.Send(msg)
        case "audio":
                msg := tgbotapi.NewAudio(tgChatID, fb)
                msg.Caption = caption
                if parseMode != "" {
                        msg.ParseMode = parseMode
                }
                msg.ReplyToMessageID = replyToID
                return b.tgBot.Send(msg)
        case "file":
                msg := tgbotapi.NewDocument(tgChatID, fb)
                msg.Caption = caption
                if parseMode != "" {
                        msg.ParseMode = parseMode
                }
                msg.ReplyToMessageID = replyToID
                return b.tgBot.Send(msg)
        default:
                // sticker и прочее — как фото
                msg := tgbotapi.NewPhoto(tgChatID, fb)
                msg.Caption = caption
                return b.tgBot.Send(msg)
        }
}

// progressReader оборачивает io.Reader и логирует прогресс каждые ~10% для файлов > 100 МБ.
type progressReader struct {
        r       io.Reader
        total   int64
        read    int64
        name    string
        lastPct int
}

func (p *progressReader) Read(buf []byte) (n int, err error) {
        n, err = p.r.Read(buf)
        p.read += int64(n)
        if p.total > 0 {
                pct := int(p.read * 100 / p.total)
                if pct/10 > p.lastPct/10 {
                        p.lastPct = pct
                        slog.Info("upload progress TG→MAX",
                                "file", p.name,
                                "pct", pct,
                                "read", formatFileSize(int(p.read)),
                                "total", formatFileSize(int(p.total)),
                        )
                }
        }
        return
}

// customUploadToMax — обход бага SDK: CDN возвращает XML вместо JSON.
// Использует потоковую (streaming) передачу через io.Pipe без буферизации файла в RAM.
// contentLength передаётся для логирования прогресса (0 = неизвестен).
func (b *Bridge) customUploadToMax(ctx context.Context, uploadType maxschemes.UploadType, reader io.Reader, fileName string, contentLength int64) (*maxschemes.UploadedInfo, error) {
        // 1. Получаем URL и token от MAX API
        apiURL := fmt.Sprintf("https://platform-api.max.ru/uploads?type=%s&v=1.2.5", string(uploadType))
        req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, nil)
        if err != nil {
                return nil, fmt.Errorf("create request: %w", err)
        }
        req.Header.Set("Authorization", b.cfg.MaxToken)

        resp, err := b.apiClient.Do(req)
        if err != nil {
                return nil, fmt.Errorf("get upload url: %w", err)
        }
        defer resp.Body.Close()

        if resp.StatusCode != 200 {
                return nil, fmt.Errorf("upload endpoint status: %d", resp.StatusCode)
        }

        endpointBody, _ := io.ReadAll(resp.Body)
        slog.Info("MAX upload endpoint response", "status", resp.StatusCode, "body", string(endpointBody))

        var endpoint maxschemes.UploadEndpoint
        if err := json.Unmarshal(endpointBody, &endpoint); err != nil {
                return nil, fmt.Errorf("decode upload endpoint: %w", err)
        }
        slog.Info("MAX upload endpoint", "url", endpoint.Url, "token", endpoint.Token)

        // Если токен уже в ответе шага 1, но URL НЕТ — значит загружать больше некуда
        if endpoint.Token != "" && endpoint.Url == "" {
                slog.Info("MAX upload ok (endpoint token, no CDN needed)")
                return &maxschemes.UploadedInfo{Token: endpoint.Token}, nil
        }
        if endpoint.Url == "" {
                return nil, fmt.Errorf("upload endpoint returned empty URL and no token")
        }
        // Используем 15-минутный таймаут для загрузки больших файлов
        uploadCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
        defer cancel()

        var cdnReq *http.Request
        var errReq error

        // 2. Загрузка на CDN.
        // Для файлов < 50 МБ буферизируем в память для точного вычисления Content-Length,
        // чтобы избежать Chunked Transfer-Encoding (который ломает MAX CDN).
        if contentLength > 0 && contentLength <= 50*1024*1024 {
                var b bytes.Buffer
                mw := multipart.NewWriter(&b)
                part, err := mw.CreateFormFile("data", fileName)
                if err != nil {
                        return nil, fmt.Errorf("create form file: %w", err)
                }
                if _, err := io.Copy(part, reader); err != nil {
                        return nil, fmt.Errorf("copy to form: %w", err)
                }
                mw.Close()

                cdnReq, errReq = http.NewRequestWithContext(uploadCtx, http.MethodPost, endpoint.Url, &b)
                if errReq != nil {
                        return nil, fmt.Errorf("create CDN request: %w", errReq)
                }
                cdnReq.Header.Set("Content-Type", mw.FormDataContentType())
        } else {
                // Потоковая загрузка файла на CDN (multipart) через io.Pipe без буферизации в памяти (Фолбэк).
                pr, pw := io.Pipe()
                mw := multipart.NewWriter(pw)

                go func() {
                        part, err := mw.CreateFormFile("data", fileName)
                        if err != nil {
                                pw.CloseWithError(fmt.Errorf("create form file: %w", err))
                                return
                        }
                        var src io.Reader = reader
                        // Для файлов > 100 МБ включаем логирование прогресса
                        if contentLength > 100*1024*1024 {
                                src = &progressReader{r: reader, total: contentLength, name: fileName}
                        }
                        if _, err := io.Copy(part, src); err != nil {
                                pw.CloseWithError(fmt.Errorf("copy to form: %w", err))
                                return
                        }
                        pw.CloseWithError(mw.Close())
                }()

                cdnReq, errReq = http.NewRequestWithContext(uploadCtx, http.MethodPost, endpoint.Url, pr)
                if errReq != nil {
                        return nil, fmt.Errorf("create CDN request: %w", errReq)
                }
                cdnReq.Header.Set("Content-Type", mw.FormDataContentType())
        }

        cdnResp, err := b.httpClient.Do(cdnReq)
        if err != nil {
                return nil, fmt.Errorf("upload to CDN (file=%s size=%s): %w", fileName, formatFileSize(int(contentLength)), err)
        }
        defer cdnResp.Body.Close()

        cdnBody, _ := io.ReadAll(cdnResp.Body)
        slog.Info("MAX CDN response", "status", cdnResp.StatusCode, "body", string(cdnBody))

        if cdnResp.StatusCode != 200 {
                slog.Error("MAX CDN upload failed", "status", cdnResp.StatusCode, "body", string(cdnBody), "file", fileName, "size", formatFileSize(int(contentLength)))
                return nil, fmt.Errorf("CDN upload status %d (file=%s): %s", cdnResp.StatusCode, fileName, string(cdnBody))
        }

        // Проверяем ошибку запрещённого расширения
        var apiErr struct {
                Code    string `json:"code"`
                Message string `json:"message"`
        }
        if json.Unmarshal(cdnBody, &apiErr) == nil && apiErr.Code == "upload.error" {
                slog.Warn("MAX upload rejected", "code", apiErr.Code, "message", apiErr.Message, "file", fileName)
                return nil, &ErrForbiddenExtension{Name: fileName}
        }

        // 3. Парсим CDN ответ (fileId в camelCase)
        var cdnResult struct {
                FileID int64  `json:"fileId"`
                Token  string `json:"token"`
        }
        if err := json.Unmarshal(cdnBody, &cdnResult); err == nil && cdnResult.Token != "" {
                slog.Info("MAX upload ok", "fileId", cdnResult.FileID)
                return &maxschemes.UploadedInfo{Token: cdnResult.Token, FileID: cdnResult.FileID}, nil
        }
        
        // Если CDN не вернул JSON токен, но вернул HTTP 200, и у нас есть токен из Шага 1:
        if endpoint.Token != "" {
                slog.Info("MAX upload ok (used endpoint token after successful CDN upload)")
                return &maxschemes.UploadedInfo{Token: endpoint.Token}, nil
        }

        return nil, fmt.Errorf("no token in CDN response (file=%s status=%d): %s", fileName, cdnResp.StatusCode, string(cdnBody))
}

const tgFileRetries = 3
const tgFileRetryDelay = 1500 * time.Millisecond

// tgPhotoRetries и tgPhotoRetryDelay используются при загрузке фото с локального Bot API.
// Локальный сервер иногда получает вебхук раньше, чем успевает закешировать файл у Telegram,
// поэтому для фото нужно больше попыток с увеличенной задержкой.
const tgPhotoRetries = 5
const tgPhotoRetryDelay = 3 * time.Second

// openTgFile открывает файл из TG: при локальном Bot API — с диска, иначе — по HTTP.
// Возвращает io.ReadCloser и размер файла (-1 если неизвестен).
func (b *Bridge) openTgFile(ctx context.Context, filePathOrURL string) (io.ReadCloser, int64, error) {
        if b.cfg.TgAPIURL != "" {
                // Локальный режим — файл на диске (shared volume)
                f, err := os.Open(filePathOrURL)
                if err != nil {
                        return nil, 0, fmt.Errorf("open local file: %w", err)
                }
                stat, _ := f.Stat()
                size := int64(-1)
                if stat != nil {
                        size = stat.Size()
                }
                return f, size, nil
        }
        // Облачный режим — скачиваем по HTTP
        dlReq, err := http.NewRequestWithContext(ctx, http.MethodGet, filePathOrURL, nil)
        if err != nil {
                return nil, 0, fmt.Errorf("create download request: %w", err)
        }
        resp, err := b.httpClient.Do(dlReq)
        if err != nil {
                return nil, 0, fmt.Errorf("download: %w", err)
        }
        if resp.StatusCode != 200 {
                resp.Body.Close()
                return nil, 0, fmt.Errorf("tg download status: %d url: %s", resp.StatusCode, filePathOrURL)
        }
        return resp.Body, resp.ContentLength, nil
}

// uploadTgPhotoToMax скачивает фото из TG и загружает в MAX через SDK (возвращает PhotoTokens).
// При ошибках getFile или скачивания делает до tgPhotoRetries попыток —
// локальный Bot API сервер иногда ещё не успел скачать файл к моменту первого обращения.
func (b *Bridge) uploadTgPhotoToMax(ctx context.Context, fileID string) (*maxschemes.PhotoTokens, error) {
        var lastErr error
        for attempt := 0; attempt < tgPhotoRetries; attempt++ {
                if attempt > 0 {
                        slog.Warn("uploadTgPhotoToMax: retry", "attempt", attempt+1, "maxAttempts", tgPhotoRetries, "err", lastErr)
                        select {
                        case <-ctx.Done():
                                return nil, ctx.Err()
                        case <-time.After(tgPhotoRetryDelay):
                        }
                }
                filePathOrURL, err := b.tgFileURL(fileID)
                if err != nil {
                        lastErr = fmt.Errorf("tg getFileURL: %w", err)
                        continue
                }
                slog.Info("uploadTgPhotoToMax: opening", "attempt", attempt+1, "src", filePathOrURL, "fileID", fileID)
                reader, _, err := b.openTgFile(ctx, filePathOrURL)
                if err != nil {
                        lastErr = err
                        continue
                }
                result, err := b.maxApi.Uploads.UploadPhotoFromReader(ctx, reader)
                reader.Close()
                return result, err
        }
        slog.Error("uploadTgPhotoToMax: all attempts failed", "attempts", tgPhotoRetries, "err", lastErr)
        return nil, lastErr
}

// uploadTgMediaToMax скачивает файл из TG и загружает в MAX (потоковая передача без буферизации в RAM).
// При использовании локального Bot API сервера (TgAPIURL != "") и ошибках getFile или скачивания
// делает до tgFileRetries попыток — локальный сервер иногда ещё не успел
// скачать файл к моменту первого обращения.
func (b *Bridge) uploadTgMediaToMax(ctx context.Context, fileID string, uploadType maxschemes.UploadType, fileName string) (*maxschemes.UploadedInfo, error) {
        maxAttempts := 1
        if b.cfg.TgAPIURL != "" {
                maxAttempts = tgFileRetries
        }

        var lastErr error
        for attempt := 0; attempt < maxAttempts; attempt++ {
                if attempt > 0 {
                        slog.Warn("uploadTgMediaToMax: retry", "attempt", attempt+1, "maxAttempts", maxAttempts, "file", fileName, "err", lastErr)
                        select {
                        case <-ctx.Done():
                                return nil, ctx.Err()
                        case <-time.After(tgFileRetryDelay):
                        }
                }
                filePathOrURL, err := b.tgFileURL(fileID)
                if err != nil {
                        lastErr = fmt.Errorf("tg getFileURL: %w", err)
                        continue
                }
                reader, contentLength, err := b.openTgFile(ctx, filePathOrURL)
                if err != nil {
                        lastErr = err
                        continue
                }
                slog.Info("TG file download started", "size", formatFileSize(int(contentLength)), "file", fileName)

                // Файлы > 2 ГБ доступны только отправителями с Telegram Premium
                const tgPremiumThreshold = 2 * 1024 * 1024 * 1024
                if contentLength > tgPremiumThreshold {
                        slog.Warn("file exceeds 2 GB — Telegram Premium required for sender",
                                "file", fileName,
                                "size", formatFileSize(int(contentLength)),
                        )
                }

                result, err := b.customUploadToMax(ctx, uploadType, reader, fileName, contentLength)
                reader.Close()
                return result, err
        }
        slog.Error("uploadTgMediaToMax: all attempts failed", "attempts", maxAttempts, "file", fileName, "err", lastErr)
        return nil, lastErr
}

// sendMaxDirect — отправка сообщения в MAX напрямую (обход SDK)
func (b *Bridge) sendMaxDirect(ctx context.Context, chatID int64, text string, attType string, token string, replyTo string) (string, error) {
        return b.sendMaxDirectFormatted(ctx, chatID, text, attType, token, replyTo, "")
}

func (b *Bridge) sendMaxDirectFormatted(ctx context.Context, chatID int64, text string, attType string, token string, replyTo string, format string) (string, error) {
        type attachment struct {
                Type    string            `json:"type"`
                Payload map[string]string `json:"payload"`
        }
        type msgBody struct {
                Text        string       `json:"text,omitempty"`
                Attachments []attachment `json:"attachments,omitempty"`
                Format      string       `json:"format,omitempty"`
                Link        *struct {
                        Type string `json:"type"`
                        Mid  string `json:"mid"`
                } `json:"link,omitempty"`
        }

        body := msgBody{Text: text, Format: format}
        if attType != "" && token != "" {
                body.Attachments = []attachment{{
                        Type:    attType,
                        Payload: map[string]string{"token": token},
                }}
        }
        if replyTo != "" {
                body.Link = &struct {
                        Type string `json:"type"`
                        Mid  string `json:"mid"`
                }{Type: "reply", Mid: replyTo}
        }

        data, err := json.Marshal(body)
        if err != nil {
                return "", err
        }

        url := fmt.Sprintf("https://platform-api.max.ru/messages?chat_id=%d&v=1.2.5", chatID)

        // Retry при attachment.not.ready (быстрые попытки)
        // Если база долго конвертирует, очередь (queue) обеспечит длинные попытки
        for attempt := 0; attempt < 3; attempt++ {
                if attempt > 0 {
                        delay := time.Duration(1+attempt) * time.Second
                        select {
                        case <-ctx.Done():
                                return "", ctx.Err()
                        case <-time.After(delay):
                        }
                        slog.Warn("MAX retry", "attempt", attempt+1, "maxAttempts", 10)
                }

                req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
                if err != nil {
                        return "", err
                }
                req.Header.Set("Authorization", b.cfg.MaxToken)
                req.Header.Set("Content-Type", "application/json")

                resp, err := b.apiClient.Do(req)
                if err != nil {
                        return "", err
                }

                respBody, _ := io.ReadAll(resp.Body)
                resp.Body.Close()

                if resp.StatusCode == 200 {
                        var result struct {
                                Message struct {
                                        Body struct {
                                                Mid string `json:"mid"`
                                        } `json:"body"`
                                } `json:"message"`
                        }
                        if err := json.Unmarshal(respBody, &result); err != nil {
                                return "", err
                        }
                        return result.Message.Body.Mid, nil
                }

                // Проверяем attachment.not.ready — ретраим
                if resp.StatusCode == 400 && strings.Contains(string(respBody), "attachment.not.ready") {
                        slog.Warn("MAX attachment not ready, waiting")
                        continue
                }

                return "", fmt.Errorf("MAX API %d: %s", resp.StatusCode, string(respBody))
        }
        return "", fmt.Errorf("MAX attachment not ready after 3 retries")
}

// formatFileSize formats file size in human-readable form.
func formatFileSize(size int) string {
        switch {
        case size >= 1024*1024:
                return fmt.Sprintf("%.1f МБ", float64(size)/1024/1024)
        case size >= 1024:
                return fmt.Sprintf("%.1f КБ", float64(size)/1024)
        default:
                return fmt.Sprintf("%d Б", size)
        }
}

// ErrFileTooLarge is returned when file exceeds the configured size limit.
type ErrFileTooLarge struct {
        Size int64
        Name string
}

func (e *ErrFileTooLarge) Error() string {
        return fmt.Sprintf("file too large: %s (%s)", e.Name, formatFileSize(int(e.Size)))
}

// ErrForbiddenExtension is returned when MAX API rejects the file extension.
type ErrForbiddenExtension struct {
        Name string
}

func (e *ErrForbiddenExtension) Error() string {
        return fmt.Sprintf("file extension forbidden by MAX: %s", e.Name)
}

// downloadURLWithLimit downloads a file from URL with an optional size limit.
// maxBytes=0 means no limit. Returns bytes and filename from Content-Disposition or URL.
func (b *Bridge) downloadURLWithLimit(url string, maxBytes int64) ([]byte, string, error) {
        resp, err := b.httpClient.Get(url)
        if err != nil {
                return nil, "", err
        }
        defer resp.Body.Close()
        if resp.StatusCode != 200 {
                return nil, "", fmt.Errorf("download status %d", resp.StatusCode)
        }

        // Extract filename from Content-Disposition
        name := ""
        if cd := resp.Header.Get("Content-Disposition"); cd != "" {
                if i := strings.Index(cd, "filename=\""); i >= 0 {
                        rest := cd[i+len("filename=\""):]
                        if j := strings.Index(rest, "\""); j >= 0 {
                                name = rest[:j]
                        }
                }
                if name == "" {
                        if i := strings.Index(cd, "filename="); i >= 0 {
                                rest := strings.TrimSpace(cd[i+len("filename="):])
                                if j := strings.IndexAny(rest, "; \t"); j >= 0 {
                                        name = rest[:j]
                                } else {
                                        name = rest
                                }
                        }
                }
        }
        if name == "" {
                name = fileNameFromURL(url)
        }

        // Fast check via Content-Length
        if maxBytes > 0 && resp.ContentLength > maxBytes {
                return nil, name, &ErrFileTooLarge{Size: resp.ContentLength, Name: name}
        }

        // Read with limit
        limit := maxBytes
        if limit <= 0 {
                limit = 1<<63 - 1
        }
        data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
        if err != nil {
                return nil, "", err
        }
        if maxBytes > 0 && int64(len(data)) > maxBytes {
                return nil, name, &ErrFileTooLarge{Size: int64(len(data)), Name: name}
        }

        return data, name, nil
}
