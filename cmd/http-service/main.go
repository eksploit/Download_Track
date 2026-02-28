package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"net/mail"
	"os"
	"time"
	"crypto/tls"
	"path"
	"github.com/scorredoira/email"
	_ "github.com/lib/pq"
)

type Server struct {
	db     *sql.DB
	jobLog *log.Logger

	smtpHost string
	smtpPort string
	smtpUser string
	smtpPass string
	fromAddr string
}

type sendRequest struct {
	APIKey  string `json:"api_key"`
	FileURL string `json:"file_url"`
}

const maxFileSize = 500 * 1024 * 1024 // 500 MB

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

	// Лог-файл для истории отправок
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

	srv := &Server{
		db:       db,
		jobLog:   jobLogger,
		smtpHost: smtpHost,
		smtpPort: smtpPort,
		smtpUser: smtpUser,
		smtpPass: smtpPass,
		fromAddr: fromAddr,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", srv.handleHealth)
	mux.HandleFunc("/send", srv.handleSend)

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

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if req.APIKey == "" || req.FileURL == "" {
		http.Error(w, "api_key and file_url are required", http.StatusBadRequest)
		return
	}

	var userID int
	var username string

	err := s.db.QueryRow(
		`SELECT users.id, telegram_users.username
         FROM users
         JOIN telegram_users ON telegram_users.user_id = users.id
         WHERE users.api_key = $1`,
		req.APIKey,
	).Scan(&userID, &username)
	if err == sql.ErrNoRows {
		http.Error(w, "invalid api_key", http.StatusUnauthorized)
		return
	}
	if err != nil {
		log.Println("db query user err:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	s.jobLog.Printf("user_id=%d username=%s url=%s status=received\n", userID, username, req.FileURL)

	// Логируем старт скачивания без предварительной проверки размера
    s.jobLog.Printf("user_id=%d username=%s url=%s status=downloading\n", userID, username, req.FileURL)


	getClient := &http.Client{
		Timeout: 0,
	}

	getResp, err := getClient.Get(req.FileURL)
	if err != nil {
		log.Println("get request err:", err)
		s.jobLog.Printf("user_id=%d username=%s url=%s status=download_error stage=get error=%q\n", userID, username, req.FileURL, err.Error())
		http.Error(w, "download failed", http.StatusBadGateway)
		return
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		s.jobLog.Printf("user_id=%d username=%s url=%s status=download_bad_status http_status=%d\n", userID, username, req.FileURL, getResp.StatusCode)
		http.Error(w, "download bad status", http.StatusBadGateway)
		return
	}

	// Имя файла из URL (последний сегмент пути)
    urlFileName := path.Base(req.FileURL)
    if urlFileName == "." || urlFileName == "/" || urlFileName == "" {
        urlFileName = "downloaded-file"
    }

    // Временный файл с этим именем как суффиксом
    tmpFile, err := os.CreateTemp("", "download-*-" + urlFileName)
    if err != nil {
        log.Println("temp file create err:", err)
        s.jobLog.Printf("user_id=%d username=%s url=%s status=download_error stage=tempfile error=%q\n", userID, username, req.FileURL, err.Error())
        http.Error(w, "internal error", http.StatusInternalServerError)
        return
    }
	defer func() {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
	}()

	written, err := io.Copy(tmpFile, getResp.Body)
	if err != nil {
		log.Println("io.Copy err:", err)
		s.jobLog.Printf("user_id=%d username=%s url=%s status=download_error stage=copy written=%d error=%q\n", userID, username, req.FileURL, written, err.Error())
		http.Error(w, "download failed", http.StatusBadGateway)
		return
	}

	s.jobLog.Printf(
		"user_id=%d username=%s url=%s status=downloaded size=%d path=%s\n",
		userID, username, req.FileURL, written, tmpFile.Name(),
	)
	    // Получаем email пользователя
    var emailAddr string
    err = s.db.QueryRow("SELECT email FROM users WHERE id=$1", userID).Scan(&emailAddr)
    if err != nil {
        log.Println("db query email err:", err)
        s.jobLog.Printf("user_id=%d username=%s url=%s status=send_error stage=get_email error=%q\n",
            userID, username, req.FileURL, err.Error())
        http.Error(w, "internal error", http.StatusInternalServerError)
        return
    }

    // Тема с датой/временем
    now := time.Now()
    subject := fmt.Sprintf("Скачанный файл на %s", now.Format("2006-01-02 15:04:05"))

    // Текст письма остаётся информативным
    body := fmt.Sprintf("Файл по ссылке %s был успешно скачан. Размер: %d байт.\n", req.FileURL, written)

	// Передаём путь к временно скачанному файлу как вложение
	if err := s.sendEmail(emailAddr, subject, body, tmpFile.Name()); err != nil {
		log.Println("sendEmail err:", err)
		s.jobLog.Printf("user_id=%d username=%s email=%s url=%s status=send_error stage=smtp error=%q\n",
			userID, username, emailAddr, req.FileURL, err.Error())
		http.Error(w, "email send failed", http.StatusBadGateway)
		return
	}

    s.jobLog.Printf("user_id=%d username=%s email=%s url=%s status=sent size=%d\n",
        userID, username, emailAddr, req.FileURL, written)

    w.WriteHeader(http.StatusOK)
    w.Write([]byte("ok"))

}

func (s *Server) sendEmail(to, subject, body, attachmentPath string) error {
    if s.smtpHost == "" || s.smtpPort == "" || s.fromAddr == "" {
        return fmt.Errorf("smtp config incomplete")
    }

    m := email.NewMessage(subject, body)
    m.From = mail.Address{
        Name:    "filemailer",
        Address: s.fromAddr,
    }
    m.To = []string{to}

    if attachmentPath != "" {
        if err := m.Attach(attachmentPath); err != nil {
            return fmt.Errorf("attach file: %w", err)
        }
    }

    addr := s.smtpHost + ":" + s.smtpPort

    conn, err := net.Dial("tcp", addr)
    if err != nil {
        return fmt.Errorf("dial smtp: %w", err)
    }
    defer conn.Close()

    c, err := smtp.NewClient(conn, s.smtpHost)
    if err != nil {
        return fmt.Errorf("smtp new client: %w", err)
    }
    defer c.Quit()

    if ok, _ := c.Extension("STARTTLS"); ok {
        tlsconfig := &tls.Config{
            ServerName: s.smtpHost,
        }
        if err = c.StartTLS(tlsconfig); err != nil {
            return fmt.Errorf("starttls: %w", err)
        }
    }

    if err = c.Mail(s.fromAddr); err != nil {
        return fmt.Errorf("mail from: %w", err)
    }
    if err = c.Rcpt(to); err != nil {
        return fmt.Errorf("rcpt to: %w", err)
    }

    w, err := c.Data()
    if err != nil {
        return fmt.Errorf("data: %w", err)
    }

    if _, err = w.Write(m.Bytes()); err != nil {
        return fmt.Errorf("write mime: %w", err)
    }
    if err = w.Close(); err != nil {
        return fmt.Errorf("data close: %w", err)
    }

    return nil
}
