package main

import (
	"fmt"
	"strings"
	"time"

	maxschemes "github.com/max-messenger/max-bot-api-client-go/schemes"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func tgName(msg *tgbotapi.Message) string {
	if msg.From == nil {
		if msg.SenderChat != nil {
			return msg.SenderChat.Title
		}
		return "Unknown"
	}
	name := msg.From.FirstName
	if msg.From.LastName != "" {
		name += " " + msg.From.LastName
	}
	return name
}

// formatAttribution собирает строку "Имя: текст" или "Имя:\nтекст" в зависимости от настройки.
func formatAttribution(name, text string, newline bool) string {
	if newline {
		return name + ":\n" + text
	}
	return name + ": " + text
}

// formatTgDateLabel возвращает строку атрибуции "[TG] DD.MM.YYYY" по Unix-timestamp.
// Telegram timestamps являются UTC, поэтому явно используем UTC для стабильного результата.
func formatTgDateLabel(date int) string {
	t := time.Unix(int64(date), 0).UTC()
	return fmt.Sprintf("[TG] %s", t.Format("02.01.2006"))
}

// msgHasMedia возвращает true, если сообщение содержит медиа-вложение.
func msgHasMedia(msg *tgbotapi.Message) bool {
	return msg.Photo != nil || msg.Video != nil || msg.Document != nil ||
		msg.Audio != nil || msg.Voice != nil || msg.VideoNote != nil ||
		msg.Animation != nil || msg.Sticker != nil
}

// formatTgCaption — для пересылки (текст или caption)
func formatTgCaption(msg *tgbotapi.Message, prefix, newline bool) string {
	name := tgName(msg)
	var text string
	if msg.Text != "" {
		text = tgEntitiesToMarkdown(msg.Text, msg.Entities)
	} else {
		text = tgEntitiesToMarkdown(msg.Caption, msg.CaptionEntities)
	}
	var result string
	if prefix {
		result = formatAttribution("[TG] "+name, text, newline)
	} else {
		result = formatAttribution(name, text, newline)
	}
	if msg.Date > 0 {
		label := formatTgDateLabel(msg.Date)
		if text != "" {
			result += "\n\n" + label
		} else if msgHasMedia(msg) {
			// Медиа без подписи: метка становится единственным caption
			result = label
		}
		// Пустое текстовое сообщение — метка не добавляется
	}
	return result
}

// formatTgMessage — для edit (полный формат)
func formatTgMessage(msg *tgbotapi.Message, prefix, newline bool) string {
	name := tgName(msg)
	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	if text == "" {
		return ""
	}
	if prefix {
		return formatAttribution("[TG] "+name, text, newline)
	}
	return formatAttribution(name, text, newline)
}

func maxName(upd *maxschemes.MessageCreatedUpdate) string {
	name := upd.Message.Sender.Name
	if name == "" {
		name = upd.Message.Sender.Username
	}
	return name
}

// formatMaxCaption — для пересылки
func formatMaxCaption(upd *maxschemes.MessageCreatedUpdate, prefix, newline bool) string {
	name := maxName(upd)
	text := upd.Message.Body.Text
	if prefix {
		return formatAttribution("[MAX] "+name, text, newline)
	}
	return formatAttribution(name, text, newline)
}

// formatTgCrosspostCaption — для кросспостинга каналов (без attribution и префиксов)
func formatTgCrosspostCaption(msg *tgbotapi.Message) string {
	var text string
	if msg.Text != "" {
		text = tgEntitiesToMarkdown(msg.Text, msg.Entities)
	} else {
		text = tgEntitiesToMarkdown(msg.Caption, msg.CaptionEntities)
	}
	if msg.Date > 0 {
		label := formatTgDateLabel(msg.Date)
		if text != "" {
			text += "\n\n" + label
		} else if msgHasMedia(msg) {
			// Медиа без подписи: метка становится единственным caption
			text = label
		}
		// Пустое текстовое сообщение — метка не добавляется
	}
	return text
}

// formatMaxCrosspostCaption — для кросспостинга каналов (без attribution и префиксов)
func formatMaxCrosspostCaption(upd *maxschemes.MessageCreatedUpdate) string {
	return upd.Message.Body.Text
}

// mimeToFilename генерирует имя файла из MIME-типа, если оригинальное имя отсутствует.
func mimeToFilename(base, mime string) string {
	ext := ""
	// sub = часть после "/" в mime type
	if i := strings.Index(mime, "/"); i >= 0 {
		sub := mime[i+1:]
		switch sub {
		case "mp4":
			ext = ".mp4"
		case "webm":
			ext = ".webm"
		case "x-matroska":
			ext = ".mkv"
		case "quicktime":
			ext = ".mov"
		case "mpeg":
			ext = ".mpeg"
		case "ogg":
			ext = ".ogg"
		case "pdf":
			ext = ".pdf"
		case "gif":
			ext = ".gif"
		default:
			ext = "." + sub
		}
	}
	return base + ext
}

// fileNameFromURL извлекает имя файла из URL, fallback "file".
func fileNameFromURL(rawURL string) string {
	if idx := strings.LastIndex(rawURL, "/"); idx >= 0 {
		name := rawURL[idx+1:]
		if q := strings.Index(name, "?"); q >= 0 {
			name = name[:q]
		}
		if name != "" {
			return name
		}
	}
	return "file"
}
