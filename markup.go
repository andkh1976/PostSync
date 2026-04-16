package main

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"unicode/utf16"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/gotd/td/tg"
	maxschemes "github.com/max-messenger/max-bot-api-client-go/schemes"
)

// --- TG Entities → Markdown (для MAX) ---

type markdownEntity struct {
	offset   int
	length   int
	open     string
	close    string
	priority int
	valid    bool
	index    int
}

type markdownEvent struct {
	pos      int
	open     bool
	marker   string
	priority int
	length   int
	index    int
}

func tgEntitiesToMarkdown(text string, entities []tgbotapi.MessageEntity) string {
	if len(entities) == 0 {
		return text
	}

	converted := make([]markdownEntity, 0, len(entities))
	for i, e := range entities {
		md := markdownEntityFromTg(e)
		md.index = i
		if md.valid {
			converted = append(converted, md)
		}
	}

	return renderMarkdownEntities(text, converted)
}

// utf16ToString конвертирует UTF-16 slice обратно в Go string.
func utf16ToString(units []uint16) string {
	runes := utf16.Decode(units)
	return string(runes)
}

// --- MTProto Entities (gotd) → Markdown (для MAX) ---

// mtprotoEntitiesToMarkdown конвертирует MTProto text + entities в markdown-текст для MAX.
// Используется в sync_worker при пересылке исторических сообщений через MTProto клиент.
func mtprotoEntitiesToMarkdown(text string, entities []tg.MessageEntityClass) string {
	if len(entities) == 0 {
		return text
	}

	converted := make([]markdownEntity, 0, len(entities))
	for i, e := range entities {
		md := markdownEntityFromMTProto(e)
		md.index = i
		if md.valid {
			converted = append(converted, md)
		}
	}

	return renderMarkdownEntities(text, converted)
}

func markdownEntityFromTg(e tgbotapi.MessageEntity) markdownEntity {
	base := markdownEntity{offset: e.Offset, length: e.Length, valid: true}
	switch e.Type {
	case "bold":
		base.open, base.close, base.priority = "**", "**", 10
	case "italic":
		base.open, base.close, base.priority = "_", "_", 20
	case "underline":
		base.open, base.close, base.priority = "__", "__", 30
	case "strikethrough":
		base.open, base.close, base.priority = "~~", "~~", 40
	case "code":
		base.open, base.close, base.priority = "`", "`", 50
	case "pre":
		base.open, base.close, base.priority = "```\n", "\n```", 60
	case "text_link":
		base.open, base.close, base.priority = "[", fmt.Sprintf("](%s)", e.URL), 70
	case "text_mention":
		if e.User == nil || e.User.ID == 0 {
			base.valid = false
			return base
		}
		base.open, base.close, base.priority = "[", fmt.Sprintf("](tg://user?id=%d)", e.User.ID), 70
	case "spoiler":
		base.open, base.close, base.priority = "||", "||", 80
	case "blockquote":
		base.open, base.close, base.priority = "> ", "", 90
	case "custom_emoji":
		base.valid = false
	default:
		base.valid = false
	}
	return base
}

func markdownEntityFromMTProto(e tg.MessageEntityClass) markdownEntity {
	base := markdownEntity{offset: e.GetOffset(), length: e.GetLength(), valid: true}
	switch e := e.(type) {
	case *tg.MessageEntityBold:
		base.open, base.close, base.priority = "**", "**", 10
	case *tg.MessageEntityItalic:
		base.open, base.close, base.priority = "_", "_", 20
	case *tg.MessageEntityUnderline:
		base.open, base.close, base.priority = "__", "__", 30
	case *tg.MessageEntityStrike:
		base.open, base.close, base.priority = "~~", "~~", 40
	case *tg.MessageEntityCode:
		base.open, base.close, base.priority = "`", "`", 50
	case *tg.MessageEntityPre:
		base.open, base.close, base.priority = "```\n", "\n```", 60
	case *tg.MessageEntityTextURL:
		base.open, base.close, base.priority = "[", fmt.Sprintf("](%s)", e.URL), 70
	case *tg.MessageEntityMentionName:
		base.open, base.close, base.priority = "[", fmt.Sprintf("](tg://user?id=%d)", e.UserID), 70
	case *tg.MessageEntitySpoiler:
		base.open, base.close, base.priority = "||", "||", 80
	case *tg.MessageEntityBlockquote:
		base.open, base.close, base.priority = "> ", "", 90
	case *tg.MessageEntityCustomEmoji:
		base.valid = false
	default:
		base.valid = false
	}
	return base
}

