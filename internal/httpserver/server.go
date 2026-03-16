package httpserver

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"download_track/internal/delivery"
	"download_track/internal/downloader"
)

// AdminNotifier — опциональный уведомитель админа (например при истечении cookies или ошибке Instagram).
type AdminNotifier interface {
	NotifyAdmin(msg string)
}

type SMTPConfig struct {
	Host     string
	Port     string
	User     string
	Pass     string
	FromAddr string
}

type Server struct {
	db               *sql.DB
	jobLog           *log.Logger
	fetcher          downloader.Fetcher
	emailDelivery    delivery.Delivery
	telegramDelivery delivery.Delivery
	bothDelivery     delivery.Delivery
	adminNotifier    AdminNotifier
	cookiesPath      string
}

type sendRequest struct {
	APIKey  string `json:"api_key"`
	FileURL string `json:"file_url"`
	Mode    string `json:"mode"` // "email", "telegram", "both"
}

func New(db *sql.DB, jobLog *log.Logger, fetcher downloader.Fetcher, email delivery.Delivery, telegram delivery.Delivery, both delivery.Delivery, adminNotifier AdminNotifier, cookiesPath string) *Server {
	return &Server{
		db:               db,
		jobLog:           jobLog,
		fetcher:          fetcher,
		emailDelivery:    email,
		telegramDelivery: telegram,
		bothDelivery:     both,
		adminNotifier:    adminNotifier,
		cookiesPath:      cookiesPath,
	}
}

// Routes регистрирует все HTTP-обработчики на переданном mux.
func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/send", s.handleSend)
	mux.HandleFunc("/cookie-status", s.handleCookieStatus)
}

// cookieStatusResponse — ответ GET /cookie-status.
type cookieStatusResponse struct {
	Available  bool   `json:"available"`
	Expired    bool   `json:"expired,omitempty"` // true только если дата истечения уже в прошлом
	ParseError bool   `json:"parse_error,omitempty"`
	Expiry     string `json:"expiry,omitempty"`
	DaysLeft   int    `json:"days_left,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (s *Server) handleCookieStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cookiesPath == "" {
		json.NewEncoder(w).Encode(cookieStatusResponse{Available: false, Error: "cookies path not configured"})
		return
	}
	if _, err := os.Stat(s.cookiesPath); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cookieStatusResponse{Available: false, Error: "unavailable"})
		return
	}
	expiry, err := downloader.CookieExpiry(s.cookiesPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cookieStatusResponse{Available: false, ParseError: true, Error: err.Error()})
		return
	}
	now := time.Now()
	if expiry.Before(now) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cookieStatusResponse{
			Available: true,
			Expired:   true,
			Expiry:    expiry.Format("02.01.2006"),
			DaysLeft:  0,
		})
		return
	}
	daysLeft := int(expiry.Sub(now).Hours() / 24)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cookieStatusResponse{
		Available: true,
		Expired:   false,
		Expiry:    expiry.Format("02.01.2006"),
		DaysLeft:  daysLeft,
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
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
	var telegramID int64

	err := s.db.QueryRow(
		`SELECT users.id, telegram_users.username, telegram_users.telegram_id
         FROM users
         JOIN telegram_users ON telegram_users.user_id = users.id
         WHERE users.api_key = $1`,
		req.APIKey,
	).Scan(&userID, &username, &telegramID)
	if err == sql.ErrNoRows {
		http.Error(w, "invalid api_key", http.StatusUnauthorized)
		return
	}
	if err != nil {
		log.Println("db query user err:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	var emailAddr string
	err = s.db.QueryRow("SELECT email FROM users WHERE id=$1", userID).Scan(&emailAddr)
	if err != nil {
		log.Println("db query email err:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if req.Mode == "" {
		req.Mode = "email"
	}

	user := delivery.User{
		ID:         userID,
		Email:      emailAddr,
		TelegramID: telegramID,
		Username:   username,
		Mode:       req.Mode,
	}

	var d delivery.Delivery

	switch req.Mode {
	case "email":
		d = s.emailDelivery
	case "telegram":
		d = s.telegramDelivery
	case "both":
		d = s.bothDelivery
	default:
		http.Error(w, "invalid mode", http.StatusBadRequest)
		return
	}

	if d == nil {
		log.Println("delivery not configured for mode:", req.Mode)
		http.Error(w, "delivery not configured", http.StatusBadGateway)
		return
	}

	result, err := s.fetcher.Fetch(r.Context(), req.FileURL)
	if err != nil {
		if s.adminNotifier != nil && strings.Contains(req.FileURL, "instagram.com") && strings.Contains(strings.ToLower(err.Error()), "login") {
			s.adminNotifier.NotifyAdmin("Не удалось скачать видео с Instagram. Возможно, истекли cookies. Обновите cookies/instagram.txt.")
		}
		log.Println("fetcher Fetch err:", err)
		http.Error(w, "fetch failed", http.StatusBadGateway)
		return
	}
	defer result.Cleanup()

	if result.VideoMeta != nil {
		s.jobLog.Printf("video fetch: url=%s estimated_1080p_bytes=%d format=%s downloaded_bytes=%d transcoded_bytes=%d\n",
			req.FileURL, result.VideoMeta.Estimated1080pBytes, result.VideoMeta.Format,
			result.VideoMeta.SourceSizeBytes, result.VideoMeta.TranscodeSizeBytes)
	}

	if err := d.SendFile(r.Context(), user, result.Path); err != nil {
		log.Println("delivery SendFile err:", err)
		http.Error(w, "delivery failed", http.StatusBadGateway)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
