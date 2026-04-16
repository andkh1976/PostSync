package main

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/gotd/td/tg"
)

func TestTgEntitiesToMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		entities []tgbotapi.MessageEntity
		expected string
	}{
		{
			name:     "no entities",
			text:     "plain text",
			entities: nil,
			expected: "plain text",
		},
		{
			name: "bold",
			text: "hello world",
			entities: []tgbotapi.MessageEntity{
				{Type: "bold", Offset: 6, Length: 5},
			},
			expected: "hello **world**",
		},
		{
			name: "italic",
			text: "hello world",
			entities: []tgbotapi.MessageEntity{
				{Type: "italic", Offset: 0, Length: 5},
			},
			expected: "_hello_ world",
		},
		{
			name: "link",
			text: "click here",
			entities: []tgbotapi.MessageEntity{
				{Type: "text_link", Offset: 6, Length: 4, URL: "https://example.com"},
			},
			expected: "click [here](https://example.com)",
		},
		{
			name: "underline",
			text: "hello world",
			entities: []tgbotapi.MessageEntity{
				{Type: "underline", Offset: 6, Length: 5},
			},
			expected: "hello __world__",
		},
		{
			name: "spoiler",
			text: "secret text",
			entities: []tgbotapi.MessageEntity{
				{Type: "spoiler", Offset: 0, Length: 6},
			},
			expected: "||secret|| text",
		},
		{
			name: "blockquote safe degradation",
			text: "quote",
			entities: []tgbotapi.MessageEntity{
				{Type: "blockquote", Offset: 0, Length: 5},
			},
			expected: "> quote",
		},
		{
			name: "text mention",
			text: "hello Alice",
			entities: []tgbotapi.MessageEntity{
				{Type: "text_mention", Offset: 6, Length: 5, User: &tgbotapi.User{ID: 42}},
			},
			expected: "hello [Alice](tg://user?id=42)",
		},
		{
			name: "adjacent entities",
			text: "abcdef",
			entities: []tgbotapi.MessageEntity{
				{Type: "bold", Offset: 0, Length: 3},
				{Type: "italic", Offset: 3, Length: 3},
			},
			expected: "**abc**_def_",
		},
		{
			name: "nested entities",
			text: "abcdef",
			entities: []tgbotapi.MessageEntity{
				{Type: "bold", Offset: 0, Length: 6},
				{Type: "italic", Offset: 2, Length: 2},
			},
			expected: "**ab_cd_ef**",
		},
		{
			name: "unknown entity skipped",
			text: "hello @user",
			entities: []tgbotapi.MessageEntity{
				{Type: "mention", Offset: 6, Length: 5},
			},
			expected: "hello @user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tgEntitiesToMarkdown(tt.text, tt.entities)
			if got != tt.expected {
				t.Errorf("tgEntitiesToMarkdown() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestMtprotoEntitiesToMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		entities []tg.MessageEntityClass
		expected string
	}{
		{
			name:     "no entities",
			text:     "plain text",
			entities: nil,
			expected: "plain text",
		},
		{
			name: "bold",
			text: "hello world",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityBold{Offset: 6, Length: 5},
			},
			expected: "hello **world**",
		},
		{
			name: "italic",
			text: "hello world",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityItalic{Offset: 0, Length: 5},
			},
			expected: "_hello_ world",
		},
		{
			name: "code",
			text: "run ls -la",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityCode{Offset: 4, Length: 6},
			},
			expected: "run `ls -la`",
		},
		{
			name: "strikethrough",
			text: "old price",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityStrike{Offset: 4, Length: 5},
			},
			expected: "old ~~price~~",
		},
		{
			name: "text url",
			text: "click here",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityTextURL{Offset: 6, Length: 4, URL: "https://example.com"},
			},
			expected: "click [here](https://example.com)",
		},
		{
			name: "underline",
			text: "hello world",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityUnderline{Offset: 6, Length: 5},
			},
			expected: "hello __world__",
		},
		{
			name: "spoiler",
			text: "secret text",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntitySpoiler{Offset: 0, Length: 6},
			},
			expected: "||secret|| text",
		},
		{
			name: "blockquote safe degradation",
			text: "quote",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityBlockquote{Offset: 0, Length: 5},
			},
			expected: "> quote",
		},
		{
			name: "mention name",
			text: "hello Alice",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityMentionName{Offset: 6, Length: 5, UserID: 42},
			},
			expected: "hello [Alice](tg://user?id=42)",
		},
		{
			name: "unknown entity skipped",
			text: "hello @user",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityMention{Offset: 6, Length: 5},
			},
			expected: "hello @user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mtprotoEntitiesToMarkdown(tt.text, tt.entities)
			if got != tt.expected {
				t.Errorf("mtprotoEntitiesToMarkdown() = %q, want %q", got, tt.expected)
			}
		})
	}
}
