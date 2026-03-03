package main

import (
    "database/sql"
    "log"
    "net/http"
    "os"
    "time"

    _ "github.com/lib/pq"

	"download_track/internal/delivery"
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

    f, err := os.OpenFile("/logs/send.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil {
        log.Fatal("open send.log:", err)
    }
    defer f.Close()

    jobLogger := log.New(f, "", log.LstdFlags)

    smtpHost := os.Getenv("SMTP_HOST")
    smtpPort := os.Getenv("SMTP_PORT")
    smtpUser := os.Getenv("SMTP_USER")
    smtpPass := os.Getenv("SMTP_PASS")
    fromAddr := os.Getenv("SMTP_FROM")

    if smtpHost == "" || smtpPort == "" || fromAddr == "" {
        log.Println("warning: SMTP settings are incomplete, email sending will likely fail")
    }

	smtpCfg := delivery.SMTPConfig{
		Host:     smtpHost,
		Port:     smtpPort,
		User:     smtpUser,
		Pass:     smtpPass,
		FromAddr: fromAddr,
	}

	emailDelivery := delivery.NewEmailDelivery(db, jobLogger, smtpCfg)

    srv := httpserver.New(db, jobLogger, emailDelivery)

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
