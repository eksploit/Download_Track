package main

import (
	"database/sql"
	"log"
	"os"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	_ "github.com/lib/pq"

	"download_track/internal/bot"
)

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

	adminChatIDStr := os.Getenv("ADMIN_CHAT_ID")
	if adminChatIDStr == "" {
		log.Fatal("ADMIN_CHAT_ID is empty")
	}
	adminChatID, err := strconv.ParseInt(adminChatIDStr, 10, 64)
	if err != nil {
		log.Fatal("invalid ADMIN_CHAT_ID:", err)
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

	// Меню команд - для пользователей
	userCommands := []tgbotapi.BotCommand{
		{Command: "start", Description: "Приветствие и проверка регистрации"},
		{Command: "register", Description: "Регистрация email: /register email@example.com"},
		{Command: "change_email", Description: "Запрос на смену email: /change_email new_email@example.com"},
		{Command: "send", Description: "Отправить файл по ссылке на почту"},
		{Command: "help", Description: "Список доступных команд"},
	}

	if _, err = botAPI.Request(tgbotapi.NewSetMyCommands(userCommands...)); err != nil {
		log.Println("set user commands err:", err)
	}

	// Меню для админа
	adminCommands := []tgbotapi.BotCommand{
		{Command: "start", Description: "Приветствие и проверка регистрации"},
		{Command: "register", Description: "Регистрация email"},
		{Command: "change_email", Description: "Запрос на смену email"},
		{Command: "send", Description: "Отправить файл по ссылке"},
		{Command: "help", Description: "Список доступных команд"},
		{Command: "approve_change", Description: "Подтвердить смену email: /approve_change <id>"},
		{Command: "reject_change", Description: "Отклонить смену email: /reject_change <id>"},
		{Command: "list_changes", Description: "Показать все заявки на смену email"},
		{Command: "cookie", Description: "Показать, сколько дней до истечения cookies Instagram"},
	}

	scope := tgbotapi.NewBotCommandScopeChat(adminChatID)
	cfg := tgbotapi.NewSetMyCommandsWithScope(scope, adminCommands...)

	if _, err = botAPI.Request(cfg); err != nil {
		log.Println("set admin commands err:", err)
	}

	b := bot.New(botAPI, db, apiBase, adminChatID)
	b.Run()
}
