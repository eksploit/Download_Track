// Пакет bot реализует Telegram-бота: жизненный цикл, диспетчеризацию обновлений и публичный API (New, Run, ErrAlreadyRegistered).
package bot

import (
	"database/sql"
	"errors"
	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var ErrAlreadyRegistered = errors.New("already registered")

type Bot struct {
	api          *tgbotapi.BotAPI
	svc          *service
	adminChatID  int64
	pendingLinks map[int64]string // telegramID -> последняя ссылка, ожидающая выбора режима
}

func New(api *tgbotapi.BotAPI, db *sql.DB, apiBase string, adminChatID int64) *Bot {
	repo := newRepo(db)
	return &Bot{
		api:          api,
		svc:          newService(repo, apiBase),
		adminChatID:  adminChatID,
		pendingLinks: make(map[int64]string),
	}
}

// Run — цикл чтения updates и делегирование в handleMessage / handleCallbackQuery.
func (b *Bot) Run() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)

	log.Println("bot started")

	for update := range updates {
		if update.CallbackQuery != nil {
			b.handleCallbackQuery(update.CallbackQuery)
			continue
		}

		if update.Message != nil {
			b.handleMessage(update.Message)
			continue
		}
	}
}
