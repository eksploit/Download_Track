package httpserver

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"

	"download_track/internal/delivery"
	"download_track/internal/downloader"
)

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
}

type sendRequest struct {
	APIKey  string `json:"api_key"`
	FileURL string `json:"file_url"`
	Mode    string `json:"mode"` // "email", "telegram", "both"
}

func New(db *sql.DB, jobLog *log.Logger, fetcher downloader.Fetcher, email delivery.Delivery, telegram delivery.Delivery, both delivery.Delivery) *Server {
	return &Server{
		db:               db,
		jobLog:           jobLog,
		fetcher:          fetcher,
		emailDelivery:    email,
		telegramDelivery: telegram,
		bothDelivery:     both,
	}
}

// Routes регистрирует все HTTP-обработчики на переданном mux.
func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/send", s.handleSend)
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
