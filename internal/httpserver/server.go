package httpserver

import (
    "database/sql"
    "encoding/json"
    "log"
    "net/http"

    "download_track/internal/delivery"
)


type SMTPConfig struct {
    Host     string
    Port     string
    User     string
    Pass     string
    FromAddr string
}

type Server struct {
    db       *sql.DB
    jobLog   *log.Logger
    delivery delivery.Delivery
}

type sendRequest struct {
    APIKey  string `json:"api_key"`
    FileURL string `json:"file_url"`
}

// New создаёт Server с теми же полями, что раньше были в main.go.
func New(db *sql.DB, jobLog *log.Logger, d delivery.Delivery) *Server {
    return &Server{
        db:       db,
        jobLog:   jobLog,
        delivery: d,
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

    var emailAddr string
    err = s.db.QueryRow("SELECT email FROM users WHERE id=$1", userID).Scan(&emailAddr)
    if err != nil {
        log.Println("db query email err:", err)
        http.Error(w, "internal error", http.StatusInternalServerError)
        return
    }

    user := delivery.User{
        ID:       userID,
        Email:    emailAddr,
        Username: username,
        // TelegramID пока не нужен
    }

    if err := s.delivery.SendFile(r.Context(), user, req.FileURL); err != nil {
        log.Println("delivery SendFile err:", err)
        http.Error(w, "delivery failed", http.StatusBadGateway)
        return
    }

    w.WriteHeader(http.StatusOK)
    _, _ = w.Write([]byte("ok"))
}