package httpserver

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"download_track/internal/delivery"
	"download_track/internal/downloader"
	"download_track/internal/joblog"
	"download_track/internal/logutil"
	"download_track/internal/requestid"
)

// AdminNotifier — опциональный уведомитель админа (например при истечении cookies или ошибке Instagram).
type AdminNotifier interface {
	NotifyAdmin(msg string) bool
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
	jobLog           *slog.Logger
	fetcher          downloader.Fetcher
	emailDelivery    delivery.Delivery
	telegramDelivery delivery.Delivery
	bothDelivery     delivery.Delivery
	adminNotifier    AdminNotifier
	cookiesPath      string
	// jobLogReadPath — путь к NDJSON job-логу (чтение для GET /job-log). Пустой — маршрут не регистрируется.
	jobLogReadPath string
	// adminJobLogToken — секрет для GET /job-log. Пустой — маршрут не регистрируется.
	adminJobLogToken string
}

type sendRequest struct {
	APIKey  string `json:"api_key"`
	FileURL string `json:"file_url"`
	Mode    string `json:"mode"` // "email", "telegram", "both"
}

func New(db *sql.DB, jobLog *slog.Logger, fetcher downloader.Fetcher, email delivery.Delivery, telegram delivery.Delivery, both delivery.Delivery, adminNotifier AdminNotifier, cookiesPath string, jobLogReadPath string, adminJobLogToken string) *Server {
	return &Server{
		db:               db,
		jobLog:           jobLog,
		fetcher:          fetcher,
		emailDelivery:    email,
		telegramDelivery: telegram,
		bothDelivery:     both,
		adminNotifier:    adminNotifier,
		cookiesPath:      cookiesPath,
		jobLogReadPath:   jobLogReadPath,
		adminJobLogToken: adminJobLogToken,
	}
}

// Routes регистрирует все HTTP-обработчики на переданном mux.
func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/send", s.handleSend)
	mux.HandleFunc("/cookie-status", s.handleCookieStatus)
	if s.adminJobLogToken != "" && s.jobLogReadPath != "" {
		mux.HandleFunc("/job-log", s.handleJobLog)
	}
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
	daysLeft := downloader.DaysLeftCeil(now, expiry)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cookieStatusResponse{
		Available: true,
		Expired:   false,
		Expiry:    expiry.Format("02.01.2006"),
		DaysLeft:  daysLeft,
	})
}

// jobLogAPIResponse — тело ответа GET /job-log.
type jobLogAPIResponse struct {
	Entries     []map[string]any `json:"entries"`
	Truncated   bool             `json:"truncated"`
	ParseErrors int              `json:"parse_errors"`
}

const defaultJobLogLines = 20

func (s *Server) handleJobLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !jobLogAdminTokenOK(r, s.adminJobLogToken) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		return
	}
	lines := defaultJobLogLines
	if q := r.URL.Query().Get("lines"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 0 {
			http.Error(w, "bad lines", http.StatusBadRequest)
			return
		}
		lines = n
	}
	res, err := joblog.TailEntries(s.jobLogReadPath, lines)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "log file not found", http.StatusNotFound)
			return
		}
		log.Println("job-log TailEntries:", err)
		http.Error(w, "log unavailable", http.StatusServiceUnavailable)
		return
	}
	out := jobLogAPIResponse{
		Truncated:   res.Truncated,
		ParseErrors: res.ParseErrors,
		Entries:     make([]map[string]any, 0, len(res.Entries)),
	}
	for _, e := range res.Entries {
		out.Entries = append(out.Entries, e.Fields)
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(out); err != nil {
		log.Println("job-log encode:", err)
	}
}

const bearerPrefix = "Bearer "

func jobLogAdminTokenOK(r *http.Request, want string) bool {
	if want == "" {
		return false
	}
	auth := r.Header.Get("Authorization")
	if len(auth) >= len(bearerPrefix) && strings.EqualFold(auth[:len(bearerPrefix)], bearerPrefix) {
		got := strings.TrimSpace(auth[len(bearerPrefix):])
		return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
	}
	got := r.Header.Get("X-Admin-Token")
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "req-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(b[:])
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

	ctx := requestid.With(r.Context(), newRequestID())

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

	result, err := s.fetcher.Fetch(ctx, req.FileURL)
	if err != nil {
		if s.adminNotifier != nil && strings.Contains(req.FileURL, "instagram.com") && strings.Contains(strings.ToLower(err.Error()), "login") {
			_ = s.adminNotifier.NotifyAdmin("Не удалось скачать видео с Instagram. Возможно, истекли cookies. Обновите cookies/instagram.txt.")
		}
		log.Println("fetcher Fetch err:", err)
		http.Error(w, "fetch failed", http.StatusBadGateway)
		return
	}
	defer result.Cleanup()

	if result.VideoMeta != nil {
		vm := result.VideoMeta
		s.jobLog.InfoContext(ctx, "video pipeline",
			slog.String("event", "video_pipeline"),
			slog.String("request_id", requestid.From(ctx)),
			slog.Int("user_id", userID),
			slog.String("mode", req.Mode),
			slog.String("file_url", logutil.TruncateString(req.FileURL, 256)),
			slog.String("format", vm.Format),
			slog.Int64("estimated_1080p_bytes", vm.Estimated1080pBytes),
			slog.Int64("downloaded_bytes", vm.SourceSizeBytes),
			slog.Int64("transcoded_bytes", vm.TranscodeSizeBytes),
			slog.Int64("probe_ms", vm.ProbeDurationMs),
			slog.Int64("ytdlp_ms", vm.DownloadDurationMs),
			slog.Int64("ffmpeg_ms", vm.TranscodeDurationMs),
		)
	}

	if err := d.SendFile(ctx, user, result.Path); err != nil {
		log.Println("delivery SendFile err:", err)
		http.Error(w, "delivery failed", http.StatusBadGateway)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
