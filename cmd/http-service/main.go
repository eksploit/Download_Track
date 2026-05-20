package main

import (
	"database/sql"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "github.com/lib/pq"

	"download_track/internal/adminnotify"
	"download_track/internal/delivery"
	"download_track/internal/downloader"
	"download_track/internal/httpserver"
)

func main() {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		log.Println("warning: DB_DSN is empty")
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatal("db open:", err)
	}
	if err := db.Ping(); err != nil {
		log.Println("warning: db ping error:", err)
	}

	jobLogPath := os.Getenv("JOB_LOG_PATH")
	if jobLogPath == "" {
		jobLogPath = "/logs/send.log"
	}
	f, err := os.OpenFile(jobLogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal("open job log:", jobLogPath, err)
	}
	defer f.Close()

	jobLogger := slog.New(slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo}))
	adminJobLogToken := os.Getenv("ADMIN_JOB_LOG_TOKEN")

	smtpHost := os.Getenv("SMTP_HOST")
	smtpPort := os.Getenv("SMTP_PORT")
	smtpUser := os.Getenv("SMTP_USER")
	smtpPass := os.Getenv("SMTP_PASS")
	fromAddr := os.Getenv("SMTP_FROM")

	if smtpHost == "" || smtpPort == "" || fromAddr == "" {
		log.Println("warning: SMTP settings are incomplete, email sending will likely fail")
	}

	telegramToken := os.Getenv("TELEGRAM_TOKEN")
	telegramAPIBase := os.Getenv("TELEGRAM_API_BASE")
	if telegramAPIBase == "" {
		telegramAPIBase = "http://telegram-bot-api:8081"
	}

	smtpCfg := delivery.SMTPConfig{
		Host:     smtpHost,
		Port:     smtpPort,
		User:     smtpUser,
		Pass:     smtpPass,
		FromAddr: fromAddr,
	}

	emailDelivery := delivery.NewEmailDelivery(db, jobLogger, smtpCfg)

	var telegramDelivery delivery.Delivery
	if telegramToken != "" {
		telegramDelivery = delivery.NewTelegramDelivery(telegramToken, telegramAPIBase, jobLogger)
	} else {
		log.Println("warning: TELEGRAM_TOKEN is empty, telegram delivery will be disabled")
	}

	var bothDelivery delivery.Delivery
	if emailDelivery != nil || telegramDelivery != nil {
		bothDelivery = &delivery.MultiDelivery{
			Email:    emailDelivery,
			Telegram: telegramDelivery,
		}
	}

	cookiesPath := os.Getenv("YTDLP_COOKIES_PATH")
	instagramMinIntervalSec := 0
	if s := os.Getenv("INSTAGRAM_MIN_INTERVAL_SECONDS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			instagramMinIntervalSec = n
		}
	}
	ytDlpSleepSec := 0
	if s := os.Getenv("YTDLP_SLEEP_INTERVAL_SECONDS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			ytDlpSleepSec = n
		}
	}
	fetcher := downloader.NewDefaultFetcher(
		20*time.Minute,
		cookiesPath,
		time.Duration(instagramMinIntervalSec)*time.Second,
		ytDlpSleepSec,
	)
	if cookiesPath != "" {
		log.Println("yt-dlp Instagram cookies: using file", cookiesPath)
	}
	adminChatID := os.Getenv("ADMIN_CHAT_ID")
	var notifier *adminnotify.Notifier
	if telegramToken != "" && adminChatID != "" {
		notifier = adminnotify.New(telegramToken, telegramAPIBase, adminChatID)
		if cookiesPath != "" {
			adminnotify.CheckCookiesFileAtStartup(cookiesPath, notifier)
			go adminnotify.RunCookieExpiryCheck(cookiesPath, notifier, 24*time.Hour)
		}
	}
	if instagramMinIntervalSec > 0 {
		log.Println("yt-dlp Instagram: min interval between starts", instagramMinIntervalSec, "s")
	}
	if ytDlpSleepSec > 0 {
		log.Println("yt-dlp Instagram: --sleep-interval", ytDlpSleepSec, "s")
	}
	var adminNotifier httpserver.AdminNotifier
	if notifier != nil {
		adminNotifier = notifier
	}
	srv := httpserver.New(db, jobLogger, fetcher, emailDelivery, telegramDelivery, bothDelivery, adminNotifier, cookiesPath, jobLogPath, adminJobLogToken)

	mux := http.NewServeMux()
	srv.Routes(mux)

	addr := ":8080"
	log.Println("http-service listening on", addr)
	s := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}

	log.Fatal(s.ListenAndServe())
}
