package bot

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"download_track/internal/urlutil"
)

func (b *Bot) handleMessage(m *tgbotapi.Message) {
	if m == nil {
		return
	}

	chatID := m.Chat.ID
	text := strings.TrimSpace(m.Text)

	if strings.HasPrefix(text, "/start") {
		registered, username, err := b.svc.IsTelegramRegistered(m.From.ID)
		if err != nil {
			log.Println("isTelegramRegistered err:", err)
			b.send(chatID, "Внутренняя ошибка, попробуй позже.")
			return
		}

		if !registered {
			b.send(chatID, "Привет! Отправь /register email@example.com для регистрации, потом просто кидай ссылки на файлы.")
		} else {
			b.send(chatID, "Привет! @"+username+". Просто кидай ссылки на файлы.")
		}
		return
	}

	if strings.HasPrefix(text, "/register") {
		parts := strings.Fields(text)
		if len(parts) != 2 {
			b.send(chatID, "Использование: /register email@example.com")
			return
		}
		email := parts[1]

		if err := b.svc.RegisterUser(m.From.ID, m.From.UserName, email); err != nil {
			if errors.Is(err, ErrAlreadyRegistered) {
				b.send(chatID, "Ты уже зарегистрирован. Просто пришли ссылку на файл.")
				return
			}
			log.Println("register err:", err)
			b.send(chatID, "Ошибка регистрации, попробуй позже.")
		} else {
			b.send(chatID, "Готово! Теперь просто пришли ссылку на файл.")
		}
		return
	}

	if strings.HasPrefix(text, "/help") {
		b.send(chatID, "Доступные команды:\n"+
			"/start - приветствие и проверка регистрации\n"+
			"/register email@example.com - регистрация\n"+
			"/change_email new_email@example.com - запрос на смену email\n"+
			"/send <ссылка> - отправить файл по ссылке на почту (можно просто прислать ссылку без команды)\n"+
			"/help - эта справка")
		if chatID == b.adminChatID {
			b.send(chatID, "Админские команды:\n"+
				"/approve_change <id> - подтвердить смену email\n"+
				"/reject_change <id> - отклонить смену email\n"+
				"/list_changes - показать все заявки")
		}

		return
	}

	if strings.HasPrefix(text, "/change_email") {
		parts := strings.Fields(text)
		if len(parts) != 2 {
			b.send(chatID, "Использование: /change_email new_email@example.com")
			return
		}
		newEmail := parts[1]

		_, adminMsg, err := b.svc.RequestEmailChange(m.From.ID, m.From.UserName, newEmail)
		if err != nil {
			log.Println("requestEmailChange err:", err)
			b.send(chatID, "Ошибка запроса на смену email, попробуй позже.")
		} else {
			b.send(b.adminChatID, adminMsg)
			b.send(chatID, "Запрос на смену email отправлен админу, ожидайте подтверждения.")
		}
		return
	}

	if strings.HasPrefix(text, "/approve_change") {
		parts := strings.Fields(text)
		if len(parts) != 2 {
			b.send(chatID, "Использование: /approve_change <request_id>")
			return
		}
		if chatID != b.adminChatID {
			return
		}
		telegramID, newEmail, err := b.svc.ApproveEmailChange(parts[1])
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				b.send(chatID, "Заявка не найдена.")
			} else {
				log.Println("approveEmailChange err:", err)
				b.send(chatID, "Ошибка подтверждения заявки: "+err.Error())
			}
		} else {
			b.send(telegramID, fmt.Sprintf("Админ сменил твой email на %s.", newEmail))
			b.send(chatID, fmt.Sprintf("Заявка #%s подтверждена, email пользователя обновлён на %s.", parts[1], newEmail))
		}
		return
	}

	if strings.HasPrefix(text, "/reject_change") {
		parts := strings.Fields(text)
		if len(parts) != 2 {
			b.send(chatID, "Использование: /reject_change <request_id>")
			return
		}
		if chatID != b.adminChatID {
			return
		}
		telegramID, newEmail, err := b.svc.RejectEmailChange(parts[1])
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				b.send(chatID, "Заявка не найдена.")
			} else {
				log.Println("rejectEmailChange err:", err)
				b.send(chatID, "Ошибка отклонения заявки: "+err.Error())
			}
		} else {
			b.send(telegramID, fmt.Sprintf("Админ отклонил смену email на %s.", newEmail))
			b.send(chatID, fmt.Sprintf("Заявка #%s отклонена.", parts[1]))
		}
		return
	}

	if strings.HasPrefix(text, "/list_changes") {
		if err := b.listEmailChanges(chatID); err != nil {
			log.Println("listEmailChanges err:", err)
			b.send(chatID, "Ошибка получения списка заявок: "+err.Error())
		}
		return
	}

	url := extractFirstURL(m)
	if url == "" {
		b.send(chatID, "Не нашёл ссылку в сообщении.")
		return
	}

	// проверка регистрации (как раньше)
	registered, _, err := b.svc.IsTelegramRegistered(m.From.ID)
	if err != nil {
		log.Println("isTelegramRegistered err:", err)
		b.send(chatID, "Внутренняя ошибка, попробуй позже.")
		return
	}
	if !registered {
		b.send(chatID, "Ты ещё не зарегистрирован. Сначала сделай /register email@example.com")
		return
	}

	// Ветка «видео в чат»: YouTube/Instagram — без клавиатуры, сразу скачивание и доставка в чат
	if urlutil.IsVideoPlatformURL(url) {
		const msgStarted = "Скачивание началось, видео придёт в этот чат, как только будет готово."
		b.send(chatID, msgStarted)
		apiKey, err := b.svc.GetAPIKeyForTelegram(m.From.ID)
		if err != nil {
			log.Println("get api key err:", err)
			b.send(chatID, "Ошибка загрузки видео.")
			return
		}
		if err := b.svc.CallSend(apiKey, url, "telegram"); err != nil {
			log.Println("CallSend video err:", err)
			b.send(chatID, "Не удалось загрузить видео. Instagram и YouTube могут ограничивать частоту запросов — подождите 5–10 минут и попробуйте снова или отправьте другую ссылку.")
			return
		}
		return
	}

	// сохраняем ссылку для выбора режима
	b.pendingLinks[m.From.ID] = url

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("На email", "delivery_email"),
			tgbotapi.NewInlineKeyboardButtonData("В этот чат", "delivery_telegram"),
			tgbotapi.NewInlineKeyboardButtonData("И туда, и туда", "delivery_both"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, "Куда отправить файл?")
	msg.ReplyMarkup = keyboard

	if _, err := b.api.Send(msg); err != nil {
		log.Println("send inline keyboard err:", err)
		b.send(chatID, "Ошибка отправки клавиатуры, попробуй позже.")
		return
	}
}

