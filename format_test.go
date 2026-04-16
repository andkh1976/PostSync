package main

import (
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	maxschemes "github.com/max-messenger/max-bot-api-client-go/schemes"
)

func TestPrepareTgTextForMax(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		entities []tgbotapi.MessageEntity
		wantText string
		wantFmt  string
	}{
		{
			name:     "plain text keeps empty format",
			text:     "plain text",
			entities: nil,
			wantText: "plain text",
			wantFmt:  "",
		},
		{
			name: "bold enables markdown",
			text: "hello world",
			entities: []tgbotapi.MessageEntity{
				{Type: "bold", Offset: 6, Length: 5},
			},
			wantText: "hello **world**",
			wantFmt:  "markdown",
		},
		{
			name: "link enables markdown",
			text: "click here",
			entities: []tgbotapi.MessageEntity{
				{Type: "text_link", Offset: 6, Length: 4, URL: "https://example.com"},
			},
			wantText: "click [here](https://example.com)",
			wantFmt:  "markdown",
		},
		{
			name: "unsupported entity keeps plain text formatting decision stable",
			text: "hello @user",
			entities: []tgbotapi.MessageEntity{
				{Type: "mention", Offset: 6, Length: 5},
			},
			wantText: "hello @user",
			wantFmt:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := prepareTgTextForMax(tt.text, tt.entities)
			if got.Text != tt.wantText {
				t.Fatalf("prepareTgTextForMax().Text = %q, want %q", got.Text, tt.wantText)
			}
			if got.Format != tt.wantFmt {
				t.Fatalf("prepareTgTextForMax().Format = %q, want %q", got.Format, tt.wantFmt)
			}
		})
	}
}

func TestPrepareTgMessageTextForMax(t *testing.T) {
	t.Run("uses caption fallback consistently", func(t *testing.T) {
		msg := &tgbotapi.Message{
			Caption: "caption text",
			CaptionEntities: []tgbotapi.MessageEntity{
				{Type: "code", Offset: 8, Length: 4},
			},
		}

		got := prepareTgMessageTextForMax(msg)
		if got.Text != "caption `text`" {
			t.Fatalf("prepareTgMessageTextForMax().Text = %q, want %q", got.Text, "caption `text`")
		}
		if got.Format != "markdown" {
			t.Fatalf("prepareTgMessageTextForMax().Format = %q, want %q", got.Format, "markdown")
		}
	})
}

func TestBuildTgCaptionForMax(t *testing.T) {
	t.Run("plain text keeps plain format", func(t *testing.T) {
		msg := &tgbotapi.Message{
			Text: "hello",
			From: &tgbotapi.User{FirstName: "Anna"},
		}
		got := buildTgCaptionForMax(msg, true, false)
		if got.Text != "[TG] Anna: hello" {
			t.Fatalf("Text = %q, want %q", got.Text, "[TG] Anna: hello")
		}
		if got.Format != "" {
			t.Fatalf("Format = %q, want empty", got.Format)
		}
	})

	t.Run("formatted caption enables markdown", func(t *testing.T) {
		msg := &tgbotapi.Message{
			Caption:         "hello world",
			CaptionEntities: []tgbotapi.MessageEntity{{Type: "bold", Offset: 6, Length: 5}},
			From:            &tgbotapi.User{FirstName: "Anna"},
		}
		got := buildTgCaptionForMax(msg, false, false)
		if got.Text != "Anna: hello **world**" {
			t.Fatalf("Text = %q, want %q", got.Text, "Anna: hello **world**")
		}
		if got.Format != "markdown" {
			t.Fatalf("Format = %q, want markdown", got.Format)
		}
	})

	t.Run("media without caption uses date label only", func(t *testing.T) {
		msg := &tgbotapi.Message{
			Photo: []tgbotapi.PhotoSize{{FileID: "x"}},
			Date:  1743638400,
			From:  &tgbotapi.User{FirstName: "Anna"},
		}
		got := buildTgCaptionForMax(msg, false, false)
		if got.Text != "[TG] 03.04.2025" {
			t.Fatalf("Text = %q, want %q", got.Text, "[TG] 03.04.2025")
		}
	})
}

func TestBuildTgCrosspostCaptionForMax(t *testing.T) {
	t.Run("crosspost with formatting and date label", func(t *testing.T) {
		msg := &tgbotapi.Message{
			Text:     "click here",
			Entities: []tgbotapi.MessageEntity{{Type: "text_link", Offset: 6, Length: 4, URL: "https://example.com"}},
			Date:     1743638400,
		}
		got := buildTgCrosspostCaptionForMax(msg)
		want := "click [here](https://example.com)\n\n[TG] 03.04.2025"
		if got.Text != want {
			t.Fatalf("Text = %q, want %q", got.Text, want)
		}
		if got.Format != "markdown" {
			t.Fatalf("Format = %q, want markdown", got.Format)
		}
	})
}

func TestFormatTgMessage_UsesFormattingForEdits(t *testing.T) {
	msg := &tgbotapi.Message{
		Text:     "edited text",
		Entities: []tgbotapi.MessageEntity{{Type: "code", Offset: 7, Length: 4}},
		From:     &tgbotapi.User{FirstName: "Ivan"},
	}
	got := formatTgMessage(msg, false, false)
	if got != "Ivan: edited `text`" {
		t.Fatalf("formatTgMessage() = %q, want %q", got, "Ivan: edited `text`")
	}
}

func TestSplitText_LongFormattedTextPreservesContent(t *testing.T) {
	text := strings.Repeat("**abc** ", 700)
	parts := splitText(text, 4000)
	joined := strings.Join(parts, "")
	joined = strings.ReplaceAll(joined, "\n", "")
	joined = strings.ReplaceAll(joined, " ", "")
	original := strings.ReplaceAll(text, " ", "")
	if joined != original {
		t.Fatalf("split/join changed content")
	}
}

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

func TestSplitText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		maxLen   int
		expected []string
	}{
		{
			name:     "no split needed",
			text:     "hello world",
			maxLen:   20,
			expected: []string{"hello world"},
		},
		{
			name:     "split by space",
			text:     "hello world how are you",
			maxLen:   10,
			expected: []string{"hello", "world how", "are you"},
		},
		{
			name:     "split by newline",
			text:     "hello\nworld\nhow\nare\nyou",
			maxLen:   15,
			expected: []string{"hello\nworld\nhow", "are\nyou"},
		},
		{
			name:     "exact split without spaces",
			text:     "1234567890abcdefghij",
			maxLen:   10,
			expected: []string{"1234567890", "abcdefghij"},
		},
		{
			name:     "multibyte characters",
			text:     "привет мир как дела",
			maxLen:   10,
			expected: []string{"привет мир", "как дела"},
		},
		{
			name:     "empty string",
			text:     "",
			maxLen:   10,
			expected: []string{""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitText(tt.text, tt.maxLen)
			if len(got) != len(tt.expected) {
				t.Fatalf("splitText() returned %d chunks, want %d", len(got), len(tt.expected))
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("chunk [%d] = %q, want %q", i, got[i], tt.expected[i])
				}
			}
		})
	}
}