func renderMarkdownEntities(text string, entities []markdownEntity) string {
	if len(entities) == 0 {
		return text
	}

	utf16units := utf16.Encode([]rune(text))
	limit := len(utf16units)
	starts := make(map[int][]markdownEntity)

	for _, entity := range entities {
		if entity.length <= 0 || entity.offset < 0 || entity.offset >= limit {
			continue
		}
		end := entity.offset + entity.length
		if end > limit {
			end = limit
		}
		if end <= entity.offset {
			continue
		}
		entity.length = end - entity.offset
		starts[entity.offset] = append(starts[entity.offset], entity)
	}

	for pos := range starts {
		sort.Slice(starts[pos], func(i, j int) bool {
			a, b := starts[pos][i], starts[pos][j]
			if a.length != b.length {
				return a.length > b.length
			}
			if a.priority != b.priority {
				return a.priority < b.priority
			}
			return a.index < b.index
		})
	}

	endEvents := make(map[int][]markdownEvent)
	startEvents := make(map[int][]markdownEvent)
	for pos, list := range starts {
		for _, entity := range list {
			startEvents[pos] = append(startEvents[pos], markdownEvent{
				pos:      pos,
				open:     true,
				marker:   entity.open,
				priority: entity.priority,
				length:   entity.length,
				index:    entity.index,
			})
			end := pos + entity.length
			endEvents[end] = append(endEvents[end], markdownEvent{
				pos:      end,
				open:     false,
				marker:   entity.close,
				priority: entity.priority,
				length:   entity.length,
				index:    entity.index,
			})
		}
	}

	for pos := range endEvents {
		sort.Slice(endEvents[pos], func(i, j int) bool {
			a, b := endEvents[pos][i], endEvents[pos][j]
			if a.length != b.length {
				return a.length < b.length
			}
			if a.priority != b.priority {
				return a.priority > b.priority
			}
			return a.index > b.index
		})
	}

	var sb strings.Builder
	for i := 0; i <= limit; i++ {
		if events := endEvents[i]; len(events) > 0 {
			for _, event := range events {
				sb.WriteString(event.marker)
			}
		}
		if events := startEvents[i]; len(events) > 0 {
			for _, event := range events {
				sb.WriteString(event.marker)
			}
		}
		if i < limit {
			if utf16.IsSurrogate(rune(utf16units[i])) && i+1 < limit {
				r := utf16.DecodeRune(rune(utf16units[i]), rune(utf16units[i+1]))
				sb.WriteRune(r)
				i++
			} else {
				sb.WriteRune(rune(utf16units[i]))
			}
		}
	}

	return sb.String()
}

// --- MAX Markups → TG HTML ---

// maxMarkupsToHTML конвертирует MAX text + markups в TG-совместимый HTML.
func maxMarkupsToHTML(text string, markups []maxschemes.MarkUp) string {
	if len(markups) == 0 {
		return html.EscapeString(text)
	}

	runes := []rune(text)
	utf16units := utf16.Encode(runes)

	type tag struct {
		pos   int
		open  bool
		order int
		tag   string
	}

	var tags []tag
	for _, m := range markups {
		var openTag, closeTag string
		switch m.Type {
		case maxschemes.MarkupStrong:
			openTag, closeTag = "<b>", "</b>"
		case maxschemes.MarkupEmphasized:
			openTag, closeTag = "<i>", "</i>"
		case maxschemes.MarkupMonospaced:
			openTag, closeTag = "<code>", "</code>"
		case maxschemes.MarkupStrikethrough:
			openTag, closeTag = "<s>", "</s>"
		case maxschemes.MarkupUnderline:
			openTag, closeTag = "<u>", "</u>"
		case maxschemes.MarkupLink:
			openTag = `<a href="` + html.EscapeString(m.URL) + `">`
			closeTag = "</a>"
		default:
			continue
		}
		tags = append(tags, tag{pos: m.From, open: true, order: 0, tag: openTag})
		tags = append(tags, tag{pos: m.From + m.Length, open: false, order: 1, tag: closeTag})
	}

	sort.Slice(tags, func(i, j int) bool {
		if tags[i].pos != tags[j].pos {
			return tags[i].pos < tags[j].pos
		}
		return tags[i].order > tags[j].order
	})

	var sb strings.Builder
	tagIdx := 0
	for i := 0; i <= len(utf16units); i++ {
		for tagIdx < len(tags) && tags[tagIdx].pos == i {
			sb.WriteString(tags[tagIdx].tag)
			tagIdx++
		}
		if i < len(utf16units) {
			if utf16.IsSurrogate(rune(utf16units[i])) && i+1 < len(utf16units) {
				r := utf16.DecodeRune(rune(utf16units[i]), rune(utf16units[i+1]))
				sb.WriteString(html.EscapeString(string(r)))
				i++
			} else {
				sb.WriteString(html.EscapeString(string(rune(utf16units[i]))))
			}
		}
	}
	return sb.String()
}
