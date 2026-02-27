package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	_ "github.com/lib/pq"
)

type Bot struct {
	api     *tgbotapi.BotAPI
	db      *sql.DB
	apiBase string
}

var urlRe = regexp.MustCompile(`https?://\S+`)

type sendReq struct {
	APIKey  string `json:"api_key"`
	FileURL string `json:"file_url"`
}

func main() {
	token := os.Getenv("TELEGRAM_TOKEN")
	if token == "" {
		log.Fatal("TELEGRAM_TOKEN is empty")
	}

	apiBase := os.Getenv("API_BASE")
	if apiBase == "" {
		apiBase = "http://http-service:8080"
	}

	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		log.Fatal("DB_DSN is empty")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatal("db open:", err)
	}
	if err := db.Ping(); err != nil {
		log.Fatal("db ping:", err)
	}

	botAPI, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatal("NewBotAPI:", err)
	}

	b := &Bot{
		api:     botAPI,
		db:      db,
		apiBase: apiBase,
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)

	log.Println("bot started")

	for update := range updates {
		if update.Message == nil {
			continue
		}
		b.handleMessage(update.Message)
	}
}

func (b *Bot) handleMessage(m *tgbotapi.Message) {
	chatID := m.Chat.ID
	text := strings.TrimSpace(m.Text)

	if strings.HasPrefix(text, "/start") {
		registered, username, err := b.isTelegramRegistered(m.From.ID)
		if err != nil {
			log.Println("isTelegramRegistered err:", err)
			b.send(chatID, "Внутренняя ошибка, попробуй позже.")
			return
		}

		if !registered {
			// текст для НЕ зарегистрированного — как был
			b.send(chatID, "Привет! Отправь /register email@example.com для регистрации, потом просто кидай ссылки на файлы.")
		} else {
			// текст для уже зарегистрированного
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
		if err := b.registerTelegramUser(m.From.ID, m.From.UserName, email); err != nil {
			log.Println("register err:", err)
			b.send(chatID, "Ошибка регистрации, попробуй позже.")
		} else {
			b.send(chatID, "Готово! Теперь просто пришли ссылку на файл.")
		}
		return
	}

	// Пытаемся найти ссылку в обычном сообщении
	url := urlRe.FindString(text)
	if url == "" {
		b.send(chatID, "Не нашёл ссылку в сообщении.")
		return
	}

	apiKey, err := b.getAPIKeyForTelegram(m.From.ID)
	if err != nil {
		log.Println("get api key err:", err)
		b.send(chatID, "Ты ещё не зарегистрирован. Сначала сделай /register email@example.com")
		return
	}

	if err := b.callSend(apiKey, url); err != nil {
		log.Println("process url err:", err)
		b.send(chatID, "Ошибка обработки ссылки: "+err.Error())
	} else {
		b.send(chatID, "Ссылка отправлена в HTTP-сервис, он обработает файл и отправит на твою почту.")
	}
}

func (b *Bot) send(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := b.api.Send(msg); err != nil {
		log.Println("send msg err:", err)
	}
}

// регистрация: создаём пользователя и привязку к telegram_id
func (b *Bot) registerTelegramUser(telegramID int64, username, email string) error {
	// уже зарегистрирован?
	var exists bool
	err := b.db.QueryRow("SELECT EXISTS(SELECT 1 FROM telegram_users WHERE telegram_id=$1)", telegramID).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		// уже есть привязка, ничего не делаем
		return nil
	}

	apiKey, err := generateAPIKey()
	if err != nil {
		return err
	}

	// создаём пользователя
	var userID int
	err = b.db.QueryRow(
		"INSERT INTO users (email, api_key) VALUES ($1,$2) RETURNING id",
		email, apiKey,
	).Scan(&userID)
	if err != nil {
		return err
	}

	// привязка telegram_id -> user_id
	_, err = b.db.Exec(
		"INSERT INTO telegram_users (telegram_id, username, user_id) VALUES ($1,$2,$3)",
		telegramID, username, userID,
	)
	return err
}

// получить api_key по telegram_id
func (b *Bot) getAPIKeyForTelegram(telegramID int64) (string, error) {
	var userID int
	err := b.db.QueryRow("SELECT user_id FROM telegram_users WHERE telegram_id=$1", telegramID).Scan(&userID)
	if err != nil {
		return "", err
	}

	var apiKey string
	err = b.db.QueryRow("SELECT api_key FROM users WHERE id=$1", userID).Scan(&apiKey)
	if err != nil {
		return "", err
	}
	return apiKey, nil
}

func (b *Bot) callSend(apiKey, fileURL string) error {
	body, _ := json.Marshal(sendReq{
		APIKey:  apiKey,
		FileURL: fileURL,
	})

	resp, err := http.Post(b.apiBase+"/send", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("send http status " + resp.Status)
	}
	return nil
}

// проверить, зарегистрирован ли telegram-пользователь
func (b *Bot) isTelegramRegistered(telegramID int64) (bool, string, error) {
	var exists bool
	var username string

	err := b.db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM telegram_users WHERE telegram_id=$1)",
		telegramID,
	).Scan(&exists)
	if err != nil {
		return false, "", err
	}
	if !exists {
		return false, "", nil
	}

	// берём username пользователя, чтобы персонализировать приветствие (опционально)
	err = b.db.QueryRow(
		"SELECT t.username FROM telegram_users t WHERE t.telegram_id=$1",
		telegramID,
	).Scan(&username)
	if err != nil {
		return true, "", err
	}

	return true, username, nil
}

func generateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
