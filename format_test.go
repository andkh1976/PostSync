package main

import (
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	maxschemes "github.com/max-messenger/max-bot-api-client-go/schemes"
)

func TestTgName(t *testing.T) {
	tests := []struct {
		name     string
		msg      *tgbotapi.Message
		expected string
	}{
		{
			name: "first name only",
			msg: &tgbotapi.Message{
				From: &tgbotapi.User{FirstName: "Ivan"},
			},
			expected: "Ivan",
		},
		{
			name: "first and last name",
			msg: &tgbotapi.Message{
				From: &tgbotapi.User{FirstName: "Ivan", LastName: "Petrov"},
			},
			expected: "Ivan Petrov",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tgName(tt.msg)
			if got != tt.expected {
				t.Errorf("tgName() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestFormatTgCaption(t *testing.T) {
	msg := &tgbotapi.Message{
		Text: "hello world",
		From: &tgbotapi.User{FirstName: "Anna"},
	}

	tests := []struct {
		name     string
		prefix   bool
		expected string
	}{
		{"with prefix", true, "[TG] Anna: hello world"},
		{"without prefix", false, "Anna: hello world"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTgCaption(msg, tt.prefix, false)
			if got != tt.expected {
				t.Errorf("formatTgCaption() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestFormatTgCaption_UsesCaption(t *testing.T) {
	msg := &tgbotapi.Message{
		Text:    "",
		Caption: "photo caption",
		From:    &tgbotapi.User{FirstName: "Bob"},
	}

	got := formatTgCaption(msg, false, false)
	expected := "Bob: photo caption"
	if got != expected {
		t.Errorf("formatTgCaption() = %q, want %q", got, expected)
	}
}

func TestFormatTgMessage(t *testing.T) {
	tests := []struct {
		name     string
		msg      *tgbotapi.Message
		prefix   bool
		expected string
	}{
		{
			name: "text with prefix",
			msg: &tgbotapi.Message{
				Text: "edited text",
				From: &tgbotapi.User{FirstName: "Ivan"},
			},
			prefix:   true,
			expected: "[TG] Ivan: edited text",
		},
		{
			name: "text without prefix",
			msg: &tgbotapi.Message{
				Text: "edited text",
				From: &tgbotapi.User{FirstName: "Ivan"},
			},
			prefix:   false,
			expected: "Ivan: edited text",
		},
		{
			name: "empty text returns empty",
			msg: &tgbotapi.Message{
				Text: "",
				From: &tgbotapi.User{FirstName: "Ivan"},
			},
			prefix:   true,
			expected: "",
		},
		{
			name: "caption fallback",
			msg: &tgbotapi.Message{
				Text:    "",
				Caption: "cap",
				From:    &tgbotapi.User{FirstName: "Ivan"},
			},
			prefix:   false,
			expected: "Ivan: cap",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTgMessage(tt.msg, tt.prefix, false)
			if got != tt.expected {
				t.Errorf("formatTgMessage() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestMaxName(t *testing.T) {
	tests := []struct {
		name     string
		upd      *maxschemes.MessageCreatedUpdate
		expected string
	}{
		{
			name: "has name",
			upd: &maxschemes.MessageCreatedUpdate{
				Message: maxschemes.Message{
					Sender: maxschemes.User{Name: "Алексей"},
				},
			},
			expected: "Алексей",
		},
		{
			name: "fallback to username",
			upd: &maxschemes.MessageCreatedUpdate{
				Message: maxschemes.Message{
					Sender: maxschemes.User{Name: "", Username: "alex42"},
				},
			},
			expected: "alex42",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maxName(tt.upd)
			if got != tt.expected {
				t.Errorf("maxName() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestFormatMaxCaption(t *testing.T) {
	upd := &maxschemes.MessageCreatedUpdate{
		Message: maxschemes.Message{
			Sender: maxschemes.User{Name: "Вася"},
			Body:   maxschemes.MessageBody{Text: "привет"},
		},
	}

	tests := []struct {
		name     string
		prefix   bool
		expected string
	}{
		{"with prefix", true, "[MAX] Вася: привет"},
		{"without prefix", false, "Вася: привет"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatMaxCaption(upd, tt.prefix, false)
			if got != tt.expected {
				t.Errorf("formatMaxCaption() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestFormatTgCrosspostCaption(t *testing.T) {
	tests := []struct {
		name     string
		msg      *tgbotapi.Message
		expected string
	}{
		{
			name:     "text",
			msg:      &tgbotapi.Message{Text: "Новый пост"},
			expected: "Новый пост",
		},
		{
			name:     "caption fallback",
			msg:      &tgbotapi.Message{Text: "", Caption: "фото"},
			expected: "фото",
		},
		{
			name:     "empty",
			msg:      &tgbotapi.Message{Text: ""},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTgCrosspostCaption(tt.msg)
			if got != tt.expected {
				t.Errorf("formatTgCrosspostCaption() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// TestFormatTgDateLabel verifies the [TG] DD.MM.YYYY format with UTC semantics.
func TestFormatTgDateLabel(t *testing.T) {
	// Unix 0 = 1970-01-01 00:00:00 UTC → 01.01.1970
	got := formatTgDateLabel(0)
	expected := "[TG] 01.01.1970"
	if got != expected {
		t.Errorf("formatTgDateLabel(0) = %q, want %q", got, expected)
	}

	// 2025-04-03 00:00:00 UTC = 1743638400
	got2 := formatTgDateLabel(1743638400)
	expected2 := "[TG] 03.04.2025"
	if got2 != expected2 {
		t.Errorf("formatTgDateLabel(1743638400) = %q, want %q", got2, expected2)
	}
}

// TestFormatTgCaptionWithDate checks label appending in formatTgCaption.
func TestFormatTgCaptionWithDate(t *testing.T) {
	const date = 1743638400 // some fixed Unix timestamp

	t.Run("text message appends label", func(t *testing.T) {
		msg := &tgbotapi.Message{
			Text: "hello",
			From: &tgbotapi.User{FirstName: "Ann"},
			Date: date,
		}
		got := formatTgCaption(msg, false, false)
		label := formatTgDateLabel(date)
		expected := "Ann: hello\n\n" + label
		if got != expected {
			t.Errorf("got %q, want %q", got, expected)
		}
	})

	t.Run("media with caption appends label", func(t *testing.T) {
		msg := &tgbotapi.Message{
			Caption: "photo text",
			Photo:   []tgbotapi.PhotoSize{{FileID: "x"}},
			From:    &tgbotapi.User{FirstName: "Ann"},
			Date:    date,
		}
		got := formatTgCaption(msg, false, false)
		label := formatTgDateLabel(date)
		expected := "Ann: photo text\n\n" + label
		if got != expected {
			t.Errorf("got %q, want %q", got, expected)
		}
	})

	t.Run("media without caption: label becomes standalone", func(t *testing.T) {
		msg := &tgbotapi.Message{
			Photo: []tgbotapi.PhotoSize{{FileID: "x"}},
			From:  &tgbotapi.User{FirstName: "Ann"},
			Date:  date,
		}
		got := formatTgCaption(msg, false, false)
		label := formatTgDateLabel(date)
		if got != label {
			t.Errorf("got %q, want %q (standalone label)", got, label)
		}
	})

	t.Run("empty non-media message: no label appended", func(t *testing.T) {
		msg := &tgbotapi.Message{
			From: &tgbotapi.User{FirstName: "Ann"},
			Date: date,
		}
		got := formatTgCaption(msg, false, false)
		label := formatTgDateLabel(date)
		// The attribution prefix is present but the date label must NOT be appended.
		if strings.Contains(got, label) {
			t.Errorf("got %q, label %q must not be present for empty non-media message", got, label)
		}
	})
}

// TestFormatTgCrosspostCaptionWithDate checks label appending in crosspost path.
func TestFormatTgCrosspostCaptionWithDate(t *testing.T) {
	const date = 1743638400

	t.Run("text appends label", func(t *testing.T) {
		msg := &tgbotapi.Message{Text: "post", Date: date}
		got := formatTgCrosspostCaption(msg)
		label := formatTgDateLabel(date)
		expected := "post\n\n" + label
		if got != expected {
			t.Errorf("got %q, want %q", got, expected)
		}
	})

	t.Run("media without caption: standalone label", func(t *testing.T) {
		msg := &tgbotapi.Message{
			Photo: []tgbotapi.PhotoSize{{FileID: "x"}},
			Date:  date,
		}
		got := formatTgCrosspostCaption(msg)
		label := formatTgDateLabel(date)
		if got != label {
			t.Errorf("got %q, want %q (standalone label)", got, label)
		}
	})

	t.Run("empty non-media: no label", func(t *testing.T) {
		msg := &tgbotapi.Message{Date: date}
		got := formatTgCrosspostCaption(msg)
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})
}

func TestFormatMaxCrosspostCaption(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected string
	}{
		{"with text", "Новость дня", "Новость дня"},
		{"empty text", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upd := &maxschemes.MessageCreatedUpdate{
				Message: maxschemes.Message{
					Body: maxschemes.MessageBody{Text: tt.text},
				},
			}
			got := formatMaxCrosspostCaption(upd)
			if got != tt.expected {
				t.Errorf("formatMaxCrosspostCaption() = %q, want %q", got, tt.expected)
			}
		})
	}
}