func (b *Bot) handleCallbackQuery(cq *tgbotapi.CallbackQuery) {
	if cq == nil {
		return
	}

	data := cq.Data
	chatID := cq.Message.Chat.ID
	telegramID := cq.From.ID

	url, ok := b.pendingLinks[telegramID]
	if !ok || url == "" {
		// нет сохранённой ссылки
		b.send(chatID, "Нет ожидающей ссылки для обработки. Отправь новую ссылку.")
		return
	}

	mode := "email"
	switch data {
	case "delivery_email":
		mode = "email"
	case "delivery_telegram":
		mode = "telegram"
	case "delivery_both":
		mode = "both"
	default:
		b.send(chatID, "Неизвестный вариант доставки.")
		return
	}

	// предупреждение если email на Gmail и файл заблокированного типа
	if mode == "email" || mode == "both" {
		blocked, err := b.svc.IsGmailBlockedDelivery(telegramID, url)
		if err == nil && blocked {
			ext := b.svc.GmailBlockedExt(url)
			b.send(chatID, fmt.Sprintf(
				"⚠️ Gmail блокирует файлы %s во вложениях.\nРекомендуем выбрать «В этот чат».",
				ext,
			))
			delete(b.pendingLinks, telegramID)
			return
		}
	}

	apiKey, err := b.svc.GetAPIKeyForTelegram(telegramID)
	if err != nil {
		log.Println("get api key err:", err)
		b.send(chatID, "Ты ещё не зарегистрирован. Сначала сделай /register email@example.com")
		return
	}

	switch mode {
	case "email":
		b.send(chatID, "Файл будет отправлен на твой email.")
	case "telegram":
		b.send(chatID, "Файл будет отправлен в этот чат.")
	case "both":
		b.send(chatID, "Файл будет отправлен и на email, и в этот чат.")
	}

	if err := b.svc.CallSend(apiKey, url, mode); err != nil {
		log.Println("process url err:", err)
		b.send(chatID, "Ошибка обработки ссылки: "+err.Error())
	}

	// очищаем pendingLinks
	delete(b.pendingLinks, telegramID)

	// ответ на callback, чтобы «часики» исчезли
	answer := tgbotapi.NewCallback(cq.ID, "")
	if _, err := b.api.Request(answer); err != nil {
		log.Println("callback answer err:", err)
	}
}

func (b *Bot) listEmailChanges(chatID int64) error {
	if chatID != b.adminChatID {
		return nil
	}

	rows, err := b.svc.ListPendingEmailChanges()
	if err != nil {
		return err
	}

	var sb strings.Builder
	sb.WriteString("Заявки на смену email:\nДля подтверждения: /approve_change x\nДля отказа: /reject_change x\n")

	for _, row := range rows {
		sb.WriteString(fmt.Sprintf(
			"#%d user_id=%d [%s]\n%s -> %s\n\n",
			row.ID, row.UserID, row.Status, row.OldEmail, row.NewEmail,
		))
	}

	if len(rows) == 0 {
		b.send(chatID, "Заявок на смену email пока нет.")
		return nil
	}

	b.send(chatID, sb.String())
	return nil
}

func (b *Bot) send(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := b.api.Send(msg); err != nil {
		log.Println("send msg err:", err)
	}
}

func extractFirstURL(m *tgbotapi.Message) string {
	if m == nil {
		return ""
	}

	runes := []rune(m.Text)

	for _, e := range m.Entities {
		if e.IsURL() {
			start := e.Offset
			end := e.Offset + e.Length
			if start < 0 || end > len(runes) {
				continue
			}
			return string(runes[start:end])
		}

		if e.IsTextLink() {
			u, err := e.ParseURL()
			if err != nil {
				continue
			}
			return u.String()
		}
	}

	return ""
}
