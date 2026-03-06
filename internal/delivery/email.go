package delivery

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"os"
	"path"
	"time"

	"github.com/scorredoira/email"
)

type SMTPConfig struct {
	Host     string
	Port     string
	User     string
	Pass     string
	FromAddr string
}

type EmailDelivery struct {
	DB         *sql.DB
	Logger     *log.Logger
	SMTP       SMTPConfig
	HTTPClient *http.Client
}

func NewEmailDelivery(db *sql.DB, logger *log.Logger, cfg SMTPConfig) *EmailDelivery {
	return &EmailDelivery{
		DB:     db,
		Logger: logger,
		SMTP:   cfg,
		HTTPClient: &http.Client{
			Timeout: 0,
		},
	}
}

func (d *EmailDelivery) SendFile(ctx context.Context, user User, src string) error {
	if d.SMTP.Host == "" || d.SMTP.Port == "" || d.SMTP.FromAddr == "" {
		return fmt.Errorf("smtp config incomplete")
	}

	d.Logger.Printf("user_id=%d username=%s url=%s mode=%s status=received\n", user.ID, user.Username, src, user.Mode)

	var attachmentPath string
	var size int64

	if isURL(src) {
		d.Logger.Printf("user_id=%d username=%s url=%s mode=%s status=downloading\n", user.ID, user.Username, src, user.Mode)

		getResp, err := d.HTTPClient.Get(src)
		if err != nil {
			log.Printf("get request err: %v\n", err)
			d.Logger.Printf("user_id=%d username=%s url=%s mode=%s status=download_error stage=get error=%q\n", user.ID, user.Username, src, user.Mode, err.Error())
			return fmt.Errorf("download failed: %w", err)
		}
		defer getResp.Body.Close()

		if getResp.StatusCode != http.StatusOK {
			d.Logger.Printf("user_id=%d username=%s url=%s mode=%s status=download_bad_status http_status=%d\n", user.ID, user.Username, src, user.Mode, getResp.StatusCode)
			return fmt.Errorf("download bad status: %d", getResp.StatusCode)
		}

		urlFileName := path.Base(src)
		if urlFileName == "." || urlFileName == "/" || urlFileName == "" {
			urlFileName = "downloaded-file"
		}

		tmpFile, err := os.CreateTemp("", "download-*-"+urlFileName)
		if err != nil {
			log.Println("temp file create err:", err)
			d.Logger.Printf("user_id=%d username=%s url=%s mode=%s status=download_error stage=tempfile error=%q\n", user.ID, user.Username, src, user.Mode, err.Error())
			return fmt.Errorf("temp file create: %w", err)
		}
		defer func() {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
		}()

		written, err := io.Copy(tmpFile, getResp.Body)
		if err != nil {
			log.Println("io.Copy err:", err)
			d.Logger.Printf("user_id=%d username=%s url=%s mode=%s status=download_error stage=copy written=%d error=%q\n", user.ID, user.Username, src, user.Mode, written, err.Error())
			return fmt.Errorf("download failed: %w", err)
		}
		attachmentPath = tmpFile.Name()
		size = written
	} else {
		// src — путь к локальному файлу
		info, err := os.Stat(src)
		if err != nil {
			d.Logger.Printf("user_id=%d username=%s path=%s mode=%s status=open_error error=%q\n", user.ID, user.Username, src, user.Mode, err.Error())
			return fmt.Errorf("stat file: %w", err)
		}
		size = info.Size()
		attachmentPath = src
	}

	d.Logger.Printf(
		"user_id=%d username=%s url=%s mode=%s status=downloaded size=%d path=%s\n",
		user.ID, user.Username, src, user.Mode, size, attachmentPath,
	)

	if user.Email == "" {
		return fmt.Errorf("user email is empty")
	}

	now := time.Now()
	subject := fmt.Sprintf("Скачанный файл на %s", now.Format("2006-01-02 15:04:05"))
	body := fmt.Sprintf("Файл по ссылке %s был успешно скачан. Размер: %d байт.\n", src, size)

	if err := d.sendEmail(user.Email, subject, body, attachmentPath); err != nil {
		log.Println("sendEmail err:", err)
		d.Logger.Printf("user_id=%d username=%s email=%s url=%s mode=%s status=send_error stage=smtp error=%q\n",
			user.ID, user.Username, user.Email, src, user.Mode, err.Error())
		return fmt.Errorf("email send failed: %w", err)
	}

	d.Logger.Printf("user_id=%d username=%s email=%s url=%s mode=%s status=sent size=%d\n",
		user.ID, user.Username, user.Email, src, user.Mode, size)

	return nil
}

func (d *EmailDelivery) sendEmail(to, subject, body, attachmentPath string) error {
	if d.SMTP.Host == "" || d.SMTP.Port == "" || d.SMTP.FromAddr == "" {
		return fmt.Errorf("smtp config incomplete")
	}

	m := email.NewMessage(subject, body)
	m.From = mail.Address{
		Name:    "filemailer",
		Address: d.SMTP.FromAddr,
	}
	m.To = []string{to}

	if attachmentPath != "" {
		if err := m.Attach(attachmentPath); err != nil {
			return fmt.Errorf("attach file: %w", err)
		}
	}

	addr := d.SMTP.Host + ":" + d.SMTP.Port

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("dial smtp: %w", err)
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, d.SMTP.Host)
	if err != nil {
		return fmt.Errorf("smtp new client: %w", err)
	}
	defer c.Quit()

	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsconfig := &tls.Config{
			ServerName: d.SMTP.Host,
		}
		if err = c.StartTLS(tlsconfig); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}

	if err = c.Mail(d.SMTP.FromAddr); err != nil {
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
