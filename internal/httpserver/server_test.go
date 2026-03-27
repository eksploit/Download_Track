package httpserver

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestServer_HandleHealth(t *testing.T) {
	discardLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(nil, discardLog, nil, nil, nil, nil, nil, "", "", "")
	mux := http.NewServeMux()
	srv.Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("статус: got %d, want %d", rr.Code, http.StatusOK)
	}
	if body := rr.Body.String(); body != "ok" {
		t.Errorf("тело: got %q, want %q", body, "ok")
	}
}

func TestServer_JobLogRouteNotRegisteredWithoutToken(t *testing.T) {
	discardLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(nil, discardLog, nil, nil, nil, nil, nil, "", "/logs/x", "")
	mux := http.NewServeMux()
	srv.Routes(mux)
	req := httptest.NewRequest(http.MethodGet, "/job-log", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("без токена маршрут не регистрируется: статус %d, ожидался 404", rr.Code)
	}
}

func TestServer_HandleJobLog_Unauthorized(t *testing.T) {
	discardLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "send.log")
	if err := os.WriteFile(logPath, []byte("{\"msg\":\"hi\"}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	srv := New(nil, discardLog, nil, nil, nil, nil, nil, "", logPath, "secret")
	mux := http.NewServeMux()
	srv.Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/job-log", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("статус: got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestServer_HandleJobLog_OK(t *testing.T) {
	discardLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "send.log")
	content := "{\"time\":\"2026-03-27T12:00:00Z\",\"level\":\"INFO\",\"msg\":\"a\",\"event\":\"video_pipeline\",\"request_id\":\"r1\"}\n"
	if err := os.WriteFile(logPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	srv := New(nil, discardLog, nil, nil, nil, nil, nil, "", logPath, "secret")
	mux := http.NewServeMux()
	srv.Routes(mux)

	req := httptest.NewRequest(http.MethodGet, "/job-log?lines=10", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("статус: got %d, body %q", rr.Code, rr.Body.String())
	}
	var body jobLogAPIResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Entries) != 1 {
		t.Fatalf("entries: got %d, want 1", len(body.Entries))
	}
	if body.Entries[0]["request_id"] != "r1" {
		t.Errorf("request_id: %v", body.Entries[0]["request_id"])
	}
	if body.ParseErrors != 0 || body.Truncated {
		t.Errorf("parse_errors=%v truncated=%v", body.ParseErrors, body.Truncated)
	}
}

func TestServer_HandleJobLog_NotFound(t *testing.T) {
	discardLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	missingPath := filepath.Join(t.TempDir(), "nonexistent.log")
	srv := New(nil, discardLog, nil, nil, nil, nil, nil, "", missingPath, "secret")
	mux := http.NewServeMux()
	srv.Routes(mux)
	req := httptest.NewRequest(http.MethodGet, "/job-log", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("статус: got %d, want 404", rr.Code)
	}
}

func TestServer_HandleJobLog_XAdminToken(t *testing.T) {
	discardLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "send.log")
	if err := os.WriteFile(logPath, []byte("{\"k\":1}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	srv := New(nil, discardLog, nil, nil, nil, nil, nil, "", logPath, "tok")
	mux := http.NewServeMux()
	srv.Routes(mux)
	req := httptest.NewRequest(http.MethodGet, "/job-log", nil)
	req.Header.Set("X-Admin-Token", "tok")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("статус: %d", rr.Code)
	}
}
