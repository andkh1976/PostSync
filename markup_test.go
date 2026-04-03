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
