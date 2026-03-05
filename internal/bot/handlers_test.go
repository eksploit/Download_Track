package bot

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestExtractFirstURL(t *testing.T) {
	tests := []struct {
		name    string
		message *tgbotapi.Message
		want    string
	}{
		{
			name:    "nil",
			message: nil,
			want:    "",
		},
		{
			name: "empty",
			message: &tgbotapi.Message{
				Text:     "",
				Entities: nil,
			},
			want: "",
		},
		{
			name: "plain text no entities",
			message: &tgbotapi.Message{
				Text:     "просто текст без ссылки",
				Entities: []tgbotapi.MessageEntity{},
			},
			want: "",
		},
		{
			name: "single url entity",
			message: &tgbotapi.Message{
				Text: "See https://example.com here",
				Entities: []tgbotapi.MessageEntity{
					{Type: "url", Offset: 4, Length: 19},
				},
			},
			want: "https://example.com",
		},
		{
			name: "text_link entity",
			message: &tgbotapi.Message{
				Text: "Click here",
				Entities: []tgbotapi.MessageEntity{
					{Type: "text_link", Offset: 0, Length: 10, URL: "https://link.com"},
				},
			},
			want: "https://link.com",
		},
		{
			name: "first of two",
			message: &tgbotapi.Message{
				Text: "First https://one.com and https://two.com",
				Entities: []tgbotapi.MessageEntity{
					{Type: "url", Offset: 6, Length: 15},
					{Type: "url", Offset: 26, Length: 15},
				},
			},
			want: "https://one.com",
		},
		{
			name: "invalid offset",
			message: &tgbotapi.Message{
				Text: "Short",
				Entities: []tgbotapi.MessageEntity{
					{Type: "url", Offset: 10, Length: 5},
				},
			},
			want: "",
		},
		{
			name: "invalid negative offset",
			message: &tgbotapi.Message{
				Text: "See https://example.com",
				Entities: []tgbotapi.MessageEntity{
					{Type: "url", Offset: -1, Length: 22},
				},
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFirstURL(tt.message)
			if got != tt.want {
				t.Errorf("extractFirstURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
